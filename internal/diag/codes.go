package diag

// Codes lists every diagnostic code the compiler emits, with a short title.
// docs/ERRORS.md documents each one, and a test checks that every code here
// has an entry there and an error test.
var Codes = map[string]string{
	"R0101": "unexpected character",
	"R0102": "unterminated string",
	"R0103": "invalid escape sequence",
	"R0104": "malformed number",
	"R0105": "syntax error",
	"R0106": "not supported in this version",
	"R0107": "break or continue outside a loop",

	"R0201": "unknown name",
	"R0202": "duplicate name",
	"R0203": "naming style",

	"R0301": "mismatched types",
	"R0302": "wrong number of arguments",
	"R0303": "field error",
	"R0304": "method error",
	"R0305": "not comparable",
	"R0306": "type annotation needed",
	"R0307": "recursive struct",
	"R0308": "invalid constant",

	"R0401": "match is not exhaustive",
	"R0402": "unreachable match arm",
	"R0403": "or-pattern sides bind differently",
	"R0404": "refutable pattern in let, for or closure parameter",

	"R0501": "assignment to an immutable value",
	"R0502": "unused Result",
	"R0503": "? on an Option in a function that returns a Result",
	"R0504": "? with a different error type",
	"R0505": "misplaced ?",

	"R0603": "capabilities cannot be made",
	"R0604": "main is missing or has the wrong signature",

	"R0900": "generated Go failed to compile",

	"W0001": "unused binding",
	"W0002": "unwrap or expect",
	"W0003": "todo",
}
