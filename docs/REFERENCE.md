# Riddim reference (M2)

Riddim is a small, statically typed language that compiles to Go. It reads like a small Rust. This file is the whole language as implemented in milestone M2; anything not here is not in M2 (see "Not in M2" at the end). Diagnostic codes are explained in `docs/ERRORS.md`.

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
        let n = t.parse_int().map_err(|e| e.context("reading ${t}"))?
        out = out.push(n)
    }
    Ok(out)
}

fn main(sys: Sys) -> Result<(), Error> {
    let shapes = [Shape::Circle(1.0), Shape::Rect(2.0, 3.0), Shape::Empty]
    let total = shapes.fold(0.0, |acc, s| acc + s.area())
    sys.out.println("total: ${total}")
    let nums = parse_all(["1", "2", "39"])?
    sys.out.println("sum ${nums.sum()}, mean ${nums.sum() as Float / nums.len() as Float}")
    let mut counts: Map<String, Int> = Map::new()
    for w in "a b a".split(" ") {
        counts = counts.insert(w, counts.get(w).unwrap_or(0) + 1)
    }
    for (w, n) in counts {
        sys.out.println("${w}: ${n}")
    }
    Ok(())
}
```

## Lexical rules

- Comments run from `//` to the end of the line. A `///` line before a declaration, field or variant is its doc comment, carried into the generated Go.
- A newline ends a statement when the line's last token can end one: a name, a literal, `)`, `]`, `}`, `?`, `return`, `break`, `continue`, `self`, `Self`, `true`, `false`. Write `;` to put two statements on one line.
- Newlines inside `( )` and `[ ]` are ignored.
- A line starting with `.`, `&&`, `||`, `|`, `=>`, `else` or a binary operator other than `-` continues the line before (so method chains and `} else {` on a new line work), unless that line ended with `return`, `break` or `continue`.
- In braces holding a list (struct fields, enum variants, struct literals, match arms), a newline counts as a comma; a trailing comma is allowed.
- Keywords: `fn let mut const struct enum impl use go if else match for in while loop break continue return self Self as true false`.
- Names: letters, digits, `_`, not starting with a digit. Types, enum variants and type parameters are `UpperCamelCase`; functions, variables, fields and methods are `snake_case` (a leading `_` is allowed and silences the unused warning); constants are `SCREAMING_SNAKE_CASE`. Anything else is error R0203, with a rename fix.
- Literals: integers `42`, `1_000`, `0xff`; floats `3.14`, `1e9`, `2.5e-3` (digits on both sides of the dot); `true`, `false`; strings `"..."` on one line with escapes `\n \t \r \\ \" \$ \u{1F600}`.
- Interpolation: `"total: ${n}, ${xs.len()} items"`. The expression in `${}` is an Int, Float, Bool, Byte or String, written as `to_string` writes it; it may hold strings and braces, on the string's line. `\$` is a literal `$`, as is a `$` not followed by `{`.

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
| `Map<K, V>` | Immutable map, iterated in insertion order; `K` comparable |
| `Option<T>` | `Some(x)` or `None` |
| `Result<T, E>` | `Ok(x)` or `Err(e)` |
| `Error` | An error message, possibly wrapping another |
| `fn(A, B) -> R` | Function value; `fn(A)` returns `()` |
| `Never` | Type of `return`, `break`, `continue`, `panic(...)`, `todo()`; fits where any type is expected |
| `Sys`, `Out`, `Env`, `Fs`, `Clock` | Capabilities (see below) |
| structs, enums | Declared in the program, possibly generic: `Stack<Int>` |

- No nil, no pointers or references, no implicit conversions. `as` converts between Int, Float and Byte only: `n as Float`; `f as Int` truncates toward zero and saturates at Int's ends (NaN gives 0); `f as Byte` saturates at 0 and 255; `n as Byte` keeps the low 8 bits. Mixing Int and Float is R0301, whose fix adds `as Float`.
- An integer literal stands for a `Float` or `Byte` where one is expected, including next to a Float or Byte operand: `let x: Float = 3`, `2 * radius`, `1 / 2.0`.
- Type arguments appear only in types: `List<Int>`. Calls never take them; they are inferred.
- Inference is local and forward. A `let` gets its type from its value and annotation, never from later lines: `let xs = []` is R0306; write `let xs: List<Int> = []`, `let m: Map<String, Int> = Map::new()`. Generic calls get their type arguments from the arguments and the expected type; closure arguments are checked last, with what the other arguments fixed.
- Constant expressions (literals, constants and operators on them) are checked when compiling: overflow and division by a constant zero are R0308.

## Declarations

```
const MAX_RETRIES: Int = 3          // Int, Float, Bool, String or Byte; literals and constants with operators
const LIMIT: Int = MAX_RETRIES * 10

/// A point in the plane.
struct Point {
    /// Metres east of the origin.
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
- Type parameters are unconstrained: a value of type `T` can be passed, stored, returned and matched with `_` or a name. It has no methods and no `==`, so it cannot be a Map key.
- A struct may not contain itself by value, also not through `Option`, `Result`, `Map` or a tuple (R0307). Enums may be recursive, and a struct may hold a `List` of itself.
- Built-in names (`Int`, `Map`, `Option`, `Ok`, `error`, ...) cannot be declared.
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
let m = Map::new().insert("a", 1)      // Map<String, Int>
```

- Field init shorthand `Point { x, y }` is not supported: write `Point { x: x, y: y }`.
- Every value behaves as if copied when bound, passed, returned or captured. Nothing can be changed through a second name.
- `==` and `!=` work on Int, Float, Bool, String, Byte, `()`, and tuples, structs, enums, Option and Result made of them. Not on List, Map, functions, Error or capabilities (R0305).
- `<`, `<=`, `>`, `>=` work on Int, Float, Byte and String. Comparisons do not chain: write `a < b && b < c`.

## Statements and expressions

- `let x = e`, `let x: T = e`, `let (a, b) = pair`, `let _ = e`. Bindings are immutable; shadowing is allowed (`let x = x + 1`).
- `let mut x = e` may be reassigned: `x = e`, `x += e`, `-=`, `*=`, `/=`. Fields of a `let mut` struct may be assigned: `p.x = 1.0`, `line.to.y += 1.0`. List elements cannot be assigned: `xs = xs.set(i, v)`.
- Blocks are expressions: their value is their last expression. `if`, `match` and blocks may be used as values: `let s = if n > 0 { "pos" } else { "neg" }`. `if` without `else` has type `()`. Braces are required.
- `if let p = e { ... } else { ... }` is `match e { p => ..., _ => ... }`: the pattern may fail to match, and its names are bound in the first block only. `else if let` chains; without `else` it has type `()`.
- A function or closure that returns `()` must end with a value of type `()` (a call that returns nothing, a loop, `if` without `else`, or a statement): `fn greet(n: String) { "hi " + n }` is an error; write `-> String`, or `let _ = e` to drop a value. Elsewhere (loop bodies, `if` without `else`, statements), a block's last expression is dropped like any statement.
- An expression statement of type `Result` must be used: `?`, `match`, or `let _ = e` (R0502).
- Loops: `while cond { }`, `loop { }` (has type `Never` if no `break` leaves it), `for x in list { }`, `for (k, v) in map { }` (keys in insertion order), `for i in 0..n { }` (excludes n), `for i in 1..=n { }` (includes n). The pattern of `for` is a name, `_` or a tuple. Ranges appear only in `for`. `break` and `continue` work in all three loops but not across a closure.
- Operators, highest precedence first: unary `-` `!`; `as`; `*` `/` `%`; `+` `-`; comparisons; `&&`; `||`. `+` also joins strings. `&&` and `||` short-circuit. No bitwise operators. `%` works on Float too.
- `panic(msg)` and `todo()` stop the program (exit status 2); `todo()` warns (W0003).
- Closures: `|x| x + 1`, `|a: Int, b: Int| a + b`, `|x: Int| -> Int { x * 2 }`, `|| 42`, `|(k, v)| k`. Parameter types may be left out when the expected function type gives them (as when passed to `map`). A closure captures values: it sees what a `let mut` held when the closure was made and cannot assign to it (R0501). Inside a closure, `return` and `?` leave the closure.
- Declared functions are values too: `xs.map(double)`, `[1.0, 2.0].map(Shape::Circle)`, `xs.map(Some)`, `log.fold(Map::new(), apply)`.

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
- `let`, `for` and closure parameters take only names, `_` and tuples of them (R0404); `if let` and `match` take any pattern.

## Errors: Result, Option and ?

- `e?` on a `Result<T, E>` gives the `T`, or returns the `Err` from the enclosing function, whose result must be `Result<_, E>` with the same `E` (R0504: convert with `.map_err(...)`).
- `e?` on an `Option<T>` gives the `T`, or returns `None` from a function returning `Option<_>`. In a function returning a Result, write `.ok_or(error("..."))?` (R0503).
- `error(msg)` makes an `Error`; `e.message()` is its text; `e.context(msg)` wraps it, and the wrapper's message is `"msg: inner message"`.
- `unwrap()` and `expect(msg)` panic on `None`/`Err` and warn (W0002). Prefer `?`, `match`, `if let` or `unwrap_or`.

## Built-in methods

| On | Methods |
|---|---|
| `Int`, `Float`, `Bool`, `Byte` | `to_string()`. A Float prints the shortest text that reads back as the same value: `6`, `0.1`, `1e+21`, `0.30000000000000004`, `NaN`, `+Inf` |
| `Int` | `abs()`, `min(o)`, `max(o)` |
| `Float` | `abs()`, `floor()`, `ceil()`, `round()` (half away from zero), `sqrt()` |
| `String` | `len()` (bytes), `is_empty()`, `contains(s)`, `starts_with(s)`, `ends_with(s)`, `find(s) -> Option<Int>` (byte offset), `split(sep) -> List<String>`, `lines() -> List<String>` (without `\n` or `\r\n`), `trim()`, `to_upper()`, `to_lower()`, `replace(old, new)` (every one), `repeat(n)`, `slice(start, end)` (byte offsets, end excluded; panics out of range or inside a character), `parse_int() -> Result<Int, Error>` (decimal, optional sign), `parse_float() -> Result<Float, Error>`, `to_string()` |
| `List<T>` | `len()`, `is_empty()`, `get(i) -> Option<T>`, `xs[i]` (panics out of range), `first()`, `last()` (Options), `push(x)`, `set(i, x)` (panics out of range), `concat(ys)`, `slice(start, end)` (panics out of range), `reverse()`, `map(f)`, `filter(f)`, `fold(init, f)` with `f(acc, x)`, `any(f)`, `all(f)`, `find(f) -> Option<T>`, `enumerate() -> List<(Int, T)>`. For comparable `T`: `contains(x)`, `index_of(x) -> Option<Int>`. For Int, Float and String: `sort()`, `min()`, `max()` (Options). For Int and Float: `sum()`. For `List<String>`: `join(sep)` |
| `Map<K, V>` | `Map::new()`, `len()`, `is_empty()`, `get(k) -> Option<V>`, `contains_key(k)`, `insert(k, v)` (a new key goes last, an existing one keeps its place), `remove(k)`, `keys() -> List<K>`, `values() -> List<V>`, `entries() -> List<(K, V)>` |
| `Option<T>` | `is_some()`, `is_none()`, `unwrap_or(d)`, `map(f)`, `and_then(f)` (f returns an Option), `ok_or(e) -> Result<T, E>`, `unwrap()`, `expect(msg)` |
| `Result<T, E>` | `is_ok()`, `is_err()`, `unwrap_or(d)`, `map(f)`, `map_err(f)`, `and_then(f)` (f returns a Result with the same E), `ok() -> Option<T>`, `unwrap()`, `expect(msg)` |
| `Error` | `message()`, `context(msg)` |
| `Out` | `print(s)`, `println(s)` |
| Free functions | `error(msg) -> Error`, `panic(msg)`, `todo()` |

Lists and maps are immutable: `push`, `set`, `insert` and `remove` return a new value. Grow one in a loop with `let mut xs: List<Int> = []` and `xs = xs.push(x)`.

## Capabilities

Code affects the world outside the program only through a capability it was handed. `main` receives the root capability, `Sys`:

```
fn main(sys: Sys) -> Result<(), Error> {
    let text = load(sys.fs, "app.conf")?
    sys.out.println("loaded ${text.len()} bytes")
    sys.err.println("this goes to standard error")
    Ok(())
}

fn load(fs: Fs, path: String) -> Result<String, Error> {
    fs.read(path).map_err(|e| e.context("reading ${path}"))
}
```

| Capability | From `Sys` | Methods |
|---|---|---|
| `Out` | `sys.out`, `sys.err` | `print(s)`, `println(s)` |
| `Env` | `sys.env` | `get(name) -> Option<String>`, `args() -> List<String>` (without the program name) |
| `Fs` | `sys.fs` | `read(path) -> Result<String, Error>` (UTF-8 text), `write(path, text) -> Result<(), Error>` (creates or replaces), `exists(path) -> Bool`, `list(dir) -> Result<List<String>, Error>` (sorted names) |
| `Clock` | `sys.clock` | `now_ms() -> Int` (Unix time in milliseconds) |

- Capabilities can be passed, stored in structs and lists, captured by closures and returned, but never made: there is no literal or constructor (R0603). Passing one delegates it.
- Relative paths are relative to the directory the program runs in. Errors read like `cannot read app.conf: no such file or directory`.
- `main` takes `sys: Sys` or nothing, and returns nothing or `Result<(), Error>` (R0604). An `Err` from `main` prints `error: <message>` to standard error and exits with status 1. A panic exits with status 2.
- `Net` and `Proc` (network and processes) come in v0.2 (R0106).

## Warnings

W0001 unused binding (the fix replaces it with `_`; names starting with `_` are exempt), W0002 `unwrap`/`expect`, W0003 `todo()`. Warnings do not stop a build.

## Differences from Rust

Each Rust or Go habit below has its own error code (R0108 to R0125) and, where the rewrite is mechanical, a fix. Habits do not stop the compiler: one check reports them all.

| Rust | Riddim |
|---|---|
| `;` ends statements; `{ x; }` has type `()` | A newline ends a statement; `;` means the same as a newline. A block's value is its last expression |
| `i32`, `u64`, `usize`, `u8`, `f64`, `bool`, `char` | `Int`, `Byte`, `Float`, `Bool`; a character is a `String` |
| `&str`, `String::from("a")`, `"a".to_owned()`, `String::new()` | `String`, `"a"`, `""` |
| `Vec<T>`, `vec![1, 2]`, `Vec::new()`, `[T; N]` | `List<T>`, `[1, 2]`, `[]`; `push` returns a new list |
| `HashMap<K, V>`, `HashMap::new()`, `m.insert(k, v);` | `Map<K, V>`, `Map::new()`, `m = m.insert(k, v)`; iterates in insertion order |
| `&x`, `&mut x`, `*x`, `&self`, `Box<T>`, lifetimes `'a` | None: values are copied; `self` by value |
| `.clone()`, `.iter()`, `.into_iter()`, `.collect()` | Not needed: list and map methods work directly, `xs.map(f).filter(g)` |
| `println!("{} {x}", a)`, `format!`, `panic!`, `todo!`, `assert!` | `sys.out.println("${a} ${x}")`, `"${a}"`, `panic(msg)`, `todo()`, `if !c { panic(msg) }` |
| `x as f64` | `x as Float`; only between Int, Float and Byte |
| `foo::<T>()`, `.collect::<Vec<_>>()` | Never: annotate the binding instead |
| `if let Some(x) = o { }`, `while let` | `if let` as in Rust; no `while let`: `loop` with `match` |
| `'outer: loop { break 'outer }` | No labels: move the loops into a function and `return` |
| `Shape::Circle(r)` in a pattern | Also `Circle(r)` |
| `Point { x, y }` shorthand | `Point { x: x, y: y }` |
| `impl Stack<i32> { }` | Only `impl<T> Stack<T> { }` |
| `pub`, `#[derive(...)]` | Not needed: one package; `==` works on every comparable type |
| Traits, modules, macros | Not in v0 |
| `&mut self` methods | `self` by value; return the new value |
| Go's `var x = 1`, `x := 1`, `x++` | `let mut x = 1`, `let x = 1`, `x += 1` |
| Go's `nil`, `if err != nil { return err }` | `None`; `?` on the call that can fail |
| Go's `func`, `for i := 0; i < n; i++`, `for cond`, `for {` | `fn`, `for i in 0..n`, `while cond`, `loop` |
| Go's `[]T`, `map[K]V`, `*T`, `for i, x := range xs` | `List<T>`, `Map<K, V>`, `T`, `for (i, x) in xs.enumerate()` |
| Go's `package main`, `import "strings"`; Rust's `use std::...` | Nothing; `use go "strings"` (M3); nothing |

## Not in M2

These come in later milestones; using them gives R0106 or an unknown-name error: `use go "..."` and Go packages (milestone M3). Not in v0: traits, modules and `pub`, concurrency, macros, the `Net` and `Proc` capabilities.
