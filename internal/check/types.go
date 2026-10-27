// Package check resolves names, checks and infers types, checks patterns and
// mutability, and records what the emitter needs to lower a program to Go.
package check

import (
	"strings"

	"github.com/riddim-hq/riddim/internal/syntax"
)

// A Type is a Riddim type.
type Type interface {
	String() string
	isType()
}

// BasicKind identifies a built-in type without parameters.
type BasicKind int

// Basic kinds.
const (
	KInt BasicKind = iota
	KFloat
	KBool
	KString
	KByte
	KUnit
	KNever
	KError
)

// Basic is Int, Float, Bool, String, Byte, (), Never or Error.
type Basic struct {
	Kind BasicKind
	name string
}

// The basic types.
var (
	Int    = &Basic{KInt, "Int"}
	Float  = &Basic{KFloat, "Float"}
	Bool   = &Basic{KBool, "Bool"}
	String = &Basic{KString, "String"}
	Byte   = &Basic{KByte, "Byte"}
	Unit   = &Basic{KUnit, "()"}
	Never  = &Basic{KNever, "Never"}
	ErrorT = &Basic{KError, "Error"}
)

type (
	// Tuple is (A, B, ...), two or more fields.
	Tuple struct{ Elems []Type }
	// List is List<T>.
	List struct{ Elem Type }
	// Option is Option<T>.
	Option struct{ Elem Type }
	// Result is Result<T, E>.
	Result struct{ Ok, Err Type }
	// Func is fn(A, B) -> R.
	Func struct {
		Params []Type
		Result Type
	}
	// Named is a struct or enum declared in the program, with type arguments.
	Named struct {
		Decl *TypeDecl
		Args []Type
	}
	// Cap is a capability: Sys or Out in this version.
	Cap struct{ Name string }
	// Var is an inference variable, bound once its type is known.
	Var struct {
		ref    Type
		origin syntax.Span
		what   string
	}
	// Invalid is the type of an expression that already has an error. It is
	// compatible with everything, so errors do not cascade.
	Invalid struct{}
)

// The capabilities of this version.
var (
	SysT = &Cap{"Sys"}
	OutT = &Cap{"Out"}
)

var invalid = &Invalid{}

func (*Basic) isType()     {}
func (*Tuple) isType()     {}
func (*List) isType()      {}
func (*Option) isType()    {}
func (*Result) isType()    {}
func (*Func) isType()      {}
func (*Named) isType()     {}
func (*TypeParam) isType() {}
func (*Cap) isType()       {}
func (*Var) isType()       {}
func (*Invalid) isType()   {}

func (t *Basic) String() string  { return t.name }
func (t *List) String() string   { return "List<" + t.Elem.String() + ">" }
func (t *Option) String() string { return "Option<" + t.Elem.String() + ">" }
func (t *Result) String() string { return "Result<" + t.Ok.String() + ", " + t.Err.String() + ">" }
func (t *Cap) String() string    { return t.Name }
func (*Invalid) String() string  { return "_" }

func (t *Tuple) String() string { return "(" + typeList(t.Elems) + ")" }

func (t *Func) String() string {
	s := "fn(" + typeList(t.Params) + ")"
	if r := Resolve(t.Result); r != Unit {
		s += " -> " + r.String()
	}
	return s
}

func (t *Named) String() string {
	if len(t.Args) == 0 {
		return t.Decl.Name
	}
	return t.Decl.Name + "<" + typeList(t.Args) + ">"
}

func (t *Var) String() string {
	if t.ref != nil {
		return t.ref.String()
	}
	return "_"
}

func typeList(ts []Type) string {
	parts := make([]string, len(ts))
	for i, t := range ts {
		parts[i] = t.String()
	}
	return strings.Join(parts, ", ")
}

// Resolve follows bound inference variables.
func Resolve(t Type) Type {
	for {
		v, ok := t.(*Var)
		if !ok || v.ref == nil {
			return t
		}
		t = v.ref
	}
}

// Deep returns t with every bound variable replaced by its type.
func Deep(t Type) Type {
	switch t := Resolve(t).(type) {
	case *Tuple:
		return &Tuple{deepList(t.Elems)}
	case *List:
		return &List{Deep(t.Elem)}
	case *Option:
		return &Option{Deep(t.Elem)}
	case *Result:
		return &Result{Deep(t.Ok), Deep(t.Err)}
	case *Func:
		return &Func{deepList(t.Params), Deep(t.Result)}
	case *Named:
		return &Named{t.Decl, deepList(t.Args)}
	default:
		return t
	}
}

func deepList(ts []Type) []Type {
	out := make([]Type, len(ts))
	for i, t := range ts {
		out[i] = Deep(t)
	}
	return out
}

// subst replaces type parameters by the types m gives them.
func subst(t Type, m map[*TypeParam]Type) Type {
	if len(m) == 0 {
		return t
	}
	switch t := Resolve(t).(type) {
	case *TypeParam:
		if r, ok := m[t]; ok {
			return r
		}
		return t
	case *Tuple:
		return &Tuple{substList(t.Elems, m)}
	case *List:
		return &List{subst(t.Elem, m)}
	case *Option:
		return &Option{subst(t.Elem, m)}
	case *Result:
		return &Result{subst(t.Ok, m), subst(t.Err, m)}
	case *Func:
		return &Func{substList(t.Params, m), subst(t.Result, m)}
	case *Named:
		return &Named{t.Decl, substList(t.Args, m)}
	default:
		return t
	}
}

func substList(ts []Type, m map[*TypeParam]Type) []Type {
	out := make([]Type, len(ts))
	for i, t := range ts {
		out[i] = subst(t, m)
	}
	return out
}

func bindParams(params []*TypeParam, args []Type) map[*TypeParam]Type {
	m := make(map[*TypeParam]Type, len(params))
	for i, p := range params {
		if i < len(args) {
			m[p] = args[i]
		}
	}
	return m
}

// FieldType returns the type of a struct field or variant field for a
// receiver whose declaration has type arguments args.
func FieldType(f *Field, decl *TypeDecl, args []Type) Type {
	return subst(f.Type, bindParams(decl.TParams, args))
}

func isUnit(t Type) bool    { return Resolve(t) == Unit }
func isNever(t Type) bool   { return Resolve(t) == Never }
func isInvalid(t Type) bool { _, ok := Resolve(t).(*Invalid); return ok }

func isResult(t Type) bool { _, ok := Resolve(t).(*Result); return ok }

// hasVars reports whether t contains an unbound inference variable.
func hasVars(t Type) bool {
	switch t := Resolve(t).(type) {
	case *Var:
		return true
	case *Tuple:
		return anyVars(t.Elems)
	case *List:
		return hasVars(t.Elem)
	case *Option:
		return hasVars(t.Elem)
	case *Result:
		return hasVars(t.Ok) || hasVars(t.Err)
	case *Func:
		return anyVars(t.Params) || hasVars(t.Result)
	case *Named:
		return anyVars(t.Args)
	}
	return false
}

func anyVars(ts []Type) bool {
	for _, t := range ts {
		if hasVars(t) {
			return true
		}
	}
	return false
}

func occurs(v *Var, t Type) bool {
	switch t := Resolve(t).(type) {
	case *Var:
		return t == v
	case *Tuple:
		for _, e := range t.Elems {
			if occurs(v, e) {
				return true
			}
		}
	case *List:
		return occurs(v, t.Elem)
	case *Option:
		return occurs(v, t.Elem)
	case *Result:
		return occurs(v, t.Ok) || occurs(v, t.Err)
	case *Func:
		for _, p := range t.Params {
			if occurs(v, p) {
				return true
			}
		}
		return occurs(v, t.Result)
	case *Named:
		for _, a := range t.Args {
			if occurs(v, a) {
				return true
			}
		}
	}
	return false
}

// unify makes a and b the same type, binding variables, and reports whether
// it could. Never unifies only with Never; use assignable where Never fits
// any type.
func (c *checker) unify(a, b Type) bool {
	a, b = Resolve(a), Resolve(b)
	if a == b {
		return true
	}
	if v, ok := a.(*Var); ok {
		return c.bind(v, b)
	}
	if v, ok := b.(*Var); ok {
		return c.bind(v, a)
	}
	if isInvalid(a) || isInvalid(b) {
		return true
	}
	switch a := a.(type) {
	case *Tuple:
		b, ok := b.(*Tuple)
		return ok && c.unifyList(a.Elems, b.Elems)
	case *List:
		b, ok := b.(*List)
		return ok && c.unify(a.Elem, b.Elem)
	case *Option:
		b, ok := b.(*Option)
		return ok && c.unify(a.Elem, b.Elem)
	case *Result:
		b, ok := b.(*Result)
		return ok && c.unify(a.Ok, b.Ok) && c.unify(a.Err, b.Err)
	case *Func:
		b, ok := b.(*Func)
		return ok && c.unifyList(a.Params, b.Params) && c.unify(a.Result, b.Result)
	case *Named:
		b, ok := b.(*Named)
		return ok && a.Decl == b.Decl && c.unifyList(a.Args, b.Args)
	}
	return false
}

func (c *checker) unifyList(a, b []Type) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !c.unify(a[i], b[i]) {
			return false
		}
	}
	return true
}

func (c *checker) bind(v *Var, t Type) bool {
	if _, ok := t.(*Var); !ok && occurs(v, t) {
		return false
	}
	v.ref = t
	if c.trying > 0 {
		c.trail = append(c.trail, v)
	}
	return true
}

// try runs f and undoes the variable bindings it made if it fails.
func (c *checker) try(f func() bool) bool {
	c.trying++
	mark := len(c.trail)
	ok := f()
	c.trying--
	if !ok {
		for _, v := range c.trail[mark:] {
			v.ref = nil
		}
	}
	c.trail = c.trail[:mark]
	return ok
}

// assignable reports whether a value of type t fits where want is expected,
// binding variables as needed. Never fits everywhere.
func (c *checker) assignable(t, want Type) bool {
	if isNever(t) {
		return true
	}
	return c.try(func() bool { return c.unify(t, want) })
}

// comparable reports whether == works on t (DESIGN §4.4).
func comparable(t Type, seen map[*TypeDecl]bool) bool {
	switch t := Resolve(t).(type) {
	case *Basic:
		return t != ErrorT
	case *Tuple:
		for _, e := range t.Elems {
			if !comparable(e, seen) {
				return false
			}
		}
		return true
	case *Option:
		return comparable(t.Elem, seen)
	case *Result:
		return comparable(t.Ok, seen) && comparable(t.Err, seen)
	case *Named:
		if seen[t.Decl] {
			return true
		}
		if seen == nil {
			seen = map[*TypeDecl]bool{}
		}
		seen[t.Decl] = true
		defer delete(seen, t.Decl)
		for _, f := range t.Decl.Fields {
			if !comparable(FieldType(f, t.Decl, t.Args), seen) {
				return false
			}
		}
		for _, v := range t.Decl.Variants {
			for _, f := range v.Fields {
				if !comparable(FieldType(f, t.Decl, t.Args), seen) {
					return false
				}
			}
		}
		return true
	case *Var, *Invalid:
		return true
	}
	return false // List, functions, capabilities, type parameters
}

// ordered reports whether <, <=, > and >= work on t.
func ordered(t Type) bool {
	switch Resolve(t) {
	case Int, Float, Byte, String:
		return true
	}
	return isInvalid(t)
}

func numeric(t Type) bool {
	switch Resolve(t) {
	case Int, Float, Byte:
		return true
	}
	return false
}
