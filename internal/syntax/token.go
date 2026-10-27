package syntax

import "fmt"

// A Kind is the kind of a token.
type Kind int

// Token kinds.
const (
	EOF  Kind = iota
	TERM      // `;` or a newline that ends a statement
	IDENT
	INT
	FLOAT
	STRING
	STRHEAD // a string's text up to its first `${`: "a ${
	STRMID  // text between two interpolations: } b ${
	STRTAIL // text after the last interpolation: } c"

	keywordsStart
	FN
	LET
	MUT
	CONST
	STRUCT
	ENUM
	IMPL
	USE
	GO
	IF
	ELSE
	MATCH
	FOR
	IN
	WHILE
	LOOP
	BREAK
	CONTINUE
	RETURN
	SELF     // self
	SELFTYPE // Self
	AS
	TRUE
	FALSE
	keywordsEnd

	LPAREN   // (
	RPAREN   // )
	LBRACK   // [
	RBRACK   // ]
	LBRACE   // {
	RBRACE   // }
	COMMA    // ,
	COLON    // :
	COLON2   // ::
	DOT      // .
	DOT2     // ..
	DOT2EQ   // ..=
	ARROW    // ->
	FATARROW // =>
	ASSIGN   // =
	EQ       // ==
	NE       // !=
	LT       // <
	LE       // <=
	GT       // >
	GE       // >=
	PLUS     // +
	MINUS    // -
	STAR     // *
	SLASH    // /
	PERCENT  // %
	NOT      // !
	ANDAND   // &&
	OROR     // ||
	PIPE     // |
	QUESTION // ?
	PLUSEQ   // +=
	MINUSEQ  // -=
	STAREQ   // *=
	SLASHEQ  // /=
)

var keywords = map[string]Kind{
	"fn": FN, "let": LET, "mut": MUT, "const": CONST, "struct": STRUCT, "enum": ENUM,
	"impl": IMPL, "use": USE, "go": GO, "if": IF, "else": ELSE, "match": MATCH,
	"for": FOR, "in": IN, "while": WHILE, "loop": LOOP, "break": BREAK,
	"continue": CONTINUE, "return": RETURN, "self": SELF, "Self": SELFTYPE,
	"as": AS, "true": TRUE, "false": FALSE,
}

var kindText = map[Kind]string{
	LPAREN: "(", RPAREN: ")", LBRACK: "[", RBRACK: "]", LBRACE: "{", RBRACE: "}",
	COMMA: ",", COLON: ":", COLON2: "::", DOT: ".", DOT2: "..", DOT2EQ: "..=",
	ARROW: "->", FATARROW: "=>", ASSIGN: "=", EQ: "==", NE: "!=", LT: "<", LE: "<=",
	GT: ">", GE: ">=", PLUS: "+", MINUS: "-", STAR: "*", SLASH: "/", PERCENT: "%",
	NOT: "!", ANDAND: "&&", OROR: "||", PIPE: "|", QUESTION: "?", PLUSEQ: "+=",
	MINUSEQ: "-=", STAREQ: "*=", SLASHEQ: "/=",
}

func init() {
	for word, k := range keywords {
		kindText[k] = word
	}
}

// String returns the token's text for keywords and punctuation.
func (k Kind) String() string {
	switch k {
	case EOF:
		return "end of file"
	case TERM:
		return "`;`"
	case IDENT:
		return "identifier"
	case INT, FLOAT:
		return "number"
	case STRING, STRHEAD, STRMID, STRTAIL:
		return "string"
	}
	if s, ok := kindText[k]; ok {
		return s
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

// IsKeyword reports whether k is a keyword.
func (k Kind) IsKeyword() bool { return k > keywordsStart && k < keywordsEnd }

// A Token is one lexical token.
type Token struct {
	Kind     Kind
	Pos, End Pos
	Text     string // identifier name or literal source; for STRING, the decoded value
	Implicit bool   // a TERM made from a newline
	NL       bool   // a newline comes between this token and the one before
}

// Span returns the token's span.
func (t Token) Span() Span { return Span{t.Pos, t.End} }

// describe returns how a diagnostic names the token.
func (t Token) describe() string {
	switch t.Kind {
	case EOF:
		return "end of file"
	case TERM:
		if t.Implicit {
			return "end of line"
		}
		return "`;`"
	case IDENT:
		return "`" + t.Text + "`"
	case INT, FLOAT:
		return "number `" + t.Text + "`"
	case STRING, STRHEAD:
		return "a string"
	case STRMID, STRTAIL:
		return "`}`"
	}
	return "`" + t.Kind.String() + "`"
}

// canEnd reports whether a line whose last token is k may end a statement
// there (DESIGN §4.1).
func canEnd(k Kind) bool {
	switch k {
	case IDENT, INT, FLOAT, STRING, STRTAIL, RPAREN, RBRACK, RBRACE, QUESTION,
		RETURN, BREAK, CONTINUE, SELF, SELFTYPE, TRUE, FALSE:
		return true
	}
	return false
}

// continues reports whether a line starting with k continues the line before
// (DESIGN §4.1): `.`, `&&`, `||`, `|`, `=>`, `else` and binary operators other
// than `-`.
func continues(k Kind) bool {
	switch k {
	case DOT, ANDAND, OROR, PIPE, FATARROW, ELSE,
		PLUS, STAR, SLASH, PERCENT, EQ, NE, LT, LE, GT, GE:
		return true
	}
	return false
}
