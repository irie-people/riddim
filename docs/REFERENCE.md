# Riddim reference (M1)

Riddim is a small, statically typed language that compiles to Go. It reads like a small Rust. This file is the whole language as implemented in milestone M1; anything not here is not in M1 (see "Not in M1" at the end). Diagnostic codes are explained in `docs/ERRORS.md`.

## Programs and commands

A program is one directory of `.rdm` files forming one package. Top-level items may come in any order and any file. Exactly one file defines `fn main`.

```
riddim check [dir] [--json]   check; exit 0 (warnings allowed), 1 on errors, 2 on usage errors
riddim build [dir] [-o out]   check, write Go into dir/.riddim/go, go build (default output ./<dir name>)
riddim run [dir] [-- args]    build and run; exits with the program's status
riddim emit [dir] [-o gen]    write the generated Go and stop
riddim version
```

`riddim check --json` prints a JSON array of diagnostics (`[]` when clean); each has `code`, `severity`, `message`, `file`, `line`, `col`, `end_line`, `end_col` (exclusive), `notes` and `fixes` (each fix a `title` and `edits` to apply).

## A complete program

```
/// Shapes and their areas.
enum Shape {
    Circle(radius: Float),
    Rect(w: Float, h: Float),
    Empty,
}

impl Shape {
    fn area(self) -> Float {
        match self {
            Circle(r) => 3.14159 * r * r,
            Rect(w, h) => w * h,
            Empty => 0.0,
        }
    }
}

fn parse_all(texts: List<String>) -> Result<List<Int>, Error> {
    let mut out: List<Int> = []
    for t in texts {
        let n = t.parse_int().map_err(|e| e.context("reading " + t))?
        out = out.push(n)
    }
    Ok(out)
}

fn main(sys: Sys) -> Result<(), Error> {
    let shapes = [Shape::Circle(1.0), Shape::Rect(2.0, 3.0), Shape::Empty]
    let total = shapes.fold(0.0, |acc, s| acc + s.area())
    sys.out.println("total: " + total.to_string())
    let nums = parse_all(["1", "2", "39"])?
    sys.out.println(nums.fold(0, |a, n| a + n).to_string())
    Ok(())
}
```

## Lexical rules

- Comments run from `//` to the end of the line; `///` is a doc comment (ignored in M1).
- A newline ends a statement when the line's last token can end one: a name, a literal, `)`, `]`, `}`, `?`, `return`, `break`, `continue`, `self`, `Self`, `true`, `false`. Write `;` to put two statements on one line.
- Newlines inside `( )` and `[ ]` are ignored.
- A line starting with `.`, `&&`, `||`, `|`, `=>`, `else` or a binary operator other than `-` continues the line before (so method chains and `} else {` on a new line work), unless that line ended with `return`, `break` or `continue`.
- In braces holding a list (struct fields, enum variants, struct literals, match arms), a newline counts as a comma; a trailing comma is allowed.
- Keywords: `fn let mut const struct enum impl use go if else match for in while loop break continue return self Self as true false`.
- Names: letters, digits, `_`, not starting with a digit. Types, enum variants and type parameters are `UpperCamelCase`; functions, variables, fields and methods are `snake_case` (a leading `_` is allowed and silences the unused warning); constants are `SCREAMING_SNAKE_CASE`. Anything else is error R0203, with a rename fix.
- Literals: integers `42`, `1_000`, `0xff`; floats `3.14`, `1e9`, `2.5e-3` (digits on both sides of the dot); strings `"..."` on one line with escapes `\n \t \r \\ \" \$ \u{1F600}`; `true`, `false`.

## Types

| Type | Meaning |
|---|---|
| `Int` | 64-bit signed. Wraps on overflow at run time; division by zero panics |
| `Float` | 64-bit IEEE 754 |
| `Bool` | `true`, `false` |
| `String` | Immutable UTF-8 text |
| `Byte` | 0 to 255; wraps |
| `()` | Unit, the type of `()`. A function without `->` returns it |
| `(A, B, ...)` | Tuple of two or more fields `.0`, `.1`, ... |
| `List<T>` | Immutable list |
| `Option<T>` | `Some(x)` or `None` |
| `Result<T, E>` | `Ok(x)` or `Err(e)` |
| `Error` | An error message, possibly wrapping another |
| `fn(A, B) -> R` | Function value; `fn(A)` returns `()` |
| `Never` | Type of `return`, `break`, `continue`, `panic(...)`, `todo()`; fits where any type is expected |
| `Sys`, `Out` | Capabilities (see below) |
| structs, enums | Declared in the program, possibly generic: `Stack<Int>` |

- No nil, no pointers or references, no implicit conversions. Mixing `Int` and `Float` is an error (R0301).
- An integer literal stands for a `Float` or `Byte` where one is expected, including next to a Float or Byte operand: `let x: Float = 3`, `2 * radius`, `1 / 2.0`.
- Type arguments appear only in types: `List<Int>`. Calls never take them; they are inferred.
- Inference is local and forward. A `let` gets its type from its value and annotation, never from later lines: `let xs = []` is R0306; write `let xs: List<Int> = []`. Generic calls get their type arguments from the arguments and the expected type; closure arguments are checked last, with what the other arguments fixed.
- Constant expressions (literals, constants and operators on them) are checked when compiling: overflow and division by a constant zero are R0308.

## Declarations

```
const MAX_RETRIES: Int = 3          // Int, Float, Bool, String or Byte; literals and constants with operators
const LIMIT: Int = MAX_RETRIES * 10

struct Point {
    x: Float,
    y: Float,
}

struct Stack<T> {
    items: List<T>,
}

enum Color { Red, Green, Blue }

enum Tree<T> {
    Leaf,
    Node(Tree<T>, T, Tree<T>),      // fields all unnamed ...
}

enum Shape {
    Circle(radius: Float),          // ... or all named
    Empty,
}

impl<T> Stack<T> {
    fn empty() -> Stack<T> {                  // associated function: Stack::empty()
        Stack { items: [] }
    }
    fn push(self, x: T) -> Stack<T> {         // method: s.push(x)
        Stack { items: self.items.push(x) }
    }
    fn map<U>(self, f: fn(T) -> U) -> Stack<U> {
        Stack { items: self.items.map(f) }
    }
}

fn largest(xs: List<Int>) -> Option<Int> {
    xs.fold(None, |best, x| match best {
        Some(b) if b >= x => Some(b),
        _ => Some(x),
    })
}
```

- Parameters and results are always annotated; bodies are inferred.
- A function's body is a block whose last expression is its result; `return e` returns early.
- `impl` adds methods to a struct or enum of the program. It names the type with its own type parameters, in order: `impl<T> Stack<T>`, never `impl Stack<Int>`. There may be several impl blocks for a type.
- A method takes `self` first: by value and immutable. A method that "changes" a value returns a new one. A function in an impl block without `self` is an associated function, called `Type::name(...)`. Inside an impl, `Self` names the type: `Self { ... }`, `Self::empty()`.
- A struct's fields and methods must have different names.
- Type parameters are unconstrained: a value of type `T` can be passed, stored, returned and matched with `_` or a name. It has no methods and no `==`.
- A struct may not contain itself by value, also not through `Option`, `Result` or a tuple (R0307). Enums may be recursive, and a struct may hold a `List` of itself.
- Built-in names (`Int`, `Option`, `Ok`, `error`, ...) cannot be declared.
- No tuple structs (`struct P(Int)`), no struct-like variants (`V { x: Int }`), no nested items.

## Values

```
let p = Point { x: 1.0, y: 2.0 }       // give every field
let q = Point { x: 0.0, ..p }          // or copy the rest from a base
let x = p.x
let t = (1, "one")
let n = t.0
let s = Shape::Circle(2.0)             // variants are written with their enum
let e = Shape::Empty
let o = Some(3)                        // Some, None, Ok, Err need no prefix
let r: Result<Int, String> = Err("bad")
```

- Field init shorthand `Point { x, y }` is not supported: write `Point { x: x, y: y }`.
- Every value behaves as if copied when bound, passed, returned or captured. Nothing can be changed through a second name.
- `==` and `!=` work on Int, Float, Bool, String, Byte, `()`, and tuples, structs, enums, Option and Result made of them. Not on List, functions, Error or capabilities (R0305).
- `<`, `<=`, `>`, `>=` work on Int, Float, Byte and String. Comparisons do not chain: write `a < b && b < c`.

## Statements and expressions

- `let x = e`, `let x: T = e`, `let (a, b) = pair`, `let _ = e`. Bindings are immutable; shadowing is allowed (`let x = x + 1`).
- `let mut x = e` may be reassigned: `x = e`, `x += e`, `-=`, `*=`, `/=`. Fields of a `let mut` struct may be assigned: `p.x = 1.0`, `line.to.y += 1.0`. List elements cannot be assigned.
- Blocks are expressions: their value is their last expression. `if`, `match` and blocks may be used as values: `let s = if n > 0 { "pos" } else { "neg" }`. `if` without `else` has type `()`. Braces are required.
- When a block's value is not used, its last expression is dropped like any statement.
- An expression statement of type `Result` must be used: `?`, `match`, or `let _ = e` (R0502).
- Loops: `while cond { }`, `loop { }` (has type `Never` if no `break` leaves it), `for x in list { }`, `for i in 0..n { }` (excludes n), `for i in 1..=n { }` (includes n). The pattern of `for` is a name, `_` or a tuple: `for (k, v) in pairs`. Ranges appear only in `for`. `break` and `continue` work in all three loops but not across a closure.
- Operators, highest precedence first: unary `-` `!`; `*` `/` `%`; `+` `-`; comparisons; `&&`; `||`. `+` also joins strings. `&&` and `||` short-circuit. No bitwise operators. `%` works on Float too.
- `panic(msg)` and `todo()` stop the program (exit status 2); `todo()` warns (W0003).
- Closures: `|x| x + 1`, `|a: Int, b: Int| a + b`, `|x: Int| -> Int { x * 2 }`, `|| 42`, `|(k, v)| k`. Parameter types may be left out when the expected function type gives them (as when passed to `map`). A closure captures values: it sees what a `let mut` held when the closure was made and cannot assign to it (R0501). Inside a closure, `return` and `?` leave the closure.
- Declared functions are values too: `xs.map(double)`, `[1.0, 2.0].map(Shape::Circle)`, `xs.map(Some)`.

## Patterns and match

```
match result {
    Ok(Some(n)) if n > 0 => "positive",
    Ok(Some(_)) => "not positive",
    Ok(None) => "missing",
    Err(e) => e.message(),
}
```

- Patterns: `_`; a snake_case name binding the value; Int (also negative), String and Bool literals; tuples `(p, q)`; variants with sub-patterns, bare (`Circle(r)`) or qualified (`Shape::Circle(r)`); `Some(p)`, `None`, `Ok(p)`, `Err(p)`; or-patterns `p | q`, whose sides bind the same names with the same types (R0403).
- A variant pattern gives all fields positionally: `Rect(w, _)`; a variant without fields has no parentheses: `Empty`.
- No Float or struct patterns: bind the value and use a guard: `x if x > 1.5 => ...`.
- Guards: `pattern if cond => body`. A guarded arm does not count toward exhaustiveness.
- `match` must be exhaustive. R0401 lists the missing cases as patterns and offers a fix that adds `pattern => todo(),` arms. An arm that can never match is R0402.
- Arms are separated by `,` or a newline; a block body `=> { ... }` needs neither.
- `let`, `for` and closure parameters take only names, `_` and tuples of them (R0404).

## Errors: Result, Option and ?

- `e?` on a `Result<T, E>` gives the `T`, or returns the `Err` from the enclosing function, whose result must be `Result<_, E>` with the same `E` (R0504: convert with `.map_err(...)`).
- `e?` on an `Option<T>` gives the `T`, or returns `None` from a function returning `Option<_>`. In a function returning a Result, write `.ok_or(error("..."))?` (R0503).
- `error(msg)` makes an `Error`; `e.message()` is its text; `e.context(msg)` wraps it, and the wrapper's message is `"msg: inner message"`.
- `unwrap()` and `expect(msg)` panic on `None`/`Err` and warn (W0002). Prefer `?`, `match` or `unwrap_or`.

## Built-in methods

| On | Methods |
|---|---|
| `Int`, `Float`, `Bool`, `Byte` | `to_string()`. A Float prints the shortest text that reads back as the same value: `6`, `0.1`, `1e+21`, `0.30000000000000004` |
| `String` | `to_string()`, `parse_int() -> Result<Int, Error>` (decimal digits with an optional sign; errors read `cannot parse "x" as Int`) |
| `List<T>` | `len()`, `get(i) -> Option<T>`, `xs[i]` (panics out of range), `push(x) -> List<T>` (a new list), `map(f) -> List<U>`, `filter(f) -> List<T>`, `fold(init, f)` with `f(acc, x)` |
| `Option<T>` | `is_some()`, `is_none()`, `unwrap_or(d)`, `map(f)`, `and_then(f)` (f returns an Option), `ok_or(e) -> Result<T, E>`, `unwrap()`, `expect(msg)` |
| `Result<T, E>` | `is_ok()`, `is_err()`, `unwrap_or(d)`, `map(f)`, `map_err(f)`, `and_then(f)` (f returns a Result with the same E), `ok() -> Option<T>`, `unwrap()`, `expect(msg)` |
| `Error` | `message()`, `context(msg)` |
| `Out` | `print(s)`, `println(s)` |
| Free functions | `error(msg) -> Error`, `panic(msg)`, `todo()` |

Lists are immutable: `push` returns a new list. Grow a list in a loop with `let mut xs: List<Int> = []` and `xs = xs.push(x)`.

## Capabilities

Code affects the world outside the program only through a capability it was handed. `main` receives the root capability:

```
fn main(sys: Sys) {
    greet(sys.out, "world")
    sys.err.println("this goes to standard error")
}

fn greet(out: Out, name: String) {
    out.println("hello, " + name)
}
```

- M1 has `Sys` with the fields `out` and `err`, both `Out` (standard output and standard error).
- Capabilities can be passed, stored and returned, but never made: there is no literal or constructor (R0603).
- `main` takes `sys: Sys` or nothing, and returns nothing or `Result<(), Error>` (R0604). An `Err` from `main` prints `error: <message>` to standard error and exits with status 1. A panic exits with status 2.

## Warnings

W0001 unused binding (the fix replaces it with `_`; names starting with `_` are exempt), W0002 `unwrap`/`expect`, W0003 `todo()`. Warnings do not stop a build.

## Differences from Rust

| Rust | Riddim |
|---|---|
| `;` ends statements; `{ x; }` has type `()` | A newline ends a statement; `;` means the same as a newline. A block's value is its last expression |
| `i32`, `u8`, `f64`, `usize`, `&str`, `String` | `Int`, `Byte`, `Float`, `String` |
| `Vec<T>`, `vec![1, 2]` | `List<T>`, `[1, 2]`; `push` returns a new list |
| `&`, `&mut`, `*`, lifetimes, `.clone()` | None: values are copied |
| `.iter()`, `.into_iter()`, `.collect()` | List methods work directly: `xs.map(f).filter(g)` |
| `println!("{}", x)`, `format!` | `sys.out.println("n = " + x.to_string())` |
| `panic!`, `todo!` | `panic(msg)`, `todo()` |
| `x as f64` | Not in M1 |
| `if let Some(x) = o { }` | `match o { Some(x) => ..., None => ... }` |
| `foo::<T>()` | Never: annotate the binding instead |
| `Shape::Circle(r)` in a pattern | Also `Circle(r)` |
| `Point { x, y }` shorthand | `Point { x: x, y: y }` |
| `impl Stack<i32> { }` | Only `impl<T> Stack<T> { }` |
| Traits, `pub`, modules, macros | Not in v0 |
| `&mut self` methods | `self` by value; return the new value |
| `HashMap` | Not in M1 |

## Not in M1

These come in later milestones; using them gives R0106 or an unknown-name error: string interpolation `"${x}"`, `as` conversions, `if let`, `Map`, `use go "..."` and Go packages, the `Env`, `Fs` and `Clock` capabilities, and these methods: on String `len`, `is_empty`, `contains`, `starts_with`, `ends_with`, `find`, `split`, `lines`, `trim`, `to_upper`, `to_lower`, `replace`, `repeat`, `slice`, `parse_float`; on Int `abs`, `min`, `max`; on Float `abs`, `floor`, `ceil`, `round`, `sqrt`; on List `is_empty`, `first`, `last`, `set`, `concat`, `slice`, `reverse`, `any`, `all`, `find`, `enumerate`, `contains`, `index_of`, `sort`, `min`, `max`, `sum`, `join`.
