package syntax

import "fmt"

// The tables and messages of the Rust and Go habits that the checker finds
// with names and types, and the token pre-scan without them (DESIGN §7.2).
// Both use these, so that a habit reads the same whichever stage reports it.

// HabitTypes maps the Rust and Go names of Riddim's primitive types to
// Riddim's (R0110).
var HabitTypes = map[string]string{
	"i8": "Int", "i16": "Int", "i32": "Int", "i64": "Int", "i128": "Int", "isize": "Int",
	"u16": "Int", "u32": "Int", "u64": "Int", "u128": "Int", "usize": "Int",
	"int": "Int", "int8": "Int", "int16": "Int", "int32": "Int", "int64": "Int",
	"uint": "Int", "uint16": "Int", "uint32": "Int", "uint64": "Int", "uintptr": "Int", "rune": "Int",
	"u8": "Byte", "uint8": "Byte", "byte": "Byte",
	"f32": "Float", "f64": "Float", "float32": "Float", "float64": "Float",
	"bool": "Bool", "str": "String", "string": "String", "char": "String", "error": "Error",
}

// HabitTypeMsg returns the message and notes for a Rust or Go type name
// (R0110), and the Riddim type to write.
func HabitTypeMsg(name string) (to, msg string, notes []string) {
	to = HabitTypes[name]
	msg = fmt.Sprintf("`%s` is not a Riddim type: write `%s`", name, to)
	switch {
	case name == "char":
		notes = append(notes, "Riddim has no character type: a character is a String of one character")
	case to == "Int" && name != "int" && name != "rune":
		notes = append(notes, "Riddim has one integer type, Int, 64 bits and signed, and Byte for 0 to 255")
	}
	return to, msg, notes
}

// HabitMethods are Rust's methods that only borrow, copy or convert, which
// Riddim's values do not need: R0117 for iterators, R0118 for copies, R0119
// for owned strings.
var HabitMethods = map[string]string{
	"iter": "R0117", "into_iter": "R0117", "iter_mut": "R0117", "collect": "R0117",
	"clone": "R0118", "cloned": "R0118", "copied": "R0118", "to_vec": "R0118",
	"to_owned": "R0119", "as_str": "R0119",
}

// HabitMethodMsg returns the message for a call of one of HabitMethods.
func HabitMethodMsg(name string) string {
	switch HabitMethods[name] {
	case "R0117":
		return fmt.Sprintf("`.%s()` is not needed: `for` and the List and Map methods, such as map, filter and fold, work on the value itself", name)
	case "R0118":
		return fmt.Sprintf("`.%s()` is not needed: values are copied when they are bound, passed or returned", name)
	}
	return fmt.Sprintf("`.%s()` is not needed: a String is a value; use it as it is", name)
}

// The messages of the other habits that need names or types to tell.
const (
	HabitVecMsg        = "`%s` is Rust: Riddim's list type is `List`"
	HabitMapMsg        = "`%s` is Rust: Riddim's map type is `Map`, which keeps its keys in insertion order"
	HabitBoxMsg        = "Riddim has no pointers or references: hold the value itself; write `%s`"
	HabitBoxNote       = "enums may be recursive, and a struct may hold a List of itself"
	HabitVecNewMsg     = "Riddim writes an empty list as `[]`; annotate the binding if its type is needed, as in `let xs: List<Int> = []`"
	HabitMapNewMsg     = "Riddim's map is `Map`: make an empty one with `Map::new()`"
	HabitBoxNewMsg     = "Riddim has no pointers or references: use the value itself"
	HabitStringNewMsg  = "a String is a value: the empty string is `\"\"`"
	HabitStringFromMsg = "a String is a value: use the string itself, without `String::from`"
	HabitNilMsg        = "Riddim has no `nil`: a value that may be absent is an Option, and its empty case is `None`"
	HabitNilTestMsg    = "Riddim has no `nil`: test an Option with `.%s`"
	HabitErrNilMsg     = "Riddim has no `nil`: a function that can fail returns a Result; pass its error on with `?`, as in `let n = text.parse_int()?`, or match on the Result"
	HabitErrNilNote    = "`x, err := f()` followed by `if err != nil { return err }` is `let x = f()?`"
)
