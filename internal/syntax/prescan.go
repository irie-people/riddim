package syntax

import (
	"strings"

	"github.com/riddim-hq/riddim/internal/diag"
)

// The token pre-scan finds the Rust and Go habits that the tokens alone show
// (DESIGN §7.2), before parsing. A syntax error drops the rest of its
// declaration, and stops the checker, which finds the habits that need names
// and types; the pre-scan's reports stand in for theirs then, so that one
// round still reports every habit. When the whole program parses, the parser
// and the checker report each habit themselves, with more to go on, and the
// pre-scan's reports are not used.
//
// The pre-scan reports a habit with the code and at the place the parser or
// the checker would, and only where the tokens make it certain: in a type,
// after a `.`, in a call of a path. It does not report a name the program
// declares, such as a method called `iter`.

// scanHabits returns the habits in a file's tokens.
func scanHabits(fs *FileSet, src *Source, toks []Token) []diag.Diagnostic {
	s := &prescan{fs: fs, src: src, toks: toks, letDepth: -1, closure: -1,
		globals: map[string]bool{}, members: map[string]bool{}, skip: map[int]bool{},
		pairs: pairBrackets(toks)}
	s.declared()
	for i := 0; i < len(toks) && toks[i].Kind != EOF; {
		i = s.step(i)
	}
	return s.diags
}

// A role is what the tokens inside a bracket are.
type role int

const (
	roleOther   role = iota
	roleParams       // the parentheses of `fn name(...)`
	roleStruct       // the braces of `struct Name { ... }`
	roleEnum         // the braces of `enum Name { ... }`
	roleVariant      // the parentheses of a variant's fields
)

type frame struct {
	kind Kind // LPAREN, LBRACK or LBRACE
	role role
}

type prescan struct {
	fs    *FileSet
	src   *Source
	toks  []Token
	diags []diag.Diagnostic

	globals map[string]bool // top-level functions, types and constants
	members map[string]bool // functions, methods and fields: names a habit method may be
	nilName bool            // the program binds the name nil

	stack    []frame
	pending  role         // the role of the next bracket: after `fn name`, `struct Name`, `enum Name`
	letDepth int          // the stack depth of the let whose pattern is being read, or -1
	closure  int          // the stack depth of the closure whose parameters are being read, or -1
	params   []sigParam   // the parameters of the function being read
	skip     map[int]bool // tokens that are part of a habit already reported
	pairs    []int        // for each opening bracket, the index of its closer, or -1
}

// pairBrackets matches the brackets of toks in one pass, so that finding a
// closer never rescans the file. A closer that does not match the innermost
// open bracket closes nothing.
func pairBrackets(toks []Token) []int {
	pairs := make([]int, len(toks))
	var open []int
	for i, t := range toks {
		pairs[i] = -1
		switch t.Kind {
		case LPAREN, LBRACK, LBRACE:
			open = append(open, i)
		case RPAREN, RBRACK, RBRACE:
			want := map[Kind]Kind{RPAREN: LPAREN, RBRACK: LBRACK, RBRACE: LBRACE}[t.Kind]
			if n := len(open); n > 0 && toks[open[n-1]].Kind == want {
				pairs[open[n-1]] = i
				open = open[:n-1]
			}
		}
	}
	return pairs
}

func (s *prescan) tok(i int) Token {
	switch {
	case i < 0:
		return Token{Kind: TERM} // the start of the file starts a statement
	case i >= len(s.toks):
		return s.toks[len(s.toks)-1]
	}
	return s.toks[i]
}

func (s *prescan) text(sp Span) string {
	from, to := s.src.Offset(sp.Start), s.src.Offset(sp.End)
	if from > to {
		return ""
	}
	return string(s.src.Src[from:to])
}

func (s *prescan) report(code string, sp Span, format string, args ...any) *diag.Diagnostic {
	s.diags = append(s.diags, s.fs.Diag(code, sp, format, args...))
	return &s.diags[len(s.diags)-1]
}

func (s *prescan) top() frame {
	if len(s.stack) == 0 {
		return frame{}
	}
	return s.stack[len(s.stack)-1]
}

// stmtStart reports whether toks[i] starts a statement or a declaration.
func (s *prescan) stmtStart(i int) bool {
	k := s.tok(i - 1).Kind
	return k == TERM || k == LBRACE
}

// operandEnd reports whether a token of kind k ends an operand, so that an
// operator after it is binary.
func operandEnd(k Kind) bool {
	switch k {
	case IDENT, INT, FLOAT, STRING, STRTAIL, RPAREN, RBRACK, RBRACE, QUESTION, TRUE, FALSE, SELF, SELFTYPE:
		return true
	}
	return false
}

// declared collects the names the program declares.
func (s *prescan) declared() {
	depth := 0
	for i, t := range s.toks {
		next := s.tok(i + 1)
		switch t.Kind {
		case LBRACE:
			depth++
		case RBRACE:
			depth--
		case STRUCT, ENUM, CONST:
			if next.Kind == IDENT {
				s.globals[next.Text] = true
			}
		case FN:
			if next.Kind == IDENT {
				s.members[next.Text] = true
				if depth == 0 {
					s.globals[next.Text] = true
				}
			}
		case IDENT:
			if t.Text == "func" && next.Kind == IDENT {
				s.members[next.Text] = true
				if depth == 0 {
					s.globals[next.Text] = true
				}
			}
			if next.Kind == COLON {
				s.members[t.Text] = true
			}
			if t.Text == "nil" {
				switch s.tok(i - 1).Kind {
				case LET, MUT, FN, CONST, PIPE:
					s.nilName = true
				}
				if next.Kind == COLON {
					s.nilName = true
				}
			}
		}
	}
}

// step reads the token at i and returns the index of the next one to read.
func (s *prescan) step(i int) int {
	if s.skip[i] {
		return i + 1
	}
	t := s.toks[i]
	switch t.Kind {
	case TERM:
		s.letDepth, s.closure, s.pending = -1, -1, roleOther
	case LPAREN, LBRACK, LBRACE:
		return s.open(i)
	case RPAREN, RBRACK, RBRACE:
		s.close(t.Kind)
	case FN:
		if s.tok(i+1).Kind == IDENT {
			s.pending, s.params = roleParams, nil
		}
	case STRUCT:
		s.pending = roleStruct
	case ENUM:
		s.pending = roleEnum
	case IMPL:
		// The checker reads no habits in an impl's type, only the parser.
		j := i + 1
		if s.tok(j).Kind == LT {
			j = s.matchAngle(j)
		}
		return max(i+1, s.typ(j, true))
	case LET:
		s.letDepth = len(s.stack)
	case ASSIGN:
		if s.letDepth == len(s.stack) {
			s.letDepth = -1
		}
	case ARROW, AS:
		return max(i+1, s.typ(i+1, false))
	case COLON:
		if s.typeColon(i) {
			return s.typeAfter(i)
		}
	case COMMA:
		if s.top().role == roleVariant {
			return max(i+1, s.variantField(i+1))
		}
	case PIPE:
		switch {
		case s.closure == len(s.stack):
			s.closure = -1
		case !operandEnd(s.tok(i - 1).Kind):
			s.closure = len(s.stack)
		}
	case AMP:
		return s.reference(i)
	case ANDAND:
		if !operandEnd(s.tok(i - 1).Kind) {
			return s.reference(i)
		}
	case STAR:
		if !operandEnd(s.tok(i - 1).Kind) {
			s.deref(i)
		}
	case INCR, DECR:
		s.incr(i)
	case COLONEQ:
		s.shortVar(i)
	case FOR:
		s.goFor(i)
	case USE:
		if s.tok(i+1).Kind == IDENT {
			end := stmtEnd(s.toks, i)
			d, _ := modDiag(s.fs, s.src, s.toks, i, end)
			s.diags = append(s.diags, d)
			return end + 1
		}
	case COLON2:
		if s.tok(i+1).Kind == LT {
			return s.turbofish(i)
		}
	case DOT:
		s.method(i)
	case IDENT:
		return s.ident(i)
	}
	return i + 1
}

func (s *prescan) open(i int) int {
	t := s.toks[i]
	r := roleOther
	switch {
	case t.Kind == LPAREN && s.pending == roleParams:
		r, s.pending = roleParams, roleOther
	case t.Kind == LBRACE && (s.pending == roleStruct || s.pending == roleEnum):
		r, s.pending = s.pending, roleOther
	case t.Kind == LBRACE:
		s.pending = roleOther
	case t.Kind == LPAREN && s.top().role == roleEnum && s.tok(i-1).Kind == IDENT:
		r = roleVariant
	}
	s.stack = append(s.stack, frame{t.Kind, r})
	if r == roleVariant {
		return max(i+1, s.variantField(i+1))
	}
	return i + 1
}

func (s *prescan) close(k Kind) {
	open := map[Kind]Kind{RPAREN: LPAREN, RBRACK: LBRACK, RBRACE: LBRACE}[k]
	if n := len(s.stack); n > 0 && s.stack[n-1].kind == open {
		s.stack = s.stack[:n-1]
	}
	if s.letDepth > len(s.stack) {
		s.letDepth = -1
	}
	if s.closure > len(s.stack) {
		s.closure = -1
	}
}

// matching returns the index of the bracket that closes the one at i, or -1.
func (s *prescan) matching(i int) int {
	if i < 0 || i >= len(s.pairs) {
		return -1
	}
	return s.pairs[i]
}

// maxAngle bounds the search for the `>` of a list of type arguments.
const maxAngle = 64

// matchAngle returns the index after the `>` that closes the `<` at i, or
// i+1 when there is none soon, before the end of the statement.
func (s *prescan) matchAngle(i int) int {
	depth := 0
	for k := i; k < len(s.toks) && k < i+maxAngle; k++ {
		switch s.toks[k].Kind {
		case LT:
			depth++
		case GT:
			if depth--; depth == 0 {
				return k + 1
			}
		case TERM, LBRACE, RBRACE, EOF:
			return i + 1
		}
	}
	return i + 1
}

// args splits the tokens between from and to at the commas outside brackets.
func (s *prescan) args(from, to int) [][2]int {
	var out [][2]int
	depth, start := 0, from
	for k := from; k < to; k++ {
		switch s.toks[k].Kind {
		case LPAREN, LBRACK, LBRACE:
			depth++
		case RPAREN, RBRACK, RBRACE:
			depth--
		case COMMA:
			if depth == 0 {
				if start < k {
					out = append(out, [2]int{start, k})
				}
				start = k + 1
			}
		}
	}
	if start < to {
		out = append(out, [2]int{start, to})
	}
	return out
}

// span returns the span of the tokens from i to j, j excluded.
func (s *prescan) span(i, j int) Span { return Span{s.toks[i].Pos, s.toks[j-1].End} }

// ---- Types ----

// typeColon reports whether the `:` at i is followed by a type: in a let,
// in the parameters of a function or closure, in a struct's fields or a
// variant's, or after `const NAME` or Go's `var name`.
func (s *prescan) typeColon(i int) bool {
	prev, pp := s.tok(i-1), s.tok(i-2)
	switch {
	case s.letDepth == len(s.stack):
		return true
	case prev.Kind != IDENT:
		return false
	case s.closure == len(s.stack), pp.Kind == CONST:
		return true
	case pp.Kind == IDENT && pp.Text == "var" && s.stmtStart(i-2):
		return true
	}
	switch s.top().role {
	case roleParams, roleStruct, roleVariant:
		return true
	}
	return false
}

// typeAfter reads the type after the `:` at i. A parameter's type that is a
// plain name is kept for the fixes of print macros.
func (s *prescan) typeAfter(i int) int {
	end := s.typ(i+1, false)
	if s.top().role == roleParams && end == i+2 && s.toks[i+1].Kind == IDENT {
		s.params = append(s.params, sigParam{s.tok(i - 1).Text, s.toks[i+1].Text})
	}
	return max(i+1, end)
}

// variantField reads a variant's field at j: a type, or a name whose `:` is
// read next.
func (s *prescan) variantField(j int) int {
	if t := s.tok(j); t.Kind == RPAREN || t.Kind == IDENT && s.tok(j+1).Kind == COLON {
		return j
	}
	return s.typ(j, false)
}

// typ reads the type at j, reports the habits in it and returns the index
// after it, or j when no type starts there. With parserOnly set it reports
// only the habits the parser finds: in a call's type arguments and an
// impl's type, which the checker never resolves.
func (s *prescan) typ(j int, parserOnly bool) int {
	t := s.tok(j)
	switch t.Kind {
	case AMP:
		k := j + 1
		if s.tok(k).Kind == MUT {
			k++
		}
		var end int
		var text string
		if in := s.tok(k); in.Kind == IDENT && in.Text == "str" && s.tok(k+1).Kind != LT {
			end, text = k+1, "String"
		} else {
			if end = s.typ(k, parserOnly); end == k {
				return j
			}
			text = s.text(s.span(k, end))
		}
		d := s.report("R0111", t.Span(), "Riddim has no references: a parameter or field holds the value itself; write `%s`", text)
		d.Fixes = append(d.Fixes, fix("use the value's type", s.fs.Edit(Span{t.Pos, s.toks[end-1].End}, text)))
		return end
	case STAR:
		end := s.typ(j+1, parserOnly)
		if end == j+1 {
			return j
		}
		d := s.report("R0111", t.Span(), "Riddim has no pointers: a parameter or field holds the value itself; write `%s`", s.text(s.span(j+1, end)))
		d.Fixes = append(d.Fixes, fix("use the value's type", s.fs.Edit(Span{t.Pos, s.toks[j+1].Pos}, "")))
		return end
	case LBRACK:
		if s.tok(j+1).Kind == RBRACK {
			end := s.typ(j+2, parserOnly)
			if end == j+2 {
				return j
			}
			s.listType(s.span(j, end), "`[]%s` is Go: Riddim's list type is `%s`", s.text(s.span(j+2, end)))
			return end
		}
		end := s.typ(j+1, parserOnly)
		if end == j+1 {
			return j
		}
		elem := s.text(s.span(j+1, end))
		switch e := s.tok(end); {
		case e.Kind == RBRACK:
			s.listType(s.span(j, end+1), "`[%s]` is a Rust slice: Riddim's list type is `%s`", elem)
			return end + 1
		case e.Kind == TERM && !e.Implicit:
			k := end
			for k < len(s.toks) && s.toks[k].Kind != RBRACK && s.toks[k].Kind != EOF {
				k++
			}
			if s.tok(k).Kind != RBRACK {
				return end
			}
			s.listType(s.span(j, k+1), "`[%s; N]` is a Rust array: Riddim's list type is `%s`", elem)
			return k + 1
		}
		return end
	case IDENT:
		if t.Text == "map" && s.tok(j+1).Kind == LBRACK {
			kEnd := s.typ(j+2, parserOnly)
			if kEnd == j+2 || s.tok(kEnd).Kind != RBRACK {
				return j
			}
			vEnd := s.typ(kEnd+1, parserOnly)
			if vEnd == kEnd+1 {
				return j
			}
			text := "Map<" + s.text(s.span(j+2, kEnd)) + ", " + s.text(s.span(kEnd+1, vEnd)) + ">"
			sp := s.span(j, vEnd)
			d := s.report("R0114", sp, "`map[K]V` is Go: Riddim's map type is `%s`", text)
			d.Fixes = append(d.Fixes, fix("write "+text, s.fs.Edit(sp, text)))
			return vEnd
		}
		end, n, ok := j+1, 0, true
		if s.tok(end).Kind == LT {
			end, n, ok = s.typeArgs(end, parserOnly)
		}
		if ok && !parserOnly && !s.globals[t.Text] {
			s.namedType(j, end, n)
		}
		return end
	case SELFTYPE:
		if s.tok(j+1).Kind == LT {
			end, _, _ := s.typeArgs(j+1, parserOnly)
			return end
		}
		return j + 1
	case LPAREN:
		end, ok := s.typeList(j+1, RPAREN, parserOnly)
		if !ok {
			return end
		}
		return end + 1
	case FN:
		if s.tok(j+1).Kind != LPAREN {
			return j
		}
		end, ok := s.typeList(j+2, RPAREN, parserOnly)
		if !ok {
			return end
		}
		if s.tok(end+1).Kind == ARROW {
			return max(end+2, s.typ(end+2, parserOnly))
		}
		return end + 1
	}
	return j
}

// typeList reads types separated by commas from j up to the closer, and
// returns the closer's index and true, or where it stopped and false.
func (s *prescan) typeList(j int, closer Kind, parserOnly bool) (int, bool) {
	for s.tok(j).Kind != closer {
		end := s.typ(j, parserOnly)
		if end == j {
			return j, false
		}
		j = end
		if s.tok(j).Kind != COMMA {
			break
		}
		j++
	}
	return j, s.tok(j).Kind == closer
}

// typeArgs reads `<A, B>` at i and returns the index after it, the number
// of arguments, and whether a `>` closed them.
func (s *prescan) typeArgs(i int, parserOnly bool) (int, int, bool) {
	j, n := i+1, 0
	for s.tok(j).Kind != GT {
		end := s.typ(j, parserOnly)
		if end == j {
			return j, n, false
		}
		n++
		j = end
		if s.tok(j).Kind != COMMA {
			break
		}
		j++
	}
	if s.tok(j).Kind != GT {
		return j, n, false
	}
	return j + 1, n, true
}

func (s *prescan) listType(sp Span, format, elem string) {
	text := "List<" + elem + ">"
	d := s.report("R0113", sp, format, elem, text)
	d.Fixes = append(d.Fixes, fix("write "+text, s.fs.Edit(sp, text)))
}

// namedType reports a type name from Rust or Go: i32 (R0110), Vec<T>
// (R0113), HashMap<K, V> (R0114), Box<T> (R0111). The name is at j, its n
// type arguments end before end.
func (s *prescan) namedType(j, end, n int) {
	t := s.toks[j]
	switch name := t.Text; {
	case n == 0 && HabitTypes[name] != "":
		to, msg, notes := HabitTypeMsg(name)
		d := s.report("R0110", t.Span(), "%s", msg)
		d.Notes = append(d.Notes, notes...)
		d.Fixes = append(d.Fixes, fix("write "+to, s.fs.Edit(t.Span(), to)))
	case n == 1 && (name == "Vec" || name == "VecDeque"):
		d := s.report("R0113", t.Span(), HabitVecMsg, name)
		d.Fixes = append(d.Fixes, fix("write List", s.fs.Edit(t.Span(), "List")))
	case n == 2 && (name == "HashMap" || name == "BTreeMap"):
		d := s.report("R0114", t.Span(), HabitMapMsg, name)
		d.Fixes = append(d.Fixes, fix("write Map", s.fs.Edit(t.Span(), "Map")))
	case n == 1 && (name == "Box" || name == "Rc" || name == "Arc" || name == "RefCell" || name == "Cell"):
		inner := s.text(s.span(j+2, end-1))
		sp := s.span(j, end)
		d := s.report("R0111", sp, HabitBoxMsg, inner)
		d.Notes = append(d.Notes, HabitBoxNote)
		d.Fixes = append(d.Fixes, fix("hold the value", s.fs.Edit(sp, inner)))
	}
}

// ---- Expressions ----

// reference reports `&x`, `&mut x`, `&&x` and `&self` (R0111).
func (s *prescan) reference(i int) int {
	t := s.toks[i]
	j := i + 1
	if s.tok(j).Kind == MUT {
		j++
	}
	next := s.tok(j)
	switch {
	case next.Kind == TERM || next.Kind == EOF:
		return i + 1
	case next.Kind == SELF && s.top().role == roleParams:
		d := s.report("R0111", t.Span(), "a method takes `self` by value: Riddim has no references, and a method that changes a value returns the new one")
		d.Fixes = append(d.Fixes, fix("take self by value", s.fs.Edit(Span{t.Pos, next.Pos}, "")))
	case s.closure == len(s.stack) || s.letDepth == len(s.stack):
		d := s.report("R0111", t.Span(), "Riddim has no references: a pattern matches the value itself")
		d.Fixes = append(d.Fixes, fix("match the value", s.fs.Edit(Span{t.Pos, next.Pos}, "")))
	default:
		d := s.report("R0111", t.Span(), "Riddim has no references: values are copied when passed, so pass the value itself")
		d.Fixes = append(d.Fixes, fix("pass the value", s.fs.Edit(Span{t.Pos, next.Pos}, "")))
	}
	return j
}

// deref reports `*x` (R0111).
func (s *prescan) deref(i int) {
	t, next := s.toks[i], s.tok(i+1)
	if next.Kind == TERM || next.Kind == EOF {
		return
	}
	d := s.report("R0111", t.Span(), "Riddim has no pointers or references, so there is nothing to dereference: use the value")
	d.Fixes = append(d.Fixes, fix("use the value", s.fs.Edit(Span{t.Pos, next.Pos}, "")))
}

// incr reports `x++` and `x--` as statements (R0123).
func (s *prescan) incr(i int) {
	switch s.tok(i + 1).Kind {
	case TERM, RBRACE, EOF:
	default:
		return
	}
	op := s.toks[i]
	repl := "+= 1"
	if op.Kind == DECR {
		repl = "-= 1"
	}
	d := s.report("R0123", op.Span(), "Riddim has no `%s`: write `%s`", op.Text, repl)
	d.Fixes = append(d.Fixes, fix("write `"+repl+"`", s.fs.Edit(Span{s.tok(i - 1).End, op.End}, " "+repl)))
}

// shortVar reports Go's `x := e` and `a, b := f()` at the start of a
// statement in a block (R0108).
func (s *prescan) shortVar(i int) {
	if len(s.stack) == 0 || s.tok(i-1).Kind != IDENT {
		return
	}
	first := i - 1
	for s.tok(first-1).Kind == COMMA && s.tok(first-2).Kind == IDENT {
		first -= 2
	}
	if !s.stmtStart(first) {
		return
	}
	op, id := s.toks[i], s.toks[first]
	if first < i-1 {
		d := s.report("R0108", Span{id.Pos, op.End}, "Riddim has no `:=` and no multiple results: a function that can fail returns a Result; write `let %s = f()?` to pass its error on", id.Text)
		d.Notes = append(d.Notes, "a function that returns two values returns a tuple: `let (a, b) = f()`")
		return
	}
	d := s.report("R0108", op.Span(), "`:=` is Go: Riddim declares a binding with `let`; write `let %s = ...`", id.Text)
	d.Fixes = append(d.Fixes, fix("write `let`", s.fs.Edit(Span{id.Pos, id.Pos}, "let "), s.fs.Edit(op.Span(), "=")))
}

// goFor reports Go's three-clause and range loops (R0108). The fix of a
// loop shaped `for i := a; i < b; i++ {` writes `for i in a..b {`.
func (s *prescan) goFor(i int) {
	name, next := s.tok(i+1), s.tok(i+2)
	if name.Kind != IDENT {
		return
	}
	switch {
	case next.Kind == COMMA && s.tok(i+3).Kind == IDENT && s.tok(i+4).Kind == COLONEQ:
		s.goRange(name, s.toks[i+4])
		s.skip[i+4] = true
		return
	case next.Kind != COLONEQ:
		return
	case s.tok(i+3).Kind == IDENT && s.tok(i+3).Text == "range":
		s.goRange(name, next)
		s.skip[i+2] = true
		return
	}
	s.skip[i+2] = true
	d := s.report("R0108", Span{name.Pos, next.End}, "Riddim's `for` runs over a range, a list or a map: write `for %s in 0..n { }`", name.Text)
	// The header: init; cond; post {. Go's i++ in it is part of the loop.
	var semis []int
	lbrace, depth := -1, 0
header:
	for k := i + 3; k < len(s.toks); k++ {
		switch t := s.toks[k]; t.Kind {
		case LPAREN, LBRACK:
			depth++
		case RPAREN, RBRACK:
			depth--
		case TERM:
			if depth == 0 && t.Implicit {
				break header // the header ends with its line
			}
			if depth == 0 {
				semis = append(semis, k)
			}
		case INCR, DECR:
			s.skip[k] = true
		case LBRACE:
			if depth == 0 {
				lbrace = k
				break header
			}
		case RBRACE, EOF:
			break header
		}
	}
	if len(semis) != 2 || lbrace < 0 || semis[0] == i+3 {
		return
	}
	c, p := semis[0]+1, semis[1]+1
	cond, op := s.tok(c), s.tok(c+1)
	if cond.Kind != IDENT || cond.Text != name.Text || op.Kind != LT && op.Kind != LE || c+2 >= semis[1] {
		return
	}
	post := s.toks[p:lbrace]
	shaped := len(post) == 2 && post[0].Kind == IDENT && post[0].Text == name.Text && post[1].Kind == INCR ||
		len(post) == 3 && post[0].Kind == IDENT && post[0].Text == name.Text && post[1].Kind == PLUSEQ && post[2].Kind == INT && post[2].Text == "1"
	if !shaped {
		return
	}
	dots := ".."
	if op.Kind == LE {
		dots = "..="
	}
	text := name.Text + " in " + s.text(s.span(i+3, semis[0])) + dots + s.text(s.span(c+2, semis[1])) + " "
	d.Fixes = append(d.Fixes, fix("write a range", s.fs.Edit(Span{name.Pos, s.toks[lbrace].Pos}, text)))
}

func (s *prescan) goRange(first, assign Token) {
	d := s.report("R0108", Span{first.Pos, assign.End}, "Go's `range` loop: Riddim's `for` runs over a list, a map or a range, as in `for x in xs { }`")
	d.Notes = append(d.Notes, "for an index, write `for (i, x) in xs.enumerate() { }`; over a map, `for (k, v) in m { }`; for a count, `for i in 0..n { }`")
}

// turbofish reports `::<T>` after a name (R0116); the checker does not read
// the types in it, so only the parser's habits there are reported.
func (s *prescan) turbofish(i int) int {
	end, _, ok := s.typeArgs(i+1, true)
	if !ok {
		return i + 1
	}
	sp := Span{s.toks[i].Pos, s.toks[end-1].End}
	d := s.report("R0116", sp, "calls take no type arguments: Riddim infers them; where it needs help, annotate the binding, as in `let xs: List<Int> = ...`")
	d.Fixes = append(d.Fixes, fix("delete the type arguments", s.fs.Edit(sp, "")))
	return end
}

// method reports `.iter()`, `.clone()`, `.to_owned()` and the like (R0117
// to R0119), unless the program declares a method or field of that name.
func (s *prescan) method(i int) {
	name := s.tok(i + 1)
	if name.Kind != IDENT || HabitMethods[name.Text] == "" || s.members[name.Text] {
		return
	}
	j := i + 2
	if s.tok(j).Kind == COLON2 && s.tok(j+1).Kind == LT {
		j = s.matchAngle(j + 1)
	}
	if s.tok(j).Kind != LPAREN {
		return
	}
	rp := s.matching(j)
	if rp < 0 {
		return
	}
	d := s.report(HabitMethods[name.Text], name.Span(), "%s", HabitMethodMsg(name.Text))
	d.Fixes = append(d.Fixes, fix("delete `."+name.Text+"()`", s.fs.Edit(Span{s.tok(i - 1).End, s.toks[rp].End}, "")))
}

// ident reads a name: Go's `var`, `func`, `package` and `import`, Rust's
// `pub`, Go's nil, a macro, or a path Rust calls to make a value.
func (s *prescan) ident(i int) int {
	t, next := s.toks[i], s.tok(i+1)
	switch t.Text {
	case "var":
		if next.Kind == IDENT && s.stmtStart(i) && len(s.stack) > 0 {
			return s.varDecl(i)
		}
	case "func":
		if next.Kind == IDENT && (s.stmtStart(i) || s.tok(i-1).Kind == IDENT && s.tok(i-1).Text == "pub") {
			d := s.report("R0124", t.Span(), "`func` is Go: Riddim declares a function with `fn`")
			d.Fixes = append(d.Fixes, fix("write `fn`", s.fs.Edit(t.Span(), "fn")))
			s.pending, s.params = roleParams, nil
		}
	case "pub":
		if s.isPub(i) {
			return s.pub(i)
		}
	case "package", "import":
		module := next.Kind == IDENT && t.Text == "package" ||
			t.Text == "import" && (next.Kind == STRING || next.Kind == LPAREN || next.Kind == IDENT && s.tok(i+2).Kind == STRING)
		if module && len(s.stack) == 0 && s.stmtStart(i) {
			end := stmtEnd(s.toks, i)
			d, _ := modDiag(s.fs, s.src, s.toks, i, end)
			s.diags = append(s.diags, d)
			return end + 1
		}
	case "nil":
		if !s.nilName {
			s.nilHabit(i)
		}
	}
	if next.Kind == NOT && next.Pos == t.End {
		switch s.tok(i + 2).Kind {
		case LPAREN, LBRACK, LBRACE:
			s.macro(i)
			return i + 2 // the arguments are read on, for the habits in them
		}
	}
	if next.Kind == COLON2 {
		s.path(i)
	}
	return i + 1
}

// varDecl reports Go's `var x = e` and `var x T = e` (R0108).
func (s *prescan) varDecl(i int) int {
	v, name, after := s.toks[i], s.toks[i+1], s.tok(i+2)
	d := s.report("R0108", v.Span(), "`var` is Go: Riddim declares a binding with `let`, and one that changes with `let mut`")
	f := fix("write `let mut`", s.fs.Edit(v.Span(), "let mut"))
	switch after.Kind {
	case IDENT, LBRACK, STAR, LPAREN, FN:
		// Go writes the type without a colon: var n int = 1.
		f.Edits = append(f.Edits, s.fs.Edit(Span{name.End, name.End}, ":"))
		d.Fixes = append(d.Fixes, f)
		return max(i+2, s.typ(i+2, false))
	}
	d.Fixes = append(d.Fixes, f)
	return i + 2
}

// isPub reports whether `pub` at i starts a declaration or a field.
func (s *prescan) isPub(i int) bool {
	if !s.stmtStart(i) && s.tok(i-1).Kind != COMMA {
		return false
	}
	t, next := s.toks[i], s.tok(i+1)
	return isDeclStart(next.Kind) || next.Kind == IDENT || next.Kind == LPAREN && next.Pos == t.End
}

// pub reports `pub` and `pub(crate)` (R0121).
func (s *prescan) pub(i int) int {
	t := s.toks[i]
	end, after := t.End, i+1
	if next := s.tok(i + 1); next.Kind == LPAREN && next.Pos == t.End {
		rp := s.matching(i + 1)
		if rp < 0 {
			return i + 1
		}
		end, after = s.toks[rp].End, rp+1
	}
	d := s.report("R0121", Span{t.Pos, end}, "Riddim has no `pub`: a program is one package, and every declaration is visible in all its files")
	d.Fixes = append(d.Fixes, fix("delete it", s.fs.Edit(Span{t.Pos, s.tok(after).Pos}, "")))
	return after
}

// nilHabit reports Go's nil (R0109). Compared with a name like err, it is
// Go's error check (R0120).
func (s *prescan) nilHabit(i int) {
	t := s.toks[i]
	isCmp := func(k Kind) bool { return k == EQ || k == NE }
	single := func(j int) bool { // toks[j] is a whole operand
		k := s.tok(j - 1).Kind
		after := s.tok(j + 1).Kind
		return s.tok(j).Kind == IDENT && k != DOT && k != COLON2 &&
			after != DOT && after != LPAREN && after != LBRACK && after != COLON2 && after != QUESTION
	}
	errName := func(name string) bool { return strings.Contains(strings.ToLower(name), "err") }
	method := func(op Token) string {
		if op.Kind == NE {
			return "is_some()"
		}
		return "is_none()"
	}
	switch {
	case isCmp(s.tok(i - 1).Kind):
		if single(i-2) && errName(s.tok(i-2).Text) {
			s.errNil(Span{s.tok(i - 2).Pos, t.End})
			return
		}
		s.report("R0109", t.Span(), HabitNilTestMsg, method(s.tok(i-1)))
	case isCmp(s.tok(i + 1).Kind):
		if single(i+2) && errName(s.tok(i+2).Text) {
			s.errNil(Span{t.Pos, s.tok(i + 2).End})
			return
		}
		s.report("R0109", t.Span(), HabitNilTestMsg, method(s.tok(i+1)))
	default:
		s.report("R0109", t.Span(), "%s", HabitNilMsg)
	}
}

func (s *prescan) errNil(sp Span) {
	d := s.report("R0120", sp, "%s", HabitErrNilMsg)
	d.Notes = append(d.Notes, HabitErrNilNote)
}

// macro reports a Rust macro call at i (R0113 for vec!, R0115 otherwise),
// with the fix the parser gives it.
func (s *prescan) macro(i int) {
	id, open := s.toks[i], s.toks[i+2]
	end := s.matching(i + 2)
	if end < 0 {
		return
	}
	repeat := false
	var ranges [][2]int
	depth, start := 0, i+3
	cut := func(k int) {
		from, to := start, k
		for from < to && s.toks[from].Kind == TERM && s.toks[from].Implicit {
			from++
		}
		for to > from && s.toks[to-1].Kind == TERM && s.toks[to-1].Implicit {
			to--
		}
		if from < to {
			ranges = append(ranges, [2]int{from, to})
		}
		start = k + 1
	}
	for k := i + 3; k < end; k++ {
		switch t := s.toks[k]; t.Kind {
		case LPAREN, LBRACK, LBRACE:
			depth++
		case RPAREN, RBRACK, RBRACE:
			depth--
		case COMMA:
			if depth == 0 {
				cut(k)
			}
		case TERM:
			if depth == 0 && !t.Implicit && id.Text == "vec" {
				repeat = true
				cut(k)
			}
		}
	}
	cut(end)
	var args []macroArg
	for _, r := range ranges {
		var a macroArg
		from, to := r[0], r[1]
		if to-from > 2 && s.toks[from].Kind == IDENT && s.toks[from+1].Kind == ASSIGN {
			a.name = s.toks[from].Text
			from += 2
		}
		a.text = s.text(s.span(from, to))
		switch first := s.toks[from]; {
		case to-from == 1 && first.Kind == STRING:
			v := first.Text
			a.str = &v
		case first.Kind == STRHEAD && s.oneString(from, to) == to:
			a.interp = true
		}
		args = append(args, a)
	}
	s.diags = append(s.diags, macroDiag(s.fs, id.Text, Span{id.Pos, s.toks[end].End}, open, args, repeat, s.params))
}

// oneString returns the index after the interpolated string literal that
// starts at i, if it ends before to.
func (s *prescan) oneString(i, to int) int {
	depth := 0
	for k := i; k < to; k++ {
		switch s.toks[k].Kind {
		case STRHEAD:
			depth++
		case STRTAIL:
			if depth--; depth == 0 {
				return k + 1
			}
		case EOF:
			return -1
		}
	}
	return -1
}

// path reports the Rust calls that make a value Riddim writes otherwise:
// Vec::new() (R0113), HashMap::new() (R0114), Box::new(x) (R0111),
// String::new() and String::from(s) (R0119).
func (s *prescan) path(i int) {
	tn := s.toks[i]
	if s.globals[tn.Text] {
		return
	}
	j := i + 1
	if s.tok(j+1).Kind == LT { // Vec::<T>::new()
		if j = s.matchAngle(j + 1); s.tok(j).Kind != COLON2 {
			return
		}
	}
	if s.tok(j+1).Kind != IDENT {
		return
	}
	fn := s.toks[j+1]
	k := j + 2
	if s.tok(k).Kind == COLON2 && s.tok(k+1).Kind == LT {
		k = s.matchAngle(k + 1)
	}
	if s.tok(k).Kind != LPAREN {
		return
	}
	rp := s.matching(k)
	if rp < 0 {
		return
	}
	args := s.args(k+1, rp)
	sp := Span{tn.Pos, s.toks[rp].End}
	replace := func(d *diag.Diagnostic, title, text string) {
		d.Fixes = append(d.Fixes, fix(title, s.fs.Edit(sp, text)))
	}
	newish := fn.Text == "new" || fn.Text == "with_capacity"
	switch name := tn.Text; {
	case (name == "Vec" || name == "List") && newish:
		replace(s.report("R0113", sp, "%s", HabitVecNewMsg), "write []", "[]")
	case (name == "HashMap" || name == "BTreeMap") && newish:
		replace(s.report("R0114", sp, "%s", HabitMapNewMsg), "write Map::new()", "Map::new()")
	case (name == "Box" || name == "Rc" || name == "Arc") && fn.Text == "new" && len(args) == 1:
		replace(s.report("R0111", sp, "%s", HabitBoxNewMsg), "use the value", s.text(s.span(args[0][0], args[0][1])))
	case name == "String" && fn.Text == "new" && len(args) == 0:
		replace(s.report("R0119", sp, "%s", HabitStringNewMsg), `write ""`, `""`)
	case name == "String" && fn.Text == "from" && len(args) == 1:
		replace(s.report("R0119", sp, "%s", HabitStringFromMsg), "use the string itself", s.text(s.span(args[0][0], args[0][1])))
	}
}
