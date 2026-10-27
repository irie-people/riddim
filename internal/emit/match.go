package emit

import (
	"strconv"
	"strings"

	"github.com/riddim-hq/riddim/internal/check"
	"github.com/riddim-hq/riddim/internal/syntax"
)

// A match lowers to an if / else if chain that tests the arms in order. The
// checker proved the match exhaustive, so the last unguarded arm needs no
// test and becomes the final else. An enum variant at the top of a pattern is
// tested with Go's `v, ok := x.(Enum_Variant)`.

// An alt is one way an arm can match: or-patterns that bind names are
// lowered as separate alternatives, each with its own copy of the body.
type alt struct {
	arm    *syntax.MatchArm
	choice map[*syntax.OrPat]int
}

type binding struct {
	local *check.Local
	acc   string
}

type patTest struct {
	conds []string
	binds []binding
}

func bindsNames(p syntax.Pattern) bool {
	found := false
	syntax.Inspect(p, func(n syntax.Node) bool {
		if _, ok := n.(*syntax.BindPat); ok {
			found = true
		}
		return !found
	})
	return found
}

// choices lists the alternatives of a pattern's or-patterns that bind names.
func choices(p syntax.Pattern) []map[*syntax.OrPat]int {
	product := func(ps []syntax.Pattern) []map[*syntax.OrPat]int {
		out := []map[*syntax.OrPat]int{{}}
		for _, q := range ps {
			var next []map[*syntax.OrPat]int
			for _, a := range out {
				for _, b := range choices(q) {
					m := map[*syntax.OrPat]int{}
					for k, v := range a {
						m[k] = v
					}
					for k, v := range b {
						m[k] = v
					}
					next = append(next, m)
				}
			}
			out = next
		}
		return out
	}
	switch p := p.(type) {
	case *syntax.OrPat:
		if !bindsNames(p) {
			return []map[*syntax.OrPat]int{{}}
		}
		var out []map[*syntax.OrPat]int
		for i, a := range p.Alts {
			for _, c := range choices(a) {
				c[p] = i
				out = append(out, c)
			}
		}
		return out
	case *syntax.TuplePat:
		return product(p.Elems)
	case *syntax.VariantPat:
		return product(p.Args)
	}
	return []map[*syntax.OrPat]int{{}}
}

// test builds the conditions under which pattern p matches the Go value x,
// and the bindings it makes.
func (f *fn) test(p syntax.Pattern, x string, choice map[*syntax.OrPat]int, out *patTest) {
	info := f.info.Pats[p]
	switch p := p.(type) {
	case *syntax.BindPat:
		out.binds = append(out.binds, binding{info.Local, x})
	case *syntax.LitPat:
		switch info.Kind {
		case check.PBool:
			if info.Bool {
				out.conds = append(out.conds, x)
			} else {
				out.conds = append(out.conds, "!"+x)
			}
		case check.PString:
			out.conds = append(out.conds, x+" == "+strconv.Quote(info.Str))
		case check.PInt:
			out.conds = append(out.conds, x+" == "+info.Int.String())
		}
	case *syntax.TuplePat:
		for i, e := range p.Elems {
			f.test(e, x+".f"+strconv.Itoa(i), choice, out)
		}
	case *syntax.VariantPat:
		switch info.Kind {
		case check.PSome:
			out.conds = append(out.conds, x+".some")
			f.test(p.Args[0], x+".value", choice, out)
		case check.PNone:
			out.conds = append(out.conds, "!"+x+".some")
		case check.POk:
			out.conds = append(out.conds, x+".ok")
			f.test(p.Args[0], x+".value", choice, out)
		case check.PErr:
			out.conds = append(out.conds, "!"+x+".ok")
			f.test(p.Args[0], x+".err", choice, out)
		case check.PVariant:
			v := info.Variant
			if v.Enum.Fieldless() {
				out.conds = append(out.conds, x+" == "+v.Enum.Name+"_"+v.Name)
				return
			}
			vt := f.variantType(v, info.TypeArgs)
			out.conds = append(out.conds, "isVariant["+vt+"]("+x+")")
			for i, a := range p.Args {
				f.test(a, x+".("+vt+")."+fieldName(v.Fields[i]), choice, out)
			}
		}
	case *syntax.OrPat:
		if i, ok := choice[p]; ok {
			f.test(p.Alts[i], x, choice, out)
			return
		}
		var sides []string
		for _, a := range p.Alts {
			var t patTest
			f.test(a, x, choice, &t)
			if len(t.conds) == 0 {
				return // this side matches everything
			}
			sides = append(sides, strings.Join(t.conds, " && "))
		}
		out.conds = append(out.conds, "("+strings.Join(sides, " || ")+")")
	}
}

// topVariant returns the variant struct type when p tests an enum variant of
// a non-integer enum at the top, so the test can be `v, ok := x.(T)`.
func (f *fn) topVariant(p syntax.Pattern) (*syntax.VariantPat, string) {
	vp, ok := p.(*syntax.VariantPat)
	if !ok {
		return nil, ""
	}
	info := f.info.Pats[p]
	if info.Kind != check.PVariant || info.Variant.Enum.Fieldless() {
		return nil, ""
	}
	return vp, f.variantType(info.Variant, info.TypeArgs)
}

// referenced returns the bindings an expression reads.
func (f *fn) referenced(n syntax.Expr) map[*check.Local]bool {
	out := map[*check.Local]bool{}
	if n == nil {
		return out
	}
	syntax.Inspect(n, func(n syntax.Node) bool {
		if id, ok := n.(*syntax.Ident); ok {
			if u := f.info.Uses[id]; u != nil && u.Kind == check.UseLocal {
				out[u.Local] = true
			}
		}
		return true
	})
	return out
}

func (f *fn) matchInto(m *syntax.MatchExpr, d dest) {
	sv := f.expr(m.Subject)
	if sv.never {
		return
	}
	st := f.typeOf(m.Subject)
	subj := sv.s
	declare := ""
	if !sv.local {
		subj = f.temp("subj")
		declare = "var " + subj + " " + f.goType(st) + " = " + sv.s + "\n"
	}
	var alts []alt
	for _, arm := range m.Arms {
		for _, c := range choices(arm.Pat) {
			alts = append(alts, alt{arm, c})
		}
	}
	used := false
	code, _ := f.capture(func() value {
		if f.guardsNeedStatements(alts) {
			used = f.matchFlags(alts, subj, d)
		} else {
			used = f.matchChain(alts, subj, d)
		}
		return value{}
	})
	f.w.WriteString(declare)
	if declare != "" && !used {
		f.printf("_ = %s\n", subj)
	}
	f.w.WriteString(code)
}

// guardsNeedStatements reports whether some guard cannot be lowered to a
// plain Go condition.
func (f *fn) guardsNeedStatements(alts []alt) bool {
	for _, a := range alts {
		if a.arm.Guard == nil {
			continue
		}
		saved, savedTerm, savedImports := f.taken, f.terminated, f.imports
		f.taken, f.imports = map[string]bool{}, map[string]bool{}
		for k, v := range saved {
			f.taken[k] = v
		}
		pre, _ := f.capture(func() value { return f.expr(a.arm.Guard) })
		f.taken, f.terminated, f.imports = saved, savedTerm, savedImports
		if pre != "" {
			return true
		}
	}
	return false
}

// declareBindings writes the bindings an arm's body reads.
func (f *fn) declareBindings(t patTest, need map[*check.Local]bool) {
	for _, b := range t.binds {
		if !need[b.local] {
			continue
		}
		name := f.bind(b.local)
		f.printf("%s := %s\n", name, b.acc)
		f.unused(b.local, name)
	}
}

// guardCond lowers a guard with the arm's bindings read straight from the
// matched value.
func (f *fn) guardCond(g syntax.Expr, t patTest) string {
	saved := map[*check.Local]string{}
	for _, b := range t.binds {
		if n, ok := f.names[b.local]; ok {
			saved[b.local] = n
		}
		f.names[b.local] = b.acc
	}
	v := f.expr(g)
	for _, b := range t.binds {
		delete(f.names, b.local)
		if n, ok := saved[b.local]; ok {
			f.names[b.local] = n
		}
	}
	if v.op {
		return "(" + v.s + ")"
	}
	return v.s
}

func (f *fn) matchChain(alts []alt, subj string, d dest) bool {
	used := false
	allTerm := true
	open := false
	vName, okName := "", ""
	for i, a := range alts {
		final := i == len(alts)-1 && a.arm.Guard == nil
		need := f.referenced(a.arm.Body)
		guardNeed := f.referenced(a.arm.Guard)
		var t patTest
		init := ""
		top := a.arm.Pat
		for {
			or, ok := top.(*syntax.OrPat)
			i, chosen := a.choice[or]
			if !ok || !chosen {
				break
			}
			top = or.Alts[i]
		}
		vp, vt := f.topVariant(top)
		switch {
		case vp != nil && !final:
			if vName == "" {
				vName, okName = f.temp("v"), f.temp("ok")
			}
			v, ok := vName, okName
			info := f.info.Pats[top]
			for j, p := range vp.Args {
				f.test(p, v+"."+fieldName(info.Variant.Fields[j]), a.choice, &t)
			}
			usesV := len(t.conds) > 0
			for _, b := range t.binds {
				if need[b.local] || guardNeed[b.local] {
					usesV = true
				}
			}
			if !usesV {
				v = "_"
			}
			init = v + ", " + ok + " := " + subj + ".(" + vt + "); "
			t.conds = append([]string{ok}, t.conds...)
		case vp != nil:
			info := f.info.Pats[top]
			for j, p := range vp.Args {
				f.test(p, subj+".("+vt+")."+fieldName(info.Variant.Fields[j]), a.choice, &t)
			}
		default:
			f.test(a.arm.Pat, subj, a.choice, &t)
		}
		if len(t.conds) > 0 || init != "" {
			used = true
		}
		for _, b := range t.binds {
			if need[b.local] || guardNeed[b.local] {
				used = true
			}
		}
		conds := t.conds
		if final {
			conds = nil
			if open && d.kind == dDiscard && isEmptyBlock(a.arm.Body) {
				allTerm = false // a final arm that does nothing needs no else
				break
			}
		}
		if a.arm.Guard != nil {
			conds = append(append([]string{}, conds...), f.guardCond(a.arm.Guard, t))
		}
		switch {
		case final && !open:
			// the only test is no test
		case final:
			f.printf("} else {\n")
		case !open:
			f.printf("if %s%s {\n", init, strings.Join(nonEmpty(conds), " && "))
			open = true
		default:
			f.printf("} else if %s%s {\n", init, strings.Join(nonEmpty(conds), " && "))
		}
		f.declareBindings(t, need)
		f.into(a.arm.Body, d)
		allTerm = allTerm && f.terminated
		f.terminated = false
	}
	lastGuarded := len(alts) > 0 && alts[len(alts)-1].arm.Guard != nil
	if lastGuarded || len(alts) == 0 {
		if open {
			f.printf("} else {\n")
		}
		f.printf("panic(\"unreachable\")\n")
	}
	if open {
		f.printf("}\n")
	}
	f.terminated = allTerm && len(alts) > 0 || len(alts) == 0
	return used
}

func isEmptyBlock(x syntax.Expr) bool {
	b, ok := x.(*syntax.BlockExpr)
	return ok && len(b.Stmts) == 0
}

func nonEmpty(conds []string) []string {
	if len(conds) == 0 {
		return []string{"true"}
	}
	return conds
}

// matchFlags lowers a match whose guards need statements: each arm is its own
// if, and a flag records that an arm matched.
func (f *fn) matchFlags(alts []alt, subj string, d dest) bool {
	matched := f.temp("matched")
	f.printf("%s := false\n", matched)
	for _, a := range alts {
		var t patTest
		f.test(a.arm.Pat, subj, a.choice, &t)
		need := f.referenced(a.arm.Body)
		for l := range f.referenced(a.arm.Guard) {
			need[l] = true
		}
		f.printf("if %s {\n", strings.Join(append([]string{"!" + matched}, t.conds...), " && "))
		f.declareBindings(t, need)
		if a.arm.Guard != nil {
			g := f.expr(a.arm.Guard)
			f.printf("if %s {\n", g.s)
		}
		f.printf("%s = true\n", matched)
		f.into(a.arm.Body, d)
		f.terminated = false
		if a.arm.Guard != nil {
			f.printf("}\n")
		}
		f.printf("}\n")
	}
	if d.kind == dReturn {
		f.printf("panic(\"unreachable\")\n")
		f.terminated = true
	} else {
		f.printf("if !%s {\npanic(\"unreachable\")\n}\n", matched)
		f.terminated = false
	}
	return true
}
