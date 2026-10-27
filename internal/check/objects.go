package check

import (
	"math/big"

	"github.com/riddim-hq/riddim/internal/syntax"
)

// A TypeParam is a type parameter of a function, struct, enum or impl.
type TypeParam struct {
	Name  string
	Ident *syntax.Ident
	refs  []syntax.Span
}

func (t *TypeParam) String() string { return t.Name }

// A TypeDecl is a struct or an enum.
type TypeDecl struct {
	Name     string
	Ident    *syntax.Ident
	Decl     syntax.Decl
	IsEnum   bool
	TParams  []*TypeParam
	Fields   []*Field   // structs
	Variants []*Variant // enums
	Methods  []*FuncObj // methods and associated functions, in order
	byName   map[string]*FuncObj
	refs     []syntax.Span
}

// Method returns the method or associated function called name.
func (d *TypeDecl) Method(name string) *FuncObj { return d.byName[name] }

// Field returns the struct field called name.
func (d *TypeDecl) Field(name string) *Field {
	for _, f := range d.Fields {
		if f.Name == name {
			return f
		}
	}
	return nil
}

// Variant returns the enum variant called name.
func (d *TypeDecl) Variant(name string) *Variant {
	for _, v := range d.Variants {
		if v.Name == name {
			return v
		}
	}
	return nil
}

// Fieldless reports whether d is an enum whose variants have no fields and
// which has no type parameters; it lowers to a Go integer type.
func (d *TypeDecl) Fieldless() bool {
	if !d.IsEnum || len(d.TParams) > 0 {
		return false
	}
	for _, v := range d.Variants {
		if len(v.Fields) > 0 {
			return false
		}
	}
	return true
}

// A Field is a struct field or a variant field. Unnamed variant fields have
// an empty Name.
type Field struct {
	Name  string
	Ident *syntax.Ident
	Type  Type
	Index int
	refs  []syntax.Span
}

// A Variant is one variant of an enum.
type Variant struct {
	Name   string
	Ident  *syntax.Ident
	Enum   *TypeDecl
	Index  int
	Fields []*Field
	Named  bool
	refs   []syntax.Span
}

// A FuncObj is a top-level function, a method (HasSelf) or an associated
// function of a struct or enum (Recv set).
type FuncObj struct {
	Name        string
	Decl        *syntax.FuncDecl
	TParams     []*TypeParam // the function's own
	Params      []Type       // without self
	ParamLocals []*Local     // without self
	Result      Type
	Recv        *TypeDecl    // owner, for methods and associated functions
	ImplTParams []*TypeParam // the impl block's
	HasSelf     bool
	SelfLocal   *Local
	refs        []syntax.Span
}

// Sig returns the function's type, without self.
func (f *FuncObj) Sig() *Func { return &Func{Params: f.Params, Result: f.Result} }

// A ConstObj is a const declaration.
type ConstObj struct {
	Name   string
	Decl   *syntax.ConstDecl
	Type   Type
	Value  *Const
	state  int // 0 unchecked, 1 checking, 2 done
	cyclic bool
	refs   []syntax.Span
}

// A Const is the value of a constant expression.
type Const struct {
	Int   *big.Int // Int and Byte
	Float float64
	Bool  bool
	Str   string
}

// A Local is a binding: a parameter, self, or a name bound by a pattern.
type Local struct {
	Name     string
	Decls    []*syntax.Ident // or-patterns bind one local in several places
	Type     Type
	Mutable  bool
	Param    bool // a parameter or self: never reported unused
	Used     bool // read in reachable code
	Assigned bool
	fn       *funcCtx
	refs     []syntax.Span
}

// Info records what the emitter needs about each node.
type Info struct {
	Types     map[syntax.Expr]Type
	Uses      map[syntax.Expr]*Use // *syntax.Ident and *syntax.PathExpr values
	Methods   map[*syntax.MethodCallExpr]*MethodUse
	Fields    map[*syntax.FieldExpr]*FieldUse
	Structs   map[*syntax.StructLit]*StructUse
	Pats      map[syntax.Pattern]*PatInfo
	Defs      map[*syntax.Ident]*Local
	Closures  map[*syntax.ClosureExpr]*ClosureInfo
	Tries     map[*syntax.TryExpr]*TryInfo
	Funcs     map[*syntax.FuncDecl]*FuncObj
	Consts    map[*syntax.ConstDecl]*ConstObj
	TypeDecls map[syntax.Decl]*TypeDecl
}

// UseKind says what a name in an expression refers to.
type UseKind int

// Use kinds.
const (
	UseLocal UseKind = iota
	UseFunc
	UseConst
	UseVariant // an enum variant: a value, or a constructor when called
	UseAssoc   // an associated function, Type::name
	UseSome
	UseNone
	UseOk
	UseErr
	UseError // error(msg)
	UsePanic
	UseTodo
)

// A Use is the meaning of a name or path in an expression.
type Use struct {
	Kind     UseKind
	Local    *Local
	Func     *FuncObj
	Const    *ConstObj
	Variant  *Variant
	TypeArgs []Type // UseFunc: the function's; UseAssoc: the impl's then the function's; UseVariant: the enum's; Some/None: [T]; Ok/Err: [T, E]
}

// A MethodUse is the meaning of a method call: a user method, or a built-in
// one named like "list.map".
type MethodUse struct {
	Func     *FuncObj
	RecvArgs []Type // user methods: the receiver type's arguments
	TypeArgs []Type // user methods: the method's own; built-ins: receiver's then own
	Builtin  string
}

// A FieldUse is the meaning of x.name: a struct field, a tuple field or a
// field of Sys.
type FieldUse struct {
	Field *Field
	Tuple int // tuple field index, or -1
	Sys   string
}

// A StructUse is the meaning of a struct literal.
type StructUse struct {
	Decl     *TypeDecl
	TypeArgs []Type
	Fields   []*Field // parallel to the literal's fields
}

// PatKind is the kind of a checked pattern.
type PatKind int

// Pattern kinds.
const (
	PWild PatKind = iota
	PBind
	PInt
	PString
	PBool
	PTuple
	PVariant
	PSome
	PNone
	POk
	PErr
	POr
)

// PatInfo is a checked pattern.
type PatInfo struct {
	Kind     PatKind
	Type     Type
	Local    *Local
	Variant  *Variant
	TypeArgs []Type // PVariant: the enum's type arguments
	Int      *big.Int
	Str      string
	Bool     bool
}

// ClosureInfo describes a closure: its type and the `let mut` bindings it
// captures, which the emitter copies when the closure is made.
type ClosureInfo struct {
	Type     *Func
	Captures []*Local
}

// TryInfo describes e?: whether e is an Option, and the result type of the
// function or closure that ? returns from.
type TryInfo struct {
	Option bool
	Outer  Type
}

// A Program is a checked program.
type Program struct {
	Fset   *syntax.FileSet
	Files  []*syntax.File
	Info   *Info
	Types  []*TypeDecl
	Funcs  []*FuncObj
	Consts []*ConstObj
	Main   *FuncObj
}
