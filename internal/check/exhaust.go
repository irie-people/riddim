package check

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/riddim-hq/riddim/internal/diag"
	"github.com/riddim-hq/riddim/internal/syntax"
)

// Exhaustiveness and unreachable arms follow Maranget, "Warnings for pattern
// matching" (JFP 2007): a matrix of patterns, specialized by constructor,
// computing the value vectors that no row matches.

// A ctor is a constructor: a variant, Some, None, Ok, Err, true, false, the
// tuple constructor, or an Int or String literal.
type ctor struct {
	key   string
	name  string
	arity int
}

// An xpat is a pattern in the matrix: a wildcard (ctor nil, no alts), a
// constructor applied to sub-patterns, or an or-pattern.
type xpat struct {
	ctor *ctor
	args []*xpat
	alts []*xpat
}

var wildPat = &xpat{}

func (p *xpat) String() string {
	if p.ctor == nil {
		return "_"
	}
	args := make([]string, len(p.args))
	for i, a := range p.args {
		args[i] = a.String()
	}
	if p.ctor.key == "tuple" {
		return "(" + strings.Join(args, ", ") + ")"
	}
	if len(args) == 0 {
		return p.ctor.name
	}
	return p.ctor.name + "(" + strings.Join(args, ", ") + ")"
}

const maxWitnesses = 32

type exh struct{ c *checker }

func (x *exh) convert(p syntax.Pattern) *xpat {
	info := x.c.info.Pats[p]
	if info == nil {
		return wildPat
	}
	switch info.Kind {
	case PInt:
		return &xpat{ctor: &ctor{key: "i" + info.Int.String(), name: info.Int.String()}}
	case PString:
		q := strconv.Quote(info.Str)
		return &xpat{ctor: &ctor{key: "s" + q, name: q}}
	case PBool:
		return &xpat{ctor: boolCtor(info.Bool)}
	case PTuple:
		tp := p.(*syntax.TuplePat)
		out := &xpat{ctor: &ctor{key: "tuple", arity: len(tp.Elems)}}
		for _, e := range tp.Elems {
			out.args = append(out.args, x.convert(e))
		}
		return out
	case PVariant, PSome, POk, PErr, PNone:
		vp := p.(*syntax.VariantPat)
		var k *ctor
		switch info.Kind {
		case PVariant:
			k = variantCtor(info.Variant)
		case PSome:
			k = &ctor{key: "Some", name: "Some", arity: 1}
		case POk:
			k = &ctor{key: "Ok", name: "Ok", arity: 1}
		case PErr:
			k = &ctor{key: "Err", name: "Err", arity: 1}
		default:
			k = &ctor{key: "None", name: "None"}
		}
		out := &xpat{ctor: k}
		for _, a := range vp.Args {
			out.args = append(out.args, x.convert(a))
		}
		return out
	case POr:
		op := p.(*syntax.OrPat)
		out := &xpat{}
		for _, a := range op.Alts {
			out.alts = append(out.alts, x.convert(a))
		}
		return out
	}
	return wildPat
}

func boolCtor(b bool) *ctor {
	if b {
		return &ctor{key: "true", name: "true"}
	}
	return &ctor{key: "false", name: "false"}
}

func variantCtor(v *Variant) *ctor {
	return &ctor{key: "v" + strconv.Itoa(v.Index), name: v.Name, arity: len(v.Fields)}
}

// ctorsOf returns the constructors of a type with finitely many of them.
func ctorsOf(t Type) []*ctor {
	switch t := Resolve(t).(type) {
	case *Basic:
		if t == Bool {
			return []*ctor{boolCtor(true), boolCtor(false)}
		}
	case *Option:
		return []*ctor{{key: "Some", name: "Some", arity: 1}, {key: "None", name: "None"}}
	case *Result:
		return []*ctor{{key: "Ok", name: "Ok", arity: 1}, {key: "Err", name: "Err", arity: 1}}
	case *Named:
		if t.Decl.IsEnum {
			out := make([]*ctor, len(t.Decl.Variants))
			for i, v := range t.Decl.Variants {
				out[i] = variantCtor(v)
			}
			return out
		}
	case *Tuple:
		return []*ctor{{key: "tuple", arity: len(t.Elems)}}
	}
	return nil
}

func isEmptyEnum(t Type) bool {
	n, ok := Resolve(t).(*Named)
	return ok && n.Decl.IsEnum && len(n.Decl.Variants) == 0
}

func fieldTypes(t Type, k *ctor) []Type {
	switch t := Resolve(t).(type) {
	case *Option:
		if k.key == "Some" {
			return []Type{t.Elem}
		}
	case *Result:
		if k.key == "Ok" {
			return []Type{t.Ok}
		}
		if k.key == "Err" {
			return []Type{t.Err}
		}
	case *Tuple:
		return t.Elems
	case *Named:
		if t.Decl.IsEnum && strings.HasPrefix(k.key, "v") {
			i, _ := strconv.Atoi(k.key[1:])
			v := t.Decl.Variants[i]
			out := make([]Type, len(v.Fields))
			for j, f := range v.Fields {
				out[j] = FieldType(f, t.Decl, t.Args)
			}
			return out
		}
	}
	return nil
}

func wilds(n int) []*xpat {
	out := make([]*xpat, n)
	for i := range out {
		out[i] = wildPat
	}
	return out
}

// expandOr replaces rows whose first pattern is an or-pattern by one row per
// alternative.
func expandOr(rows [][]*xpat) [][]*xpat {
	var out [][]*xpat
	for _, r := range rows {
		if len(r) > 0 && len(r[0].alts) > 0 {
			var alts [][]*xpat
			for _, a := range r[0].alts {
				alts = append(alts, append([]*xpat{a}, r[1:]...))
			}
			out = append(out, expandOr(alts)...)
			continue
		}
		out = append(out, r)
	}
	return out
}

func specialize(rows [][]*xpat, k *ctor) [][]*xpat {
	var out [][]*xpat
	for _, r := range rows {
		h := r[0]
		switch {
		case h.ctor == nil:
			out = append(out, append(wilds(k.arity), r[1:]...))
		case h.ctor.key == k.key:
			out = append(out, append(append([]*xpat{}, h.args...), r[1:]...))
		}
	}
	return out
}

func defaultRows(rows [][]*xpat) [][]*xpat {
	var out [][]*xpat
	for _, r := range rows {
		if r[0].ctor == nil {
			out = append(out, r[1:])
		}
	}
	return out
}

func headKeys(rows [][]*xpat) map[string]bool {
	keys := map[string]bool{}
	for _, r := range rows {
		if r[0].ctor != nil {
			keys[r[0].ctor.key] = true
		}
	}
	return keys
}

func complete(ctors []*ctor, used map[string]bool) bool {
	if len(ctors) == 0 {
		return false
	}
	for _, k := range ctors {
		if !used[k.key] {
			return false
		}
	}
	return true
}

// useful reports whether vector q matches a value that no row matches.
func (x *exh) useful(rows [][]*xpat, q []*xpat, tys []Type) bool {
	if len(q) == 0 {
		return len(rows) == 0
	}
	rows = expandOr(rows)
	h, t := q[0], tys[0]
	if len(h.alts) > 0 {
		for _, a := range h.alts {
			if x.useful(rows, append([]*xpat{a}, q[1:]...), tys) {
				return true
			}
		}
		return false
	}
	if h.ctor != nil {
		return x.useful(specialize(rows, h.ctor), append(append([]*xpat{}, h.args...), q[1:]...),
			append(fieldTypes(t, h.ctor), tys[1:]...))
	}
	ctors := ctorsOf(t)
	if complete(ctors, headKeys(rows)) {
		for _, k := range ctors {
			if x.useful(specialize(rows, k), append(wilds(k.arity), q[1:]...), append(fieldTypes(t, k), tys[1:]...)) {
				return true
			}
		}
		return false
	}
	return x.useful(defaultRows(rows), q[1:], tys[1:])
}

// missing returns vectors of patterns, one per group of values that no row
// matches. At the top of an empty column it lists every constructor.
func (x *exh) missing(rows [][]*xpat, tys []Type, top bool) [][]*xpat {
	if len(tys) == 0 {
		if len(rows) == 0 {
			return [][]*xpat{{}}
		}
		return nil
	}
	rows = expandOr(rows)
	t := tys[0]
	if isEmptyEnum(t) {
		return nil // no values, so nothing is missing
	}
	ctors := ctorsOf(t)
	used := headKeys(rows)
	if complete(ctors, used) {
		var out [][]*xpat
		for _, k := range ctors {
			for _, w := range x.missing(specialize(rows, k), append(fieldTypes(t, k), tys[1:]...), false) {
				head := &xpat{ctor: k, args: w[:k.arity]}
				out = append(out, append([]*xpat{head}, w[k.arity:]...))
				if len(out) >= maxWitnesses {
					return out
				}
			}
		}
		return out
	}
	ws := x.missing(defaultRows(rows), tys[1:], false)
	if len(ws) == 0 {
		return nil
	}
	heads := []*xpat{wildPat}
	if len(ctors) > 0 && (len(used) > 0 || top && len(rows) == 0) {
		heads = nil
		for _, k := range ctors {
			if !used[k.key] {
				heads = append(heads, &xpat{ctor: k, args: wilds(k.arity)})
			}
		}
	}
	var out [][]*xpat
	for _, w := range ws {
		for _, h := range heads {
			out = append(out, append([]*xpat{h}, w...))
			if len(out) >= maxWitnesses {
				return out
			}
		}
	}
	return out
}

func (c *checker) patErrorf(code string, sp syntax.Span, format string, args ...any) *diag.Diagnostic {
	c.pats = append(c.pats, c.fset.Diag(code, sp, format, args...))
	return &c.pats[len(c.pats)-1]
}

// checkMatch reports unreachable arms (R0402) and missing cases (R0401) with
// a fix that adds an arm for each.
func (c *checker) checkMatch(m *syntax.MatchExpr) {
	if len(c.errs) > 0 {
		return // patterns may be malformed; this stage is not reported anyway
	}
	st := Deep(c.info.Types[m.Subject])
	if isInvalid(st) || hasVars(st) {
		return
	}
	x := &exh{c: c}
	var rows [][]*xpat
	guarded := false
	for _, arm := range m.Arms {
		q := x.convert(arm.Pat)
		if !x.useful(rows, []*xpat{q}, []Type{st}) {
			d := c.patErrorf("R0402", arm.Pat.Span(), "this arm can never match: the arms before it cover every value it matches")
			d.Notes = append(d.Notes, "remove it, or move it before the arm that covers it")
		}
		if arm.Guard == nil {
			rows = append(rows, []*xpat{q})
		} else {
			guarded = true
		}
	}
	ws := x.missing(rows, []Type{st}, true)
	if len(ws) == 0 {
		return
	}
	pats := make([]string, len(ws))
	for i, w := range ws {
		pats[i] = w[0].String()
	}
	shown := pats
	more := ""
	if len(shown) > 10 {
		shown, more = shown[:10], fmt.Sprintf(" and %d more", len(pats)-10)
	}
	d := c.patErrorf("R0401", m.Span(), "match on %s is missing %s: %s%s", st, plural(len(pats), "case"), strings.Join(shown, ", "), more)
	if guarded {
		d.Notes = append(d.Notes, "a guarded arm does not count toward exhaustiveness")
	}
	d.Fixes = append(d.Fixes, c.addArms(m, pats))
}

// addArms makes the fix that adds `pattern => todo(),` for each missing case.
func (c *checker) addArms(m *syntax.MatchExpr, pats []string) diag.Fix {
	title := "add the missing arm"
	if len(pats) > 1 {
		title = "add the missing arms"
	}
	src := c.fset.Source(m.Match)
	rbLine, rbCol := src.LineCol(m.Rbrace)
	lineText, _ := src.Line(rbLine)
	before := string([]rune(lineText)[:rbCol-1])
	if strings.TrimSpace(before) == "" {
		// The closing brace is on its own line: insert whole lines before it.
		indent := before + "    "
		if len(m.Arms) > 0 {
			al, ac := src.LineCol(m.Arms[0].Pat.Span().Start)
			t, _ := src.Line(al)
			if prefix := string([]rune(t)[:ac-1]); strings.TrimSpace(prefix) == "" {
				indent = prefix
			}
		}
		var b strings.Builder
		for _, p := range pats {
			b.WriteString(indent + p + " => todo(),\n")
		}
		at := src.LineStart(rbLine)
		return diag.Fix{Title: title, Edits: []diag.Edit{c.fset.Edit(syntax.Span{Start: at, End: at}, b.String())}}
	}
	arms := make([]string, len(pats))
	for i, p := range pats {
		arms[i] = p + " => todo()"
	}
	text := strings.Join(arms, ", ")
	if len(m.Arms) == 0 {
		return diag.Fix{Title: title, Edits: []diag.Edit{c.fset.Edit(syntax.Span{Start: m.Lbrace + 1, End: m.Rbrace}, " "+text+" ")}}
	}
	end := m.Arms[len(m.Arms)-1].Body.Span().End
	between := string(src.Src[src.Offset(end):src.Offset(m.Rbrace)])
	if i := strings.Index(between, ","); i >= 0 {
		at := end + syntax.Pos(i) + 1
		return diag.Fix{Title: title, Edits: []diag.Edit{c.fset.Edit(syntax.Span{Start: at, End: at}, " "+text+",")}}
	}
	return diag.Fix{Title: title, Edits: []diag.Edit{c.fset.Edit(syntax.Span{Start: end, End: end}, ", "+text)}}
}
