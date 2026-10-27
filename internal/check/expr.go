package check

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/riddim-hq/riddim/internal/diag"
	"github.com/riddim-hq/riddim/internal/syntax"
)

// expr checks e where a value of type want is expected (nil when nothing is
// expected) and returns its type. A mismatch is reported here.
func (c *checker) expr(e syntax.Expr, want Type) Type {
	t := c.expr0(e, want)
	c.info.Types[e] = t
	if isInvalid(t) {
		// An expression with an error poisons the type it was to fix.
		if v, ok := Resolve(want).(*Var); ok {
			v.ref = invalid
		}
		return t
	}
	if want != nil && t != nil && !isInvalid(t) && !isInvalid(want) && !c.assignable(t, want) {
		c.mismatch(e, want, t)
		return invalid
	}
	return t
}

func (c *checker) mismatch(e syntax.Expr, want, got Type) {
	w, g := Resolve(want), Resolve(got)
	d := c.errorf("R0301", e.Span(), "mismatched types: expected %s, found %s", Deep(w), Deep(g))
	if (w == Int && g == Float) || (w == Float && g == Int) {
		d.Notes = append(d.Notes, "Riddim has no implicit conversions between Int and Float")
	}
	if to := widening(g, w); to != "" {
		d.Fixes = append(d.Fixes, c.asFix(e, to))
	}
	if g == Unit {
		if b, ok := unparen(e).(*syntax.BlockExpr); ok && !hasTail(b) {
			d.Notes = append(d.Notes, "the block ends without a value")
		}
	}
}

func unparen(e syntax.Expr) syntax.Expr {
	for {
		p, ok := e.(*syntax.ParenExpr)
		if !ok {
			return e
		}
		e = p.X
	}
}

func (c *checker) expr0(e syntax.Expr, want Type) Type {
	switch e := e.(type) {
	case *syntax.IntLit:
		return c.intLit(e, want, false, e.Span())
	case *syntax.FloatLit:
		return c.floatLit(e)
	case *syntax.StringLit:
		c.consts[e] = &Const{Str: e.Value}
		return String
	case *syntax.InterpLit:
		for _, x := range e.Exprs {
			c.interpolated(x)
		}
		return String
	case *syntax.BoolLit:
		c.consts[e] = &Const{Bool: e.Value}
		return Bool
	case *syntax.UnitLit:
		return Unit
	case *syntax.Ident:
		return c.ident(e, want)
	case *syntax.PathExpr:
		return c.path(e, want, false)
	case *syntax.CallExpr:
		return c.call(e, want)
	case *syntax.MethodCallExpr:
		return c.methodCall(e, want)
	case *syntax.FieldExpr:
		return c.field(e)
	case *syntax.IndexExpr:
		return c.index(e)
	case *syntax.TryExpr:
		return c.tryExpr(e)
	case *syntax.UnaryExpr:
		return c.unary(e, want)
	case *syntax.AsExpr:
		return c.cast(e)
	case *syntax.BinaryExpr:
		return c.binary(e, want)
	case *syntax.ParenExpr:
		t := c.expr(e.X, want)
		if k := c.consts[e.X]; k != nil {
			c.consts[e] = k
		}
		return t
	case *syntax.TupleExpr:
		var wt *Tuple
		if t, ok := Resolve(want).(*Tuple); ok && len(t.Elems) == len(e.Elems) {
			wt = t
		}
		elems := make([]Type, len(e.Elems))
		for i, x := range e.Elems {
			var w Type
			if wt != nil {
				w = wt.Elems[i]
			}
			elems[i] = c.expr(x, w)
		}
		return &Tuple{elems}
	case *syntax.ListExpr:
		var elem Type
		if l, ok := Resolve(want).(*List); ok {
			elem = l.Elem
		} else if isInvalid(want) {
			elem = invalid
		} else {
			elem = c.newVar(e.Span(), "the element type of this list")
		}
		for _, x := range e.Elems {
			c.expr(x, elem)
		}
		return &List{elem}
	case *syntax.StructLit:
		return c.structLit(e, want)
	case *syntax.BlockExpr:
		return c.block(e, want, false)
	case *syntax.IfExpr:
		return c.ifExpr(e, want, false)
	case *syntax.MatchExpr:
		return c.match(e, want, false)
	case *syntax.WhileExpr:
		c.expr(e.Cond, Bool)
		c.loopBody(e.Body)
		return Unit
	case *syntax.LoopExpr:
		if c.loopBody(e.Body) {
			return Unit
		}
		return Never
	case *syntax.ForExpr:
		c.forExpr(e)
		return Unit
	case *syntax.ClosureExpr:
		return c.closure(e, want)
	case *syntax.ReturnExpr:
		c.ret(e)
		return Never
	case *syntax.BreakExpr:
		if len(c.loops) > 0 {
			c.loops[len(c.loops)-1].hasBreak = true
		}
		return Never
	case *syntax.ContinueExpr:
		return Never
	}
	return invalid
}

// use marks a local as read, unless the code is unreachable, and records the
// copies closures must make of `let mut` bindings.
func (c *checker) useLocal(l *Local, sp syntax.Span) {
	if !c.dead {
		l.Used = true
	}
	l.refs = append(l.refs, sp)
	if l.Mutable {
		for f := c.fn; f != nil && f != l.fn; f = f.parent {
			found := false
			for _, x := range f.captures {
				if x == l {
					found = true
				}
			}
			if !found {
				f.captures = append(f.captures, l)
			}
		}
	}
}

func (c *checker) ident(e *syntax.Ident, want Type) Type {
	if l := c.lookupLocal(e.Name); l != nil {
		c.useLocal(l, e.Span())
		c.info.Uses[e] = &Use{Kind: UseLocal, Local: l}
		return l.Type
	}
	switch g := c.globals[e.Name].(type) {
	case *FuncObj:
		g.refs = append(g.refs, e.Span())
		targs := c.fresh(g.TParams, e.Span(), "`"+g.Name+"`")
		c.info.Uses[e] = &Use{Kind: UseFunc, Func: g, TypeArgs: targs}
		return subst(g.Sig(), bindParams(g.TParams, targs))
	case *ConstObj:
		g.refs = append(g.refs, e.Span())
		c.checkConst(g)
		if g.Value != nil {
			c.consts[e] = g.Value
		}
		c.info.Uses[e] = &Use{Kind: UseConst, Const: g}
		return g.Type
	case *TypeDecl:
		g.refs = append(g.refs, e.Span())
		if g.IsEnum {
			c.errorf("R0201", e.Span(), "`%s` is an enum, not a value; write a variant, as in `%s::%s`", e.Name, e.Name, firstVariant(g))
		} else {
			c.errorf("R0201", e.Span(), "`%s` is a struct, not a value; write a struct literal, as in `%s { ... }`", e.Name, e.Name)
		}
		return invalid
	}
	switch e.Name {
	case "None":
		elem := c.wantElem(want, e.Span(), "the type of `None`")
		c.info.Uses[e] = &Use{Kind: UseNone, TypeArgs: []Type{elem}}
		return &Option{elem}
	case "Some":
		elem := c.wantElem(want, e.Span(), "the type of `Some`")
		c.info.Uses[e] = &Use{Kind: UseSome, TypeArgs: []Type{elem}}
		return fn1(elem, &Option{elem})
	case "Ok", "Err":
		ok, er := c.wantResult(want, e.Span(), e.Name == "Ok")
		kind := map[string]UseKind{"Ok": UseOk, "Err": UseErr}[e.Name]
		c.info.Uses[e] = &Use{Kind: kind, TypeArgs: []Type{ok, er}}
		if e.Name == "Ok" {
			return fn1(ok, &Result{ok, er})
		}
		return fn1(er, &Result{ok, er})
	case "error":
		c.info.Uses[e] = &Use{Kind: UseError}
		return fn1(String, ErrorT)
	case "panic", "todo":
		c.errorf("R0301", e.Span(), "`%s` must be called: `%s(...)`", e.Name, e.Name)
		return invalid
	case "self":
		c.errorf("R0201", e.Span(), "`self` is only available in methods that take `self`")
		return invalid
	case "Self":
		c.errorf("R0201", e.Span(), "`Self` is a type, not a value; write `Self { ... }` or `Self::name`")
		return invalid
	}
	d := c.unknown(e, "name", c.valueNames())
	if en := c.enumWithVariant(e.Name); en != "" {
		d.Notes = append(d.Notes, fmt.Sprintf("variants are written with their enum: `%s::%s`", en, e.Name))
	}
	return invalid
}

func firstVariant(td *TypeDecl) string {
	if len(td.Variants) > 0 {
		return td.Variants[0].Name
	}
	return "Variant"
}

func (c *checker) enumWithVariant(name string) string {
	var found []string
	for _, td := range c.prog.Types {
		if td.IsEnum && td.Variant(name) != nil {
			found = append(found, td.Name)
		}
	}
	sort.Strings(found)
	if len(found) > 0 {
		return found[0]
	}
	return ""
}

func (c *checker) valueNames() []string {
	names := c.localNames()
	for name, g := range c.globals {
		if _, ok := g.(*TypeDecl); !ok {
			names = append(names, name)
		}
	}
	return append(names, "Some", "None", "Ok", "Err", "error", "panic", "todo")
}

// fresh makes inference variables for type parameters.
func (c *checker) fresh(params []*TypeParam, sp syntax.Span, of string) []Type {
	out := make([]Type, len(params))
	for i, p := range params {
		out[i] = c.newVar(sp, fmt.Sprintf("type parameter %s of %s", p.Name, of))
	}
	return out
}

func (c *checker) wantElem(want Type, sp syntax.Span, what string) Type {
	if isInvalid(want) {
		return invalid
	}
	if o, ok := Resolve(want).(*Option); ok {
		return o.Elem
	}
	if f, ok := Resolve(want).(*Func); ok {
		if o, ok := Resolve(f.Result).(*Option); ok {
			return o.Elem
		}
	}
	return c.newVar(sp, what)
}

func (c *checker) wantResult(want Type, sp syntax.Span, isOk bool) (Type, Type) {
	if isInvalid(want) {
		return invalid, invalid
	}
	w := Resolve(want)
	if f, ok := w.(*Func); ok {
		w = Resolve(f.Result)
	}
	if r, ok := w.(*Result); ok {
		return r.Ok, r.Err
	}
	if isOk {
		return c.newVar(sp, "the value type of `Ok`"), c.newVar(sp, "the error type of `Ok(...)`")
	}
	return c.newVar(sp, "the value type of `Err(...)`"), c.newVar(sp, "the error type of `Err`")
}

// pathType resolves the type part of Type::name. Self names the impl's type.
func (c *checker) pathType(id *syntax.Ident) (*TypeDecl, []Type, bool) {
	if id.Name == "Self" {
		if c.selfDecl == nil {
			c.errorf("R0201", id.Span(), "`Self` is only available inside an impl block")
			return nil, nil, false
		}
		return c.selfDecl, c.selfType.(*Named).Args, true
	}
	switch id.Name {
	case "Sys", "Out":
		d := c.errorf("R0603", id.Span(), "capabilities cannot be made: there is no `%s::...`", id.Name)
		d.Notes = append(d.Notes, "main receives `sys: Sys`; other functions receive the capabilities they are passed")
		return nil, nil, false
	}
	td, ok := c.globals[id.Name].(*TypeDecl)
	if !ok {
		if _, isBuiltin := reserved[id.Name]; isBuiltin {
			c.errorf("R0201", id.Span(), "`%s` has no associated functions in this version", id.Name)
		} else {
			c.unknown(id, "type", c.typeNames())
		}
		return nil, nil, false
	}
	td.refs = append(td.refs, id.Span())
	return td, nil, true
}

// path checks Type::name used as a value; called is set when it is the
// function of a call, which reports arguments itself.
func (c *checker) path(e *syntax.PathExpr, want Type, called bool) Type {
	td, targs, ok := c.pathType(e.Type)
	if !ok {
		return invalid
	}
	name := e.Name.Name
	if targs == nil {
		targs = c.fresh(td.TParams, e.Span(), "`"+td.Name+"`")
	}
	if v := td.Variant(name); v != nil {
		v.refs = append(v.refs, e.Name.Span())
		c.info.Uses[e] = &Use{Kind: UseVariant, Variant: v, TypeArgs: targs}
		self := &Named{Decl: td, Args: targs}
		if len(v.Fields) == 0 {
			return self
		}
		params := make([]Type, len(v.Fields))
		for i, f := range v.Fields {
			params[i] = FieldType(f, td, targs)
		}
		return &Func{params, self}
	}
	if m := td.Method(name); m != nil {
		m.refs = append(m.refs, e.Name.Span())
		if m.HasSelf {
			c.errorf("R0304", e.Span(), "`%s` is a method: call it on a value, as in `x.%s()`", name, name)
			return invalid
		}
		own := c.fresh(m.TParams, e.Span(), "`"+td.Name+"::"+name+"`")
		c.info.Uses[e] = &Use{Kind: UseAssoc, Func: m, TypeArgs: append(append([]Type{}, targs...), own...)}
		sub := bindParams(m.ImplTParams, targs)
		for k, v := range bindParams(m.TParams, own) {
			sub[k] = v
		}
		return subst(m.Sig(), sub)
	}
	var cands []string
	for _, v := range td.Variants {
		cands = append(cands, v.Name)
	}
	for _, m := range td.Methods {
		if !m.HasSelf {
			cands = append(cands, m.Name)
		}
	}
	what := "associated function"
	if td.IsEnum {
		what = "variant or associated function"
	}
	msg := fmt.Sprintf("`%s` has no %s `%s`", td.Name, what, name)
	if close := closest(name, cands); len(close) > 0 {
		msg += "; did you mean " + orList(close) + "?"
	}
	c.errorf("R0201", e.Name.Span(), "%s", msg)
	return invalid
}

func isClosure(e syntax.Expr) bool {
	_, ok := unparen(e).(*syntax.ClosureExpr)
	return ok
}

// args checks call arguments against parameter types: closures last, so they
// see the types the other arguments fixed (DESIGN §4.2).
func (c *checker) args(callee string, sp syntax.Span, args []syntax.Expr, params []Type) {
	if len(args) != len(params) {
		c.errorf("R0302", sp, "%s takes %s, found %d", callee, plural(len(params), "argument"), len(args))
		for _, a := range args {
			c.expr(a, nil)
		}
		return
	}
	for i, a := range args {
		if !isClosure(a) {
			c.expr(a, params[i])
		}
	}
	for i, a := range args {
		if isClosure(a) {
			c.expr(a, params[i])
		}
	}
}

// callWith checks a call to a function of the given parameter and result
// types: the expected type first, then the arguments.
func (c *checker) callWith(callee string, sp syntax.Span, args []syntax.Expr, params []Type, result, want Type) Type {
	if want != nil && !isNever(result) {
		c.assignable(result, want)
	}
	c.args(callee, sp, args, params)
	return result
}

func (c *checker) call(e *syntax.CallExpr, want Type) Type {
	switch fun := unparen(e.Fun).(type) {
	case *syntax.Ident:
		if c.lookupLocal(fun.Name) == nil && c.globals[fun.Name] == nil {
			switch fun.Name {
			case "Some":
				c.info.Types[fun] = invalid
				if len(e.Args) != 1 {
					c.errorf("R0302", e.Span(), "`Some` takes 1 argument, found %d", len(e.Args))
					return invalid
				}
				elem := c.wantElem(want, e.Span(), "the type of `Some`")
				c.expr(e.Args[0], elem)
				c.info.Uses[fun] = &Use{Kind: UseSome, TypeArgs: []Type{elem}}
				return &Option{elem}
			case "Ok", "Err":
				c.info.Types[fun] = invalid
				if len(e.Args) != 1 {
					c.errorf("R0302", e.Span(), "`%s` takes 1 argument, found %d", fun.Name, len(e.Args))
					return invalid
				}
				ok, er := c.wantResult(want, e.Span(), fun.Name == "Ok")
				if fun.Name == "Ok" {
					c.expr(e.Args[0], ok)
					c.info.Uses[fun] = &Use{Kind: UseOk, TypeArgs: []Type{ok, er}}
				} else {
					c.expr(e.Args[0], er)
					c.info.Uses[fun] = &Use{Kind: UseErr, TypeArgs: []Type{ok, er}}
				}
				return &Result{ok, er}
			case "None":
				c.errorf("R0302", e.Span(), "`None` takes no arguments: write `None`")
				return invalid
			case "error":
				c.info.Types[fun] = invalid
				c.info.Uses[fun] = &Use{Kind: UseError}
				c.args("`error`", e.Span(), e.Args, []Type{String})
				return ErrorT
			case "panic":
				c.info.Types[fun] = invalid
				c.info.Uses[fun] = &Use{Kind: UsePanic}
				c.args("`panic`", e.Span(), e.Args, []Type{String})
				return Never
			case "todo":
				c.info.Types[fun] = invalid
				c.info.Uses[fun] = &Use{Kind: UseTodo}
				c.args("`todo`", e.Span(), e.Args, nil)
				c.warnf("W0003", e.Span(), "`todo()` panics when reached; finish this code before shipping it")
				return Never
			}
		}
		if g, ok := c.globals[fun.Name].(*FuncObj); ok && c.lookupLocal(fun.Name) == nil {
			g.refs = append(g.refs, fun.Span())
			targs := c.fresh(g.TParams, e.Span(), "`"+g.Name+"`")
			c.info.Uses[fun] = &Use{Kind: UseFunc, Func: g, TypeArgs: targs}
			sig := subst(g.Sig(), bindParams(g.TParams, targs)).(*Func)
			c.info.Types[fun] = sig
			return c.callWith("`"+g.Name+"`", e.Span(), e.Args, sig.Params, sig.Result, want)
		}
		if td, ok := c.globals[fun.Name].(*TypeDecl); ok && c.lookupLocal(fun.Name) == nil {
			td.refs = append(td.refs, fun.Span())
			c.info.Types[fun] = invalid
			if td.IsEnum {
				c.errorf("R0201", fun.Span(), "`%s` is an enum, not a function; write a variant, as in `%s::%s(...)`", fun.Name, fun.Name, firstVariant(td))
			} else {
				c.errorf("R0201", fun.Span(), "`%s` is a struct, not a function; write a struct literal, as in `%s { ... }`", fun.Name, fun.Name)
			}
			for _, a := range e.Args {
				c.expr(a, nil)
			}
			return invalid
		}
	case *syntax.PathExpr:
		t := c.path(fun, nil, true)
		c.info.Types[fun] = t
		use := c.info.Uses[fun]
		if use == nil {
			for _, a := range e.Args {
				c.expr(a, nil)
			}
			return invalid
		}
		if use.Kind == UseVariant && len(use.Variant.Fields) == 0 {
			c.errorf("R0302", e.Span(), "`%s` has no fields: write `%s::%s` without parentheses", use.Variant.Name, fun.Type.Name, use.Variant.Name)
			return invalid
		}
		sig := Resolve(t).(*Func)
		return c.callWith("`"+fun.Type.Name+"::"+fun.Name.Name+"`", e.Span(), e.Args, sig.Params, sig.Result, want)
	}
	t := c.expr(e.Fun, nil)
	switch ft := Resolve(t).(type) {
	case *Func:
		return c.callWith("this function", e.Span(), e.Args, ft.Params, ft.Result, want)
	case *Invalid:
	default:
		c.errorf("R0301", e.Fun.Span(), "cannot call a value of type %s", Deep(ft))
	}
	for _, a := range e.Args {
		c.expr(a, nil)
	}
	return invalid
}

func (c *checker) methodCall(e *syntax.MethodCallExpr, want Type) Type {
	rt := Resolve(c.expr(e.Recv, nil))
	name := e.Name.Name
	fail := func() Type {
		for _, a := range e.Args {
			c.expr(a, nil)
		}
		return invalid
	}
	switch r := rt.(type) {
	case *Invalid, *Var:
		return fail()
	case *Named:
		m := r.Decl.Method(name)
		if m == nil || !m.HasSelf {
			var d *diag.Diagnostic
			switch {
			case m != nil:
				m.refs = append(m.refs, e.Name.Span())
				d = c.errorf("R0304", e.Name.Span(), "`%s` is an associated function, not a method: call it as `%s::%s(...)`", name, r.Decl.Name, name)
			case r.Decl.Field(name) != nil:
				d = c.errorf("R0304", e.Name.Span(), "`%s` is a field of %s, not a method", name, r.Decl.Name)
				if _, isFn := Resolve(r.Decl.Field(name).Type).(*Func); isFn {
					d.Notes = append(d.Notes, fmt.Sprintf("to call a function stored in a field, write `(x.%s)(...)`", name))
				}
			default:
				var cands []string
				for _, m := range r.Decl.Methods {
					if m.HasSelf {
						cands = append(cands, m.Name)
					}
				}
				msg := fmt.Sprintf("%s has no method `%s`", r.Decl.Name, name)
				if close := closest(name, cands); len(close) > 0 {
					msg += "; did you mean " + orList(close) + "?"
				}
				c.errorf("R0304", e.Name.Span(), "%s", msg)
			}
			return fail()
		}
		m.refs = append(m.refs, e.Name.Span())
		own := c.fresh(m.TParams, e.Span(), "`"+name+"`")
		sub := bindParams(m.ImplTParams, r.Args)
		for k, v := range bindParams(m.TParams, own) {
			sub[k] = v
		}
		c.info.Methods[e] = &MethodUse{Func: m, RecvArgs: r.Args, TypeArgs: own}
		sig := subst(m.Sig(), sub).(*Func)
		return c.callWith("`"+name+"`", e.Span(), e.Args, sig.Params, sig.Result, want)
	case *TypeParam:
		c.errorf("R0304", e.Name.Span(), "a value of type parameter %s has no methods in this version", r.Name)
		return fail()
	}
	table, recvArgs, typeName := methodsOf(rt)
	b := table[name]
	if b == nil {
		var cands []string
		for n := range table {
			cands = append(cands, n)
		}
		msg := fmt.Sprintf("%s has no method `%s`", typeName, name)
		if isLater(typeName, name) {
			msg = fmt.Sprintf("`%s` on %s is not supported in this version", name, typeName)
		} else if alias, ok := methodAliases[name]; ok && table[alias] != nil {
			msg += "; did you mean `" + alias + "`?"
		} else if close := closest(name, cands); len(close) > 0 {
			msg += "; did you mean " + orList(close) + "?"
		}
		c.errorf("R0304", e.Name.Span(), "%s", msg)
		return fail()
	}
	own := make([]Type, b.own)
	for i := range own {
		own[i] = c.newVar(e.Span(), fmt.Sprintf("the result type of `%s`", name))
	}
	params, result := b.sig(recvArgs, own)
	c.info.Methods[e] = &MethodUse{Builtin: b.key, TypeArgs: append(append([]Type{}, recvArgs...), own...)}
	if b.warn != "" {
		c.warnf("W0002", e.Name.Span(), "%s", b.warn)
	}
	return c.callWith("`"+name+"`", e.Span(), e.Args, params, result, want)
}

// methodAliases are names other languages use for built-in methods.
var methodAliases = map[string]string{
	"length": "len", "size": "len", "count": "len",
	"append": "push", "add": "push",
	"unwrap_or_default": "unwrap_or",
	"to_str":            "to_string", "as_str": "to_string",
}

func (c *checker) field(e *syntax.FieldExpr) Type {
	xt := Resolve(c.expr(e.X, nil))
	name := e.Name.Name
	if n, err := strconv.Atoi(name); err == nil {
		tup, ok := xt.(*Tuple)
		if !ok {
			if !isInvalid(xt) {
				c.errorf("R0303", e.Name.Span(), "`.%s` needs a tuple, found %s", name, Deep(xt))
			}
			return invalid
		}
		if n >= len(tup.Elems) {
			c.errorf("R0303", e.Name.Span(), "%s has no field `.%d`; its fields are .0 to .%d", Deep(tup), n, len(tup.Elems)-1)
			return invalid
		}
		c.info.Fields[e] = &FieldUse{Tuple: n}
		return tup.Elems[n]
	}
	switch x := xt.(type) {
	case *Named:
		if x.Decl.IsEnum {
			c.errorf("R0303", e.Name.Span(), "%s is an enum: use match to reach the fields of its variants", x.Decl.Name)
			return invalid
		}
		if f := x.Decl.Field(name); f != nil {
			f.refs = append(f.refs, e.Name.Span())
			c.info.Fields[e] = &FieldUse{Field: f, Tuple: -1}
			return FieldType(f, x.Decl, x.Args)
		}
		var cands []string
		for _, f := range x.Decl.Fields {
			cands = append(cands, f.Name)
		}
		msg := fmt.Sprintf("%s has no field `%s`", x.Decl.Name, name)
		if close := closest(name, cands); len(close) > 0 {
			msg += "; did you mean " + orList(close) + "?"
		}
		d := c.errorf("R0303", e.Name.Span(), "%s", msg)
		if m := x.Decl.Method(name); m != nil && m.HasSelf {
			d.Notes = append(d.Notes, fmt.Sprintf("`%s` is a method: call it, as in `x.%s()`", name, name))
		}
		return invalid
	case *Cap:
		if x == SysT && (name == "out" || name == "err") {
			c.info.Fields[e] = &FieldUse{Sys: name, Tuple: -1}
			return OutT
		}
		if x == SysT {
			c.errorf("R0303", e.Name.Span(), "Sys has no field `%s` in this version; it has `out` and `err`", name)
		} else {
			c.errorf("R0303", e.Name.Span(), "%s has no fields", x.Name)
		}
		return invalid
	case *Invalid, *Var:
		return invalid
	}
	c.errorf("R0303", e.Name.Span(), "%s has no fields", Deep(xt))
	return invalid
}

func (c *checker) index(e *syntax.IndexExpr) Type {
	xt := Resolve(c.expr(e.X, nil))
	c.expr(e.Index, Int)
	switch x := xt.(type) {
	case *List:
		return x.Elem
	case *Invalid, *Var:
		return invalid
	}
	c.errorf("R0301", e.X.Span(), "only a List can be indexed, found %s", Deep(xt))
	return invalid
}

func (c *checker) tryExpr(e *syntax.TryExpr) Type {
	xt := Resolve(c.expr(e.X, nil))
	if c.fn == nil {
		return invalid
	}
	outer := Resolve(c.fn.result)
	switch x := xt.(type) {
	case *Result:
		c.info.Tries[e] = &TryInfo{Outer: c.fn.result}
		switch o := outer.(type) {
		case *Result:
			if !c.assignable(x.Err, o.Err) {
				d := c.errorf("R0504", e.Span(), "`?` would return an Err of %s, but the function returns %s", Deep(x.Err), Deep(o))
				d.Notes = append(d.Notes, fmt.Sprintf("convert the error first, as in `.map_err(|e| ...)?`, to make a %s", Deep(o.Err)))
			}
		case *Var:
			c.bind(o, &Result{c.pendingVar(e.Span(), "the value type of this closure's Result"), x.Err})
		case *Option:
			d := c.errorf("R0505", e.Span(), "`?` on a Result in a function that returns %s", Deep(o))
			d.Notes = append(d.Notes, "turn the Result into an Option first: `.ok()?`")
		case *Invalid:
		default:
			c.notReturning(e, o)
		}
		return x.Ok
	case *Option:
		c.info.Tries[e] = &TryInfo{Option: true, Outer: c.fn.result}
		switch o := outer.(type) {
		case *Option:
		case *Var:
			c.bind(o, &Option{c.pendingVar(e.Span(), "the value type of this closure's Option")})
		case *Result:
			d := c.errorf("R0503", e.Span(), "`?` on an Option in a function that returns %s: turn the Option into a Result first", Deep(o))
			switch Resolve(o.Err) {
			case ErrorT:
				d.Fixes = append(d.Fixes, diag.Fix{Title: "turn None into an error",
					Edits: []diag.Edit{c.fset.Edit(syntax.Span{Start: e.Quest, End: e.Quest}, `.ok_or(error("missing value"))`)}})
			case String:
				d.Fixes = append(d.Fixes, diag.Fix{Title: "turn None into an error",
					Edits: []diag.Edit{c.fset.Edit(syntax.Span{Start: e.Quest, End: e.Quest}, `.ok_or("missing value")`)}})
			default:
				d.Notes = append(d.Notes, "write `.ok_or(e)?` with the error to return for None")
			}
		case *Invalid:
		default:
			c.notReturning(e, o)
		}
		return x.Elem
	case *Invalid, *Var:
		return invalid
	}
	c.errorf("R0301", e.Span(), "`?` needs a Result or an Option, found %s", Deep(xt))
	return invalid
}

// pendingVar makes a variable for part of the current closure's result type;
// it is checked when the closure's body ends rather than at the end of the
// statement that made it.
func (c *checker) pendingVar(sp syntax.Span, what string) *Var {
	v := &Var{origin: sp, what: what}
	c.fn.pending = append(c.fn.pending, v)
	return v
}

func (c *checker) notReturning(e *syntax.TryExpr, outer Type) {
	d := c.errorf("R0505", e.Span(), "`?` returns early, but the function returns %s, not a Result or an Option", Deep(outer))
	d.Notes = append(d.Notes, "handle the case with match, or make the function return a Result; main may return Result<(), Error>")
}

func isIntLit(e syntax.Expr) bool {
	switch e := unparen(e).(type) {
	case *syntax.IntLit:
		return true
	case *syntax.UnaryExpr:
		return e.Op == syntax.MINUS && isIntLit(e.X)
	}
	return false
}

func (c *checker) unary(e *syntax.UnaryExpr, want Type) Type {
	if e.Op == syntax.NOT {
		c.expr(e.X, Bool)
		c.foldUnary(e, Bool)
		return Bool
	}
	if lit, ok := e.X.(*syntax.IntLit); ok {
		t := c.intLit(lit, want, true, e.Span())
		c.info.Types[lit] = t
		if Resolve(t) == Byte {
			c.errorf("R0301", e.Span(), "a Byte cannot be negative")
			return invalid
		}
		if k := c.consts[lit]; k != nil {
			c.consts[e] = k // intLit read the literal negated
		}
		return t
	}
	var w Type
	if Resolve(want) == Int || Resolve(want) == Float {
		w = want
	}
	t := c.expr(e.X, w)
	switch Resolve(t) {
	case Int, Float:
		c.foldUnary(e, t)
		return t
	case Never:
		return Never
	}
	if !isInvalid(t) {
		c.errorf("R0301", e.Span(), "`-` needs an Int or a Float, found %s", Deep(t))
	}
	return invalid
}

var opText = map[syntax.Kind]string{
	syntax.PLUS: "+", syntax.MINUS: "-", syntax.STAR: "*", syntax.SLASH: "/", syntax.PERCENT: "%",
	syntax.EQ: "==", syntax.NE: "!=", syntax.LT: "<", syntax.LE: "<=", syntax.GT: ">", syntax.GE: ">=",
	syntax.ANDAND: "&&", syntax.OROR: "||",
	syntax.PLUSEQ: "+=", syntax.MINUSEQ: "-=", syntax.STAREQ: "*=", syntax.SLASHEQ: "/=",
}

// operands checks both sides of a binary operator. An integer literal on one
// side takes the type of the other side, so `2 * x` works for a Float x.
func (c *checker) operands(e *syntax.BinaryExpr, want Type) (Type, Type) {
	hint := func(t Type) Type {
		if numeric(t) {
			return t
		}
		return nil
	}
	if want == nil && isIntLit(e.X) && !isIntLit(e.Y) {
		rt := c.expr(e.Y, nil)
		return c.expr(e.X, hint(rt)), rt
	}
	lt := c.expr(e.X, want)
	if isIntLit(e.Y) {
		return lt, c.expr(e.Y, hint(lt))
	}
	return lt, c.expr(e.Y, nil)
}

func (c *checker) binary(e *syntax.BinaryExpr, want Type) Type {
	op := opText[e.Op]
	switch e.Op {
	case syntax.ANDAND, syntax.OROR:
		c.expr(e.X, Bool)
		c.expr(e.Y, Bool)
		c.foldBinary(e, Bool)
		return Bool
	case syntax.EQ, syntax.NE, syntax.LT, syntax.LE, syntax.GT, syntax.GE:
		lt, rt := c.operands(e, nil)
		if isInvalid(lt) || isInvalid(rt) {
			return Bool
		}
		if !c.assignable(rt, lt) && !c.assignable(lt, rt) {
			c.errorf("R0301", e.Span(), "cannot compare %s with %s", Deep(lt), Deep(rt))
			return Bool
		}
		if isNever(lt) {
			lt = rt
		}
		if e.Op == syntax.EQ || e.Op == syntax.NE {
			if !comparable(lt, nil) {
				d := c.errorf("R0305", e.Span(), "%s cannot be compared with `%s`", Deep(lt), op)
				d.Notes = append(d.Notes, "== works on Int, Float, Bool, String, Byte, and on tuples, structs, enums, Option and Result made of them")
			}
		} else if !ordered(lt) {
			c.errorf("R0305", e.Span(), "%s has no order: `%s` works on Int, Float, Byte and String", Deep(lt), op)
		}
		c.foldBinary(e, lt)
		return Bool
	}
	var w Type
	if numeric(want) || e.Op == syntax.PLUS && Resolve(want) == String {
		w = want
	}
	lt, rt := c.operands(e, w)
	return c.arith(e, op, lt, rt)
}

// arith types an arithmetic operator on operands of types lt and rt.
func (c *checker) arith(e *syntax.BinaryExpr, op string, lt, rt Type) Type {
	if isInvalid(lt) || isInvalid(rt) {
		return invalid
	}
	if isNever(lt) || isNever(rt) {
		return Never
	}
	l, r := Resolve(lt), Resolve(rt)
	if !c.assignable(rt, lt) {
		var d *diag.Diagnostic
		if (l == Int && r == Float) || (l == Float && r == Int) {
			d = c.errorf("R0301", e.Span(), "cannot mix Int and Float in `%s`", op)
			d.Notes = append(d.Notes, "Riddim has no implicit conversions: convert the Int with `as Float`")
		} else {
			d = c.errorf("R0301", e.Span(), "mismatched types in `%s`: %s and %s", op, Deep(l), Deep(r))
		}
		// Convert the narrower operand to the wider one's type.
		if to := widening(r, l); to != "" {
			d.Fixes = append(d.Fixes, c.asFix(e.Y, to))
		} else if to := widening(l, r); to != "" {
			d.Fixes = append(d.Fixes, c.asFix(e.X, to))
		}
		return invalid
	}
	if !numeric(l) && !(e.Op == syntax.PLUS && l == String) {
		if e.Op == syntax.PLUS {
			c.errorf("R0301", e.Span(), "`+` needs Int, Float, Byte or String operands, found %s", Deep(l))
		} else {
			c.errorf("R0301", e.Span(), "`%s` needs Int, Float or Byte operands, found %s", op, Deep(l))
		}
		return invalid
	}
	c.foldBinary(e, l)
	return l
}

// interpolated checks an expression in "${...}": it must be an Int, Float,
// Bool, Byte or String (DESIGN §4.5).
func (c *checker) interpolated(x syntax.Expr) {
	t := c.expr(x, nil)
	switch r := Resolve(t).(type) {
	case *Basic:
		if r != Unit && r != ErrorT {
			return
		}
	case *Var, *Invalid:
		return
	}
	d := c.errorf("R0301", x.Span(), "cannot interpolate a %s: `${...}` takes an Int, Float, Bool, Byte or String", Deep(t))
	switch r := Resolve(t).(type) {
	case *Basic:
		if r == ErrorT {
			d.Notes = append(d.Notes, "interpolate the error's message")
			end := x.Span().End
			d.Fixes = append(d.Fixes, diag.Fix{Title: "interpolate its message",
				Edits: []diag.Edit{c.fset.Edit(syntax.Span{Start: end, End: end}, ".message()")}})
		}
	case *List:
		d.Notes = append(d.Notes, "join the elements into a String first, as in `xs.map(|x| x.to_string()).join(\", \")`")
	case *Option, *Result:
		d.Notes = append(d.Notes, "handle both cases first, for example with match or `.unwrap_or(...)`")
	case *Named:
		d.Notes = append(d.Notes, fmt.Sprintf("give %s a method that returns a String and interpolate its result, as in `${x.show()}`", r.Decl.Name))
	}
}

// cast checks `x as T`, which converts between Int, Float and Byte. A
// constant operand gives a constant result.
func (c *checker) cast(e *syntax.AsExpr) Type {
	to := c.resolveType(e.Type)
	from := c.expr(e.X, nil)
	if isInvalid(to) || isInvalid(from) {
		return invalid
	}
	if isNever(from) {
		return Never
	}
	f, t := Resolve(from), Resolve(to)
	if !numeric(f) || !numeric(t) {
		d := c.errorf("R0301", e.Span(), "`as` converts between Int, Float and Byte, not %s to %s", Deep(f), Deep(t))
		switch {
		case f == String && (t == Int || t == Float):
			d.Notes = append(d.Notes, fmt.Sprintf("parse the text instead: `.parse_%s()` returns a Result", strings.ToLower(t.String())))
		case t == String:
			d.Notes = append(d.Notes, "make text with `.to_string()` or interpolation, as in \"${x}\"")
		case f == Bool && numeric(t):
			d.Notes = append(d.Notes, "write `if b { 1 } else { 0 }`")
		}
		return invalid
	}
	info := &CastInfo{From: f, To: t}
	if k := c.consts[e.X]; k != nil {
		info.Const = convertConst(k, f, t)
		c.consts[e] = info.Const
	}
	c.info.Casts[e] = info
	return t
}

// widening returns the type a value of type from converts to, with `as`,
// to fit where a value of type to is expected without losing its value: Int
// and Byte to Float, Byte to Int. It returns "" for any other pair.
func widening(from, to Type) string {
	switch f, t := Resolve(from), Resolve(to); {
	case t == Float && (f == Int || f == Byte):
		return "Float"
	case t == Int && f == Byte:
		return "Int"
	}
	return ""
}

// asFix converts x with `as T`, adding parentheses where the precedence of
// `as`, tighter than any binary operator, needs them.
func (c *checker) asFix(x syntax.Expr, to string) diag.Fix {
	sp := x.Span()
	fix := diag.Fix{Title: "convert it to " + to}
	switch x.(type) {
	case *syntax.BinaryExpr, *syntax.ClosureExpr, *syntax.ReturnExpr:
		fix.Edits = append(fix.Edits, c.fset.Edit(syntax.Span{Start: sp.Start, End: sp.Start}, "("),
			c.fset.Edit(syntax.Span{Start: sp.End, End: sp.End}, ") as "+to))
	default:
		fix.Edits = append(fix.Edits, c.fset.Edit(syntax.Span{Start: sp.End, End: sp.End}, " as "+to))
	}
	return fix
}

func (c *checker) structLit(e *syntax.StructLit, want Type) Type {
	name := e.Type.Name
	rest := func() Type {
		for _, f := range e.Fields {
			c.expr(f.Value, nil)
		}
		if e.Base != nil {
			c.expr(e.Base, nil)
		}
		return invalid
	}
	var td *TypeDecl
	var targs []Type
	switch name {
	case "Sys", "Out":
		d := c.errorf("R0603", e.Type.Span(), "capabilities cannot be made: there is no `%s { ... }`", name)
		d.Notes = append(d.Notes, "main receives `sys: Sys`; other functions receive the capabilities they are passed")
		return rest()
	case "Self":
		if c.selfDecl == nil {
			c.errorf("R0201", e.Type.Span(), "`Self` is only available inside an impl block")
			return rest()
		}
		td, targs = c.selfDecl, c.selfType.(*Named).Args
	default:
		g := c.globals[name]
		td, _ = g.(*TypeDecl)
		if td == nil {
			if _, ok := reserved[name]; ok || g != nil {
				c.errorf("R0303", e.Type.Span(), "`%s` is not a struct", name)
			} else {
				c.unknown(e.Type, "type", c.typeNames())
			}
			return rest()
		}
		td.refs = append(td.refs, e.Type.Span())
	}
	if td.IsEnum {
		c.errorf("R0303", e.Type.Span(), "`%s` is an enum: build a variant, as in `%s::%s(...)`", td.Name, td.Name, firstVariant(td))
		return rest()
	}
	if targs == nil {
		targs = c.fresh(td.TParams, e.Span(), "`"+td.Name+"`")
	}
	self := &Named{Decl: td, Args: targs}
	if want != nil {
		c.assignable(self, want)
	}
	use := &StructUse{Decl: td, TypeArgs: targs}
	given := map[string]bool{}
	for _, fi := range e.Fields {
		f := td.Field(fi.Name.Name)
		use.Fields = append(use.Fields, f)
		if f == nil {
			var cands []string
			for _, f := range td.Fields {
				cands = append(cands, f.Name)
			}
			msg := fmt.Sprintf("%s has no field `%s`", td.Name, fi.Name.Name)
			if close := closest(fi.Name.Name, cands); len(close) > 0 {
				msg += "; did you mean " + orList(close) + "?"
			}
			c.errorf("R0303", fi.Name.Span(), "%s", msg)
			c.expr(fi.Value, nil)
			continue
		}
		f.refs = append(f.refs, fi.Name.Span())
		if given[f.Name] {
			c.errorf("R0303", fi.Name.Span(), "field `%s` is given twice", f.Name)
		}
		given[f.Name] = true
		c.expr(fi.Value, FieldType(f, td, targs))
	}
	if e.Base != nil {
		c.expr(e.Base, self)
	} else {
		var missing []string
		for _, f := range td.Fields {
			if !given[f.Name] {
				missing = append(missing, "`"+f.Name+"`")
			}
		}
		if len(missing) > 0 {
			word := "field"
			if len(missing) > 1 {
				word = "fields"
			}
			d := c.errorf("R0303", e.Type.Span(), "missing %s %s in `%s`", word, andList(missing), td.Name)
			d.Notes = append(d.Notes, "give every field, or copy the rest from another value with `..base`")
		}
	}
	c.info.Structs[e] = use
	return self
}

func andList(items []string) string {
	if len(items) == 1 {
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}
