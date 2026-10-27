package check

import (
	"fmt"

	"github.com/riddim-hq/riddim/internal/diag"
	"github.com/riddim-hq/riddim/internal/syntax"
)

// block checks a block. Its value is that of its last statement when that is
// an expression; with discard set, that value is dropped like any statement's
// and the block has type (). A block that cannot finish has type Never.
func (c *checker) block(b *syntax.BlockExpr, want Type, discard bool) Type {
	c.pushScope()
	defer c.popScope()
	savedDead := c.dead
	defer func() { c.dead = savedDead }()
	diverged := false
	for i, s := range b.Stmts {
		last := i == len(b.Stmts)-1
		if es, ok := s.(*syntax.ExprStmt); ok && last && !discard {
			mark := len(c.vars)
			t := c.expr(es.X, want)
			c.endStmt(mark)
			if diverged {
				return Never
			}
			if want != nil && !isInvalid(t) {
				return want
			}
			return t
		}
		if isNever(c.stmt(s)) {
			diverged = true
			c.dead = true
		}
	}
	if diverged {
		return Never
	}
	if want != nil && !discard && !isUnit(want) && !isInvalid(want) {
		if _, isVar := Resolve(want).(*Var); !isVar {
			d := c.errorf("R0301", syntax.Span{Start: b.Rbrace, End: b.Rbrace + 1}, "expected a value of type %s, but the block ends without one", Deep(want))
			d.Notes = append(d.Notes, "the last line of a block is its value")
			return invalid
		}
	}
	return Unit
}

// stmt checks a statement and returns its type: Never when it cannot finish.
func (c *checker) stmt(s syntax.Stmt) Type {
	mark := len(c.vars)
	defer c.endStmt(mark)
	switch s := s.(type) {
	case *syntax.LetStmt:
		return c.let(s)
	case *syntax.AssignStmt:
		c.assign(s)
		return Unit
	case *syntax.ExprStmt:
		return c.discard(s.X)
	}
	return Unit
}

// discard checks an expression whose value is not used. A Result must not be
// dropped this way (R0502).
func (c *checker) discard(e syntax.Expr) Type {
	var t Type
	switch x := e.(type) {
	case *syntax.IfExpr:
		t = c.ifExpr(x, nil, true)
	case *syntax.MatchExpr:
		t = c.match(x, nil, true)
	case *syntax.BlockExpr:
		t = c.block(x, nil, true)
	default:
		t = c.expr(e, nil)
		if isResult(t) {
			c.unusedResult(e)
		}
		return t
	}
	c.info.Types[e] = t
	return t
}

func (c *checker) unusedResult(e syntax.Expr) {
	d := c.errorf("R0502", e.Span(), "this Result is not used: handle it with ? or match, or discard it on purpose with `let _ = ...`")
	t := Resolve(c.info.Types[e]).(*Result)
	if c.fn != nil {
		if o, ok := Resolve(c.fn.result).(*Result); ok && c.assignable(t.Err, o.Err) {
			end := e.Span().End
			d.Fixes = append(d.Fixes, diag.Fix{Title: "return the error with ?",
				Edits: []diag.Edit{c.fset.Edit(syntax.Span{Start: end, End: end}, "?")}})
		}
	}
	start := e.Span().Start
	d.Fixes = append(d.Fixes, diag.Fix{Title: "discard it on purpose",
		Edits: []diag.Edit{c.fset.Edit(syntax.Span{Start: start, End: start}, "let _ = ")}})
}

func (c *checker) let(s *syntax.LetStmt) Type {
	var ann Type
	if s.Type != nil {
		ann = c.resolveType(s.Type)
	}
	t := c.expr(s.Value, ann)
	if ann != nil {
		t = ann
	}
	c.irrefutable(s.Pat, "a `let`")
	b := &binder{mut: s.Mut, locals: map[string]*Local{}}
	c.pat(s.Pat, t, b)
	b.declare(c)
	if isNever(c.info.Types[s.Value]) {
		return Never
	}
	return Unit
}

// assign checks `target = value` and the compound forms.
func (c *checker) assign(s *syntax.AssignStmt) {
	target := unparen(s.Target)
	var tt Type = invalid
	switch t := target.(type) {
	case *syntax.Ident:
		l := c.lookupLocal(t.Name)
		if l == nil {
			if g := c.globals[t.Name]; g != nil {
				c.errorf("R0501", t.Span(), "cannot assign to `%s`: only `let mut` bindings can be assigned", t.Name)
			} else {
				c.unknown(t, "name", c.valueNames())
			}
			break
		}
		l.refs = append(l.refs, t.Span())
		c.info.Uses[t] = &Use{Kind: UseLocal, Local: l}
		c.info.Types[t] = l.Type
		if c.checkAssignable(l, t.Span()) {
			tt = l.Type
		}
		if s.Op != syntax.ASSIGN {
			c.useLocal(l, t.Span())
		}
		l.Assigned = true
	case *syntax.FieldExpr:
		root := target
		for {
			f, ok := root.(*syntax.FieldExpr)
			if !ok {
				break
			}
			root = unparen(f.X)
		}
		id, ok := root.(*syntax.Ident)
		if !ok {
			c.errorf("R0501", target.Span(), "cannot assign to a field of a temporary value; assign to a field of a `let mut` binding")
			break
		}
		ft := c.expr(target, nil)
		if l := c.lookupLocal(id.Name); l != nil {
			if c.checkAssignable(l, id.Span()) {
				tt = ft
			}
		} else if c.globals[id.Name] != nil {
			c.errorf("R0501", id.Span(), "cannot assign to a field of `%s`: only `let mut` bindings can be assigned", id.Name)
		}
	case *syntax.IndexExpr:
		c.expr(target, nil)
		d := c.errorf("R0501", target.Span(), "cannot assign to a List element: lists are immutable")
		d.Notes = append(d.Notes, "build a new list instead, for example with map, or push onto a `let mut` list")
	default:
		c.expr(target, nil)
		c.errorf("R0501", target.Span(), "cannot assign to this expression: assign to a `let mut` binding or one of its fields")
	}
	if s.Op == syntax.ASSIGN {
		c.expr(s.Value, nilIfInvalid(tt))
		return
	}
	// x op= y is x = x op y.
	op := opText[s.Op]
	var w Type
	if !isInvalid(tt) {
		w = tt
	}
	var vt Type
	if isIntLit(s.Value) {
		vt = c.expr(s.Value, w)
	} else {
		vt = c.expr(s.Value, nil)
	}
	if isInvalid(tt) || isInvalid(vt) {
		return
	}
	l, r := Resolve(tt), Resolve(vt)
	switch {
	case !c.assignable(vt, tt):
		if (l == Int && r == Float) || (l == Float && r == Int) {
			d := c.errorf("R0301", s.Value.Span(), "cannot mix Int and Float in `%s`", op)
			d.Notes = append(d.Notes, "Riddim has no implicit conversions")
		} else {
			c.errorf("R0301", s.Value.Span(), "mismatched types in `%s`: %s and %s", op, Deep(l), Deep(r))
		}
	case !numeric(l) && !(s.Op == syntax.PLUSEQ && l == String):
		c.errorf("R0301", s.Span(), "`%s` needs Int, Float or Byte operands, found %s", op, Deep(l))
	case (s.Op == syntax.SLASHEQ) && c.consts[s.Value] != nil && isZero(c.consts[s.Value], l):
		c.errorf("R0308", s.Value.Span(), "division by zero")
	}
}

func nilIfInvalid(t Type) Type {
	if isInvalid(t) {
		return nil
	}
	return t
}

// checkAssignable reports assignments to immutable bindings and to bindings
// captured by a closure (R0501).
func (c *checker) checkAssignable(l *Local, sp syntax.Span) bool {
	if l.fn != c.fn {
		d := c.errorf("R0501", sp, "cannot assign to `%s` inside a closure: closures capture values, not variables", l.Name)
		d.Notes = append(d.Notes, "return the new value from the closure instead")
		return false
	}
	if !l.Mutable {
		var d *diag.Diagnostic
		if l.Name == "self" {
			d = c.errorf("R0501", sp, "`self` is immutable: return a new value instead, as in `%s { x: 1, ..self }`", c.selfName())
		} else {
			d = c.errorf("R0501", sp, "`%s` is immutable: declare it with `let mut %s` to assign to it", l.Name, l.Name)
		}
		if l.Param {
			d.Notes = append(d.Notes, fmt.Sprintf("parameters are immutable; copy it first: `let mut %s = %s`", l.Name, l.Name))
		}
		return false
	}
	return true
}

func (c *checker) selfName() string {
	if c.selfDecl != nil {
		return c.selfDecl.Name
	}
	return "Self"
}

// loopBody checks the body of a loop and reports whether a break leaves it.
func (c *checker) loopBody(b *syntax.BlockExpr) bool {
	lc := &loopCtx{}
	c.loops = append(c.loops, lc)
	c.info.Types[b] = c.block(b, nil, true)
	c.loops = c.loops[:len(c.loops)-1]
	return lc.hasBreak
}

func (c *checker) forExpr(e *syntax.ForExpr) {
	var elem Type = invalid
	if e.End != nil {
		c.expr(e.Iter, Int)
		c.expr(e.End, Int)
		elem = Int
	} else {
		switch it := Resolve(c.expr(e.Iter, nil)).(type) {
		case *List:
			elem = it.Elem
		case *Invalid, *Var:
		default:
			d := c.errorf("R0301", e.Iter.Span(), "`for` needs a List or a range `a..b`, found %s", Deep(it))
			d.Notes = append(d.Notes, "ranges are written in the for loop itself: `for i in 0..n`")
		}
	}
	c.pushScope()
	c.irrefutable(e.Pat, "a `for` loop")
	b := &binder{locals: map[string]*Local{}}
	c.pat(e.Pat, elem, b)
	b.declare(c)
	c.loopBody(e.Body)
	c.popScope()
}

// ifExpr checks an if. With discard set, the branch values are dropped.
func (c *checker) ifExpr(e *syntax.IfExpr, want Type, discard bool) Type {
	c.expr(e.Cond, Bool)
	if e.Else == nil {
		c.info.Types[e.Then] = c.block(e.Then, nil, true)
		return Unit
	}
	if discard {
		t1 := c.block(e.Then, nil, true)
		c.info.Types[e.Then] = t1
		t2 := c.discard(e.Else)
		if isNever(t1) && isNever(t2) {
			return Never
		}
		return Unit
	}
	t1 := c.block(e.Then, want, false)
	c.info.Types[e.Then] = t1
	w := want
	if w == nil && !isNever(t1) && !isInvalid(t1) {
		w = t1
	}
	t2 := c.expr(e.Else, w)
	switch {
	case isNever(t1) && isNever(t2):
		return Never
	case want != nil:
		return want
	case isNever(t1):
		return t2
	}
	return t1
}

func (c *checker) match(e *syntax.MatchExpr, want Type, discard bool) Type {
	st := c.expr(e.Subject, nil)
	result := want
	allNever := true
	for _, arm := range e.Arms {
		c.pushScope()
		b := &binder{locals: map[string]*Local{}}
		c.pat(arm.Pat, st, b)
		b.declare(c)
		if arm.Guard != nil {
			c.expr(arm.Guard, Bool)
		}
		var t Type
		if discard {
			t = c.discard(arm.Body)
		} else {
			t = c.expr(arm.Body, result)
			if result == nil && !isNever(t) && !isInvalid(t) {
				result = t
			}
		}
		if !isNever(t) {
			allNever = false
		}
		c.popScope()
	}
	c.matches = append(c.matches, e)
	switch {
	case allNever:
		return Never
	case discard:
		return Unit
	case result == nil:
		return invalid
	}
	return result
}

func (c *checker) closure(e *syntax.ClosureExpr, want Type) Type {
	var wf *Func
	if f, ok := Resolve(want).(*Func); ok && len(f.Params) == len(e.Params) {
		wf = f
	}
	params := make([]Type, len(e.Params))
	for i, p := range e.Params {
		var t Type
		switch {
		case p.Type != nil:
			t = c.resolveType(p.Type)
			if wf != nil && !c.assignable(wf.Params[i], t) {
				c.errorf("R0301", p.Type.Span(), "mismatched types: this parameter is %s, but %s is expected", Deep(t), Deep(wf.Params[i]))
			}
		case wf != nil:
			t = wf.Params[i]
			if _, unknown := Resolve(t).(*Var); unknown {
				c.closureParamUnknown(p)
				t = invalid
			}
		default:
			c.closureParamUnknown(p)
			t = invalid
		}
		params[i] = t
	}
	var result Type
	switch {
	case e.Result != nil:
		result = c.resolveType(e.Result)
		if wf != nil && !c.assignable(result, wf.Result) {
			c.errorf("R0301", e.Result.Span(), "mismatched types: this closure returns %s, but %s is expected", Deep(result), Deep(wf.Result))
		}
	case wf != nil:
		result = wf.Result
	default:
		result = c.newVar(e.Body.Span(), "the result type of this closure")
	}
	ctx := &funcCtx{result: result, parent: c.fn}
	saved := saveCtx(c)
	c.fn, c.loops = ctx, nil
	c.pushScope()
	b := &binder{locals: map[string]*Local{}}
	for i, p := range e.Params {
		c.irrefutable(p.Pat, "a closure parameter")
		c.pat(p.Pat, params[i], b)
	}
	b.declare(c)
	c.body(e.Body, result, "this closure")
	c.popScope()
	saved.restore(c)
	if len(ctx.pending) > 0 {
		mark := len(c.vars)
		c.vars = append(c.vars, ctx.pending...)
		c.endStmt(mark)
	}
	ft := &Func{Params: params, Result: result}
	c.info.Closures[e] = &ClosureInfo{Type: ft, Captures: ctx.captures}
	return ft
}

func (c *checker) closureParamUnknown(p *syntax.ClosureParam) {
	name := "_"
	if bp, ok := p.Pat.(*syntax.BindPat); ok {
		name = bp.Name.Name
	}
	d := c.errorf("R0306", p.Pat.Span(), "cannot infer the type of closure parameter `%s`", name)
	d.Notes = append(d.Notes, fmt.Sprintf("annotate it, as in `|%s: Int| ...`", name))
}

func (c *checker) ret(e *syntax.ReturnExpr) {
	if c.fn == nil {
		return
	}
	res := c.fn.result
	if e.Value == nil {
		if v, ok := Resolve(res).(*Var); ok {
			c.bind(v, Unit)
			return
		}
		if !isUnit(res) && !isInvalid(res) {
			c.errorf("R0301", e.Span(), "`return` needs a value: the function returns %s", Deep(res))
		}
		return
	}
	c.expr(e.Value, res)
}

// irrefutable reports patterns that can fail to match where every value must
// match (R0404).
func (c *checker) irrefutable(p syntax.Pattern, where string) {
	if bad := refutablePart(p); bad != nil {
		d := c.errorf("R0404", bad.Span(), "the pattern of %s must match every value, but this one can fail", where)
		d.Notes = append(d.Notes, "use match to handle the other cases; let, for and closure parameters take names, _ and tuples of them")
	}
}

func refutablePart(p syntax.Pattern) syntax.Pattern {
	switch p := p.(type) {
	case *syntax.WildcardPat, *syntax.BindPat:
		return nil
	case *syntax.TuplePat:
		for _, e := range p.Elems {
			if bad := refutablePart(e); bad != nil {
				return bad
			}
		}
		return nil
	}
	return p
}

// A binder collects the names a pattern binds. For the second and later sides
// of an or-pattern, reuse holds the first side's names: each side must bind
// the same names with the same types (R0403).
type binder struct {
	mut    bool
	locals map[string]*Local
	order  []*Local
	reuse  map[string]*Local
	seen   map[string]bool
}

func (b *binder) bind(c *checker, id *syntax.Ident, t Type) *Local {
	if b.reuse != nil {
		l := b.reuse[id.Name]
		if l == nil {
			c.errorf("R0403", id.Span(), "`%s` is bound on only one side of `|`: each side must bind the same names", id.Name)
			return &Local{Name: id.Name, Type: invalid, fn: c.fn}
		}
		if !c.assignable(t, l.Type) {
			c.errorf("R0403", id.Span(), "`%s` is %s on one side of `|` and %s on the other", id.Name, Deep(l.Type), Deep(t))
		}
		b.seen[id.Name] = true
		l.Decls = append(l.Decls, id)
		c.info.Defs[id] = l
		return l
	}
	if b.locals[id.Name] != nil {
		c.errorf("R0202", id.Span(), "`%s` is bound twice in this pattern", id.Name)
		return &Local{Name: id.Name, Type: invalid, fn: c.fn}
	}
	l := &Local{Name: id.Name, Decls: []*syntax.Ident{id}, Type: t, Mutable: b.mut, fn: c.fn}
	b.locals[id.Name] = l
	b.order = append(b.order, l)
	c.info.Defs[id] = l
	c.fnLocals = append(c.fnLocals, l)
	c.style(id, snake, &l.refs)
	return l
}

// bound returns the names b has bound so far.
func (b *binder) bound() map[string]*Local {
	out := map[string]*Local{}
	if b.reuse != nil {
		for name := range b.seen {
			out[name] = b.reuse[name]
		}
		return out
	}
	for name, l := range b.locals {
		out[name] = l
	}
	return out
}

// declare puts the bound names in scope.
func (b *binder) declare(c *checker) {
	for _, l := range b.order {
		c.scope.names[l.Name] = l
	}
}
