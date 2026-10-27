# Riddim diagnostics

Every diagnostic has a stable code, an exact span and, where it can, a fix that applies mechanically. `riddim check --json` prints them as data (DESIGN §7.1); `riddim check` prints the same as text. Codes starting with R are errors; codes starting with W are warnings, which do not stop a build.

The compiler stops after the first stage that reports errors: syntax (R01xx), then names and types (R02xx, R03xx, R05xx, R06xx, R0403, R0404, and the habits R0109, R0110, R0113, R0114 and R0117 to R0120 that need names or types to tell), then patterns (R0401, R0402). W0001 waits until there are no errors.

R0108 to R0125 are habits carried over from Rust or Go (DESIGN §7.2). Each has its own code and, where the rewrite is mechanical, a fix. Habits do not stop the stages: the compiler reads on as if each fix were made, so one round reports every habit. When a syntax error stops the parser, the habits the tokens alone show are still reported, each once.

This file covers milestone M2. Each entry shows the mistake, then the fix.

## R0101 unexpected character

A character Riddim does not use: `&` between two values (no bitwise operators), `'` (strings use double quotes), `#`, `@`, `^`, `~`, or `$` outside a string.

    let both = a & b       // mistake: no bitwise operators
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

The parser expected something else. The message says what it expected and what it found; after an error the parser moves on to the next top-level declaration. Go's `for {` and `for cond {` get a fix to `loop {` and `while cond {`.

    fn area(w, h) -> Int { w * h }
    fn area(w: Int, h: Int) -> Int { w * h }    // fix: parameters are annotated

## R0106 not supported in this version

The `Net` and `Proc` capabilities, for the network and other processes, and `sys.net` and `sys.proc`, come in v0.2.

    fn fetch(net: Net, url: String) -> String { "" }
    fn fetch(body: String) -> String { body }    // fix: take what it would fetch as a value

## R0107 break or continue outside a loop

`break` and `continue` work in `while`, `loop` and `for`, but not across a closure.

    xs.map(|x| { if x == 0 { break }; x })
    for x in xs { if x == 0 { break } }          // fix: use a loop

## R0108 Go's var or :=

Riddim declares a binding with `let`, and one that is reassigned with `let mut`. Go's `for i := 0; i < n; i++` is a range loop. Go's `n, err := f()` is `let n = f()?`, since a function that can fail returns a Result, and Go's `for i, x := range xs` is `for (i, x) in xs.enumerate()` or `for x in xs` (no fix for these two).

    var total = 0
    let mut total = 0                            // fix; `n := 5` becomes `let n = 5`

## R0109 nil

Riddim has no nil: a value that may be absent is an Option. The fix writes `None`, `Ok(())` where a `Result<(), E>` is expected, and `x.is_none()` for `x == nil` on an Option.

    return nil
    return None                                  // fix

## R0110 Rust or Go type name

Riddim's primitive types are `Int` (64-bit signed), `Float`, `Bool`, `String`, `Byte` and `Error`. The fix renames `i32`, `u64`, `usize`, `int`, `f64`, `float64`, `bool`, `str`, `string`, `u8`, `byte` and the like; `char` becomes String.

    fn area(w: i32, h: f64) -> f64
    fn area(w: Int, h: Float) -> Float                                            // fix

## R0111 reference or pointer

Riddim has no references or pointers: every value is copied when it is bound, passed or returned. The fix deletes `&`, `&mut` and `*`, writes `&str` as `String`, `&self` as `self`, and `Box<T>` or `Box::new(x)` as the value.

    fn len_of(s: &str) -> Int
    fn len_of(s: String) -> Int                  // fix

## R0112 lifetime or loop label

Nothing is borrowed, so there are no lifetimes; `break` and `continue` leave the innermost loop, so there are no labels. The fix deletes them.

    fn first<'a>(xs: &'a List<Int>) -> Int
    fn first(xs: &List<Int>) -> Int                                              // fix (then R0111)

## R0113 Vec, vec! or a Go slice

Riddim's list is `List<T>`, written `[1, 2]`; the empty list is `[]`. The fix rewrites `Vec<T>`, `vec![...]`, `Vec::new()`, Go's `[]T` and Rust's `[T; N]` and `[T]`. `vec![x; n]` has no fix: build the list in a loop.

    let xs: Vec<i32> = vec![1, 2]
    let xs: List<i32> = [1, 2]                                                   // fix (then R0110)

## R0114 HashMap or a Go map

Riddim's map is `Map<K, V>`, made with `Map::new()` and iterated in insertion order. The fix rewrites `HashMap`, `BTreeMap`, `HashMap::new()` and Go's `map[K]V`.

    let m: HashMap<String, Int> = HashMap::new()
    let m: Map<String, Int> = Map::new()                                         // fix

## R0115 macro

Riddim has no macros. The fix rewrites `println!`, `print!`, `eprintln!` and `eprint!` to an Out capability's `println` or `print` (from a `Sys` or `Out` parameter, else `sys.out`), `format!` to interpolation, `panic!` and `unreachable!` to `panic(...)`, and `todo!` and `unimplemented!` to `todo()`. A format spec such as `{:.2}`, and other macros, have no fix.

    println!("{} items, total {}", n, total)
    sys.out.println("${n} items, total ${total}")                                // fix

## R0116 type arguments on a call

Calls take no type arguments: Riddim infers them. The fix deletes `::<...>`; where inference needs help, annotate the binding.

    let n = identity::<Int>(5)
    let n = identity(5)                                                          // fix

## R0117 .iter(), .into_iter() or .collect()

`for` and the List and Map methods work on the value itself; there are no iterators. The fix deletes the call.

    let ys = xs.iter().map(|x| x * 2).collect()
    let ys = xs.map(|x| x * 2)                                                   // fix

## R0118 .clone()

Values are copied when they are bound, passed or returned, so nothing needs cloning. The fix deletes `.clone()`, `.cloned()`, `.copied()` and `.to_vec()`.

    let q = p.clone()
    let q = p                                                                    // fix

## R0119 String::from or .to_owned()

A String is a value. The fix writes the string itself for `String::from(s)`, `.to_owned()` and `.as_str()`, and `""` for `String::new()`.

    let s = String::from("hello")
    let s = "hello"                                                              // fix

## R0120 if err != nil

Riddim has no nil, and errors are not separate results: a function that can fail returns a Result. `?` returns its error from the enclosing function; `match` handles it. No fix: the call and the check are separate statements.

    if err != nil { return err }
    let n = text.parse_int()?                    // fix: `?` on the call that can fail

## R0121 pub

A program is one package, so every declaration is visible in all its files. The fix deletes `pub` and `pub(crate)`.

    pub fn area(self) -> Float
    fn area(self) -> Float                                                       // fix

## R0122 attribute

Riddim has no attributes: nothing needs deriving, since `==` works on every comparable type and values are always copied. The fix deletes `#[...]` and `#![...]`, with the line when nothing else is on it.

    #[derive(Debug, Clone, PartialEq)]
    struct Point { x: Int }
    struct Point { x: Int }                                                      // fix

## R0123 ++ or --

Riddim has no increment operators. The fix writes `+= 1` or `-= 1`.

    count++
    count += 1                                                                   // fix

## R0124 func

Riddim declares a function with `fn`. The fix renames `func`.

    func main() { }
    fn main() { }                                                                // fix

## R0125 Go's package or import, or Rust's use

A program is the `.rdm` files of one directory, and Riddim's own types need no import. The fix deletes Go's `package` clause and Rust's `use std::...;`, and writes Go's `import "strings"` as `use go "strings"`.

    import "strings"
    use go "strings"                                                             // fix

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

A value of one type where another is expected: an argument, an annotation, a branch, an operand, a call of something that is not a function, `as` between types other than Int, Float and Byte, or an interpolated value that is not an Int, Float, Bool, Byte or String. Riddim has no implicit conversions; an integer literal may stand for a Float or a Byte where one is expected. Where an Int or a Byte meets a Float, or a Byte an Int, the fix converts it with `as`.

    let count = 2
    let total = count * 1.5                      // mistake: count is an Int
    let total = count as Float * 1.5             // fix

## R0302 wrong number of arguments

A call, a variant, a pattern or a type has the wrong number of arguments or fields; an impl block must name its type with its own type parameters.

    let s = Shape::Rect(1.0)
    let s = Shape::Rect(1.0, 2.0)                // fix

## R0303 field error

A struct literal misses a field or names an unknown one, a field is given twice, or a value has no such field. Struct literals give every field or copy the rest with `..base`.

    let p = Point { x: 1 }
    let p = Point { x: 1, y: 0 }                 // fix

## R0304 method error

The type has no such method, an associated function is called on a value, an impl block names a built-in type, or a list method is used on elements it does not work on: `sort`, `min` and `max` need Int, Float or String, `sum` Int or Float, `join` String (its fix maps the elements with `to_string`).

    let p = q.origin()                           // origin has no self
    let p = Point::origin()                      // fix

## R0305 not comparable

`==` and `!=` work on Int, Float, Bool, String, Byte and on tuples, structs, enums, Option and Result made of them; not on List, Map, functions, Error, capabilities or type parameters. `<`, `<=`, `>`, `>=` work on Int, Float, Byte and String. A Map's keys, and the elements `contains` and `index_of` look for, must be comparable too.

    if xs == ys { }
    if xs.len() == ys.len() { }                  // fix

## R0306 type annotation needed

Inference flows forward: a `let` gets its type from its value and annotation, never from later lines. A closure parameter needs a type unless the call it is passed to gives one. `Map::new()` needs its key and value types: `let m: Map<String, Int> = Map::new()`.

    let xs = []
    let xs: List<Int> = []                       // fix

## R0307 recursive struct

A struct cannot contain itself by value, directly or through Option, Result, Map or a tuple. Use an enum, which may be recursive, or a List.

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

There is no literal or constructor for Sys, Out, Env, Fs or Clock, and their names are not values. `main` receives `sys: Sys`, whose fields are `out`, `err`, `env`, `fs` and `clock`; pass on what a function needs.

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
