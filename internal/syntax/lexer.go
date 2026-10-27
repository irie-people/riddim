package syntax

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/riddim-hq/riddim/internal/diag"
)

// Lex splits a source file into tokens. Newlines become TERM tokens where they
// end a statement (DESIGN §4.1). The token list always ends with EOF.
func Lex(fs *FileSet, src *Source) ([]Token, []diag.Diagnostic) {
	lx := &lexer{fs: fs, src: src, b: src.Src}
	lx.run()
	return terminate(lx.toks), lx.diags
}

type lexer struct {
	fs    *FileSet
	src   *Source
	b     []byte
	off   int
	nl    bool
	toks  []Token
	diags []diag.Diagnostic
}

func (lx *lexer) errorf(code string, start, end int, format string, args ...any) {
	lx.diags = append(lx.diags, lx.fs.Diag(code, Span{lx.src.Pos(start), lx.src.Pos(end)}, format, args...))
}

func (lx *lexer) emit(k Kind, start int, text string) {
	lx.toks = append(lx.toks, Token{Kind: k, Pos: lx.src.Pos(start), End: lx.src.Pos(lx.off), Text: text, NL: lx.nl})
	lx.nl = false
}

func (lx *lexer) run() {
	if len(lx.b) >= 3 && lx.b[0] == 0xEF && lx.b[1] == 0xBB && lx.b[2] == 0xBF {
		lx.off = 3
	}
	for {
		lx.skipSpace()
		if lx.off >= len(lx.b) {
			lx.emit(EOF, lx.off, "")
			return
		}
		start := lx.off
		c := lx.b[lx.off]
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
			lx.nl = true
			lx.off++
		case c == ' ' || c == '\t' || c == '\r':
			lx.off++
		case c == '/' && lx.off+1 < len(lx.b) && lx.b[lx.off+1] == '/':
			for lx.off < len(lx.b) && lx.b[lx.off] != '\n' {
				lx.off++
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

// string lexes "..." with escapes \n \t \r \\ \" \$ \u{...}. Interpolation,
// ${...}, is not part of this version.
func (lx *lexer) string(start int) {
	lx.off++
	var sb strings.Builder
	for {
		if lx.off >= len(lx.b) || lx.b[lx.off] == '\n' {
			lx.errorf("R0102", start, lx.off, "unterminated string")
			d := &lx.diags[len(lx.diags)-1]
			d.Notes = append(d.Notes, "strings end on the line they start; write \\n for a newline")
			lx.emit(STRING, start, sb.String())
			return
		}
		c := lx.b[lx.off]
		switch {
		case c == '"':
			lx.off++
			lx.emit(STRING, start, sb.String())
			return
		case c == '\\':
			lx.escape(&sb)
		case c == '$' && lx.peek(1) == '{':
			lx.errorf("R0106", lx.off, lx.off+2, "string interpolation `${...}` is not supported in this version")
			d := &lx.diags[len(lx.diags)-1]
			d.Notes = append(d.Notes, "join strings with +: \"total: \" + n.to_string(); write \\$ for a literal $")
			sb.WriteByte('$')
			lx.off++
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
	{"..=", DOT2EQ}, {"::", COLON2}, {"..", DOT2}, {"->", ARROW}, {"=>", FATARROW},
	{"==", EQ}, {"!=", NE}, {"<=", LE}, {">=", GE}, {"&&", ANDAND}, {"||", OROR},
	{"+=", PLUSEQ}, {"-=", MINUSEQ}, {"*=", STAREQ}, {"/=", SLASHEQ},
	{"(", LPAREN}, {")", RPAREN}, {"[", LBRACK}, {"]", RBRACK}, {"{", LBRACE},
	{"}", RBRACE}, {",", COMMA}, {":", COLON}, {".", DOT}, {"=", ASSIGN}, {"<", LT},
	{">", GT}, {"+", PLUS}, {"-", MINUS}, {"*", STAR}, {"/", SLASH}, {"%", PERCENT},
	{"!", NOT}, {"|", PIPE}, {"?", QUESTION}, {";", TERM},
}

func (lx *lexer) punct(start int) {
	rest := lx.b[lx.off:]
	for _, p := range puncts {
		if len(rest) >= len(p.text) && string(rest[:len(p.text)]) == p.text {
			lx.off += len(p.text)
			lx.emit(p.kind, start, p.text)
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
