package emit

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/riddim-hq/riddim/internal/check"
	"github.com/riddim-hq/riddim/internal/syntax"
)

// A fn lowers one Go function: a Riddim function or method, with the
// closures inside it. Every binding and temporary in it gets its own Go name.
type fn struct {
	*emitter
	names      map[*check.Local]string
	taken      map[string]bool
	user       map[string]bool // names of the function's own bindings
	ctx        *fnCtx
	terminated bool // the statements written so far end in a Go terminating statement
}

// fnCtx is the function or closure that return and ? leave.
type fnCtx struct {
	result  check.Type
	closure bool // closures always have a Go result; () becomes struct{}
}

// A value is a lowered expression.
type value struct {
	s        string
	never    bool // evaluation does not finish; s is empty
	stable   bool // reading it later gives the same result
	concrete bool // a variant struct whose static Go type is not its enum's
	call     bool // a Go call that may stand as a statement
	op       bool // a unary or binary expression: needs parentheses as an operand
	local    bool // a binding read
}

type destKind int

const (
	dDiscard destKind = iota
	dAssign
	dReturn
)

type dest struct {
	kind destKind
	name string
}

var discard = dest{kind: dDiscard}

func (e *emitter) newFunc(fo *check.FuncObj) *fn {
	f := &fn{emitter: e, names: map[*check.Local]string{}, taken: map[string]bool{}, user: map[string]bool{}}
	for g := range e.globals {
		f.taken[g] = true
	}
	if fo != nil {
		f.ctx = &fnCtx{result: fo.Result}
		for _, p := range fo.Decl.Params {
			f.user[goName(p.Name.Name)] = true
		}
		syntax.Inspect(fo.Decl.Body, func(n syntax.Node) bool {
			if b, ok := n.(*syntax.BindPat); ok {
				f.user[goName(b.Name.Name)] = true
			}
			return true
		})
	}
	return f
}

// bind gives a binding its Go name: its own, or name_1, name_2 when that is
// taken by a shadowed binding.
func (f *fn) bind(l *check.Local) string {
	base := goName(l.Name)
	name := base
	for i := 1; f.taken[name]; i++ {
		if cand := fmt.Sprintf("%s_%d", base, i); !f.user[cand] {
			name = cand
		}
	}
	f.taken[name] = true
	f.names[l] = name
	return name
}

// temp makes a name for a temporary.
func (f *fn) temp(hint string) string {
	name := hint
	for i := 1; f.taken[name] || f.user[name]; i++ {
		name = fmt.Sprintf("%s%d", hint, i)
	}
	f.taken[name] = true
	return name
}

func (f *fn) nameOf(l *check.Local) string {
	if n, ok := f.names[l]; ok {
		return n
	}
	return goName(l.Name)
}

// capture runs lower with output going to a buffer, and returns what it wrote
// and its value.
func (f *fn) capture(lower func() value) (string, value) {
	saved := f.w
	b := &strings.Builder{}
	f.w = b
	v := lower()
	f.w = saved
	return b.String(), v
}

func (f *fn) typeOf(x syntax.Expr) check.Type { return f.info.Types[x] }

func isNeverType(t check.Type) bool { return check.Resolve(t) == check.Never }

// body lowers a function body.
func (f *fn) body(b *syntax.BlockExpr, result check.Type) {
	if noResult(result) {
		f.blockInto(b, discard)
		return
	}
	f.blockInto(b, dest{kind: dReturn})
}

// exprVal lowers an expression that needs no statements, such as a constant.
func (f *fn) exprVal(x syntax.Expr) string { return f.expr(x).s }

// operand returns v ready to be the operand of a selector, call or index.
func operand(v value) string {
	if v.op || v.concrete && strings.HasPrefix(v.s, "func") {
		return "(" + v.s + ")"
	}
	return v.s
}

// list lowers sibling expressions left to right. When a later one writes
// statements, earlier unstable values are first saved in temporaries, so the
// order of evaluation is kept.
func (f *fn) list(xs []syntax.Expr) ([]value, bool) {
	pres := make([]string, len(xs))
	vals := make([]value, len(xs))
	last := -1
	for i, x := range xs {
		pres[i], vals[i] = f.capture(func() value { return f.expr(x) })
		if pres[i] != "" {
			last = i
		}
		if vals[i].never {
			for j := 0; j < i; j++ {
				f.w.WriteString(pres[j])
				if !vals[j].stable {
					f.printf("_ = %s\n", vals[j].s)
				}
			}
			f.w.WriteString(pres[i])
			return nil, true
		}
	}
	for i, x := range xs {
		f.w.WriteString(pres[i])
		if i < last && !vals[i].stable {
			vals[i] = f.spill(vals[i], f.typeOf(x))
		}
	}
	return vals, false
}

// spill saves a value in a new temporary.
func (f *fn) spill(v value, t check.Type) value {
	name := f.temp("tmp")
	if v.concrete {
		f.printf("var %s %s = %s\n", name, f.goType(t), v.s)
	} else {
		f.printf("%s := %s\n", name, v.s)
	}
	return value{s: name, stable: true}
}

func joinVals(vs []value) string {
	parts := make([]string, len(vs))
	for i, v := range vs {
		parts[i] = v.s
	}
	return strings.Join(parts, ", ")
}

// into lowers x so that its value goes to d.
func (f *fn) into(x syntax.Expr, d dest) {
	switch x := x.(type) {
	case *syntax.ParenExpr:
		f.into(x.X, d)
		return
	case *syntax.BlockExpr:
		f.blockInto(x, d)
		return
	case *syntax.IfExpr:
		f.ifInto(x, d)
		return
	case *syntax.MatchExpr:
		f.matchInto(x, d)
		return
	}
	v := f.expr(x)
	if v.never || f.terminated {
		return
	}
	f.deliver(v, f.typeOf(x), d)
}

// deliver sends a value to a destination.
func (f *fn) deliver(v value, t check.Type, d dest) {
	switch d.kind {
	case dDiscard:
		switch {
		case v.call:
			f.printf("%s\n", v.s)
		case v.s == "struct{}{}":
		default:
			f.printf("_ = %s\n", v.s)
		}
	case dAssign:
		f.printf("%s = %s\n", d.name, v.s)
	case dReturn:
		if f.ctx.closure || !noResult(f.ctx.result) {
			f.printf("return %s\n", v.s)
		} else {
			f.deliver(v, t, discard)
			f.printf("return\n")
		}
		f.terminated = true
	}
}

// unitInto delivers () to d, for a block that has no value.
func (f *fn) unitInto(d dest) {
	switch d.kind {
	case dAssign:
		f.printf("%s = struct{}{}\n", d.name)
	case dReturn:
		if f.ctx.closure {
			f.printf("return struct{}{}\n")
		} else {
			f.printf("return\n")
		}
		f.terminated = true
	}
}

func hasTail(b *syntax.BlockExpr) bool {
	if len(b.Stmts) == 0 {
		return false
	}
	_, ok := b.Stmts[len(b.Stmts)-1].(*syntax.ExprStmt)
	return ok
}

// blockInto lowers a block. Statements after one that cannot finish are
// left out. Afterwards f.terminated tells whether the block terminates.
func (f *fn) blockInto(b *syntax.BlockExpr, d dest) {
	f.terminated = false
	for i, s := range b.Stmts {
		if f.terminated {
			break
		}
		f.line(s.Span().Start)
		if es, ok := s.(*syntax.ExprStmt); ok && i == len(b.Stmts)-1 && d.kind != dDiscard {
			f.into(es.X, d)
			continue
		}
		f.stmt(s)
	}
	if !f.terminated && d.kind != dDiscard && !hasTail(b) {
		f.unitInto(d)
	}
}

func (f *fn) stmt(s syntax.Stmt) {
	switch s := s.(type) {
	case *syntax.LetStmt:
		f.let(s)
	case *syntax.AssignStmt:
		f.assign(s)
	case *syntax.ExprStmt:
		f.into(s.X, discard)
		if isNeverType(f.typeOf(s.X)) && !f.terminated {
			// A call to a function that returns Never.
			f.printf("panic(\"unreachable\")\n")
			f.terminated = true
		}
	}
}

func isControl(x syntax.Expr) bool {
	switch x.(type) {
	case *syntax.IfExpr, *syntax.MatchExpr, *syntax.BlockExpr:
		return true
	}
	return false
}

func (f *fn) let(s *syntax.LetStmt) {
	switch p := s.Pat.(type) {
	case *syntax.WildcardPat:
		f.into(s.Value, discard)
	case *syntax.BindPat:
		l := f.info.Defs[p.Name]
		if isControl(s.Value) && !isNeverType(f.typeOf(s.Value)) {
			name := f.bind(l)
			f.printf("var %s %s\n", name, f.goType(l.Type))
			f.into(s.Value, dest{kind: dAssign, name: name})
			f.terminated = false
			f.unused(l, name)
			return
		}
		v := f.expr(s.Value)
		if v.never || f.terminated {
			return
		}
		name := f.bind(l)
		if v.concrete {
			f.printf("var %s %s = %s\n", name, f.goType(l.Type), v.s)
		} else {
			f.printf("%s := %s\n", name, v.s)
		}
		f.unused(l, name)
	case *syntax.TuplePat:
		if !bindsNames(p) {
			f.into(s.Value, discard)
			return
		}
		v := f.expr(s.Value)
		if v.never || f.terminated {
			return
		}
		base := v.s
		if !v.local {
			base = f.temp("tmp")
			f.printf("%s := %s\n", base, v.s)
		}
		f.destructure(p, base)
	}
}

// unused writes `_ = name` for a binding that is never read, so Go does not
// reject it.
func (f *fn) unused(l *check.Local, name string) {
	if !l.Used {
		f.printf("_ = %s\n", name)
	}
}

// destructure binds the names of an irrefutable tuple pattern from base.
func (f *fn) destructure(p syntax.Pattern, base string) {
	switch p := p.(type) {
	case *syntax.BindPat:
		l := f.info.Defs[p.Name]
		name := f.bind(l)
		f.printf("%s := %s\n", name, base)
		f.unused(l, name)
	case *syntax.TuplePat:
		for i, el := range p.Elems {
			f.destructure(el, fmt.Sprintf("%s.f%d", base, i))
		}
	}
}

func (f *fn) assign(s *syntax.AssignStmt) {
	v := f.expr(s.Value)
	if v.never || f.terminated {
		return
	}
	op := map[syntax.Kind]string{syntax.ASSIGN: "=", syntax.PLUSEQ: "+=", syntax.MINUSEQ: "-=", syntax.STAREQ: "*=", syntax.SLASHEQ: "/="}[s.Op]
	f.printf("%s %s %s\n", f.place(s.Target), op, v.s)
}

// place lowers an assignment target: a binding and its fields.
func (f *fn) place(x syntax.Expr) string {
	switch x := x.(type) {
	case *syntax.ParenExpr:
		return f.place(x.X)
	case *syntax.Ident:
		return f.nameOf(f.info.Uses[x].Local)
	case *syntax.FieldExpr:
		return f.place(x.X) + "." + f.fieldSel(x)
	}
	return "_"
}

func (f *fn) fieldSel(x *syntax.FieldExpr) string {
	fu := f.info.Fields[x]
	switch {
	case fu == nil:
		return x.Name.Name
	case fu.Field != nil:
		return fieldName(fu.Field)
	case fu.Sys != "":
		return fu.Sys
	}
	return fmt.Sprintf("f%d", fu.Tuple)
}

// ---- Control flow ----

func (f *fn) ifInto(x *syntax.IfExpr, d dest) {
	if x.Pat != nil {
		f.ifLetInto(x, d)
		return
	}
	c := f.expr(x.Cond)
	if c.never {
		return
	}
	f.ifRest(x, c.s, d)
}

func (f *fn) ifRest(x *syntax.IfExpr, cond string, d dest) {
	thenD := d
	if x.Else == nil {
		thenD = discard
	}
	f.printf("if %s {\n", cond)
	f.blockInto(x.Then, thenD)
	t1 := f.terminated
	f.terminated = false
	switch el := x.Else.(type) {
	case nil:
		f.printf("}\n")
		f.terminated = false
	case *syntax.IfExpr:
		if el.Pat != nil {
			f.printf("} else {\n")
			f.ifLetInto(el, d)
			t2 := f.terminated
			f.printf("}\n")
			f.terminated = t1 && t2
			return
		}
		pre, c := f.capture(func() value { return f.expr(el.Cond) })
		if pre == "" && !c.never {
			f.printf("} else ")
			f.ifRest(el, c.s, d)
			f.terminated = t1 && f.terminated
			return
		}
		f.printf("} else {\n")
		f.w.WriteString(pre)
		t2 := true
		if !c.never {
			f.ifRest(el, c.s, d)
			t2 = f.terminated
		}
		f.printf("}\n")
		f.terminated = t1 && t2
	case *syntax.BlockExpr:
		f.printf("} else {\n")
		f.blockInto(el, d)
		t2 := f.terminated
		f.printf("}\n")
		f.terminated = t1 && t2
	}
}

// ifLetInto lowers `if let p = x { a } else { b }` as the match it stands
// for, `match x { p => a, _ => b }`. Without an else, a's value is dropped.
func (f *fn) ifLetInto(x *syntax.IfExpr, d dest) {
	rest := x.Else
	if rest == nil {
		rest = &syntax.BlockExpr{Lbrace: x.Then.Rbrace, Rbrace: x.Then.Rbrace}
		d = discard
	}
	m := &syntax.MatchExpr{Match: x.If, Subject: x.Cond, Lbrace: x.Then.Lbrace, Rbrace: x.Then.Rbrace,
		Arms: []*syntax.MatchArm{{Pat: x.Pat, Body: x.Then}, {Pat: &syntax.WildcardPat{Under: x.If}, Body: rest}}}
	f.matchInto(m, d)
	if x.Else == nil {
		f.terminated = false
	}
}

func (f *fn) loopBody(b *syntax.BlockExpr) {
	f.blockInto(b, discard)
	f.terminated = false
}

func (f *fn) while(x *syntax.WhileExpr) {
	pre, c := f.capture(func() value { return f.expr(x.Cond) })
	if pre == "" && !c.never {
		f.printf("for %s {\n", c.s)
	} else {
		f.printf("for {\n")
		f.w.WriteString(pre)
		if c.never {
			f.printf("}\n")
			f.terminated = true
			return
		}
		if c.op {
			f.printf("if !(%s) {\nbreak\n}\n", c.s)
		} else {
			f.printf("if !%s {\nbreak\n}\n", c.s)
		}
	}
	f.loopBody(x.Body)
	f.printf("}\n")
	f.terminated = false
}

func (f *fn) loop(x *syntax.LoopExpr) {
	f.printf("for {\n")
	f.loopBody(x.Body)
	f.printf("}\n")
	f.terminated = isNeverType(f.typeOf(x))
}

func (f *fn) forLoop(x *syntax.ForExpr) {
	if x.End != nil {
		vals, never := f.list([]syntax.Expr{x.Iter, x.End})
		if never {
			return
		}
		end := vals[1].s
		if !vals[1].stable {
			end = f.temp("end")
			f.printf("%s := %s\n", end, vals[1].s)
		}
		var name string
		if bp, ok := x.Pat.(*syntax.BindPat); ok {
			name = f.bind(f.info.Defs[bp.Name])
		} else {
			name = f.temp("i")
		}
		cmp := "<"
		if x.Inclusive {
			cmp = "<="
		}
		f.printf("for %s := %s; %s %s %s; %s++ {\n", name, vals[0].s, name, cmp, end, name)
		f.loopBody(x.Body)
		f.printf("}\n")
		f.terminated = false
		return
	}
	it := f.expr(x.Iter)
	if it.never {
		return
	}
	if _, ok := check.Deep(f.typeOf(x.Iter)).(*check.Map); ok {
		f.forMap(x, it)
		return
	}
	switch p := x.Pat.(type) {
	case *syntax.BindPat:
		l := f.info.Defs[p.Name]
		if l.Used {
			f.printf("for _, %s := range %s {\n", f.bind(l), it.s)
		} else {
			f.printf("for range %s {\n", it.s)
		}
	case *syntax.TuplePat:
		if !bindsNames(p) {
			f.printf("for range %s {\n", it.s)
			break
		}
		tmp := f.temp("item")
		f.printf("for _, %s := range %s {\n", tmp, it.s)
		f.destructure(p, tmp)
	default:
		f.printf("for range %s {\n", it.s)
	}
	f.loopBody(x.Body)
	f.printf("}\n")
	f.terminated = false
}

// forMap lowers a for loop over a Map: over its keys in order, reading each
// value by its position. The map is the value it had when the loop started.
func (f *fn) forMap(x *syntax.ForExpr, it value) {
	m := it.s
	if !it.local || !it.stable {
		m = f.temp("m")
		f.printf("%s := %s\n", m, it.s)
	}
	idx, key := "_", "_"
	var bind []func()
	switch p := x.Pat.(type) {
	case *syntax.BindPat:
		if l := f.info.Defs[p.Name]; l.Used {
			idx, key = f.temp("i"), f.temp("k")
			bind = append(bind, func() {
				f.printf("%s := %s{%s, %s.vals[%s]}\n", f.bind(l), f.goType(l.Type), key, m, idx)
			})
		}
	case *syntax.TuplePat:
		switch kp := p.Elems[0].(type) {
		case *syntax.BindPat:
			if l := f.info.Defs[kp.Name]; l.Used {
				key = f.bind(l)
			}
		default:
			if bindsNames(kp) {
				key = f.temp("k")
				bind = append(bind, func() { f.destructure(kp, key) })
			}
		}
		if vp := p.Elems[1]; bindsNames(vp) {
			idx = f.temp("i")
			bind = append(bind, func() { f.destructure(vp, m+".vals["+idx+"]") })
		}
	}
	switch {
	case idx == "_" && key == "_":
		f.printf("for range %s.keys {\n", m)
	case idx == "_":
		f.printf("for _, %s := range %s.keys {\n", key, m)
	case key == "_":
		f.printf("for %s := range %s.keys {\n", idx, m)
	default:
		f.printf("for %s, %s := range %s.keys {\n", idx, key, m)
	}
	for _, b := range bind {
		b()
	}
	f.loopBody(x.Body)
	f.printf("}\n")
	f.terminated = false
}

// controlValue lowers an if, match or block whose value is needed.
func (f *fn) controlValue(x syntax.Expr) value {
	t := f.typeOf(x)
	if isNeverType(t) {
		f.into(x, discard)
		f.terminated = true
		return value{never: true}
	}
	name := f.temp("tmp")
	f.printf("var %s %s\n", name, f.goType(t))
	f.into(x, dest{kind: dAssign, name: name})
	f.terminated = false
	return value{s: name, stable: true}
}

// ---- Expressions ----

// expr lowers x, writing the statements it needs, and returns its value.
func (f *fn) expr(x syntax.Expr) value {
	switch x := x.(type) {
	case *syntax.IntLit:
		return value{s: f.intLit(x.Text, f.typeOf(x), false), stable: true}
	case *syntax.FloatLit:
		return value{s: x.Text, stable: true}
	case *syntax.StringLit:
		return value{s: strconv.Quote(x.Value), stable: true}
	case *syntax.InterpLit:
		return f.interp(x)
	case *syntax.BoolLit:
		return value{s: strconv.FormatBool(x.Value), stable: true}
	case *syntax.UnitLit:
		return value{s: "struct{}{}", stable: true}
	case *syntax.Ident:
		return f.ident(x)
	case *syntax.PathExpr:
		return f.path(x)
	case *syntax.ParenExpr:
		v := f.expr(x.X)
		if v.never {
			return v
		}
		v.s = "(" + v.s + ")"
		v.op = false
		return v
	case *syntax.CallExpr:
		return f.call(x)
	case *syntax.MethodCallExpr:
		return f.methodCall(x)
	case *syntax.FieldExpr:
		v := f.expr(x.X)
		if v.never {
			return v
		}
		return value{s: operand(v) + "." + f.fieldSel(x), stable: v.stable}
	case *syntax.IndexExpr:
		vals, never := f.list([]syntax.Expr{x.X, x.Index})
		if never {
			return value{never: true}
		}
		return value{s: operand(vals[0]) + "[" + vals[1].s + "]"}
	case *syntax.TryExpr:
		return f.try(x)
	case *syntax.UnaryExpr:
		return f.unary(x)
	case *syntax.AsExpr:
		return f.cast(x)
	case *syntax.BinaryExpr:
		return f.binary(x)
	case *syntax.TupleExpr:
		vals, never := f.list(x.Elems)
		if never {
			return value{never: true}
		}
		return value{s: f.goType(f.typeOf(x)) + "{" + joinVals(vals) + "}"}
	case *syntax.ListExpr:
		vals, never := f.list(x.Elems)
		if never {
			return value{never: true}
		}
		return value{s: f.goType(f.typeOf(x)) + "{" + joinVals(vals) + "}"}
	case *syntax.StructLit:
		return f.structLit(x)
	case *syntax.BlockExpr, *syntax.IfExpr, *syntax.MatchExpr:
		return f.controlValue(x)
	case *syntax.WhileExpr:
		f.while(x)
		return value{s: "struct{}{}", stable: true}
	case *syntax.LoopExpr:
		f.loop(x)
		if f.terminated {
			return value{never: true}
		}
		return value{s: "struct{}{}", stable: true}
	case *syntax.ForExpr:
		f.forLoop(x)
		return value{s: "struct{}{}", stable: true}
	case *syntax.ClosureExpr:
		return f.closure(x)
	case *syntax.ReturnExpr:
		f.ret(x)
		return value{never: true}
	case *syntax.BreakExpr:
		f.printf("break\n")
		f.terminated = true
		return value{never: true}
	case *syntax.ContinueExpr:
		f.printf("continue\n")
		f.terminated = true
		return value{never: true}
	}
	return value{s: "struct{}{}"}
}

// interp lowers "a ${x} b" to a concatenation, "a " + strconv.Itoa(x) + " b",
// evaluating the interpolated expressions in order.
func (f *fn) interp(x *syntax.InterpLit) value {
	vals, never := f.list(x.Exprs)
	if never {
		return value{never: true}
	}
	var pieces []string
	stable, op := true, false
	for i, part := range x.Parts {
		if part != "" {
			pieces = append(pieces, strconv.Quote(part))
		}
		if i < len(vals) {
			pieces = append(pieces, f.text(vals[i], f.typeOf(x.Exprs[i])))
			stable = stable && vals[i].stable
			op = vals[i].op && check.Resolve(f.typeOf(x.Exprs[i])) == check.String
		}
	}
	switch len(pieces) {
	case 0:
		return value{s: `""`, stable: true}
	case 1:
		return value{s: pieces[0], stable: stable, op: op}
	}
	return value{s: strings.Join(pieces, " + "), stable: stable, op: true}
}

// text converts a value of type Int, Float, Bool, Byte or String to a
// String, as its to_string method does.
func (f *fn) text(v value, t check.Type) string {
	conv := ""
	switch check.Resolve(t) {
	case check.Int:
		conv = "strconv.Itoa(" + v.s + ")"
	case check.Float:
		conv = "strconv.FormatFloat(" + v.s + ", 'g', -1, 64)"
	case check.Bool:
		conv = "strconv.FormatBool(" + v.s + ")"
	case check.Byte:
		conv = "strconv.Itoa(int(" + v.s + "))"
	default:
		return v.s
	}
	f.imports["strconv"] = true
	return conv
}

// intLit writes an integer literal of type Int, Float or Byte.
func (f *fn) intLit(text string, t check.Type, neg bool) string {
	v, _ := check.ParseIntLit(text)
	if neg {
		v = new(big.Int).Neg(v)
	}
	switch check.Resolve(t) {
	case check.Float:
		return v.String() + ".0"
	case check.Byte:
		return "byte(" + v.String() + ")"
	}
	return v.String()
}

func (f *fn) ident(x *syntax.Ident) value {
	use := f.info.Uses[x]
	if use == nil {
		return value{s: goName(x.Name)}
	}
	switch use.Kind {
	case check.UseLocal:
		return value{s: f.nameOf(use.Local), stable: !use.Local.Mutable, local: true}
	case check.UseConst:
		return value{s: use.Const.Name, stable: true}
	case check.UseFunc:
		return f.funcValue(goName(use.Func.Name), use.Func, use.TypeArgs, f.typeOf(x))
	case check.UseNone:
		return value{s: "None[" + f.goType(use.TypeArgs[0]) + "]()", stable: true}
	case check.UseSome:
		return value{s: "Some[" + f.goType(use.TypeArgs[0]) + "]", stable: true}
	case check.UseOk:
		return value{s: "Ok" + typeArgs(f.typeList(use.TypeArgs)), stable: true}
	case check.UseErr:
		return value{s: "Err" + typeArgs(f.typeList(use.TypeArgs)), stable: true}
	case check.UseError:
		f.imports["errors"] = true
		return value{s: "errors.New", stable: true}
	}
	return value{s: goName(x.Name)}
}

// funcValue is a declared function used as a value. A function without a Go
// result is wrapped so that, like every function value, it returns struct{}.
func (f *fn) funcValue(name string, fo *check.FuncObj, targs []check.Type, t check.Type) value {
	name += typeArgs(f.typeList(targs))
	if !noResult(fo.Result) {
		return value{s: name, stable: true}
	}
	ft := check.Deep(t).(*check.Func)
	var params, args []string
	for i, p := range ft.Params {
		params = append(params, fmt.Sprintf("p%d %s", i, f.goType(p)))
		args = append(args, fmt.Sprintf("p%d", i))
	}
	end := "return struct{}{}"
	if check.Resolve(fo.Result) == check.Never {
		end = "panic(\"unreachable\")"
	}
	return value{s: fmt.Sprintf("func(%s) struct{} {\n%s(%s)\n%s\n}", strings.Join(params, ", "), name, strings.Join(args, ", "), end), stable: true}
}

func (f *fn) path(x *syntax.PathExpr) value {
	use := f.info.Uses[x]
	switch use.Kind {
	case check.UseVariant:
		v := use.Variant
		if v.Enum.Fieldless() {
			return value{s: v.Enum.Name + "_" + v.Name, stable: true}
		}
		vt := f.variantType(v, use.TypeArgs)
		if len(v.Fields) == 0 {
			return value{s: vt + "{}", stable: true, concrete: true}
		}
		// A variant constructor used as a function.
		ft := check.Deep(f.typeOf(x)).(*check.Func)
		var params, inits []string
		for i, p := range ft.Params {
			params = append(params, fmt.Sprintf("p%d %s", i, f.goType(p)))
			inits = append(inits, fmt.Sprintf("%s: p%d", fieldName(v.Fields[i]), i))
		}
		return value{s: fmt.Sprintf("func(%s) %s {\nreturn %s{%s}\n}", strings.Join(params, ", "), f.goType(ft.Result), vt, strings.Join(inits, ", ")), stable: true}
	case check.UseAssoc:
		return f.funcValue(use.Func.Recv.Name+"_"+use.Func.Name, use.Func, use.TypeArgs, f.typeOf(x))
	case check.UseMapNew:
		t := "Map" + typeArgs(f.typeList(use.TypeArgs))
		return value{s: "func() " + t + " {\nreturn " + t + "{}\n}", stable: true}
	}
	return value{s: "nil"}
}

// callResult finishes a call to a declared function: one without a Go result
// stands as a statement and has the value ().
func (f *fn) callResult(call string, result check.Type) value {
	switch check.Resolve(result) {
	case check.Unit:
		f.printf("%s\n", call)
		return value{s: "struct{}{}", stable: true}
	case check.Never:
		f.printf("%s\npanic(\"unreachable\")\n", call)
		f.terminated = true
		return value{never: true}
	}
	return value{s: call, call: true}
}

func (f *fn) call(x *syntax.CallExpr) value {
	use := f.info.Uses[unparen(x.Fun)]
	if use != nil && use.Kind != check.UseLocal {
		vals, never := f.list(x.Args)
		if never {
			return value{never: true}
		}
		args := joinVals(vals)
		switch use.Kind {
		case check.UseFunc:
			return f.callResult(goName(use.Func.Name)+typeArgs(f.typeList(use.TypeArgs))+"("+args+")", use.Func.Result)
		case check.UseAssoc:
			fo := use.Func
			return f.callResult(fo.Recv.Name+"_"+fo.Name+typeArgs(f.typeList(use.TypeArgs))+"("+args+")", fo.Result)
		case check.UseVariant:
			v := use.Variant
			var inits []string
			for i, a := range vals {
				if v.Named {
					inits = append(inits, fieldName(v.Fields[i])+": "+a.s)
				} else {
					inits = append(inits, a.s)
				}
			}
			return value{s: f.variantType(v, use.TypeArgs) + "{" + strings.Join(inits, ", ") + "}", concrete: true}
		case check.UseMapNew:
			return value{s: "Map" + typeArgs(f.typeList(use.TypeArgs)) + "{}", stable: true}
		case check.UseSome:
			return value{s: "Some[" + f.goType(use.TypeArgs[0]) + "](" + args + ")"}
		case check.UseOk:
			return value{s: "Ok" + typeArgs(f.typeList(use.TypeArgs)) + "(" + args + ")"}
		case check.UseErr:
			return value{s: "Err" + typeArgs(f.typeList(use.TypeArgs)) + "(" + args + ")"}
		case check.UseError:
			f.imports["errors"] = true
			return value{s: "errors.New(" + args + ")"}
		case check.UsePanic:
			f.printf("panic(%s)\n", args)
			f.terminated = true
			return value{never: true}
		case check.UseTodo:
			f.printf("panic(\"not yet implemented\")\n")
			f.terminated = true
			return value{never: true}
		}
	}
	vals, never := f.list(append([]syntax.Expr{x.Fun}, x.Args...))
	if never {
		return value{never: true}
	}
	return value{s: operand(vals[0]) + "(" + joinVals(vals[1:]) + ")", call: true}
}

func unparen(x syntax.Expr) syntax.Expr {
	for {
		p, ok := x.(*syntax.ParenExpr)
		if !ok {
			return x
		}
		x = p.X
	}
}

func (f *fn) methodCall(x *syntax.MethodCallExpr) value {
	mu := f.info.Methods[x]
	vals, never := f.list(append([]syntax.Expr{x.Recv}, x.Args...))
	if never {
		return value{never: true}
	}
	recv, args := vals[0], vals[1:]
	if fo := mu.Func; fo != nil {
		if !fo.Recv.IsEnum {
			return f.callResult(operand(recv)+"."+goName(fo.Name)+typeArgs(f.typeList(mu.TypeArgs))+"("+joinVals(args)+")", fo.Result)
		}
		targs := append(append([]check.Type{}, mu.RecvArgs...), mu.TypeArgs...)
		all := append([]value{recv}, args...)
		return f.callResult(fo.Recv.Name+"_"+fo.Name+typeArgs(f.typeList(targs))+"("+joinVals(all)+")", fo.Result)
	}
	return f.builtin(mu, recv, args)
}

func (f *fn) builtin(mu *check.MethodUse, recv value, args []value) value {
	r := operand(recv)
	ta := typeArgs(f.typeList(mu.TypeArgs))
	call := func(name string) value {
		return value{s: name + ta + "(" + joinVals(append([]value{recv}, args...)) + ")"}
	}
	switch mu.Builtin {
	case "list.len":
		return value{s: "len(" + recv.s + ")"}
	case "list.get":
		return call("listGet")
	case "list.push":
		return call("listPush")
	case "list.map":
		return call("listMap")
	case "list.filter":
		return call("listFilter")
	case "list.fold":
		return call("listFold")
	case "list.is_empty":
		return value{s: "len(" + recv.s + ") == 0", op: true}
	case "list.first":
		return call("listFirst")
	case "list.last":
		return call("listLast")
	case "list.set":
		return call("listSet")
	case "list.concat":
		return call("listConcat")
	case "list.slice":
		return call("listSlice")
	case "list.reverse":
		return call("listReverse")
	case "list.any":
		return call("listAny")
	case "list.all":
		return call("listAll")
	case "list.find":
		return call("listFind")
	case "list.enumerate":
		f.helper("listEnumerate")
		return call("listEnumerate")
	case "list.contains":
		return call("listContains")
	case "list.index_of":
		return call("listIndexOf")
	case "list.sort":
		return call("listSort")
	case "list.min":
		return call("listMin")
	case "list.max":
		return call("listMax")
	case "list.sum":
		return call("listSum")
	case "list.join":
		return f.pkgCall("strings", "Join", recv, args)
	case "map.len":
		return value{s: "len(" + r + ".keys)"}
	case "map.is_empty":
		return value{s: "len(" + r + ".keys) == 0", op: true}
	case "map.get":
		return call("mapGet")
	case "map.contains_key":
		return call("mapContainsKey")
	case "map.insert":
		return call("mapInsert")
	case "map.remove":
		return call("mapRemove")
	case "map.keys":
		return value{s: r + ".keys", stable: recv.stable}
	case "map.values":
		return value{s: r + ".vals", stable: recv.stable}
	case "map.entries":
		f.helper("mapEntries")
		return call("mapEntries")
	case "option.is_some":
		return value{s: r + ".some"}
	case "option.is_none":
		return value{s: "!" + r + ".some", op: true}
	case "option.unwrap_or":
		return call("optionUnwrapOr")
	case "option.map":
		return call("optionMap")
	case "option.and_then":
		return call("optionAndThen")
	case "option.ok_or":
		return call("optionOkOr")
	case "option.unwrap":
		return call("optionUnwrap")
	case "option.expect":
		return call("optionExpect")
	case "result.is_ok":
		return value{s: r + ".ok"}
	case "result.is_err":
		return value{s: "!" + r + ".ok", op: true}
	case "result.unwrap_or":
		return call("resultUnwrapOr")
	case "result.map":
		return call("resultMap")
	case "result.map_err":
		return call("resultMapErr")
	case "result.and_then":
		return call("resultAndThen")
	case "result.ok":
		return call("resultOk")
	case "result.unwrap":
		return call("resultUnwrap")
	case "result.expect":
		return call("resultExpect")
	case "error.message":
		return value{s: r + ".Error()"}
	case "error.context":
		f.imports["fmt"] = true
		if !recv.stable && !args[0].stable {
			recv = f.spill(recv, check.ErrorT)
		}
		return value{s: fmt.Sprintf("fmt.Errorf(\"%%s: %%w\", %s, %s)", args[0].s, recv.s)}
	case "int.to_string":
		f.imports["strconv"] = true
		return value{s: "strconv.Itoa(" + recv.s + ")"}
	case "float.to_string":
		f.imports["strconv"] = true
		return value{s: "strconv.FormatFloat(" + recv.s + ", 'g', -1, 64)"}
	case "bool.to_string":
		f.imports["strconv"] = true
		return value{s: "strconv.FormatBool(" + recv.s + ")"}
	case "byte.to_string":
		f.imports["strconv"] = true
		return value{s: "strconv.Itoa(int(" + recv.s + "))"}
	case "string.to_string":
		return recv
	case "string.parse_int":
		return value{s: "parseInt(" + recv.s + ")"}
	case "string.parse_float":
		return value{s: "parseFloat(" + recv.s + ")"}
	case "string.len":
		return value{s: "len(" + recv.s + ")"}
	case "string.is_empty":
		return value{s: r + ` == ""`, op: true}
	case "string.contains":
		return f.pkgCall("strings", "Contains", recv, args)
	case "string.starts_with":
		return f.pkgCall("strings", "HasPrefix", recv, args)
	case "string.ends_with":
		return f.pkgCall("strings", "HasSuffix", recv, args)
	case "string.find":
		return call("stringFind")
	case "string.split":
		return f.pkgCall("strings", "Split", recv, args)
	case "string.lines":
		return call("stringLines")
	case "string.trim":
		return f.pkgCall("strings", "TrimSpace", recv, args)
	case "string.to_upper":
		return f.pkgCall("strings", "ToUpper", recv, args)
	case "string.to_lower":
		return f.pkgCall("strings", "ToLower", recv, args)
	case "string.replace":
		return f.pkgCall("strings", "ReplaceAll", recv, args)
	case "string.repeat":
		return f.pkgCall("strings", "Repeat", recv, args)
	case "string.slice":
		return call("stringSlice")
	case "int.abs":
		return call("intAbs")
	case "int.min":
		return value{s: "min(" + joinVals(append([]value{recv}, args...)) + ")"}
	case "int.max":
		return value{s: "max(" + joinVals(append([]value{recv}, args...)) + ")"}
	case "float.abs":
		return f.pkgCall("math", "Abs", recv, args)
	case "float.floor":
		return f.pkgCall("math", "Floor", recv, args)
	case "float.ceil":
		return f.pkgCall("math", "Ceil", recv, args)
	case "float.round":
		return f.pkgCall("math", "Round", recv, args)
	case "float.sqrt":
		return f.pkgCall("math", "Sqrt", recv, args)
	case "out.print", "out.println":
		f.printf("%s.%s(%s)\n", r, strings.TrimPrefix(mu.Builtin, "out."), joinVals(args))
		return value{s: "struct{}{}", stable: true}
	}
	return value{s: "nil"}
}

// pkgCall lowers a built-in method to a call of a Go standard library
// function that takes the receiver first.
func (f *fn) pkgCall(pkg, name string, recv value, args []value) value {
	f.imports[pkg] = true
	return value{s: pkg + "." + name + "(" + joinVals(append([]value{recv}, args...)) + ")"}
}

func (f *fn) try(x *syntax.TryExpr) value {
	v := f.expr(x.X)
	if v.never {
		return v
	}
	info := f.info.Tries[x]
	name := v.s
	if !v.local || !v.stable {
		hint := "res"
		if info.Option {
			hint = "opt"
		}
		name = f.temp(hint)
		f.printf("%s := %s\n", name, v.s)
	}
	outer := check.Deep(info.Outer)
	if info.Option {
		elem := outer.(*check.Option).Elem
		f.printf("if !%s.some {\nreturn None[%s]()\n}\n", name, f.goType(elem))
	} else {
		res := outer.(*check.Result)
		f.printf("if !%s.ok {\nreturn Err[%s, %s](%s.err)\n}\n", name, f.goType(res.Ok), f.goType(res.Err), name)
	}
	return value{s: name + ".value", stable: true}
}

func (f *fn) ret(x *syntax.ReturnExpr) {
	switch {
	case x.Value == nil:
		f.unitInto(dest{kind: dReturn})
	case noResult(f.ctx.result):
		f.into(x.Value, discard)
		if !f.terminated {
			f.unitInto(dest{kind: dReturn})
		}
	default:
		f.into(x.Value, dest{kind: dReturn})
	}
	f.terminated = true
}

func (f *fn) unary(x *syntax.UnaryExpr) value {
	if lit, ok := x.X.(*syntax.IntLit); ok && x.Op == syntax.MINUS {
		return value{s: f.intLit(lit.Text, f.typeOf(x), true), stable: true, op: true}
	}
	v := f.expr(x.X)
	if v.never {
		return v
	}
	op := "-"
	if x.Op == syntax.NOT {
		op = "!"
	}
	s := v.s
	if v.op || strings.HasPrefix(s, "-") {
		s = "(" + s + ")"
	}
	return value{s: op + s, stable: v.stable, op: true}
}

// cast lowers `x as T`. Float to Int or Byte goes through the runtime,
// which truncates toward zero and saturates: Go's own conversion gives an
// implementation-dependent value out of range. A conversion Go would reject
// in a constant (a fraction dropped, a Byte overflowed) is written folded.
func (f *fn) cast(x *syntax.AsExpr) value {
	ci := f.info.Casts[x]
	from, to := check.Resolve(ci.From), check.Resolve(ci.To)
	if k := ci.Const; k != nil && (from == check.Float && to != check.Float || from == check.Int && to == check.Byte) {
		if to == check.Byte {
			return value{s: "byte(" + k.Int.String() + ")", stable: true}
		}
		return value{s: k.Int.String(), stable: true, op: k.Int.Sign() < 0}
	}
	v := f.expr(x.X)
	if v.never || from == to {
		return v
	}
	var conv string
	switch {
	case to == check.Float:
		conv = "float64"
	case from == check.Float && to == check.Int:
		conv = "floatToInt"
	case from == check.Float:
		conv = "floatToByte"
	case to == check.Int:
		conv = "int"
	default:
		conv = "byte"
	}
	return value{s: conv + "(" + v.s + ")", stable: v.stable}
}

var goOps = map[syntax.Kind]string{
	syntax.PLUS: "+", syntax.MINUS: "-", syntax.STAR: "*", syntax.SLASH: "/", syntax.PERCENT: "%",
	syntax.EQ: "==", syntax.NE: "!=", syntax.LT: "<", syntax.LE: "<=", syntax.GT: ">", syntax.GE: ">=",
	syntax.ANDAND: "&&", syntax.OROR: "||",
}

// isConst reports whether x lowers to a Go constant expression.
func (f *fn) isConst(x syntax.Expr) bool {
	switch x := x.(type) {
	case *syntax.IntLit, *syntax.FloatLit, *syntax.StringLit, *syntax.BoolLit:
		return true
	case *syntax.Ident:
		u := f.info.Uses[x]
		return u != nil && u.Kind == check.UseConst
	case *syntax.ParenExpr:
		return f.isConst(x.X)
	case *syntax.UnaryExpr:
		return f.isConst(x.X)
	case *syntax.AsExpr:
		return f.info.Casts[x].Const != nil
	case *syntax.BinaryExpr:
		return f.isConst(x.X) && f.isConst(x.Y) && !(x.Op == syntax.PERCENT && check.Resolve(f.typeOf(x)) == check.Float)
	}
	return false
}

func (f *fn) binary(x *syntax.BinaryExpr) value {
	if x.Op == syntax.ANDAND || x.Op == syntax.OROR {
		l := f.expr(x.X)
		if l.never {
			return l
		}
		pre, r := f.capture(func() value { return f.expr(x.Y) })
		if pre == "" && !r.never {
			return value{s: l.s + " " + goOps[x.Op] + " " + r.s, op: true, stable: l.stable && r.stable}
		}
		// The right side needs statements: run them only when it is evaluated.
		name := f.temp("cond")
		f.printf("%s := %s\n", name, l.s)
		if x.Op == syntax.ANDAND {
			f.printf("if %s {\n", name)
		} else {
			f.printf("if !%s {\n", name)
		}
		f.w.WriteString(pre)
		if !r.never {
			f.printf("%s = %s\n", name, r.s)
		}
		f.printf("}\n")
		f.terminated = false
		return value{s: name, stable: true}
	}
	vals, never := f.list([]syntax.Expr{x.X, x.Y})
	if never {
		return value{never: true}
	}
	l, r := vals[0], vals[1]
	operandT := check.Resolve(f.typeOf(x.X))
	if operandT == check.Never {
		operandT = check.Resolve(f.typeOf(x.Y))
	}
	if x.Op == syntax.PERCENT && operandT == check.Float {
		f.imports["math"] = true
		return value{s: "math.Mod(" + l.s + ", " + r.s + ")"}
	}
	ls, rs := l.s, r.s
	if named, ok := operandT.(*check.Named); ok && named.Decl.IsEnum && (x.Op == syntax.EQ || x.Op == syntax.NE) {
		// Compare as the enum's interface type, not as variant structs.
		if l.concrete {
			ls = f.goType(named) + "(" + ls + ")"
		}
		if r.concrete {
			rs = f.goType(named) + "(" + rs + ")"
		}
	}
	if operandT == check.Float && f.isConst(x.X) && f.isConst(x.Y) && !strings.HasPrefix(ls, "float64(") {
		// Go folds constant expressions exactly; a typed operand makes it round
		// each step to float64, as Riddim's Float arithmetic does.
		ls = "float64(" + ls + ")"
	}
	return value{s: ls + " " + goOps[x.Op] + " " + rs, op: true, stable: l.stable && r.stable}
}

func (f *fn) structLit(x *syntax.StructLit) value {
	su := f.info.Structs[x]
	xs := make([]syntax.Expr, 0, len(x.Fields)+1)
	for _, fi := range x.Fields {
		xs = append(xs, fi.Value)
	}
	if x.Base != nil {
		xs = append(xs, x.Base)
	}
	vals, never := f.list(xs)
	if never {
		return value{never: true}
	}
	typ := su.Decl.Name + typeArgs(f.typeList(su.TypeArgs))
	if x.Base == nil {
		inits := make([]string, len(x.Fields))
		for i := range x.Fields {
			inits[i] = fieldName(su.Fields[i]) + ": " + vals[i].s
		}
		return value{s: typ + "{" + strings.Join(inits, ", ") + "}"}
	}
	// The fields are evaluated before the base, as written.
	base := vals[len(vals)-1]
	for i := range x.Fields {
		if !vals[i].stable && !base.stable {
			vals[i] = f.spill(vals[i], f.typeOf(x.Fields[i].Value))
		}
	}
	name := f.temp("tmp")
	f.printf("%s := %s\n", name, base.s)
	for i := range x.Fields {
		f.printf("%s.%s = %s\n", name, fieldName(su.Fields[i]), vals[i].s)
	}
	return value{s: name, stable: true}
}

func (f *fn) closure(x *syntax.ClosureExpr) value {
	ci := f.info.Closures[x]
	saved := map[*check.Local]string{}
	for _, l := range ci.Captures {
		name := f.bindCopy(l)
		f.printf("%s := %s\n", name, f.nameOf(l))
		saved[l] = f.names[l]
		f.names[l] = name
	}
	var params []string
	var tuples []func()
	for i, p := range x.Params {
		t := f.goType(ci.Type.Params[i])
		switch pp := p.Pat.(type) {
		case *syntax.BindPat:
			params = append(params, f.bind(f.info.Defs[pp.Name])+" "+t)
		case *syntax.TuplePat:
			name := f.temp("arg")
			params = append(params, name+" "+t)
			tuples = append(tuples, func() { f.destructure(pp, name) })
		default:
			params = append(params, "_ "+t)
		}
	}
	savedCtx, savedTerm := f.ctx, f.terminated
	f.ctx = &fnCtx{result: ci.Type.Result, closure: true}
	body, _ := f.capture(func() value {
		for _, t := range tuples {
			t()
		}
		if noResult(ci.Type.Result) {
			f.into(x.Body, discard)
			if !f.terminated {
				f.printf("return struct{}{}\n")
			}
		} else {
			f.into(x.Body, dest{kind: dReturn})
		}
		return value{}
	})
	f.ctx, f.terminated = savedCtx, savedTerm
	for l, n := range saved {
		f.names[l] = n
	}
	return value{s: fmt.Sprintf("func(%s) %s {\n%s}", strings.Join(params, ", "), f.goType(ci.Type.Result), body), stable: true}
}

// bindCopy names the copy a closure makes of a captured `let mut` binding.
func (f *fn) bindCopy(l *check.Local) string {
	base := goName(l.Name)
	for i := 1; ; i++ {
		cand := fmt.Sprintf("%s_%d", base, i)
		if !f.taken[cand] && !f.user[cand] {
			f.taken[cand] = true
			return cand
		}
	}
}
