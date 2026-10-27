# Riddim diagnostics

Every diagnostic has a stable code, an exact span and, where it can, a fix that applies mechanically. `riddim check --json` prints them as data (DESIGN §7.1); `riddim check` prints the same as text. Codes starting with R are errors; codes starting with W are warnings, which do not stop a build.

The compiler stops after the first stage that reports errors: syntax (R01xx), then names and types (R02xx, R03xx, R05xx, R06xx, R0403, R0404), then patterns (R0401, R0402). W0001 waits until there are no errors.

This file covers milestone M1. Each entry shows the mistake, then the fix.

## R0101 unexpected character

A character Riddim does not use: `&`, `'`, `#`, `@`, `^`, `~`, or `$` outside a string.

    let both = a & b       // mistake: no bitwise operators or references
    let both = a && b      // fix

## R0102 unterminated string

A string must end on the line it starts.

    let s = "two
    let s = "two\nlines"   // fix: write \n for a newline

## R0103 invalid escape sequence

Strings know `\n \t \r \\ \" \$` and `\u{...}` with 1 to 6 hex digits.

    let s = "a\qb"
    let s = "a\\qb"        // fix

## R0104 malformed number

Digit separators go between digits; letters cannot follow a number.

    let n = 1__000
    let n = 1_000          // fix

## R0105 syntax error

The parser expected something else. The message says what it expected and what it found; after an error the parser moves on to the next top-level declaration.

    fn area(w, h) -> Int { w * h }
    fn area(w: Int, h: Int) -> Int { w * h }    // fix: parameters are annotated

## R0106 not supported in this version

String interpolation, `as`, `if let`, `use go`, and the `Map`, `Env`, `Fs` and `Clock` types come in later milestones.

    let s = "total: ${n}"
    let s = "total: " + n.to_string()           // fix

## R0107 break or continue outside a loop

`break` and `continue` work in `while`, `loop` and `for`, but not across a closure.

    xs.map(|x| { if x == 0 { break }; x })
    for x in xs { if x == 0 { break } }          // fix: use a loop

## R0201 unknown name

The name is not declared, or is a type used as a value. The message lists the closest known names. Enum variants are written with their enum in expressions.

    let c = Circle(1.0)
    let c = Shape::Circle(1.0)                   // fix

## R0202 duplicate name

A name declared twice in the same place: two top-level items, two fields, variants, parameters or type parameters, a field and a method of one struct, a name bound twice in one pattern, or a built-in name such as `Option`, `Ok` or `error`.

    struct Option { }
    struct Choice { }                            // fix

## R0203 naming style

Types, variants and type parameters are UpperCamelCase; functions, variables, fields and methods are snake_case; constants are SCREAMING_SNAKE_CASE. The fix renames the declaration and every use.

    fn getValue() -> Int { 1 }
    fn get_value() -> Int { 1 }                  // fix

## R0301 mismatched types

A value of one type where another is expected: an argument, an annotation, a branch, an operand, a call of something that is not a function. Riddim has no implicit conversions; an integer literal may stand for a Float or a Byte where one is expected.

    let count = 2
    let total = count * 1.5                      // mistake: count is an Int
    let count = 2.0                              // fix: make it a Float (`as` comes in a later version)

## R0302 wrong number of arguments

A call, a variant, a pattern or a type has the wrong number of arguments or fields; an impl block must name its type with its own type parameters.

    let s = Shape::Rect(1.0)
    let s = Shape::Rect(1.0, 2.0)                // fix

## R0303 field error

A struct literal misses a field or names an unknown one, a field is given twice, or a value has no such field. Struct literals give every field or copy the rest with `..base`.

    let p = Point { x: 1 }
    let p = Point { x: 1, y: 0 }                 // fix

## R0304 method error

The type has no such method, an associated function is called on a value, or an impl block names a built-in type.

    let p = q.origin()                           // origin has no self
    let p = Point::origin()                      // fix

## R0305 not comparable

`==` and `!=` work on Int, Float, Bool, String, Byte and on tuples, structs, enums, Option and Result made of them; not on List, functions, Error, capabilities or type parameters. `<`, `<=`, `>`, `>=` work on Int, Float, Byte and String.

    if xs == ys { }
    if xs.len() == ys.len() { }                  // fix

## R0306 type annotation needed

Inference flows forward: a `let` gets its type from its value and annotation, never from later lines. A closure parameter needs a type unless the call it is passed to gives one.

    let xs = []
    let xs: List<Int> = []                       // fix

## R0307 recursive struct

A struct cannot contain itself by value, directly or through Option, Result or a tuple. Use an enum, which may be recursive, or a List.

    struct Node { next: Option<Node> }
    enum Chain { End, Link(Int, Chain) }         // fix

## R0308 invalid constant

A constant expression overflows its type, divides by zero, or a `const` has a value that is not literals and constants combined with operators, or a type other than Int, Float, Bool, String or Byte. A Byte holds 0 to 255.

    let x = 10 / 0
    let x = 10 / d                               // fix: a divisor known only at run time panics if it is 0

## R0401 match is not exhaustive

The message lists the missing cases as patterns. The fix adds an arm for each with `todo()` as the body. A guarded arm does not count.

    match shape { Circle(r) => r }
    match shape { Circle(r) => r, Rect(_, _) => todo(), Empty => todo() }   // fix

## R0402 unreachable match arm

The arms before it already match every value this one matches.

    match n { _ => 0, 1 => 1 }
    match n { 1 => 1, _ => 0 }                   // fix: specific arms first

## R0403 or-pattern sides bind differently

Each side of `|` must bind the same names, with the same types.

    Circle(r) | Rect(w, _) => 1.0
    Circle(r) | Rect(r, _) => r                  // fix

## R0404 refutable pattern in let, for or closure parameter

These take names, `_` and tuples of them. Use `match` for anything that can fail to match.

    let Some(x) = find(xs)
    let x = match find(xs) { Some(x) => x, None => 0 }   // fix

## R0501 assignment to an immutable value

Only `let mut` bindings and their fields can be assigned. Parameters and `self` are immutable; closures capture values and cannot assign to outer bindings; list elements cannot be assigned.

    let n = 0
    let mut n = 0                                // fix

## R0502 unused Result

A Result used as a statement must be handled with `?` or `match`, or dropped on purpose with `let _ =`. The fixes add `?` (when the function returns a compatible Result) or `let _ = `.

    save(data)
    save(data)?                                  // fix

## R0503 ? on an Option in a function that returns a Result

Turn the Option into a Result first. The fix inserts `.ok_or(error("missing value"))`.

    let x = xs.get(0)?
    let x = xs.get(0).ok_or(error("empty list"))?   // fix

## R0504 ? with a different error type

`?` returns the error as it is, so the function's Result must have the same error type.

    let n = text.parse_int()?                    // in a function returning Result<Int, String>
    let n = text.parse_int().map_err(|e| e.message())?   // fix

## R0505 misplaced ?

`?` returns early, so the function must return a Result (for a Result) or an Option (for an Option). In a function that returns an Option, turn a Result into an Option with `.ok()?`.

    fn main() { let n = "1".parse_int()? }
    fn main() -> Result<(), Error> { let n = "1".parse_int()?; Ok(()) }   // fix

## R0603 capabilities cannot be made

There is no literal or constructor for Sys or Out. `main` receives `sys: Sys`; pass on what a function needs.

    let out = Out { }
    fn log(out: Out) { out.println("hi") }       // fix: take it as a parameter

## R0604 main is missing or has the wrong signature

A program has exactly one `fn main`, which takes `sys: Sys` or nothing and returns nothing or `Result<(), Error>`.

    fn main(args: List<String>) -> Int { 0 }
    fn main(sys: Sys) -> Result<(), Error> { Ok(()) }   // fix

## R0900 generated Go failed to compile

The Go that Riddim generated did not compile. This is a bug in Riddim, not in the program: please report it with the program that caused it. The position is the `.rdm` line the Go came from.

## W0001 unused binding

A binding that is never read. The fix replaces it with `_`; a name starting with `_` is not reported.

    let x = compute()
    let _ = compute()                            // fix

## W0002 unwrap or expect

`unwrap()` and `expect(msg)` panic on None or Err. Handle the case instead.

    let n = text.parse_int().unwrap()
    let n = text.parse_int()?                    // fix

## W0003 todo

`todo()` panics when it runs. Finish the code before shipping it.

    fn area(r: Rect) -> Float { todo() }
    fn area(r: Rect) -> Float { r.w * r.h }      // fix
