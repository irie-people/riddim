package syntax

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/irie-people/riddim/internal/diag"
)

// Lex splits a source file into tokens. Newlines become TERM tokens where they
// end a statement (DESIGN §4.1). The token list always ends with EOF.
func Lex(fs *FileSet, src *Source) ([]Token, []diag.Diagnostic) {
	lx := &lexer{fs: fs, src: src, b: src.Src}
	lx.run()
	lx.lifetimes()
	return terminate(lx.toks), lx.diags
}

// habitCodes are the lexer's reports of Rust habits. The lexer drops what
// they report cleanly, so unlike other lexical errors they do not hide the
// parser's errors on the same line.
var habitCodes = map[string]bool{"R0112": true, "R0122": true}

type lexer struct {
	fs     *FileSet
	src    *Source
	b      []byte
	off    int
	nl     bool
	toks   []Token
	diags  []diag.Diagnostic
	interp []interpFrame // the `${` open around lx.off, innermost last
	docs   []string      // /// lines waiting for the next token
}

// An interpFrame is an open `${` in a string: depth counts the `{` opened
// inside it and not yet closed, quote is the offset of its string's `"`.
type interpFrame struct {
	depth int
	quote int
}

func (lx *lexer) errorf(code string, start, end int, format string, args ...any) {
	lx.diags = append(lx.diags, lx.fs.Diag(code, Span{lx.src.Pos(start), lx.src.Pos(end)}, format, args...))
}

func (lx *lexer) emit(k Kind, start int, text string) {
	lx.toks = append(lx.toks, Token{Kind: k, Pos: lx.src.Pos(start), End: lx.src.Pos(lx.off), Text: text, NL: lx.nl, Doc: lx.docs})
	lx.nl = false
	lx.docs = nil
}

func (lx *lexer) run() {
	if len(lx.b) >= 3 && lx.b[0] == 0xEF && lx.b[1] == 0xBB && lx.b[2] == 0xBF {
		lx.off = 3
	}
	for {
		lx.skipSpace()
		if lx.off >= len(lx.b) {
			if len(lx.interp) > 0 {
				lx.unterminated(lx.interp[0].quote)
			}
			lx.emit(EOF, lx.off, "")
			return
		}
		start := lx.off
		c := lx.b[lx.off]
		if n := len(lx.interp); n > 0 {
			// Inside `${...}`: the `}` that matches the `${` resumes the string.
			top := &lx.interp[n-1]
			switch {
			case c == '{':
				top.depth++
			case c == '}' && top.depth > 0:
				top.depth--
			case c == '}':
				quote := top.quote
				lx.interp = lx.interp[:n-1]
				lx.off++
				lx.stringPart(start, quote, false)
				continue
			}
		}
		switch {
		case c == '_' || c < utf8.RuneSelf && unicode.IsLetter(rune(c)):
			lx.ident(start)
		case c >= utf8.RuneSelf:
			r, size := utf8.DecodeRune(lx.b[lx.off:])
			if r == utf8.RuneError && size == 1 {
				lx.off++
				lx.errorf("R0101", start, lx.off, "invalid UTF-8 in source")
				continue
			}
			if unicode.IsLetter(r) {
				lx.ident(start)
				continue
			}
			lx.off += size
			lx.errorf("R0101", start, lx.off, "unexpected character %q", r)
		case c >= '0' && c <= '9':
			lx.number(start)
		case c == '"':
			lx.string(start)
		default:
			lx.punct(start)
		}
	}
}

func (lx *lexer) skipSpace() {
	for lx.off < len(lx.b) {
		switch c := lx.b[lx.off]; {
		case c == '\n':
			if len(lx.interp) > 0 {
				lx.unterminated(lx.interp[0].quote)
			}
			lx.nl = true
			lx.off++
		case c == ' ' || c == '\t' || c == '\r':
			lx.off++
		case c == '/' && lx.off+1 < len(lx.b) && lx.b[lx.off+1] == '/':
			start := lx.off
			for lx.off < len(lx.b) && lx.b[lx.off] != '\n' {
				lx.off++
			}
			// A /// comment that starts a line documents the next declaration.
			text := string(lx.b[start:lx.off])
			if strings.HasPrefix(text, "///") && !strings.HasPrefix(text, "////") && (lx.nl || len(lx.toks) == 0) {
				lx.docs = append(lx.docs, strings.TrimRight(text[3:], " \t\r"))
			}
		default:
			return
		}
	}
}

func isIdentRune(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

func (lx *lexer) ident(start int) {
	for lx.off < len(lx.b) {
		r, size := utf8.DecodeRune(lx.b[lx.off:])
		if !isIdentRune(r) {
			break
		}
		lx.off += size
	}
	word := string(lx.b[start:lx.off])
	if k, ok := keywords[word]; ok {
		lx.emit(k, start, word)
		return
	}
	lx.emit(IDENT, start, word)
}

func (lx *lexer) peek(i int) byte {
	if lx.off+i < len(lx.b) {
		return lx.b[lx.off+i]
	}
	return 0
}

func isDigit(c byte) bool    { return c >= '0' && c <= '9' }
func isHexDigit(c byte) bool { return isDigit(c) || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' }

func (lx *lexer) digits(ok func(byte) bool) {
	for lx.off < len(lx.b) && (ok(lx.b[lx.off]) || lx.b[lx.off] == '_') {
		lx.off++
	}
}

// number lexes 42, 1_000, 0xff, 3.14 and 1e9. After a `.` only digits are
// read, so t.0.1 is a tuple field of a tuple field.
func (lx *lexer) number(start int) {
	afterDot := len(lx.toks) > 0 && lx.toks[len(lx.toks)-1].Kind == DOT && !lx.nl
	kind := INT
	if lx.peek(0) == '0' && (lx.peek(1) == 'x' || lx.peek(1) == 'X') && !afterDot {
		lx.off += 2
		lx.digits(isHexDigit)
	} else {
		lx.digits(isDigit)
		if !afterDot {
			if lx.peek(0) == '.' && isDigit(lx.peek(1)) {
				kind = FLOAT
				lx.off++
				lx.digits(isDigit)
			}
			if c := lx.peek(0); c == 'e' || c == 'E' {
				save := lx.off
				lx.off++
				if c := lx.peek(0); c == '+' || c == '-' {
					lx.off++
				}
				if isDigit(lx.peek(0)) {
					kind = FLOAT
					lx.digits(isDigit)
				} else {
					lx.off = save
				}
			}
		}
	}
	bad := false
	for lx.off < len(lx.b) {
		r, size := utf8.DecodeRune(lx.b[lx.off:])
		if !isIdentRune(r) {
			break
		}
		bad = true
		lx.off += size
	}
	text := string(lx.b[start:lx.off])
	if bad || !validNumber(text) {
		lx.errorf("R0104", start, lx.off, "malformed number `%s`", text)
	}
	lx.emit(kind, start, text)
}

// validNumber checks digit separators: `_` only between two digits.
func validNumber(text string) bool {
	body := text
	if strings.HasPrefix(body, "0x") || strings.HasPrefix(body, "0X") {
		body = body[2:]
		if body == "" {
			return false
		}
		for i := 0; i < len(body); i++ {
			if body[i] == '_' && (i == 0 || i == len(body)-1 || body[i-1] == '_') {
				return false
			}
		}
		return true
	}
	for i := 0; i < len(body); i++ {
		if body[i] != '_' {
			continue
		}
		if i == 0 || i == len(body)-1 || !isHexDigit(body[i-1]) || !isDigit(body[i+1]) {
			return false
		}
		if body[i-1] == 'e' || body[i-1] == 'E' {
			return false
		}
	}
	return true
}

// string lexes "..." with escapes \n \t \r \\ \" \$ \u{...}. A string with
// interpolations, "a ${x} b", becomes STRHEAD, the tokens of x, and STRTAIL;
// with more interpolations, STRMID tokens come between them.
func (lx *lexer) string(start int) {
	lx.off++
	lx.stringPart(start, start, true)
}

// stringPart lexes string text from lx.off to the closing quote or the next
// `${`. The first part of a string (head) starts at its quote; a later part
// starts at the `}` that ended an interpolation. quote is the offset of the
// string's opening quote.
func (lx *lexer) stringPart(start, quote int, head bool) {
	var sb strings.Builder
	end, open := STRTAIL, STRMID
	if head {
		end, open = STRING, STRHEAD
	}
	for {
		if lx.off >= len(lx.b) || lx.b[lx.off] == '\n' {
			lx.unterminated(quote)
			lx.emit(end, start, sb.String())
			return
		}
		c := lx.b[lx.off]
		switch {
		case c == '"':
			lx.off++
			lx.emit(end, start, sb.String())
			return
		case c == '\\':
			lx.escape(&sb)
		case c == '$' && lx.peek(1) == '{':
			lx.off += 2
			lx.emit(open, start, sb.String())
			lx.interp = append(lx.interp, interpFrame{quote: quote})
			return
		default:
			r, size := utf8.DecodeRune(lx.b[lx.off:])
			if r == utf8.RuneError && size == 1 {
				lx.errorf("R0101", lx.off, lx.off+1, "invalid UTF-8 in string")
			}
			sb.WriteRune(r)
			lx.off += size
		}
	}
}

// unterminated reports a string that does not end on its line. Any open
// interpolations end with it.
func (lx *lexer) unterminated(quote int) {
	lx.errorf("R0102", quote, lx.off, "unterminated string")
	d := &lx.diags[len(lx.diags)-1]
	d.Notes = append(d.Notes, "strings end on the line they start; write \\n for a newline")
	lx.interp = nil
}

func (lx *lexer) escape(sb *strings.Builder) {
	start := lx.off
	lx.off++ // backslash
	if lx.off >= len(lx.b) || lx.b[lx.off] == '\n' {
		lx.errorf("R0103", start, lx.off, "invalid escape at end of line")
		return
	}
	c := lx.b[lx.off]
	lx.off++
	switch c {
	case 'n':
		sb.WriteByte('\n')
	case 't':
		sb.WriteByte('\t')
	case 'r':
		sb.WriteByte('\r')
	case '\\', '"', '$':
		sb.WriteByte(c)
	case 'u':
		if lx.peek(0) != '{' {
			lx.errorf("R0103", start, lx.off, "invalid escape `\\u`: write \\u{1F600}")
			return
		}
		lx.off++
		hexStart := lx.off
		for lx.off < len(lx.b) && isHexDigit(lx.b[lx.off]) {
			lx.off++
		}
		hex := string(lx.b[hexStart:lx.off])
		if lx.peek(0) != '}' || hex == "" || len(hex) > 6 {
			for lx.off < len(lx.b) && lx.b[lx.off] != '}' && lx.b[lx.off] != '"' && lx.b[lx.off] != '\n' {
				lx.off++
			}
			if lx.peek(0) == '}' {
				lx.off++
			}
			lx.errorf("R0103", start, lx.off, "invalid escape `%s`: write \\u{} around 1 to 6 hex digits", lx.b[start:lx.off])
			return
		}
		lx.off++
		v, _ := strconv.ParseUint(hex, 16, 32)
		if v > unicode.MaxRune || v >= 0xD800 && v <= 0xDFFF {
			lx.errorf("R0103", start, lx.off, "invalid escape `%s`: not a Unicode scalar value", lx.b[start:lx.off])
			return
		}
		sb.WriteRune(rune(v))
	default:
		r, size := utf8.DecodeRune(lx.b[lx.off-1:])
		lx.off += size - 1
		lx.errorf("R0103", start, lx.off, "invalid escape `\\%c`", r)
		d := &lx.diags[len(lx.diags)-1]
		d.Notes = append(d.Notes, "escapes are \\n \\t \\r \\\\ \\\" \\$ and \\u{...}")
	}
}

var puncts = []struct {
	text string
	kind Kind
}{
	{"..=", DOT2EQ}, {"::", COLON2}, {":=", COLONEQ}, {"..", DOT2}, {"->", ARROW}, {"=>", FATARROW},
	{"==", EQ}, {"!=", NE}, {"<=", LE}, {">=", GE}, {"&&", ANDAND}, {"||", OROR},
	{"+=", PLUSEQ}, {"-=", MINUSEQ}, {"*=", STAREQ}, {"/=", SLASHEQ},
	{"(", LPAREN}, {")", RPAREN}, {"[", LBRACK}, {"]", RBRACK}, {"{", LBRACE},
	{"}", RBRACE}, {",", COMMA}, {":", COLON}, {".", DOT}, {"=", ASSIGN}, {"<", LT},
	{">", GT}, {"+", PLUS}, {"-", MINUS}, {"*", STAR}, {"/", SLASH}, {"%", PERCENT},
	{"!", NOT}, {"|", PIPE}, {"?", QUESTION}, {";", TERM},
}

func (lx *lexer) punct(start int) {
	rest := lx.b[lx.off:]
	if k := lx.incr(rest); k != EOF {
		lx.off += 2
		lx.emit(k, start, string(rest[:2]))
		return
	}
	for _, p := range puncts {
		if len(rest) >= len(p.text) && string(rest[:len(p.text)]) == p.text {
			lx.off += len(p.text)
			lx.emit(p.kind, start, p.text)
			return
		}
	}
	switch rest[0] {
	case '&':
		// After an operand, & is a bitwise and; elsewhere a reference, which
		// the parser reports.
		if !lx.afterOperand() {
			lx.off++
			lx.emit(AMP, start, "&")
			return
		}
	case '\'':
		if lx.lifetime(start) {
			return
		}
	case '#':
		if lx.attribute(start) {
			return
		}
	}
	r, size := utf8.DecodeRune(rest)
	lx.off += size
	msg := fmt.Sprintf("unexpected character `%c`", r)
	switch r {
	case '\'':
		// A Rust character literal: report it once.
		if end := strings.IndexByte(string(rest[1:min(len(rest), 12)]), '\''); end >= 0 && !strings.Contains(string(rest[1:end+1]), "\n") {
			lx.off = start + end + 2
		}
		msg += "; strings use double quotes"
	case '&':
		msg += "; Riddim has no references or bitwise operators: pass the value"
	}
	lx.errorf("R0101", start, lx.off, "%s", msg)
}

// afterOperand reports whether the token before lx.off, on the same line,
// ends an operand, so that an operator there is a binary one.
func (lx *lexer) afterOperand() bool {
	if len(lx.toks) == 0 || lx.nl {
		return false
	}
	switch lx.toks[len(lx.toks)-1].Kind {
	case IDENT, INT, FLOAT, STRING, STRTAIL, RPAREN, RBRACK, TRUE, FALSE, SELF, QUESTION:
		return true
	}
	return false
}

// incr returns INCR or DECR at Go's `x++` and `x--`: after an operand and
// before the end of a statement. Otherwise it returns EOF.
func (lx *lexer) incr(rest []byte) Kind {
	if len(rest) < 2 || rest[0] != rest[1] || rest[0] != '+' && rest[0] != '-' || !lx.afterOperand() {
		return EOF
	}
	j := 2
	for j < len(rest) && (rest[j] == ' ' || rest[j] == '\t' || rest[j] == '\r') {
		j++
	}
	if j < len(rest) && !strings.ContainsRune("\n;)}{/", rune(rest[j])) {
		return EOF
	}
	if rest[0] == '+' {
		return INCR
	}
	return DECR
}

// lifetime lexes a Rust lifetime or loop label, 'a: a quote and a name that
// no second quote closes, which would make it a character literal.
func (lx *lexer) lifetime(start int) bool {
	i := lx.off + 1
	for i < len(lx.b) {
		r, size := utf8.DecodeRune(lx.b[i:])
		if !isIdentRune(r) {
			break
		}
		i += size
	}
	first, _ := utf8.DecodeRune(lx.b[lx.off+1:])
	if i == lx.off+1 || unicode.IsDigit(first) || i < len(lx.b) && lx.b[i] == '\'' {
		return false
	}
	lx.off = i
	lx.emit(LIFETIME, start, string(lx.b[start:i]))
	return true
}

// attribute skips a Rust attribute, #[...] or #![...], on one line, and
// reports it (R0122) with a fix that deletes it, with its line when nothing
// else is on it.
func (lx *lexer) attribute(start int) bool {
	i := lx.off + 1
	if i < len(lx.b) && lx.b[i] == '!' {
		i++
	}
	if i >= len(lx.b) || lx.b[i] != '[' {
		return false
	}
	depth := 0
	for ; i < len(lx.b) && lx.b[i] != '\n'; i++ {
		if lx.b[i] == '[' {
			depth++
		} else if lx.b[i] == ']' {
			if depth--; depth == 0 {
				break
			}
		}
	}
	if depth != 0 {
		return false
	}
	end := i + 1
	lx.off = end
	from, to := start, end
	for to < len(lx.b) && (lx.b[to] == ' ' || lx.b[to] == '\t') {
		to++
	}
	lineStart := start
	for lineStart > 0 && lx.b[lineStart-1] != '\n' {
		lineStart--
	}
	if strings.TrimSpace(string(lx.b[lineStart:start])) == "" && (to == len(lx.b) || lx.b[to] == '\n' || lx.b[to] == '\r') {
		from = lineStart
		for to < len(lx.b) && lx.b[to] != '\n' {
			to++
		}
		if to < len(lx.b) {
			to++
		}
	}
	lx.errorf("R0122", start, end, "Riddim has no attributes: `%s` has no effect; delete it", lx.b[start:end])
	d := &lx.diags[len(lx.diags)-1]
	d.Notes = append(d.Notes, "Riddim derives nothing: == works on every comparable type, and values are always copied")
	d.Fixes = append(d.Fixes, diag.Fix{Title: "delete the attribute",
		Edits: []diag.Edit{lx.fs.Edit(Span{lx.src.Pos(from), lx.src.Pos(to)}, "")}})
	return true
}

// lifetimes reports Rust lifetimes and loop labels (R0112) and drops them
// from the tokens, with the brackets, commas and colons only they needed, so
// that the parser reads the code as the fix leaves it.
func (lx *lexer) lifetimes() {
	var out []Token
	var nl bool
	var doc []string
	for i := 0; i < len(lx.toks); i++ {
		t := lx.toks[i]
		if t.Kind != LIFETIME {
			t.NL = t.NL || nl
			if t.Doc == nil {
				t.Doc = doc
			}
			nl, doc = false, nil
			out = append(out, t)
			continue
		}
		next := lx.toks[i+1] // the tokens end with EOF
		var prev Token
		if len(out) > 0 {
			prev = out[len(out)-1]
		}
		after := func(j int) Pos { // up to the token after j, unless that starts a line
			if j+1 < len(lx.toks) && !lx.toks[j+1].NL {
				return lx.toks[j+1].Pos
			}
			return lx.toks[j].End
		}
		label := false
		del := Span{t.Pos, after(i)}
		switch {
		case prev.Kind == LT && next.Kind == GT: // <'a>
			del = Span{prev.Pos, next.End}
			out = out[:len(out)-1]
			i++
		case next.Kind == COMMA: // <'a, T>
			del = Span{t.Pos, after(i + 1)}
			i++
		case prev.Kind == COMMA && next.Kind == GT: // <T, 'a>
			del = Span{prev.Pos, t.End}
			out = out[:len(out)-1]
		case next.Kind == COLON && (i+2 >= len(lx.toks) || lx.toks[i+2].Kind != IDENT): // 'outer: loop
			label = true
			del = Span{t.Pos, after(i + 1)}
			i++
		case prev.Kind == BREAK || prev.Kind == CONTINUE: // break 'outer
			label = true
			del = Span{prev.End, t.End}
		}
		nl, doc = nl || t.NL, append(doc, t.Doc...)
		var d diag.Diagnostic
		if label {
			d = lx.fs.Diag("R0112", t.Span(), "Riddim has no loop labels: `break` and `continue` leave the innermost loop; delete `%s`", t.Text)
			d.Notes = append(d.Notes, "to leave an outer loop, move the loops into a function and `return`")
		} else {
			d = lx.fs.Diag("R0112", t.Span(), "Riddim has no lifetimes: values are copied, so nothing is borrowed; delete `%s`", t.Text)
		}
		d.Fixes = append(d.Fixes, diag.Fix{Title: "delete it", Edits: []diag.Edit{lx.fs.Edit(del, "")}})
		lx.diags = append(lx.diags, d)
	}
	lx.toks = out
}

// terminate turns newlines into TERM tokens. Inside ( ) and [ ] newlines are
// ignored. Elsewhere a newline ends a statement when the line's last token can
// end one, unless the next line starts with a token that continues it and the
// line did not end with return, break or continue.
func terminate(raw []Token) []Token {
	out := make([]Token, 0, len(raw)+len(raw)/4)
	var stack []Kind
	insert := func(prev Token) {
		out = append(out, Token{Kind: TERM, Pos: prev.End, End: prev.End, Implicit: true})
	}
	for i, t := range raw {
		inParens := len(stack) > 0 && stack[len(stack)-1] != LBRACE
		if i > 0 && (t.NL || t.Kind == EOF) && !inParens {
			prev := raw[i-1]
			if canEnd(prev.Kind) {
				jump := prev.Kind == RETURN || prev.Kind == BREAK || prev.Kind == CONTINUE
				if t.Kind == EOF || jump || !continues(t.Kind) {
					insert(prev)
				}
			}
		}
		out = append(out, t)
		switch t.Kind {
		case LPAREN, LBRACK, LBRACE:
			stack = append(stack, t.Kind)
		case RPAREN, RBRACK, RBRACE:
			open := map[Kind]Kind{RPAREN: LPAREN, RBRACK: LBRACK, RBRACE: LBRACE}[t.Kind]
			for j := len(stack) - 1; j >= 0; j-- {
				if stack[j] == open {
					stack = stack[:j]
					break
				}
			}
		}
	}
	return out
}
