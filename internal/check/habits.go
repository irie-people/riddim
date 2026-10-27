package check

import (
	"github.com/riddim-hq/riddim/internal/diag"
	"github.com/riddim-hq/riddim/internal/syntax"
)

// The Rust and Go habits that need names or types to tell apart from
// Riddim (DESIGN §7.2). Each has its own code and, where the rewrite is
// mechanical, a fix; checking goes on as if the fix had been made.

// text returns the source text of a span.
func (c *checker) text(sp syntax.Span) string {
	src := c.fset.Source(sp.Start)
	return string(src.Src[src.Offset(sp.Start):src.Offset(sp.End)])
}

func (c *checker) replaceFix(d *diag.Diagnostic, title string, sp syntax.Span, text string) {
	d.Fixes = append(d.Fixes, diag.Fix{Title: title, Edits: []diag.Edit{c.fset.Edit(sp, text)}})
}

var basicByName = map[string]Type{"Int": Int, "Float": Float, "Bool": Bool, "String": String, "Byte": Byte, "Error": ErrorT}

// habitType resolves a type written as Rust or Go would write it: i32, f64,
// &str's str (R0110), Vec<T> (R0113), HashMap<K, V> (R0114), Box<T> (R0111).
// It returns nil for any other name.
func (c *checker) habitType(t *syntax.NamedType, args []Type) Type {
	name := t.Name.Name
	if _, ok := syntax.HabitTypes[name]; ok && len(args) == 0 {
		to, msg, notes := syntax.HabitTypeMsg(name)
		d := c.errorf("R0110", t.Name.Span(), "%s", msg)
		d.Notes = append(d.Notes, notes...)
		c.replaceFix(d, "write "+to, t.Name.Span(), to)
		return basicByName[to]
	}
	switch name {
	case "Vec", "VecDeque":
		if len(args) == 1 {
			d := c.errorf("R0113", t.Name.Span(), syntax.HabitVecMsg, name)
			c.replaceFix(d, "write List", t.Name.Span(), "List")
			return &List{args[0]}
		}
	case "HashMap", "BTreeMap":
		if len(args) == 2 {
			d := c.errorf("R0114", t.Name.Span(), syntax.HabitMapMsg, name)
			c.replaceFix(d, "write Map", t.Name.Span(), "Map")
			return &Map{args[0], args[1]}
		}
	case "Box", "Rc", "Arc", "RefCell", "Cell":
		if len(args) == 1 {
			inner := c.text(t.Args[0].Span())
			d := c.errorf("R0111", t.Span(), syntax.HabitBoxMsg, inner)
			d.Notes = append(d.Notes, syntax.HabitBoxNote)
			c.replaceFix(d, "hold the value", t.Span(), inner)
			return args[0]
		}
	}
	return nil
}

// habitPath reports Rust's constructors of the types Riddim spells
// differently: Vec::new() (R0113), HashMap::new() (R0114), String::new() and
// String::from(s) (R0119). It returns false for any other call.
func (c *checker) habitPath(e *syntax.CallExpr, fun *syntax.PathExpr, want Type) (Type, bool) {
	tn, fname := fun.Type.Name, fun.Name.Name
	if c.globals[tn] != nil {
		return nil, false
	}
	c.info.Types[fun] = invalid
	switch {
	case (tn == "Vec" || tn == "List") && (fname == "new" || fname == "with_capacity"):
		d := c.errorf("R0113", e.Span(), "%s", syntax.HabitVecNewMsg)
		c.replaceFix(d, "write []", e.Span(), "[]")
		for _, a := range e.Args {
			c.expr(a, nil)
		}
		if l, ok := Resolve(want).(*List); ok {
			return l, true
		}
		return invalid, true
	case (tn == "HashMap" || tn == "BTreeMap") && (fname == "new" || fname == "with_capacity"):
		d := c.errorf("R0114", e.Span(), "%s", syntax.HabitMapNewMsg)
		c.replaceFix(d, "write Map::new()", e.Span(), "Map::new()")
		for _, a := range e.Args {
			c.expr(a, nil)
		}
		if m, ok := Resolve(want).(*Map); ok {
			return m, true
		}
		return invalid, true
	case (tn == "Box" || tn == "Rc" || tn == "Arc") && fname == "new" && len(e.Args) == 1:
		d := c.errorf("R0111", e.Span(), "%s", syntax.HabitBoxNewMsg)
		c.replaceFix(d, "use the value", e.Span(), c.text(e.Args[0].Span()))
		return c.expr(e.Args[0], want), true
	case tn == "String" && fname == "new" && len(e.Args) == 0:
		d := c.errorf("R0119", e.Span(), "%s", syntax.HabitStringNewMsg)
		c.replaceFix(d, `write ""`, e.Span(), `""`)
		return String, true
	case tn == "String" && fname == "from" && len(e.Args) == 1:
		d := c.errorf("R0119", e.Span(), "%s", syntax.HabitStringFromMsg)
		c.replaceFix(d, "use the string itself", e.Span(), c.text(e.Args[0].Span()))
		c.expr(e.Args[0], String)
		return String, true
	}
	return nil, false
}

// habitMethod reports one of syntax.HabitMethods called on a value whose
// type has no method of that name, with a fix that deletes the call, and
// types the call as its receiver.
func (c *checker) habitMethod(e *syntax.MethodCallExpr, rt Type) (Type, bool) {
	name := e.Name.Name
	code := syntax.HabitMethods[name]
	if code == "" || name == "as_str" && Resolve(rt) != String {
		return nil, false
	}
	d := c.errorf(code, e.Name.Span(), "%s", syntax.HabitMethodMsg(name))
	c.replaceFix(d, "delete `."+name+"()`", syntax.Span{Start: e.Recv.Span().End, End: e.Rparen + 1}, "")
	for _, a := range e.Args {
		c.expr(a, nil)
	}
	return rt, true
}

// undeclared reports whether name is neither a local nor a global.
func (c *checker) undeclared(name string) bool {
	return c.lookupLocal(name) == nil && c.globals[name] == nil
}

func (c *checker) isNil(x syntax.Expr) bool {
	id, ok := unparen(x).(*syntax.Ident)
	return ok && id.Name == "nil" && c.undeclared("nil")
}

// nilValue reports Go's nil used as a value (R0109). Where an Option is
// expected the fix writes None; where a Result<(), E> is, Ok(()).
func (c *checker) nilValue(e *syntax.Ident, want Type) Type {
	d := c.errorf("R0109", e.Span(), "%s", syntax.HabitNilMsg)
	switch w := Resolve(want).(type) {
	case *Option:
		c.replaceFix(d, "write None", e.Span(), "None")
		return w
	case *Result:
		if isUnit(w.Ok) {
			d.Notes = append(d.Notes, "a function that returns Result<(), E> returns `Ok(())` when nothing failed")
			c.replaceFix(d, "write Ok(())", e.Span(), "Ok(())")
		}
	case *Basic:
		if w == ErrorT {
			d.Notes = append(d.Notes, "an Error always holds a message; a function that can fail returns Result<T, Error>")
		}
	case nil, *Var:
		c.replaceFix(d, "write None", e.Span(), "None")
	}
	return invalid
}

// nilCompare checks `x == nil` and `x != nil`. On an Option it is R0109,
// with a fix that calls is_none() or is_some(); on anything else it is Go's
// error check, `if err != nil`, which is `?` in Riddim (R0120). It returns
// false when neither side is nil.
func (c *checker) nilCompare(e *syntax.BinaryExpr) bool {
	nilSide, other := e.Y, e.X
	if !c.isNil(e.Y) {
		if !c.isNil(e.X) {
			return false
		}
		nilSide, other = e.X, e.Y
	}
	c.info.Types[nilSide] = invalid
	var ot Type = invalid
	if id, ok := unparen(other).(*syntax.Ident); ok && c.undeclared(id.Name) {
		c.info.Types[other] = invalid // Go's err, which nothing declared
	} else {
		ot = c.expr(other, nil)
	}
	if _, ok := Resolve(ot).(*Option); ok {
		method := "is_none()"
		if e.Op == syntax.NE {
			method = "is_some()"
		}
		d := c.errorf("R0109", nilSide.Span(), syntax.HabitNilTestMsg, method)
		text := c.text(other.Span())
		switch unparen(other).(type) {
		case *syntax.BinaryExpr, *syntax.UnaryExpr, *syntax.AsExpr, *syntax.ClosureExpr:
			text = "(" + text + ")"
		}
		c.replaceFix(d, "call "+method, e.Span(), text+"."+method)
		return true
	}
	d := c.errorf("R0120", e.Span(), "%s", syntax.HabitErrNilMsg)
	d.Notes = append(d.Notes, syntax.HabitErrNilNote)
	return true
}

// macro types a Rust macro call, which the parser reported (R0115), as what
// its fix leaves: println! prints, format! makes a String, panic! and todo!
// do not return. Its arguments are checked for the errors and habits in
// them. A macro without a Riddim form has no type, so nothing else reports
// it.
func (c *checker) macro(e *syntax.MacroCall) Type {
	var t Type
	switch e.Name.Name {
	case "println", "print", "eprintln", "eprint", "assert", "assert_eq", "assert_ne", "debug_assert":
		t = Unit
	case "format":
		t = String
	case "panic", "unreachable", "todo", "unimplemented":
		t = Never
	default:
		return invalid
	}
	for _, a := range e.Args {
		c.expr(a, nil)
	}
	return t
}

// hasMember reports whether a struct or enum of the program has a method or
// a field called name.
func (c *checker) hasMember(name string) bool {
	for _, td := range c.prog.Types {
		if td.Method(name) != nil || td.Field(name) != nil {
			return true
		}
	}
	return false
}
