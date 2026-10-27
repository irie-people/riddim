package syntax

// A Node is any syntax tree node.
type Node interface{ Span() Span }

// A File is one parsed source file.
type File struct {
	Source *Source
	Decls  []Decl
}

// An Ident is a name.
type Ident struct {
	NamePos Pos
	Name    string
}

func (x *Ident) Span() Span { return Span{x.NamePos, x.NamePos + Pos(len(x.Name))} }

// ---- Declarations ----

// A Decl is a top-level declaration.
type Decl interface {
	Node
	declNode()
}

// A FuncDecl is `fn name<T>(params) -> Result { body }`, at top level or in
// an impl block.
type FuncDecl struct {
	Fn         Pos
	Name       *Ident
	TypeParams []*Ident
	Params     []*Param
	Result     TypeExpr // nil for ()
	Body       *BlockExpr
}

// A Param is a function parameter; the receiver is a Param with Self set.
type Param struct {
	Name *Ident
	Type TypeExpr // nil for self
	Self bool
}

// A StructDecl is `struct Name<T> { fields }`.
type StructDecl struct {
	Struct     Pos
	Name       *Ident
	TypeParams []*Ident
	Fields     []*FieldDecl
	Rbrace     Pos
}

// A FieldDecl is `name: Type` in a struct.
type FieldDecl struct {
	Name *Ident
	Type TypeExpr
}

// An EnumDecl is `enum Name<T> { variants }`.
type EnumDecl struct {
	Enum       Pos
	Name       *Ident
	TypeParams []*Ident
	Variants   []*VariantDecl
	Rbrace     Pos
}

// A VariantDecl is `Name`, `Name(T, U)` or `Name(a: T, b: U)`.
type VariantDecl struct {
	Name   *Ident
	Fields []*VariantField
	Rparen Pos // NoPos without parentheses
}

// A VariantField is one field of a variant; Name is nil when unnamed.
type VariantField struct {
	Name *Ident
	Type TypeExpr
}

// An ImplDecl is `impl<T> Type<T> { methods }`.
type ImplDecl struct {
	Impl       Pos
	TypeParams []*Ident
	Type       *NamedType
	Methods    []*FuncDecl
	Rbrace     Pos
}

// A ConstDecl is `const NAME: Type = value`.
type ConstDecl struct {
	Const Pos
	Name  *Ident
	Type  TypeExpr
	Value Expr
}

func (d *FuncDecl) Span() Span   { return Span{d.Fn, d.Body.Rbrace + 1} }
func (d *StructDecl) Span() Span { return Span{d.Struct, d.Rbrace + 1} }
func (d *EnumDecl) Span() Span   { return Span{d.Enum, d.Rbrace + 1} }
func (d *ImplDecl) Span() Span   { return Span{d.Impl, d.Rbrace + 1} }
func (d *ConstDecl) Span() Span  { return Span{d.Const, d.Value.Span().End} }

func (*FuncDecl) declNode()   {}
func (*StructDecl) declNode() {}
func (*EnumDecl) declNode()   {}
func (*ImplDecl) declNode()   {}
func (*ConstDecl) declNode()  {}

// ---- Types ----

// A TypeExpr is a type as written.
type TypeExpr interface {
	Node
	typeNode()
}

// A NamedType is `Name` or `Name<A, B>`, including built-in types and Self.
type NamedType struct {
	Name   *Ident
	Args   []TypeExpr
	Rangle Pos // NoPos without type arguments
}

// A TupleType is `()` or `(A, B, ...)`.
type TupleType struct {
	Lparen, Rparen Pos
	Elems          []TypeExpr
}

// A FuncType is `fn(A, B) -> R`.
type FuncType struct {
	Fn     Pos
	Params []TypeExpr
	Result TypeExpr // nil for ()
	End    Pos
}

func (t *NamedType) Span() Span {
	if t.Rangle != NoPos {
		return Span{t.Name.NamePos, t.Rangle + 1}
	}
	return t.Name.Span()
}
func (t *TupleType) Span() Span { return Span{t.Lparen, t.Rparen + 1} }
func (t *FuncType) Span() Span  { return Span{t.Fn, t.End} }

func (*NamedType) typeNode() {}
func (*TupleType) typeNode() {}
func (*FuncType) typeNode()  {}

// ---- Statements ----

// A Stmt is a statement in a block.
type Stmt interface {
	Node
	stmtNode()
}

// A LetStmt is `let [mut] pattern [: Type] = value`.
type LetStmt struct {
	Let   Pos
	Mut   bool
	Pat   Pattern
	Type  TypeExpr
	Value Expr
}

// An AssignStmt is `target op value` with op one of = += -= *= /=.
type AssignStmt struct {
	Target Expr
	OpPos  Pos
	Op     Kind
	Value  Expr
}

// An ExprStmt is an expression used as a statement. The last statement of a
// block, when it is an ExprStmt, gives the block its value.
type ExprStmt struct{ X Expr }

func (s *LetStmt) Span() Span    { return Span{s.Let, s.Value.Span().End} }
func (s *AssignStmt) Span() Span { return Span{s.Target.Span().Start, s.Value.Span().End} }
func (s *ExprStmt) Span() Span   { return s.X.Span() }

func (*LetStmt) stmtNode()    {}
func (*AssignStmt) stmtNode() {}
func (*ExprStmt) stmtNode()   {}

// ---- Expressions ----

// An Expr is an expression.
type Expr interface {
	Node
	exprNode()
}

type (
	// IntLit is an integer literal; Text is the source text.
	IntLit struct {
		ValuePos Pos
		Text     string
	}
	// FloatLit is a floating-point literal.
	FloatLit struct {
		ValuePos Pos
		Text     string
	}
	// StringLit is a string literal; Value is decoded.
	StringLit struct {
		Start, End Pos
		Value      string
	}
	// BoolLit is true or false.
	BoolLit struct {
		ValuePos Pos
		Value    bool
	}
	// UnitLit is `()`.
	UnitLit struct{ Lparen, Rparen Pos }
	// PathExpr is `Type::Name`, e.g. Shape::Circle or Point::new.
	PathExpr struct {
		Type *Ident // may be Self
		Name *Ident
	}
	// CallExpr is `fun(args)`.
	CallExpr struct {
		Fun    Expr
		Args   []Expr
		Rparen Pos
	}
	// MethodCallExpr is `recv.name(args)`.
	MethodCallExpr struct {
		Recv   Expr
		Name   *Ident
		Args   []Expr
		Rparen Pos
	}
	// FieldExpr is `x.name` or a tuple field `x.0`.
	FieldExpr struct {
		X    Expr
		Name *Ident
	}
	// IndexExpr is `x[i]`.
	IndexExpr struct {
		X      Expr
		Index  Expr
		Rbrack Pos
	}
	// TryExpr is `x?`.
	TryExpr struct {
		X     Expr
		Quest Pos
	}
	// AsExpr is `x as Type`.
	AsExpr struct {
		X    Expr
		As   Pos
		Type TypeExpr
	}
	// UnaryExpr is `-x` or `!x`.
	UnaryExpr struct {
		OpPos Pos
		Op    Kind
		X     Expr
	}
	// BinaryExpr is `x op y`.
	BinaryExpr struct {
		X     Expr
		OpPos Pos
		Op    Kind
		Y     Expr
	}
	// ParenExpr is `(x)`.
	ParenExpr struct {
		Lparen Pos
		X      Expr
		Rparen Pos
	}
	// TupleExpr is `(a, b, ...)`.
	TupleExpr struct {
		Lparen Pos
		Elems  []Expr
		Rparen Pos
	}
	// ListExpr is `[a, b, ...]`.
	ListExpr struct {
		Lbrack Pos
		Elems  []Expr
		Rbrack Pos
	}
	// StructLit is `Name { field: value, ..base }`.
	StructLit struct {
		Type   *Ident // may be Self
		Fields []*FieldInit
		Base   Expr // nil without ..base
		Rbrace Pos
	}
	// BlockExpr is `{ stmts }`.
	BlockExpr struct {
		Lbrace Pos
		Stmts  []Stmt
		Rbrace Pos
	}
	// IfExpr is `if cond { } else ...`; Else is nil, a *BlockExpr or an *IfExpr.
	IfExpr struct {
		If   Pos
		Cond Expr
		Then *BlockExpr
		Else Expr
	}
	// MatchExpr is `match subject { arms }`.
	MatchExpr struct {
		Match   Pos
		Subject Expr
		Lbrace  Pos
		Arms    []*MatchArm
		Rbrace  Pos
	}
	// WhileExpr is `while cond { }`.
	WhileExpr struct {
		While Pos
		Cond  Expr
		Body  *BlockExpr
	}
	// LoopExpr is `loop { }`.
	LoopExpr struct {
		Loop Pos
		Body *BlockExpr
	}
	// ForExpr is `for pat in iter { }`; with End set, iter..end or iter..=end.
	ForExpr struct {
		For       Pos
		Pat       Pattern
		Iter      Expr
		End       Expr
		Inclusive bool
		Body      *BlockExpr
	}
	// ClosureExpr is `|params| body` or `|params| -> R { body }`.
	ClosureExpr struct {
		Pipe   Pos
		Params []*ClosureParam
		Result TypeExpr
		Body   Expr
	}
	// ReturnExpr is `return [value]`.
	ReturnExpr struct {
		Return Pos
		Value  Expr
	}
	// BreakExpr is `break`.
	BreakExpr struct{ Break Pos }
	// ContinueExpr is `continue`.
	ContinueExpr struct{ Continue Pos }
)

// A FieldInit is `name: value` in a struct literal.
type FieldInit struct {
	Name  *Ident
	Value Expr
}

// A MatchArm is `pattern [if guard] => body`.
type MatchArm struct {
	Pat   Pattern
	Guard Expr
	Body  Expr
}

// Span returns the arm's span, from pattern to body.
func (a *MatchArm) Span() Span { return Span{a.Pat.Span().Start, a.Body.Span().End} }

// A ClosureParam is `pattern [: Type]`.
type ClosureParam struct {
	Pat  Pattern
	Type TypeExpr
}

func (x *IntLit) Span() Span         { return Span{x.ValuePos, x.ValuePos + Pos(len(x.Text))} }
func (x *FloatLit) Span() Span       { return Span{x.ValuePos, x.ValuePos + Pos(len(x.Text))} }
func (x *StringLit) Span() Span      { return Span{x.Start, x.End} }
func (x *BoolLit) Span() Span        { return Span{x.ValuePos, x.ValuePos + Pos(len(boolText(x.Value)))} }
func (x *UnitLit) Span() Span        { return Span{x.Lparen, x.Rparen + 1} }
func (x *PathExpr) Span() Span       { return Span{x.Type.NamePos, x.Name.Span().End} }
func (x *CallExpr) Span() Span       { return Span{x.Fun.Span().Start, x.Rparen + 1} }
func (x *MethodCallExpr) Span() Span { return Span{x.Recv.Span().Start, x.Rparen + 1} }
func (x *FieldExpr) Span() Span      { return Span{x.X.Span().Start, x.Name.Span().End} }
func (x *IndexExpr) Span() Span      { return Span{x.X.Span().Start, x.Rbrack + 1} }
func (x *TryExpr) Span() Span        { return Span{x.X.Span().Start, x.Quest + 1} }
func (x *AsExpr) Span() Span         { return Span{x.X.Span().Start, x.Type.Span().End} }
func (x *UnaryExpr) Span() Span      { return Span{x.OpPos, x.X.Span().End} }
func (x *BinaryExpr) Span() Span     { return Span{x.X.Span().Start, x.Y.Span().End} }
func (x *ParenExpr) Span() Span      { return Span{x.Lparen, x.Rparen + 1} }
func (x *TupleExpr) Span() Span      { return Span{x.Lparen, x.Rparen + 1} }
func (x *ListExpr) Span() Span       { return Span{x.Lbrack, x.Rbrack + 1} }
func (x *StructLit) Span() Span      { return Span{x.Type.NamePos, x.Rbrace + 1} }
func (x *BlockExpr) Span() Span      { return Span{x.Lbrace, x.Rbrace + 1} }
func (x *MatchExpr) Span() Span      { return Span{x.Match, x.Rbrace + 1} }
func (x *WhileExpr) Span() Span      { return Span{x.While, x.Body.Rbrace + 1} }
func (x *LoopExpr) Span() Span       { return Span{x.Loop, x.Body.Rbrace + 1} }
func (x *ForExpr) Span() Span        { return Span{x.For, x.Body.Rbrace + 1} }
func (x *ClosureExpr) Span() Span    { return Span{x.Pipe, x.Body.Span().End} }
func (x *BreakExpr) Span() Span      { return Span{x.Break, x.Break + 5} }
func (x *ContinueExpr) Span() Span   { return Span{x.Continue, x.Continue + 8} }

func (x *IfExpr) Span() Span {
	if x.Else != nil {
		return Span{x.If, x.Else.Span().End}
	}
	return Span{x.If, x.Then.Rbrace + 1}
}

func (x *ReturnExpr) Span() Span {
	if x.Value != nil {
		return Span{x.Return, x.Value.Span().End}
	}
	return Span{x.Return, x.Return + 6}
}

func boolText(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func (*IntLit) exprNode()         {}
func (*FloatLit) exprNode()       {}
func (*StringLit) exprNode()      {}
func (*BoolLit) exprNode()        {}
func (*UnitLit) exprNode()        {}
func (*Ident) exprNode()          {}
func (*PathExpr) exprNode()       {}
func (*CallExpr) exprNode()       {}
func (*MethodCallExpr) exprNode() {}
func (*FieldExpr) exprNode()      {}
func (*IndexExpr) exprNode()      {}
func (*TryExpr) exprNode()        {}
func (*AsExpr) exprNode()         {}
func (*UnaryExpr) exprNode()      {}
func (*BinaryExpr) exprNode()     {}
func (*ParenExpr) exprNode()      {}
func (*TupleExpr) exprNode()      {}
func (*ListExpr) exprNode()       {}
func (*StructLit) exprNode()      {}
func (*BlockExpr) exprNode()      {}
func (*IfExpr) exprNode()         {}
func (*MatchExpr) exprNode()      {}
func (*WhileExpr) exprNode()      {}
func (*LoopExpr) exprNode()       {}
func (*ForExpr) exprNode()        {}
func (*ClosureExpr) exprNode()    {}
func (*ReturnExpr) exprNode()     {}
func (*BreakExpr) exprNode()      {}
func (*ContinueExpr) exprNode()   {}

// ---- Patterns ----

// A Pattern is a pattern in match, let, for or a closure parameter.
type Pattern interface {
	Node
	patNode()
}

type (
	// WildcardPat is `_`.
	WildcardPat struct{ Under Pos }
	// BindPat is a snake_case name that binds the matched value.
	BindPat struct{ Name *Ident }
	// LitPat is an Int, String or Bool literal; Neg marks a leading `-`.
	LitPat struct {
		Minus Pos // NoPos without `-`
		Lit   Expr
	}
	// TuplePat is `(p, q, ...)`.
	TuplePat struct {
		Lparen Pos
		Elems  []Pattern
		Rparen Pos
	}
	// VariantPat is `Name`, `Name(p, ...)`, `Type::Name(p, ...)`, and also
	// Some(p), None, Ok(p) and Err(p).
	VariantPat struct {
		Type   *Ident // nil when bare
		Name   *Ident
		Args   []Pattern
		Rparen Pos // NoPos without parentheses
	}
	// OrPat is `p | q | ...`.
	OrPat struct{ Alts []Pattern }
)

func (p *WildcardPat) Span() Span { return Span{p.Under, p.Under + 1} }
func (p *BindPat) Span() Span     { return p.Name.Span() }
func (p *TuplePat) Span() Span    { return Span{p.Lparen, p.Rparen + 1} }
func (p *OrPat) Span() Span {
	return Span{p.Alts[0].Span().Start, p.Alts[len(p.Alts)-1].Span().End}
}

func (p *LitPat) Span() Span {
	sp := p.Lit.Span()
	if p.Minus != NoPos {
		sp.Start = p.Minus
	}
	return sp
}

func (p *VariantPat) Span() Span {
	start := p.Name.NamePos
	if p.Type != nil {
		start = p.Type.NamePos
	}
	if p.Rparen != NoPos {
		return Span{start, p.Rparen + 1}
	}
	return Span{start, p.Name.Span().End}
}

func (*WildcardPat) patNode() {}
func (*BindPat) patNode()     {}
func (*LitPat) patNode()      {}
func (*TuplePat) patNode()    {}
func (*VariantPat) patNode()  {}
func (*OrPat) patNode()       {}
