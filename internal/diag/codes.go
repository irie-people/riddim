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

	// The Rust and Go habits of DESIGN §7.2, each with its own code.
	"R0108": "Go's var or :=",
	"R0109": "nil",
	"R0110": "Rust or Go type name",
	"R0111": "reference or pointer",
	"R0112": "lifetime or loop label",
	"R0113": "Vec, vec! or a Go slice",
	"R0114": "HashMap or a Go map",
	"R0115": "macro",
	"R0116": "type arguments on a call",
	"R0117": ".iter(), .into_iter() or .collect()",
	"R0118": ".clone()",
	"R0119": "String::from or .to_owned()",
	"R0120": "if err != nil",
	"R0121": "pub",
	"R0122": "attribute",
	"R0123": "++ or --",
	"R0124": "func",
	"R0125": "Go's package or import, or Rust's use",

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

	"R0601": "Go package not allowed",
	"R0602": "Go function or constant without a Riddim type",
	"R0603": "capabilities cannot be made",
	"R0604": "main is missing or has the wrong signature",

	"R0900": "generated Go failed to compile",

	"W0001": "unused binding",
	"W0002": "unwrap or expect",
	"W0003": "todo",
}

// IsHabit reports whether code is one of the Rust and Go habits (DESIGN
// §7.2), R0108 to R0125. The compiler reads a program as if each habit's fix
// were made, so a habit does not stop the stages after the one that found it.
func IsHabit(code string) bool {
	return len(code) == 5 && code >= "R0108" && code <= "R0125"
}
