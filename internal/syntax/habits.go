package syntax

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/riddim-hq/riddim/internal/diag"
)

// The Rust and Go habits that show in the syntax (DESIGN §7.2). Each gets its
// own code and, where the rewrite is mechanical, a fix. The parser then reads
// the code as the fix would leave it, so it can go on and report the rest.

// text returns the source text of a span.
func (p *parser) text(sp Span) string {
	return string(p.src.Src[p.src.Offset(sp.Start):p.src.Offset(sp.End)])
}

func fix(title string, edits ...diag.Edit) diag.Fix { return diag.Fix{Title: title, Edits: edits} }

// peekAt returns the token n places after the current one.
func (p *parser) peekAt(n int) Token {
	if p.pos+n < len(p.toks) {
		return p.toks[p.pos+n]
	}
	return p.toks[len(p.toks)-1]
}

// varStmt parses Go's `var x = e` and `var x T = e` as `let mut` (R0108).
func (p *parser) varStmt() Stmt {
	v := p.tok
	p.next()
	d := p.report("R0108", v.Span(), "`var` is Go: Riddim declares a binding with `let`, and one that changes with `let mut`")
	f := fix("write `let mut`", p.fs.Edit(v.Span(), "let mut"))
	name := p.ident("a name after `var`")
	s := &LetStmt{Let: v.Pos, Mut: true, Pat: &BindPat{Name: name}}
	switch p.tok.Kind {
	case COLON:
		p.next()
		s.Type = p.typ()
	case IDENT, LBRACK, STAR, LPAREN, FN:
		// Go writes the type without a colon: var n int = 1.
		f.Edits = append(f.Edits, p.fs.Edit(Span{name.Span().End, name.Span().End}, ":"))
		s.Type = p.typ()
	}
	d.Fixes = append(d.Fixes, f)
	if p.tok.Kind != ASSIGN {
		p.fail("R0105", s.Pat.Span(), "a let needs a value, as in `let mut %s = 0`", name.Name)
	}
	p.next()
	s.Value = p.expr()
	return s
}

// shortVarDecl parses Go's `x := e` as `let x = e` (R0108).
func (p *parser) shortVarDecl(x Expr) Stmt {
	op := p.tok
	id, ok := x.(*Ident)
	if !ok {
		p.fail("R0108", op.Span(), "`:=` is Go: Riddim declares a binding with `let`, as in `let x = 1`")
	}
	p.next()
	d := p.report("R0108", op.Span(), "`:=` is Go: Riddim declares a binding with `let`; write `let %s = ...`", id.Name)
	d.Fixes = append(d.Fixes, fix("write `let`",
		p.fs.Edit(Span{id.NamePos, id.NamePos}, "let "), p.fs.Edit(op.Span(), "=")))
	return &LetStmt{Let: id.NamePos, Pat: &BindPat{Name: id}, Value: p.expr()}
}

// multiDecl reports Go's `n, err := f()`: a function that can fail returns a
// Result in Riddim, and `?` passes its error on (R0108).
func (p *parser) multiDecl(first *Ident) {
	i := p.pos
	for p.toks[i].Kind == COMMA && p.toks[i+1].Kind == IDENT {
		i += 2
	}
	if p.toks[i].Kind != COLONEQ {
		return
	}
	d := p.report("R0108", Span{first.NamePos, p.toks[i].End}, "Riddim has no `:=` and no multiple results: a function that can fail returns a Result; write `let %s = f()?` to pass its error on", first.Name)
	d.Notes = append(d.Notes, "a function that returns two values returns a tuple: `let (a, b) = f()`")
	panic(bailout{})
}

// goFor parses Go's `for i := a; i < b; i++ {` (R0108). When the loop has
// exactly that shape, the fix writes `for i in a..b {`.
func (p *parser) goFor(pos Pos) Expr {
	if p.peekAt(2).Kind == IDENT && p.peekAt(2).Text == "range" {
		p.goRange(p.tok, p.peek())
	}
	name := p.ident("a name")
	assign := p.tok
	p.next()
	d := p.report("R0108", Span{name.NamePos, assign.End}, "Riddim's `for` runs over a range, a list or a map: write `for %s in 0..n { }`", name.Name)
	start := p.exprNoStruct()
	var end Expr
	inclusive, shaped := false, false
	if p.tok.Kind == TERM && !p.tok.Implicit {
		p.next()
		if b, ok := p.exprNoStruct().(*BinaryExpr); ok && (b.Op == LT || b.Op == LE) {
			if id, ok := b.X.(*Ident); ok && id.Name == name.Name {
				end, inclusive = b.Y, b.Op == LE
			}
		}
		if p.tok.Kind == TERM && !p.tok.Implicit && p.peek().Kind == IDENT && p.peek().Text == name.Name {
			p.next()
			p.next()
			switch {
			case p.tok.Kind == INCR:
				p.next()
				shaped = end != nil
			case p.tok.Kind == PLUSEQ && p.peek().Kind == INT && p.peek().Text == "1":
				p.next()
				p.next()
				shaped = end != nil
			}
		}
	}
	for p.tok.Kind != LBRACE && p.tok.Kind != RBRACE && p.tok.Kind != EOF {
		p.next()
	}
	if shaped && p.tok.Kind == LBRACE {
		dots := ".."
		if inclusive {
			dots = "..="
		}
		text := name.Name + " in " + p.text(start.Span()) + dots + p.text(end.Span()) + " "
		d.Fixes = append(d.Fixes, fix("write a range", p.fs.Edit(Span{name.NamePos, p.tok.Pos}, text)))
	}
	if end == nil {
		end = start
	}
	return &ForExpr{For: pos, Pat: &BindPat{Name: name}, Iter: start, End: end, Inclusive: inclusive, Body: p.loopBody()}
}

// goRange reports Go's `for i := range xs {` and `for i, x := range xs {`
// (R0108) from the first name to the `:=`. Which Riddim loop it becomes
// depends on what xs is, so there is no fix, and the declaration is dropped.
func (p *parser) goRange(first, assign Token) {
	d := p.report("R0108", Span{first.Pos, assign.End}, "Go's `range` loop: Riddim's `for` runs over a list, a map or a range, as in `for x in xs { }`")
	d.Notes = append(d.Notes, "for an index, write `for (i, x) in xs.enumerate() { }`; over a map, `for (k, v) in m { }`; for a count, `for i in 0..n { }`")
	panic(bailout{})
}

// moduleHabit reports a line that Go or Rust needs and Riddim does not
// (R0125): Go's `package main` and `import`, Rust's `use path;`. It skips the
// line, and for Go's imports returns the `use go` declarations the fix
// writes. The token pre-scan reports the same line with modDiag.
func (p *parser) moduleHabit() []Decl {
	start, end := p.pos, stmtEnd(p.toks, p.pos)
	d, uses := modDiag(p.fs, p.src, p.toks, start, end)
	p.diags = append(p.diags, d)
	for p.pos <= end {
		p.next()
	}
	var decls []Decl
	for _, u := range uses {
		decls = append(decls, u)
	}
	return decls
}

// modDiag reports the Go or Rust module line in toks[start..end] (R0125).
// For Go's imports it also returns the `use go` declarations that the fix
// writes.
func modDiag(fs *FileSet, src *Source, toks []Token, start, end int) (diag.Diagnostic, []*UseDecl) {
	first, last := toks[start], toks[end]
	sp := Span{first.Pos, last.End}
	del := lineSpan(src, sp)
	switch first.Text {
	case "package":
		d := fs.Diag("R0125", sp, "Riddim has no package clause: a program is the .rdm files of one directory")
		d.Fixes = append(d.Fixes, fix("delete it", fs.Edit(del, "")))
		return d, nil
	case "import":
		d := fs.Diag("R0125", sp, "`import` is Go: Riddim imports a Go package with `use go \"path\"`")
		var uses []*UseDecl
		var lines []string
		ok := true
		for i := start + 1; i <= end; i++ {
			switch t := toks[i]; t.Kind {
			case STRING:
				uses = append(uses, &UseDecl{Use: first.Pos, Path: t.Text, PathSpan: t.Span()})
				lines = append(lines, "use go "+strconv.Quote(t.Text))
			case LPAREN, RPAREN, TERM:
			default:
				ok = false // a named, dot or blank import
			}
		}
		if ok && len(lines) > 0 {
			d.Fixes = append(d.Fixes, fix("write use go", fs.Edit(sp, strings.Join(lines, "\n"))))
		} else {
			d.Notes = append(d.Notes, "Riddim calls a Go package by its own name: `use go \"strings\"`, then `strings.Fields(s)`")
		}
		return d, uses
	}
	d := fs.Diag("R0125", sp, "Riddim has no modules: List, Map, Option, Result and the capabilities are built in, and `use` imports only Go packages, as in `use go \"strings\"`")
	d.Fixes = append(d.Fixes, fix("delete it", fs.Edit(del, "")))
	return d, nil
}

// stmtEnd returns the index of the last token of the statement that starts
// at toks[i]: the one before the `;` or end of line outside braces.
func stmtEnd(toks []Token, i int) int {
	end, depth := i, 0
	for ; toks[i].Kind != EOF; i++ {
		switch toks[i].Kind {
		case LBRACE:
			depth++
		case RBRACE:
			if depth == 0 {
				return end
			}
			depth--
		case TERM:
			if depth == 0 {
				return end
			}
		}
		end = i
	}
	return end
}

// lineSpan widens sp to its whole lines, with the line ending, when nothing
// else is on them; otherwise it adds an explicit `;` and the blanks after sp.
func lineSpan(src *Source, sp Span) Span {
	b := src.Src
	from, to := src.Offset(sp.Start), src.Offset(sp.End)
	for to < len(b) && (b[to] == ' ' || b[to] == '\t') {
		to++
	}
	if to < len(b) && b[to] == ';' {
		to++
		for to < len(b) && (b[to] == ' ' || b[to] == '\t') {
			to++
		}
	}
	lineStart := from
	for lineStart > 0 && b[lineStart-1] != '\n' {
		lineStart--
	}
	if strings.TrimSpace(string(b[lineStart:from])) != "" {
		return Span{sp.Start, src.Pos(to)}
	}
	if to < len(b) && b[to] == '/' && to+1 < len(b) && b[to+1] == '/' {
		for to < len(b) && b[to] != '\n' {
			to++
		}
	}
	switch {
	case to == len(b):
	case b[to] == '\r' && to+1 < len(b) && b[to+1] == '\n':
		to += 2
	case b[to] == '\n':
		to++
	default:
		return Span{sp.Start, src.Pos(to)}
	}
	return Span{src.Pos(lineStart), src.Pos(to)}
}

// useGo parses `use go "path"`. A path written without quotes is reported
// with a fix that adds them.
func (p *parser) useGo() Decl {
	use := p.expect(USE, "`use`")
	p.expect(GO, "`go`")
	t := p.tok
	switch t.Kind {
	case STRING:
		p.next()
		return &UseDecl{Use: use.Pos, Path: t.Text, PathSpan: t.Span()}
	case IDENT:
		path, end := t.Text, t.End
		p.next()
		for p.tok.Kind == SLASH && p.tok.Pos == end && p.peek().Kind == IDENT && p.peek().Pos == p.tok.End {
			p.next()
			path += "/" + p.tok.Text
			end = p.tok.End
			p.next()
		}
		sp := Span{t.Pos, end}
		d := p.report("R0105", sp, "the package of `use go` is a string: write `use go %s`", strconv.Quote(path))
		d.Fixes = append(d.Fixes, fix("quote it", p.fs.Edit(sp, strconv.Quote(path))))
		return &UseDecl{Use: use.Pos, Path: path, PathSpan: sp}
	}
	p.unexpected("the package to import, as in `use go \"strings\"`")
	return nil
}

// incrStmt parses Go's `x++` and `x--` as `x += 1` and `x -= 1` (R0123).
func (p *parser) incrStmt(x Expr) Stmt {
	op := p.tok
	p.next()
	kind, repl := PLUSEQ, "+= 1"
	if op.Kind == DECR {
		kind, repl = MINUSEQ, "-= 1"
	}
	d := p.report("R0123", op.Span(), "Riddim has no `%s`: write `%s`", op.Text, repl)
	d.Fixes = append(d.Fixes, fix("write `"+repl+"`", p.fs.Edit(Span{x.Span().End, op.End}, " "+repl)))
	return &AssignStmt{Target: x, OpPos: op.Pos, Op: kind, Value: &IntLit{ValuePos: op.Pos, Text: "1"}}
}

// pub skips Rust's `pub` and `pub(crate)` before a declaration or field
// (R0121): a program is one package, so there is nothing to export.
func (p *parser) pub() {
	t := p.tok
	p.next()
	end := t.End
	if p.tok.Kind == LPAREN && p.tok.Pos == t.End {
		for p.tok.Kind != RPAREN && p.tok.Kind != EOF {
			p.next()
		}
		end = p.tok.End
		p.next()
	}
	d := p.report("R0121", Span{t.Pos, end}, "Riddim has no `pub`: a program is one package, and every declaration is visible in all its files")
	d.Fixes = append(d.Fixes, fix("delete it", p.fs.Edit(Span{t.Pos, p.tok.Pos}, "")))
	if p.tok.Doc == nil {
		p.tok.Doc = t.Doc
	}
}

// isPub reports whether the parser is at `pub` before a declaration or field.
func (p *parser) isPub() bool {
	if p.tok.Kind != IDENT || p.tok.Text != "pub" {
		return false
	}
	next := p.peek()
	return isDeclStart(next.Kind) || next.Kind == IDENT || next.Kind == LPAREN && next.Pos == p.tok.End
}

// goFunc reads Go's `func name(` as `fn name(` (R0124).
func (p *parser) goFunc() {
	d := p.report("R0124", p.tok.Span(), "`func` is Go: Riddim declares a function with `fn`")
	d.Fixes = append(d.Fixes, fix("write `fn`", p.fs.Edit(p.tok.Span(), "fn")))
	p.tok.Kind = FN
}

// reference parses Rust's `&x` and `&mut x` as x (R0111).
func (p *parser) reference() Expr {
	t := p.tok
	p.next()
	if p.tok.Kind == MUT {
		p.next()
	}
	x := p.unaryExpr()
	d := p.report("R0111", t.Span(), "Riddim has no references: values are copied when passed, so pass the value itself")
	d.Fixes = append(d.Fixes, fix("pass the value", p.fs.Edit(Span{t.Pos, x.Span().Start}, "")))
	return x
}

// deref parses `*x` as x (R0111).
func (p *parser) deref() Expr {
	t := p.tok
	p.next()
	x := p.unaryExpr()
	d := p.report("R0111", t.Span(), "Riddim has no pointers or references, so there is nothing to dereference: use the value")
	d.Fixes = append(d.Fixes, fix("use the value", p.fs.Edit(Span{t.Pos, x.Span().Start}, "")))
	return x
}

// refType parses Rust's `&T`, `&mut T` and `&str` as T (R0111): a parameter
// or field holds the value itself.
func (p *parser) refType() TypeExpr {
	amp := p.tok
	p.next()
	if p.tok.Kind == MUT {
		p.next()
	}
	inner := p.typ()
	end := inner.Span().End
	text := p.text(inner.Span())
	if nt, ok := inner.(*NamedType); ok && nt.Name.Name == "str" && len(nt.Args) == 0 {
		text = "String"
		inner = &NamedType{Name: &Ident{NamePos: nt.Name.NamePos, Name: "String"}}
	}
	d := p.report("R0111", amp.Span(), "Riddim has no references: a parameter or field holds the value itself; write `%s`", text)
	d.Fixes = append(d.Fixes, fix("use the value's type", p.fs.Edit(Span{amp.Pos, end}, text)))
	return inner
}

// ptrType parses Go's `*T` as T (R0111).
func (p *parser) ptrType() TypeExpr {
	star := p.tok
	p.next()
	inner := p.typ()
	d := p.report("R0111", star.Span(), "Riddim has no pointers: a parameter or field holds the value itself; write `%s`", p.text(inner.Span()))
	d.Fixes = append(d.Fixes, fix("use the value's type", p.fs.Edit(Span{star.Pos, inner.Span().Start}, "")))
	return inner
}

// sliceType parses Go's `[]T` and Rust's `[T; N]` and `[T]` as List<T>
// (R0113).
func (p *parser) sliceType() TypeExpr {
	lb := p.tok
	p.next()
	var elem TypeExpr
	var sp Span
	msg := "`[]%s` is Go: Riddim's list type is `%s`"
	switch {
	case p.tok.Kind == RBRACK:
		p.next()
		elem = p.typ()
		sp = Span{lb.Pos, elem.Span().End}
	default:
		elem = p.typ()
		if p.tok.Kind == RBRACK {
			sp = Span{lb.Pos, p.tok.End}
			p.next()
			msg = "`[%s]` is a Rust slice: Riddim's list type is `%s`"
			break
		}
		if p.tok.Kind != TERM || p.tok.Implicit {
			p.unexpected("`;` or `]` in an array type")
		}
		for p.tok.Kind != RBRACK && p.tok.Kind != EOF {
			p.next()
		}
		sp = Span{lb.Pos, p.expect(RBRACK, "`]`").End}
		msg = "`[%s; N]` is a Rust array: Riddim's list type is `%s`"
	}
	text := "List<" + p.text(elem.Span()) + ">"
	d := p.report("R0113", sp, msg, p.text(elem.Span()), text)
	d.Fixes = append(d.Fixes, fix("write "+text, p.fs.Edit(sp, text)))
	return &NamedType{Name: &Ident{NamePos: lb.Pos, Name: "List"}, Args: []TypeExpr{elem}, Rangle: sp.End - 1}
}

// goMapType parses Go's `map[K]V` as Map<K, V> (R0114).
func (p *parser) goMapType() TypeExpr {
	start := p.tok.Pos
	p.next()
	p.next()
	key := p.typ()
	p.expect(RBRACK, "`]`")
	val := p.typ()
	text := "Map<" + p.text(key.Span()) + ", " + p.text(val.Span()) + ">"
	sp := Span{start, val.Span().End}
	d := p.report("R0114", sp, "`map[K]V` is Go: Riddim's map type is `%s`", text)
	d.Fixes = append(d.Fixes, fix("write "+text, p.fs.Edit(sp, text)))
	return &NamedType{Name: &Ident{NamePos: start, Name: "Map"}, Args: []TypeExpr{key, val}, Rangle: sp.End - 1}
}

// turbofish skips Rust's `::<T>` after a function, method or type name
// (R0116): calls take no type arguments in Riddim.
func (p *parser) turbofish() {
	if p.tok.Kind != COLON2 || p.peek().Kind != LT {
		return
	}
	start := p.tok.Pos
	p.next()
	p.next()
	for p.tok.Kind != GT {
		p.typ()
		if p.tok.Kind != COMMA {
			break
		}
		p.next()
	}
	end := p.expect(GT, "`>` to end the type arguments").End
	d := p.report("R0116", Span{start, end}, "calls take no type arguments: Riddim infers them; where it needs help, annotate the binding, as in `let xs: List<Int> = ...`")
	d.Fixes = append(d.Fixes, fix("delete the type arguments", p.fs.Edit(Span{start, end}, "")))
}

// A macroArg is an argument of a Rust macro call: its source text, and for
// a plain string literal its value. name is set for `name = x`, and text is
// then the text of x.
type macroArg struct {
	name   string
	text   string
	str    *string // the value of a plain string literal
	interp bool    // a single interpolated string literal
}

// A sigParam is a parameter of the enclosing function whose type is a plain
// name, as in `sys: Sys`: where a print macro's fix finds an Out to print
// with.
type sigParam struct{ name, typ string }

// macro parses a Rust macro call: name!(...), name![...] or name!{...}.
// Riddim has no macros: vec![...] is read as the list it stands for, and the
// others as a MacroCall, which the checker types as what the fix leaves.
func (p *parser) macro(id *Ident) Expr {
	p.next() // !
	open := p.tok
	closer := map[Kind]Kind{LPAREN: RPAREN, LBRACK: RBRACK, LBRACE: RBRACE}[open.Kind]
	p.next()
	saved := p.noStruct
	p.noStruct = false
	var args []macroArg
	var exprs []Expr
	repeat := false
	for {
		p.skipImplicit(closer)
		if p.tok.Kind == closer {
			break
		}
		var a macroArg
		if p.tok.Kind == IDENT && p.peek().Kind == ASSIGN {
			a.name = p.tok.Text
			p.next()
			p.next()
		}
		x := p.expr()
		a.text = p.text(x.Span())
		switch x := x.(type) {
		case *StringLit:
			a.str = &x.Value
		case *InterpLit:
			a.interp = true
		}
		args = append(args, a)
		exprs = append(exprs, x)
		p.skipImplicit(closer)
		switch {
		case p.tok.Kind == COMMA:
			p.next()
		case p.tok.Kind == TERM && !p.tok.Implicit && id.Name == "vec":
			repeat = true
			p.next()
		case p.tok.Kind != closer:
			p.unexpected(fmt.Sprintf("`,` or `%s`", closer))
		}
	}
	end := p.tok.End
	p.next()
	p.noStruct = saved
	var params []sigParam
	for _, prm := range p.params {
		if nt, ok := prm.Type.(*NamedType); ok && len(nt.Args) == 0 {
			params = append(params, sigParam{prm.Name.Name, nt.Name.Name})
		}
	}
	p.diags = append(p.diags, macroDiag(p.fs, id.Name, Span{id.NamePos, end}, open, args, repeat, params))
	if id.Name == "vec" {
		if repeat && len(exprs) > 0 {
			exprs = exprs[:1] // vec![x; n]: a list of x
		}
		return &ListExpr{Lbrack: open.Pos, Elems: exprs, Rbrack: end - 1}
	}
	return &MacroCall{Name: id, Args: exprs, End: end}
}

// macroDiag reports a Rust macro call spanning sp: vec! is R0113, the others
// R0115. The fix, where Riddim has a plain equivalent, writes it: println!
// prints with an Out, format! interpolates, panic! and todo! are functions.
func macroDiag(fs *FileSet, name string, sp Span, open Token, args []macroArg, repeat bool, params []sigParam) diag.Diagnostic {
	replace := func(d *diag.Diagnostic, title, text string) {
		d.Fixes = append(d.Fixes, fix(title, fs.Edit(sp, text)))
	}
	switch name {
	case "vec":
		d := fs.Diag("R0113", sp, "`vec![...]` is Rust: Riddim writes a list as `[...]`")
		switch {
		case repeat:
			d.Notes = append(d.Notes, "Riddim has no repeat literal: build the list in a loop with `push`")
		case open.Kind == LBRACK:
			d.Fixes = append(d.Fixes, fix("write a list", fs.Edit(Span{sp.Start, open.Pos}, "")))
		}
		return d
	case "println", "print", "eprintln", "eprint":
		out, found := outFor(params, strings.HasPrefix(name, "e"))
		method := strings.TrimPrefix(name, "e")
		d := fs.Diag("R0115", sp, "Riddim has no macros: print with an Out capability, as in `%s.%s(\"n = ${n}\")`", out, method)
		if text, ok := formatText(args); ok {
			replace(&d, "call "+method, out+"."+method+"("+text+")")
		}
		if !found {
			d.Notes = append(d.Notes, "printing needs the Out capability: main takes `sys: Sys`; pass `sys.out` on to the functions that print")
		}
		return d
	case "format":
		d := fs.Diag("R0115", sp, "Riddim has no macros: build text with interpolation, as in \"n = ${n}\"")
		if text, ok := formatText(args); ok && len(args) > 0 {
			replace(&d, "interpolate", text)
		}
		return d
	case "panic", "unreachable":
		d := fs.Diag("R0115", sp, "Riddim has no macros: `panic(msg)` is a function")
		text, ok := `"explicit panic"`, true
		if name == "unreachable" {
			text = `"unreachable"`
		}
		if len(args) > 0 {
			text, ok = formatText(args)
		}
		if ok {
			replace(&d, "call panic", "panic("+text+")")
		}
		return d
	case "todo", "unimplemented":
		d := fs.Diag("R0115", sp, "Riddim has no macros: `todo()` is a function, and takes no message")
		replace(&d, "call todo", "todo()")
		return d
	}
	d := fs.Diag("R0115", sp, "Riddim has no macros, and `%s!` has no Riddim form", name)
	switch name {
	case "assert", "assert_eq", "assert_ne", "debug_assert":
		d.Notes = append(d.Notes, "check the condition with `if` and call `panic(\"...\")`")
	case "matches":
		d.Notes = append(d.Notes, "test a pattern with `if let p = x { true } else { false }`")
	case "write", "writeln", "dbg":
		d.Notes = append(d.Notes, "print with an Out capability's `print` or `println`")
	}
	return d
}

// skipImplicit skips the newlines inside a macro's braces.
func (p *parser) skipImplicit(closer Kind) {
	for closer == RBRACE && p.tok.Kind == TERM && p.tok.Implicit {
		p.next()
	}
}

// outFor returns the Out capability a print macro's fix should print with:
// a Sys or Out parameter of the enclosing function, or else sys.out (sys.err
// for eprint!), reporting whether it found one.
func outFor(params []sigParam, stderr bool) (string, bool) {
	field := "out"
	if stderr {
		field = "err"
	}
	for _, prm := range params {
		switch {
		case prm.typ == "Sys":
			return prm.name + "." + field, true
		case prm.typ == "Out" && !stderr:
			return prm.name, true
		}
	}
	return "sys." + field, false
}

// formatText turns a macro's format string and arguments into a Riddim
// string: println!("{} is {}", a, b) prints "${a} is ${b}". No arguments
// give "".
func formatText(args []macroArg) (string, bool) {
	if len(args) == 0 {
		return `""`, true
	}
	first := args[0]
	switch {
	case first.interp:
		return first.text, len(args) == 1
	case first.str != nil:
		var pos []string
		named := map[string]string{}
		for _, a := range args[1:] {
			if strings.ContainsAny(a.text, "\n\r") {
				return "", false
			}
			if a.name != "" {
				named[a.name] = a.text
			} else {
				pos = append(pos, a.text)
			}
		}
		return rustFormat(*first.str, pos, named)
	}
	return "", false
}

// rustFormat turns a Rust format string and the source text of its arguments
// into a Riddim string literal with interpolations: "{} and {name}" with x
// gives "${x} and ${name}". It fails on a format spec Riddim has no
// equivalent for, such as {:.2}, and when the arguments do not match.
func rustFormat(f string, pos []string, named map[string]string) (string, bool) {
	var b strings.Builder
	b.WriteByte('"')
	next := 0
	used := make([]bool, len(pos))
	for i := 0; i < len(f); {
		c := f[i]
		switch {
		case c == '{' && i+1 < len(f) && f[i+1] == '{':
			b.WriteByte('{')
			i += 2
		case c == '}' && i+1 < len(f) && f[i+1] == '}':
			b.WriteByte('}')
			i += 2
		case c == '{':
			j := strings.IndexByte(f[i:], '}')
			if j < 0 {
				return "", false
			}
			name, spec, _ := strings.Cut(f[i+1:i+j], ":")
			if spec != "" && spec != "?" && spec != "#?" {
				return "", false
			}
			var text string
			if name == "" {
				if next >= len(pos) {
					return "", false
				}
				name = strconv.Itoa(next)
				next++
			}
			if k, err := strconv.Atoi(name); err == nil {
				if k < 0 || k >= len(pos) {
					return "", false
				}
				text, used[k] = pos[k], true
			} else if t, ok := named[name]; ok {
				text = t
			} else if isName(name) {
				text = name // Rust's inline argument, {x}
			} else {
				return "", false
			}
			b.WriteString("${" + text + "}")
			i += j + 1
		case c == '}':
			return "", false
		default:
			r, size := utf8.DecodeRuneInString(f[i:])
			writeEscaped(&b, r)
			i += size
		}
	}
	for _, u := range used {
		if !u {
			return "", false
		}
	}
	b.WriteByte('"')
	return b.String(), true
}

func isName(s string) bool {
	for i, r := range s {
		if !isIdentRune(r) || i == 0 && r >= '0' && r <= '9' {
			return false
		}
	}
	return s != ""
}

// writeEscaped writes r as it would appear in a Riddim string literal.
func writeEscaped(b *strings.Builder, r rune) {
	switch r {
	case '\\':
		b.WriteString(`\\`)
	case '"':
		b.WriteString(`\"`)
	case '\n':
		b.WriteString(`\n`)
	case '\t':
		b.WriteString(`\t`)
	case '\r':
		b.WriteString(`\r`)
	case '$':
		b.WriteString(`\$`)
	default:
		if r < 0x20 || r == 0x7f {
			fmt.Fprintf(b, `\u{%X}`, r)
		} else {
			b.WriteRune(r)
		}
	}
}
