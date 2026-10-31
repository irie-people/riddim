package syntax

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/irie-people/riddim/internal/diag"
)

// Parse lexes and parses one source file. It reports every independent
// syntax error it can: after an error it skips to the next top-level
// declaration.
//
// Parsing reads on after a Rust or Go habit as if its fix were made, so a
// file whose only errors are habits is Complete. The token pre-scan's
// reports go in f.Habits for the driver to use when the parser or the
// checker cannot reach them (DESIGN §7.2).
func Parse(fs *FileSet, src *Source) (*File, []diag.Diagnostic) {
	toks, lexDiags := Lex(fs, src)
	p := &parser{fs: fs, src: src, toks: toks, tok: toks[0]}
	f := &File{Source: src, Habits: scanHabits(fs, src, toks)}
	for p.tok.Kind != EOF {
		if p.tok.Kind == TERM {
			p.next()
			continue
		}
		f.Decls = append(f.Decls, p.declRecover()...)
	}
	// A lexical error often confuses the parser on the same line; keep only
	// the lexical one there.
	bad := map[int]bool{}
	for _, d := range lexDiags {
		if !habitCodes[d.Code] {
			bad[d.Line] = true
		}
	}
	diags := lexDiags
	for _, d := range p.diags {
		if !bad[d.Line] {
			diags = append(diags, d)
		}
	}
	f.Complete = !p.bailed && !diag.HasBlocking(diags)
	return f, diags
}

type parser struct {
	fs       *FileSet
	src      *Source
	toks     []Token
	pos      int
	tok      Token
	depth    int // unclosed `{` consumed so far
	loops    int // enclosing loops in the current function or closure
	noStruct bool
	params   []*Param // the parameters of the function being parsed
	diags    []diag.Diagnostic
	bailed   bool // a declaration was dropped after a syntax error
}

type bailout struct{}

func (p *parser) next() {
	switch p.tok.Kind {
	case LBRACE:
		p.depth++
	case RBRACE:
		p.depth--
	}
	if p.pos < len(p.toks)-1 {
		p.pos++
	}
	p.tok = p.toks[p.pos]
}

func (p *parser) peek() Token {
	if p.pos+1 < len(p.toks) {
		return p.toks[p.pos+1]
	}
	return p.toks[len(p.toks)-1]
}

// report records an error and goes on parsing.
func (p *parser) report(code string, sp Span, format string, args ...any) *diag.Diagnostic {
	p.diags = append(p.diags, p.fs.Diag(code, sp, format, args...))
	return &p.diags[len(p.diags)-1]
}

// fail records an error and abandons the current declaration.
func (p *parser) fail(code string, sp Span, format string, args ...any) {
	p.report(code, sp, format, args...)
	panic(bailout{})
}

func (p *parser) unexpected(what string) {
	p.fail("R0105", p.tok.Span(), "expected %s, found %s", what, p.tok.describe())
}

func (p *parser) expect(k Kind, what string) Token {
	if p.tok.Kind != k {
		p.unexpected(what)
	}
	t := p.tok
	p.next()
	return t
}

func (p *parser) skipTerms() {
	for p.tok.Kind == TERM {
		p.next()
	}
}

func (p *parser) ident(what string) *Ident {
	if p.tok.Kind != IDENT {
		p.unexpected(what)
	}
	id := &Ident{NamePos: p.tok.Pos, Name: p.tok.Text}
	p.next()
	return id
}

func isDeclStart(k Kind) bool {
	switch k {
	case FN, STRUCT, ENUM, IMPL, CONST, USE:
		return true
	}
	return false
}

func (p *parser) declRecover() (ds []Decl) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(bailout); !ok {
				panic(r)
			}
			p.bailed = true
			p.sync()
			ds = nil
		}
	}()
	return p.decl()
}

// sync skips to the next declaration keyword that starts a line at the top
// level, or at column 1.
func (p *parser) sync() {
	p.next()
	for p.tok.Kind != EOF {
		start := isDeclStart(p.tok.Kind) || p.tok.Kind == IDENT && goDeclWords[p.tok.Text]
		if start && p.tok.NL {
			if _, col := p.src.LineCol(p.tok.Pos); p.depth <= 0 || col == 1 {
				p.depth = 0
				p.loops = 0
				p.noStruct = false
				return
			}
		}
		p.next()
	}
}

// goDeclWords are the words that start a declaration in Rust or Go but not
// in Riddim; after a syntax error, the parser resumes at them too.
var goDeclWords = map[string]bool{"pub": true, "func": true, "package": true, "import": true}

// isModuleHabit reports whether the parser is at Go's `package name` or
// `import`, or Rust's `use path`, which Riddim does not need (R0125).
func (p *parser) isModuleHabit() bool {
	switch next := p.peek(); {
	case p.tok.Kind == USE:
		return next.Kind == IDENT
	case p.tok.Kind != IDENT:
		return false
	case p.tok.Text == "package":
		return next.Kind == IDENT
	case p.tok.Text == "import":
		return next.Kind == STRING || next.Kind == LPAREN || next.Kind == IDENT && p.peekAt(2).Kind == STRING
	}
	return false
}

func (p *parser) decl() []Decl {
	if p.isModuleHabit() {
		ds := p.moduleHabit()
		if p.tok.Kind != EOF {
			p.expect(TERM, "end of line")
		}
		return ds
	}
	if p.isPub() {
		p.pub()
	}
	if p.tok.Kind == IDENT && p.tok.Text == "func" && p.peek().Kind == IDENT {
		p.goFunc()
	}
	var d Decl
	switch p.tok.Kind {
	case FN:
		d = p.funcDecl(false)
	case STRUCT:
		d = p.structDecl()
	case ENUM:
		d = p.enumDecl()
	case IMPL:
		d = p.implDecl()
	case CONST:
		d = p.constDecl()
	case USE:
		d = p.useGo()
	default:
		p.unexpected("a declaration (fn, struct, enum, impl, const or use go)")
	}
	if p.tok.Kind != EOF {
		p.expect(TERM, "end of line after the declaration")
	}
	return []Decl{d}
}

func (p *parser) typeParams() []*Ident {
	if p.tok.Kind != LT {
		return nil
	}
	p.next()
	var tps []*Ident
	for {
		tps = append(tps, p.ident("a type parameter name"))
		if p.tok.Kind == COMMA {
			p.next()
			if p.tok.Kind == GT {
				break
			}
			continue
		}
		break
	}
	p.expect(GT, "`,` or `>`")
	return tps
}

func (p *parser) funcDecl(inImpl bool) *FuncDecl {
	doc := p.tok.Doc
	fn := p.expect(FN, "`fn`").Pos
	name := p.ident("a function name")
	tps := p.typeParams()
	p.expect(LPAREN, "`(`")
	var params []*Param
	for p.tok.Kind != RPAREN {
		if p.tok.Kind == AMP && (p.peek().Kind == SELF || p.peek().Kind == MUT && p.peekAt(2).Kind == SELF) {
			amp := p.tok
			p.next()
			if p.tok.Kind == MUT {
				p.next()
			}
			d := p.report("R0111", amp.Span(), "a method takes `self` by value: Riddim has no references, and a method that changes a value returns the new one")
			d.Fixes = append(d.Fixes, fix("take self by value", p.fs.Edit(Span{amp.Pos, p.tok.Pos}, "")))
		}
		switch {
		case p.tok.Kind == SELF:
			t := p.tok
			if !inImpl {
				p.report("R0105", t.Span(), "`self` is only a parameter of methods in impl blocks")
			} else if len(params) > 0 {
				p.report("R0105", t.Span(), "`self` must be the first parameter")
			}
			p.next()
			if p.tok.Kind == COLON {
				p.fail("R0105", p.tok.Span(), "`self` has no type annotation: write `self`")
			}
			params = append(params, &Param{Name: &Ident{NamePos: t.Pos, Name: "self"}, Self: true})
		case p.tok.Kind == MUT:
			p.fail("R0105", p.tok.Span(), "parameters are immutable: drop `mut` and write `let mut x = x` in the body")
		default:
			pname := p.ident("a parameter name or `)`")
			if p.tok.Kind != COLON {
				p.fail("R0105", pname.Span(), "parameter `%s` needs a type, as in `%s: Int`", pname.Name, pname.Name)
			}
			p.next()
			params = append(params, &Param{Name: pname, Type: p.typ()})
		}
		if p.tok.Kind == COMMA {
			p.next()
			continue
		}
		if p.tok.Kind != RPAREN {
			p.unexpected("`,` or `)`")
		}
	}
	p.next()
	var result TypeExpr
	if p.tok.Kind == ARROW {
		p.next()
		result = p.typ()
	}
	if p.tok.Kind != LBRACE {
		p.unexpected("`{` to start the function body")
	}
	saved, savedParams := p.loops, p.params
	p.loops, p.params = 0, params
	body := p.block()
	p.loops, p.params = saved, savedParams
	return &FuncDecl{Doc: doc, Fn: fn, Name: name, TypeParams: tps, Params: params, Result: result, Body: body}
}

func (p *parser) structDecl() *StructDecl {
	doc := p.tok.Doc
	pos := p.expect(STRUCT, "`struct`").Pos
	name := p.ident("a struct name")
	tps := p.typeParams()
	switch p.tok.Kind {
	case LPAREN:
		p.fail("R0105", p.tok.Span(), "tuple structs are not supported: name the fields, as in `struct %s { x: Int }`", name.Name)
	case TERM, EOF:
		p.fail("R0105", name.Span(), "a struct needs braces, as in `struct %s {}`", name.Name)
	}
	p.expect(LBRACE, "`{`")
	var fields []*FieldDecl
	for {
		p.skipTerms()
		if p.tok.Kind == RBRACE {
			break
		}
		if p.isPub() {
			p.pub()
		}
		fdoc := p.tok.Doc
		fname := p.ident("a field name or `}`")
		if p.tok.Kind != COLON {
			p.fail("R0105", fname.Span(), "field `%s` needs a type, as in `%s: Int`", fname.Name, fname.Name)
		}
		p.next()
		fields = append(fields, &FieldDecl{Doc: fdoc, Name: fname, Type: p.typ()})
		if p.tok.Kind == COMMA || p.tok.Kind == TERM {
			p.next()
			continue
		}
		if p.tok.Kind != RBRACE {
			p.unexpected("`,` or `}`")
		}
	}
	rb := p.tok.Pos
	p.next()
	return &StructDecl{Doc: doc, Struct: pos, Name: name, TypeParams: tps, Fields: fields, Rbrace: rb}
}

func (p *parser) enumDecl() *EnumDecl {
	doc := p.tok.Doc
	pos := p.expect(ENUM, "`enum`").Pos
	name := p.ident("an enum name")
	tps := p.typeParams()
	p.expect(LBRACE, "`{`")
	var variants []*VariantDecl
	for {
		p.skipTerms()
		if p.tok.Kind == RBRACE {
			break
		}
		v := &VariantDecl{Doc: p.tok.Doc}
		v.Name = p.ident("a variant name or `}`")
		switch p.tok.Kind {
		case LBRACE:
			p.fail("R0105", p.tok.Span(), "variant fields go in parentheses, as in `%s(x: Int)`", v.Name.Name)
		case LPAREN:
			p.next()
			named := -1
			for p.tok.Kind != RPAREN {
				f := &VariantField{}
				isNamed := p.tok.Kind == IDENT && p.peek().Kind == COLON
				if named == -1 {
					named = map[bool]int{false: 0, true: 1}[isNamed]
				} else if (named == 1) != isNamed {
					p.fail("R0105", p.tok.Span(), "the fields of a variant are either all named or all unnamed")
				}
				if isNamed {
					f.Name = p.ident("a field name")
					p.next()
				}
				f.Type = p.typ()
				v.Fields = append(v.Fields, f)
				if p.tok.Kind == COMMA {
					p.next()
					continue
				}
				if p.tok.Kind != RPAREN {
					p.unexpected("`,` or `)`")
				}
			}
			v.Rparen = p.tok.Pos
			p.next()
		}
		variants = append(variants, v)
		if p.tok.Kind == COMMA || p.tok.Kind == TERM {
			p.next()
			continue
		}
		if p.tok.Kind != RBRACE {
			p.unexpected("`,` or `}`")
		}
	}
	rb := p.tok.Pos
	p.next()
	return &EnumDecl{Doc: doc, Enum: pos, Name: name, TypeParams: tps, Variants: variants, Rbrace: rb}
}

func (p *parser) implDecl() *ImplDecl {
	doc := p.tok.Doc
	pos := p.expect(IMPL, "`impl`").Pos
	tps := p.typeParams()
	t := p.typ()
	nt, ok := t.(*NamedType)
	if !ok {
		p.fail("R0105", t.Span(), "impl needs a struct or enum name, as in `impl Point { }`")
	}
	p.expect(LBRACE, "`{`")
	var methods []*FuncDecl
	for {
		p.skipTerms()
		if p.tok.Kind == RBRACE {
			break
		}
		if p.isPub() {
			p.pub()
		}
		if p.tok.Kind == IDENT && p.tok.Text == "func" && p.peek().Kind == IDENT {
			p.goFunc()
		}
		if p.tok.Kind != FN {
			p.unexpected("`fn` or `}` in the impl block")
		}
		methods = append(methods, p.funcDecl(true))
		if p.tok.Kind != RBRACE {
			p.expect(TERM, "end of line after the method")
		}
	}
	rb := p.tok.Pos
	p.next()
	return &ImplDecl{Doc: doc, Impl: pos, TypeParams: tps, Type: nt, Methods: methods, Rbrace: rb}
}

func (p *parser) constDecl() *ConstDecl {
	doc := p.tok.Doc
	pos := p.expect(CONST, "`const`").Pos
	name := p.ident("a constant name")
	if p.tok.Kind != COLON {
		p.fail("R0105", name.Span(), "a constant needs a type, as in `const %s: Int = 1`", name.Name)
	}
	p.next()
	t := p.typ()
	p.expect(ASSIGN, "`=`")
	return &ConstDecl{Doc: doc, Const: pos, Name: name, Type: t, Value: p.expr()}
}

// ---- Types ----

func (p *parser) typ() TypeExpr {
	switch p.tok.Kind {
	case AMP:
		return p.refType()
	case STAR:
		return p.ptrType()
	case LBRACK:
		return p.sliceType()
	case IDENT, SELFTYPE:
		if p.tok.Text == "map" && p.peek().Kind == LBRACK {
			return p.goMapType()
		}
		name := &Ident{NamePos: p.tok.Pos, Name: p.tok.Text}
		p.next()
		nt := &NamedType{Name: name}
		if p.tok.Kind == LT {
			p.next()
			for {
				nt.Args = append(nt.Args, p.typ())
				if p.tok.Kind == COMMA {
					p.next()
					if p.tok.Kind == GT {
						break
					}
					continue
				}
				break
			}
			nt.Rangle = p.expect(GT, "`,` or `>`").Pos
		}
		return nt
	case LPAREN:
		lp := p.tok.Pos
		p.next()
		var elems []TypeExpr
		for p.tok.Kind != RPAREN {
			elems = append(elems, p.typ())
			if p.tok.Kind == COMMA {
				p.next()
				continue
			}
			if p.tok.Kind != RPAREN {
				p.unexpected("`,` or `)`")
			}
		}
		rp := p.tok.Pos
		p.next()
		if len(elems) == 1 {
			p.fail("R0105", Span{lp, rp + 1}, "a tuple type has two or more fields; write the type without parentheses")
		}
		return &TupleType{Lparen: lp, Rparen: rp, Elems: elems}
	case FN:
		fn := p.tok.Pos
		p.next()
		p.expect(LPAREN, "`(`")
		var params []TypeExpr
		for p.tok.Kind != RPAREN {
			params = append(params, p.typ())
			if p.tok.Kind == COMMA {
				p.next()
				continue
			}
			if p.tok.Kind != RPAREN {
				p.unexpected("`,` or `)`")
			}
		}
		end := p.tok.End
		p.next()
		ft := &FuncType{Fn: fn, Params: params, End: end}
		if p.tok.Kind == ARROW {
			p.next()
			ft.Result = p.typ()
			ft.End = ft.Result.Span().End
		}
		return ft
	}
	p.unexpected("a type")
	return nil
}

// ---- Statements ----

func (p *parser) block() *BlockExpr {
	lb := p.expect(LBRACE, "`{`").Pos
	saved := p.noStruct
	p.noStruct = false
	b := &BlockExpr{Lbrace: lb}
	for {
		p.skipTerms()
		if p.tok.Kind == RBRACE {
			break
		}
		if p.tok.Kind == EOF {
			p.fail("R0105", p.tok.Span(), "expected `}` to close the block opened at line %d", p.line(lb))
		}
		b.Stmts = append(b.Stmts, p.stmt())
		if p.tok.Kind != RBRACE && p.tok.Kind != TERM {
			p.unexpected("end of statement")
		}
	}
	b.Rbrace = p.tok.Pos
	p.next()
	p.noStruct = saved
	return b
}

func (p *parser) line(pos Pos) int {
	l, _ := p.src.LineCol(pos)
	return l
}

func isAssignOp(k Kind) bool {
	switch k {
	case ASSIGN, PLUSEQ, MINUSEQ, STAREQ, SLASHEQ:
		return true
	}
	return false
}

func (p *parser) stmt() Stmt {
	switch p.tok.Kind {
	case LET:
		return p.letStmt()
	case USE:
		if p.isModuleHabit() {
			pos := p.tok.Pos
			p.moduleHabit()
			return &ExprStmt{X: &UnitLit{Lparen: pos, Rparen: pos}}
		}
		p.fail("R0105", p.tok.Span(), "`use` declarations go at the top level of a file")
	case FN, STRUCT, ENUM, IMPL, CONST:
		p.fail("R0105", p.tok.Span(), "`%s` declarations go at the top level of a file", p.tok.Kind)
	case IDENT:
		if p.tok.Text == "var" && p.peek().Kind == IDENT {
			return p.varStmt()
		}
	}
	x := p.expr()
	switch p.tok.Kind {
	case ASSIGN, PLUSEQ, MINUSEQ, STAREQ, SLASHEQ:
		op := p.tok
		p.next()
		return &AssignStmt{Target: x, OpPos: op.Pos, Op: op.Kind, Value: p.expr()}
	case COLONEQ:
		return p.shortVarDecl(x)
	case INCR, DECR:
		return p.incrStmt(x)
	case COMMA:
		if id, ok := x.(*Ident); ok {
			p.multiDecl(id)
		}
	}
	return &ExprStmt{X: x}
}

func (p *parser) letStmt() Stmt {
	s := &LetStmt{Let: p.tok.Pos}
	p.next()
	if p.tok.Kind == MUT {
		s.Mut = true
		p.next()
	}
	s.Pat = p.pattern()
	if p.tok.Kind == COLON {
		p.next()
		s.Type = p.typ()
	}
	if p.tok.Kind != ASSIGN {
		if p.tok.Kind == TERM || p.tok.Kind == RBRACE {
			p.fail("R0105", s.Pat.Span(), "a let needs a value, as in `let x = 0`")
		}
		p.unexpected("`=`")
	}
	p.next()
	s.Value = p.expr()
	return s
}

// ---- Expressions ----

func (p *parser) expr() Expr { return p.orExpr() }

// exprNoStruct parses a condition, subject or iterable, where `Name {` would
// be read as the start of the block.
func (p *parser) exprNoStruct() Expr {
	saved := p.noStruct
	p.noStruct = true
	x := p.expr()
	p.noStruct = saved
	return x
}

func (p *parser) orExpr() Expr {
	x := p.andExpr()
	for p.tok.Kind == OROR {
		op := p.tok
		p.next()
		x = &BinaryExpr{X: x, OpPos: op.Pos, Op: OROR, Y: p.andExpr()}
	}
	return x
}

func (p *parser) andExpr() Expr {
	x := p.cmpExpr()
	for p.tok.Kind == ANDAND {
		op := p.tok
		p.next()
		x = &BinaryExpr{X: x, OpPos: op.Pos, Op: ANDAND, Y: p.cmpExpr()}
	}
	return x
}

func isCmp(k Kind) bool {
	switch k {
	case EQ, NE, LT, LE, GT, GE:
		return true
	}
	return false
}

func (p *parser) cmpExpr() Expr {
	x := p.addExpr()
	if isCmp(p.tok.Kind) {
		op := p.tok
		p.next()
		x = &BinaryExpr{X: x, OpPos: op.Pos, Op: op.Kind, Y: p.addExpr()}
		if isCmp(p.tok.Kind) {
			p.fail("R0105", p.tok.Span(), "comparisons cannot be chained: write `a < b && b < c`")
		}
	}
	return x
}

func (p *parser) addExpr() Expr {
	x := p.mulExpr()
	for p.tok.Kind == PLUS || p.tok.Kind == MINUS {
		op := p.tok
		p.next()
		x = &BinaryExpr{X: x, OpPos: op.Pos, Op: op.Kind, Y: p.mulExpr()}
	}
	return x
}

func (p *parser) mulExpr() Expr {
	x := p.castExpr()
	for p.tok.Kind == STAR || p.tok.Kind == SLASH || p.tok.Kind == PERCENT {
		op := p.tok
		p.next()
		x = &BinaryExpr{X: x, OpPos: op.Pos, Op: op.Kind, Y: p.castExpr()}
	}
	return x
}

// castExpr parses `x as T`, which binds tighter than `*` and looser than
// unary operators: `-x as Float` converts -x.
func (p *parser) castExpr() Expr {
	x := p.unaryExpr()
	for p.tok.Kind == AS {
		as := p.tok.Pos
		p.next()
		x = &AsExpr{X: x, As: as, Type: p.typ()}
	}
	return x
}

func (p *parser) unaryExpr() Expr {
	switch p.tok.Kind {
	case MINUS, NOT:
		op := p.tok
		p.next()
		return &UnaryExpr{OpPos: op.Pos, Op: op.Kind, X: p.unaryExpr()}
	case AMP, ANDAND:
		return p.reference()
	case STAR:
		return p.deref()
	}
	return p.postfix(p.primary())
}

func (p *parser) args() ([]Expr, Pos) {
	p.expect(LPAREN, "`(`")
	saved := p.noStruct
	p.noStruct = false
	var args []Expr
	for p.tok.Kind != RPAREN {
		args = append(args, p.expr())
		if p.tok.Kind == COMMA {
			p.next()
			continue
		}
		if p.tok.Kind != RPAREN {
			p.unexpected("`,` or `)`")
		}
	}
	rp := p.tok.Pos
	p.next()
	p.noStruct = saved
	return args, rp
}

func (p *parser) postfix(x Expr) Expr {
	for {
		switch p.tok.Kind {
		case DOT:
			p.next()
			if p.tok.Kind == INT {
				t := p.tok
				if strings.Trim(t.Text, "0123456789") != "" {
					p.fail("R0105", t.Span(), "expected a tuple field number, found `%s`", t.Text)
				}
				p.next()
				x = &FieldExpr{X: x, Name: &Ident{NamePos: t.Pos, Name: t.Text}}
				continue
			}
			name := p.ident("a field or method name")
			p.turbofish()
			if p.tok.Kind == LPAREN {
				args, rp := p.args()
				x = &MethodCallExpr{Recv: x, Name: name, Args: args, Rparen: rp}
			} else {
				x = &FieldExpr{X: x, Name: name}
			}
		case LPAREN:
			args, rp := p.args()
			x = &CallExpr{Fun: x, Args: args, Rparen: rp}
		case LBRACK:
			p.next()
			saved := p.noStruct
			p.noStruct = false
			idx := p.expr()
			p.noStruct = saved
			rb := p.expect(RBRACK, "`]`").Pos
			x = &IndexExpr{X: x, Index: idx, Rbrack: rb}
		case QUESTION:
			x = &TryExpr{X: x, Quest: p.tok.Pos}
			p.next()
		default:
			return x
		}
	}
}

func startsExpr(k Kind) bool {
	switch k {
	case INT, FLOAT, STRING, STRHEAD, TRUE, FALSE, IDENT, SELF, SELFTYPE, LPAREN, LBRACK, LBRACE,
		IF, MATCH, WHILE, LOOP, FOR, PIPE, OROR, RETURN, BREAK, CONTINUE, MINUS, NOT:
		return true
	}
	return false
}

func (p *parser) primary() Expr {
	t := p.tok
	switch t.Kind {
	case INT:
		p.next()
		return &IntLit{ValuePos: t.Pos, Text: t.Text}
	case FLOAT:
		p.next()
		return &FloatLit{ValuePos: t.Pos, Text: t.Text}
	case STRING:
		p.next()
		return &StringLit{Start: t.Pos, End: t.End, Value: t.Text}
	case STRHEAD:
		return p.interp()
	case TRUE, FALSE:
		p.next()
		return &BoolLit{ValuePos: t.Pos, Value: t.Kind == TRUE}
	case SELF:
		p.next()
		return &Ident{NamePos: t.Pos, Name: "self"}
	case IDENT, SELFTYPE:
		p.next()
		id := &Ident{NamePos: t.Pos, Name: t.Text}
		if p.tok.Kind == NOT && p.tok.Pos == t.End && t.Kind == IDENT {
			switch p.peek().Kind {
			case LPAREN, LBRACK, LBRACE:
				return p.macro(id)
			}
		}
		p.turbofish()
		if p.tok.Kind == COLON2 {
			p.next()
			name := p.ident("a variant or function name after `::`")
			p.turbofish()
			if p.tok.Kind == COLON2 {
				p.fail("R0105", p.tok.Span(), "a path has two parts, as in `Shape::Circle`")
			}
			return &PathExpr{Type: id, Name: name}
		}
		if p.tok.Kind == LBRACE && !p.noStruct {
			return p.structLit(id)
		}
		return id
	case LPAREN:
		return p.parenExpr()
	case LBRACK:
		p.next()
		saved := p.noStruct
		p.noStruct = false
		l := &ListExpr{Lbrack: t.Pos}
		for p.tok.Kind != RBRACK {
			l.Elems = append(l.Elems, p.expr())
			if p.tok.Kind == COMMA {
				p.next()
				continue
			}
			if p.tok.Kind != RBRACK {
				p.unexpected("`,` or `]`")
			}
		}
		l.Rbrack = p.tok.Pos
		p.next()
		p.noStruct = saved
		return l
	case LBRACE:
		return p.block()
	case IF:
		return p.ifExpr()
	case MATCH:
		return p.matchExpr()
	case WHILE:
		p.next()
		if p.tok.Kind == LET {
			p.fail("R0105", Span{t.Pos, p.tok.End}, "`while let` is not supported: write `loop { match e { p => ..., _ => break } }`")
		}
		cond := p.exprNoStruct()
		return &WhileExpr{While: t.Pos, Cond: cond, Body: p.loopBody()}
	case LOOP:
		p.next()
		return &LoopExpr{Loop: t.Pos, Body: p.loopBody()}
	case FOR:
		return p.forExpr()
	case PIPE, OROR:
		return p.closure()
	case RETURN:
		p.next()
		r := &ReturnExpr{Return: t.Pos}
		if startsExpr(p.tok.Kind) {
			r.Value = p.expr()
		}
		return r
	case BREAK, CONTINUE:
		p.next()
		if p.loops == 0 {
			p.report("R0107", t.Span(), "`%s` outside a loop", t.Kind)
		}
		if t.Kind == BREAK {
			return &BreakExpr{Break: t.Pos}
		}
		return &ContinueExpr{Continue: t.Pos}
	}
	p.unexpected("an expression")
	return nil
}

// interp parses an interpolated string: STRHEAD expr (STRMID expr)* STRTAIL.
func (p *parser) interp() Expr {
	lit := &InterpLit{Start: p.tok.Pos, Parts: []string{p.tok.Text}}
	p.next()
	saved := p.noStruct
	p.noStruct = false
	defer func() { p.noStruct = saved }()
	for {
		if p.tok.Kind == STRMID || p.tok.Kind == STRTAIL {
			p.fail("R0105", p.tok.Span(), "expected an expression in `${}`; write `\\$` for a literal $")
		}
		lit.Exprs = append(lit.Exprs, p.expr())
		switch p.tok.Kind {
		case STRMID:
			lit.Parts = append(lit.Parts, p.tok.Text)
			p.next()
		case STRTAIL:
			lit.Parts = append(lit.Parts, p.tok.Text)
			lit.End = p.tok.End
			p.next()
			return lit
		default:
			p.unexpected("`}` to end the interpolation")
		}
	}
}

func (p *parser) loopBody() *BlockExpr {
	if p.tok.Kind != LBRACE {
		p.unexpected("`{` to start the loop body")
	}
	p.loops++
	b := p.block()
	p.loops--
	return b
}

func (p *parser) parenExpr() Expr {
	lp := p.tok.Pos
	p.next()
	if p.tok.Kind == RPAREN {
		rp := p.tok.Pos
		p.next()
		return &UnitLit{Lparen: lp, Rparen: rp}
	}
	saved := p.noStruct
	p.noStruct = false
	defer func() { p.noStruct = saved }()
	x := p.expr()
	if p.tok.Kind != COMMA {
		rp := p.expect(RPAREN, "`)`").Pos
		return &ParenExpr{Lparen: lp, X: x, Rparen: rp}
	}
	elems := []Expr{x}
	for p.tok.Kind == COMMA {
		p.next()
		if p.tok.Kind == RPAREN {
			break
		}
		elems = append(elems, p.expr())
	}
	rp := p.expect(RPAREN, "`,` or `)`").Pos
	if len(elems) == 1 {
		p.fail("R0105", Span{lp, rp + 1}, "a tuple has two or more fields; write the value without parentheses")
	}
	return &TupleExpr{Lparen: lp, Elems: elems, Rparen: rp}
}

func (p *parser) structLit(name *Ident) Expr {
	p.expect(LBRACE, "`{`")
	saved := p.noStruct
	p.noStruct = false
	s := &StructLit{Type: name}
	for {
		p.skipTerms()
		if p.tok.Kind == RBRACE {
			break
		}
		if p.tok.Kind == DOT2 {
			p.next()
			s.Base = p.expr()
			p.skipTerms()
			if p.tok.Kind == COMMA {
				p.next()
				p.skipTerms()
			}
			if p.tok.Kind != RBRACE {
				p.fail("R0105", p.tok.Span(), "`..base` must come last in a struct literal")
			}
			break
		}
		fname := p.ident("a field name or `}`")
		if p.tok.Kind != COLON {
			p.fail("R0105", fname.Span(), "expected `:` and a value after field `%s`, as in `%s: %s`", fname.Name, fname.Name, fname.Name)
		}
		p.next()
		s.Fields = append(s.Fields, &FieldInit{Name: fname, Value: p.expr()})
		if p.tok.Kind == COMMA || p.tok.Kind == TERM {
			p.next()
			continue
		}
		if p.tok.Kind != RBRACE {
			p.unexpected("`,` or `}`")
		}
	}
	s.Rbrace = p.tok.Pos
	p.next()
	p.noStruct = saved
	return s
}

func (p *parser) ifExpr() Expr {
	pos := p.expect(IF, "`if`").Pos
	x := &IfExpr{If: pos}
	if p.tok.Kind == LET {
		p.next()
		x.Pat = p.pattern()
		p.expect(ASSIGN, "`=` after the pattern of `if let`")
	}
	x.Cond = p.exprNoStruct()
	if p.tok.Kind != LBRACE {
		p.unexpected("`{` after the condition")
	}
	x.Then = p.block()
	if p.tok.Kind == ELSE {
		p.next()
		switch p.tok.Kind {
		case IF:
			x.Else = p.ifExpr()
		case LBRACE:
			x.Else = p.block()
		default:
			p.unexpected("`{` or `if` after `else`")
		}
	}
	return x
}

func (p *parser) matchExpr() Expr {
	pos := p.expect(MATCH, "`match`").Pos
	m := &MatchExpr{Match: pos, Subject: p.exprNoStruct()}
	if p.tok.Kind != LBRACE {
		p.unexpected("`{` after the match subject")
	}
	m.Lbrace = p.tok.Pos
	p.next()
	saved := p.noStruct
	p.noStruct = false
	for {
		p.skipTerms()
		if p.tok.Kind == RBRACE {
			break
		}
		arm := &MatchArm{Pat: p.pattern()}
		if p.tok.Kind == IF {
			p.next()
			arm.Guard = p.expr()
		}
		p.expect(FATARROW, "`=>`")
		arm.Body = p.expr()
		if isAssignOp(p.tok.Kind) {
			p.fail("R0105", p.tok.Span(), "an assignment is a statement, not a value: put it in a block, as in `=> { x = 1 }`")
		}
		m.Arms = append(m.Arms, arm)
		if p.tok.Kind == COMMA || p.tok.Kind == TERM {
			p.next()
			continue
		}
		if p.tok.Kind != RBRACE {
			p.unexpected("`,` or `}` after the match arm")
		}
	}
	m.Rbrace = p.tok.Pos
	p.next()
	p.noStruct = saved
	return m
}

func (p *parser) forExpr() Expr {
	pos := p.expect(FOR, "`for`").Pos
	kw := Span{pos, pos + 3}
	switch {
	case p.tok.Kind == LBRACE:
		d := p.report("R0105", kw, "a `for` loop runs over a list, a map or a range: a loop without an end is `loop { }`")
		d.Fixes = append(d.Fixes, fix("write `loop`", p.fs.Edit(kw, "loop")))
		return &LoopExpr{Loop: pos, Body: p.loopBody()}
	case p.tok.Kind == IDENT && p.peek().Kind == COLONEQ:
		return p.goFor(pos)
	case p.tok.Kind == IDENT && p.peek().Kind == COMMA && p.peekAt(2).Kind == IDENT && p.peekAt(3).Kind == COLONEQ:
		p.goRange(p.tok, p.peekAt(3))
	}
	f := &ForExpr{For: pos, Pat: p.pattern()}
	if _, ok := f.Pat.(*BindPat); ok && p.tok.Kind != IN {
		switch k := p.tok.Kind; {
		case k == LBRACE || k == DOT || k == LPAREN || k == ANDAND || k == OROR || isCmp(k):
			d := p.report("R0105", kw, "a `for` loop runs over a list, a map or a range: a loop with a condition is `while cond { }`")
			d.Fixes = append(d.Fixes, fix("write `while`", p.fs.Edit(kw, "while")))
			panic(bailout{})
		}
	}
	p.expect(IN, "`in`")
	f.Iter = p.exprNoStruct()
	if p.tok.Kind == DOT2 || p.tok.Kind == DOT2EQ {
		f.Inclusive = p.tok.Kind == DOT2EQ
		p.next()
		f.End = p.exprNoStruct()
	}
	f.Body = p.loopBody()
	return f
}

func (p *parser) closure() Expr {
	c := &ClosureExpr{Pipe: p.tok.Pos}
	if p.tok.Kind == OROR {
		p.next()
	} else {
		p.next()
		for p.tok.Kind != PIPE {
			cp := &ClosureParam{Pat: p.patternNoOr()}
			if p.tok.Kind == COLON {
				p.next()
				cp.Type = p.typ()
			}
			c.Params = append(c.Params, cp)
			if p.tok.Kind == COMMA {
				p.next()
				continue
			}
			if p.tok.Kind != PIPE {
				p.unexpected("`,` or `|`")
			}
		}
		p.next()
	}
	savedLoops, savedNS := p.loops, p.noStruct
	p.loops, p.noStruct = 0, false
	if p.tok.Kind == ARROW {
		p.next()
		c.Result = p.typ()
		if p.tok.Kind != LBRACE {
			p.unexpected("`{`: a closure with a result type has a block body")
		}
		c.Body = p.block()
	} else {
		c.Body = p.expr()
	}
	p.loops, p.noStruct = savedLoops, savedNS
	return c
}

// ---- Patterns ----

func (p *parser) pattern() Pattern {
	first := p.patternNoOr()
	if p.tok.Kind != PIPE {
		return first
	}
	or := &OrPat{Alts: []Pattern{first}}
	for p.tok.Kind == PIPE {
		p.next()
		or.Alts = append(or.Alts, p.patternNoOr())
	}
	return or
}

func startsUpper(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return unicode.IsUpper(r)
}

func (p *parser) patternNoOr() Pattern {
	t := p.tok
	switch t.Kind {
	case IDENT, SELFTYPE:
		if t.Text == "_" {
			p.next()
			return &WildcardPat{Under: t.Pos}
		}
		p.next()
		id := &Ident{NamePos: t.Pos, Name: t.Text}
		if p.tok.Kind == COLON2 {
			p.next()
			return p.variantArgs(&VariantPat{Type: id, Name: p.ident("a variant name after `::`")})
		}
		if t.Kind == SELFTYPE {
			p.fail("R0105", id.Span(), "expected `Self::Variant` in a pattern")
		}
		if startsUpper(id.Name) {
			return p.variantArgs(&VariantPat{Name: id})
		}
		if p.tok.Kind == LPAREN {
			p.fail("R0105", id.Span(), "`%s(...)` is not a pattern: variant names start with an uppercase letter", id.Name)
		}
		return &BindPat{Name: id}
	case INT, STRING, TRUE, FALSE:
		return &LitPat{Lit: p.primary()}
	case AMP:
		p.next()
		if p.tok.Kind == MUT {
			p.next()
		}
		inner := p.patternNoOr()
		d := p.report("R0111", t.Span(), "Riddim has no references: a pattern matches the value itself")
		d.Fixes = append(d.Fixes, fix("match the value", p.fs.Edit(Span{t.Pos, inner.Span().Start}, "")))
		return inner
	case FLOAT:
		p.fail("R0105", t.Span(), "a Float literal cannot be a pattern; bind it and use a guard: `x if x == %s`", t.Text)
	case STRHEAD:
		p.fail("R0105", t.Span(), "a pattern cannot interpolate; bind the string and use a guard: `s if s == ...`")
	case MINUS:
		p.next()
		if p.tok.Kind != INT {
			p.unexpected("an integer after `-` in a pattern")
		}
		return &LitPat{Minus: t.Pos, Lit: p.primary()}
	case LPAREN:
		p.next()
		var elems []Pattern
		for p.tok.Kind != RPAREN {
			elems = append(elems, p.pattern())
			if p.tok.Kind == COMMA {
				p.next()
				continue
			}
			if p.tok.Kind != RPAREN {
				p.unexpected("`,` or `)`")
			}
		}
		rp := p.tok.Pos
		p.next()
		switch len(elems) {
		case 0:
			p.fail("R0105", Span{t.Pos, rp + 1}, "`()` is not a pattern; use `_`")
		case 1:
			return elems[0]
		}
		return &TuplePat{Lparen: t.Pos, Elems: elems, Rparen: rp}
	}
	p.unexpected("a pattern")
	return nil
}

func (p *parser) variantArgs(v *VariantPat) Pattern {
	if p.tok.Kind != LPAREN {
		return v
	}
	p.next()
	for p.tok.Kind != RPAREN {
		v.Args = append(v.Args, p.pattern())
		if p.tok.Kind == COMMA {
			p.next()
			continue
		}
		if p.tok.Kind != RPAREN {
			p.unexpected("`,` or `)`")
		}
	}
	v.Rparen = p.tok.Pos
	p.next()
	return v
}
