# Riddim v0: design and implementation plan

Date: 2026-10-27. Status: v0 plan. Read with [AGENTS.md](../AGENTS.md). Every source is listed in [PROVENANCE.md](../PROVENANCE.md).

Riddim is a small, statically typed language that agents write, a compiler checks and people review. It reads like a small Rust, compiles to readable Go, and calls the Go packages it is allowed to. Its first user is [Irie](https://github.com/irie-hq/irie), which runs many coding agents side by side on cloud and local models. Riddim exists to make their output cheap to verify, so that more of the work can go to small local models and more agents can run at once without a person reading every line.

**Glossary.**
- **Agent:** a coding model working through a harness (Claude Code, Codex CLI, opencode and others).
- **Capability:** a value that grants access to something outside the program: output, the environment, files, the clock; later the network and processes. Code can touch only what it was handed.
- **Diagnostic:** an error or warning from the compiler, with a stable code, an exact span and, where possible, a fix that applies mechanically.
- **Reference:** `docs/REFERENCE.md`, the whole language in one file small enough to put in every agent's prompt.
- **RB:** the Riddim Bench: the same tasks, written by the same models, in Go, Rust and Riddim (§9).

---

## 1. Goal

### 1.1 What "better than Go and Rust" means

Riddim runs on Go's runtime, so at run time it is as fast as Go and no faster. On tight CPU loops Rust stays ahead, and v0 does not try to change that. Riddim aims to beat both languages where a crew of agents spends its time and money: the tokens and fix-up rounds an agent needs to reach a correct program, the time a build takes, and the defects that get past the compiler to a reviewer. RB (§9) measures each target before anyone claims it.

| Measure | Go | Rust | Riddim target |
|---|---|---|---|
| Agent pass rate on RB, first try and after 3 compiler rounds | baseline | baseline | higher than both, for a local model and a cloud model |
| Tokens to a passing program (median) | baseline | baseline | lowest |
| Clean build of an RB task | 1× | several times Go | at most 1.5× Go |
| Run time on RB compute tasks | 1× | usually faster | within 1.1× of hand-written Go |
| Nil dereference, ignored error, missed case in a match | all possible | prevented | prevented |
| Data races | possible | prevented by the borrow checker | prevented by construction: values, not shared references (§4.8) |
| What a dependency can touch | anything | anything, plus `build.rs` at build time | only the capabilities it is handed (§5) |
| Concepts to learn before writing correct code | few | many (ownership, lifetimes, traits, async) | few: values, enums, `Result`, capabilities |

### 1.2 Principles

1. **Written by agents, reviewed by people.** Every rule is chosen for a model writing code and a person reading a diff. One way to write each thing, no hidden control flow, no macros, no operator overloading, no reflection.
2. **Familiar on purpose.** The syntax is close to Rust's, so models reuse what they already know. Every place Riddim differs from Rust is listed in one table in the reference.
3. **The whole language fits in one prompt.** `docs/REFERENCE.md` stays under 24 KB (about 6,000 tokens), a tenth of the 64k context Irie requires from a local model. A test enforces the size.
4. **The compiler is the first reviewer.** Diagnostics are data first (`riddim check --json`) and text second. Each has a stable code documented in `docs/ERRORS.md`, an exact span and, where it can, a fix that applies mechanically.
5. **No nil, no exceptions, no ignored errors, no ambient authority.** Absence is `Option`, failure is `Result`, and the outside world arrives as capabilities.
6. **Deterministic by default.** Same source and inputs, same output. Time, files and the environment come only through capabilities, so tests can hand in fakes and every failure replays. Maps iterate in insertion order.
7. **Go underneath.** Riddim compiles to Go that `gofmt` leaves unchanged, with `//line` directives so Go's errors and stack traces point at `.rdm` lines. Go's toolchain, cross-compilation and static binaries come with it.
8. **Merge-friendly.** Top-level declarations may appear in any order and one formatter decides layout, so agents in separate worktrees collide less. A declaration-level merge driver follows (§10.2).

### 1.3 Not in v0

| Not in v0 | When |
|---|---|
| Modules, `pub`, more than one package per program | v0.2 |
| Traits (lowered to Go interfaces) | v0.2 |
| Concurrency (`scope`, `spawn`, channels) | v0.3, after the value rules hold (§4.8) |
| Third-party Go modules, `trusted` packages, Capslock checks | v0.2 (§5.3) |
| Formatter, `riddim fix`, `test` blocks with fake capabilities | v0.1 |
| Network and process capabilities | v0.2 |
| Language server, merge driver | v0.4 |
| A native backend | Only if RB shows run time matters more than the agent loop (§10.2) |

## 2. How Irie uses Riddim

| Irie feature (Irie DESIGN §6.2) | What Riddim adds |
|---|---|
| Review in tandem: a reviewer from another supplier reviews each diff | `riddim check --json` runs first. It costs nothing and takes milliseconds. Failures go back to the worker as a note, so the reviewer model only sees code that compiles and passes its tests |
| Tiers and budget-aware routing (`+fast` local, `+strong` cloud) | Routing by verification: a task starts on the local tier and moves up only after N failed check-and-test rounds. A small reference and exact diagnostics are what make local models useful for more tasks |
| Delegation and shared context | A subtask's reach is explicit in its signature. `riddim caps --diff` (v0.2) flags a change that widens what code can touch, so a person reviews that change, not every line |
| Many agents in one repo, each in its own worktree | Order-free declarations, one formatter and the declaration-level merge driver (v0.4) |
| Irie's own core: an append-only journal, states derived from events | Event enums, exhaustive `match` and a fold fit it directly. Because Riddim compiles to Go, Irie can move one package at a time (the state derivation first) and call it from Go. Never a rewrite |

## 3. Toolchain

### 3.1 Commands

| Command | Does |
|---|---|
| `riddim check [dir] [--json]` | Parse and check every `.rdm` file in `dir` (default `.`). Text diagnostics, or a JSON array (`[]` when clean) with `--json`. Exit 0 when there are no errors (warnings allowed), 1 on errors, 2 on usage or internal errors |
| `riddim build [dir] [-o out]` | Check, write Go into `dir/.riddim/go/`, then `go build -trimpath`. Default output: `./<dir name>` |
| `riddim run [dir] [-- args]` | Build, then run with `args`; exit with the program's status |
| `riddim emit [dir] [-o gen]` | Write the generated Go and stop |
| `riddim version` | Print the version |

A Riddim program is one directory of `.rdm` files that form one package. Exactly one file defines `fn main`.

### 3.2 Pipeline

Lex, parse, resolve names, check types, check patterns, check capabilities and Go imports, lower to Go, `go/format`, `go build`. Stop after the first stage that reports errors. Within a stage, report every independent error and drop errors caused by an earlier one: an expression whose type failed is poisoned, and its uses report nothing.

Generated Go that fails to compile is a Riddim bug: report R0900 with Go's message, mapped through `//line`.

### 3.3 Compiler layout

| Path | What |
|---|---|
| `cmd/riddim` | CLI |
| `internal/syntax` | Tokens, lexer, AST, parser |
| `internal/check` | Names, types, inference, patterns, capabilities |
| `internal/gointerop` | Reading Go package types (`go/importer` in source mode) and the allow list (§5.3) |
| `internal/emit` | Lowering to Go; the runtime, embedded with `go:embed` |
| `internal/diag` | Diagnostics: codes, JSON and text |
| `internal/driver` | The check, build and run pipeline |
| `testdata/` | Golden tests (§8) |
| `examples/` | Example programs (§8) |

The compiler uses Go's standard library only. Module path `github.com/riddim-hq/riddim`, `go 1.27`.

## 4. The language (v0)

This section is normative for v0. `docs/REFERENCE.md` restates it compactly for agents.

### 4.1 Lexical

- Source files are UTF-8 and end in `.rdm`.
- Comments run from `//` to the end of the line. `///` is a doc comment on the next declaration and is carried into the Go output.
- Inside `(` `)` and `[` `]`, newlines are ignored. Elsewhere a newline ends a statement when the line's last token can end one: an identifier, a literal, `)`, `]`, `}`, `?`, `return`, `break`, `continue`, `self`, `Self`, `true`, `false`. `;` is accepted and means the same as that newline.
- A line whose first token is `.`, `&&`, `||`, `|`, `=>`, `else`, or a binary operator other than `-`, continues the previous line, unless that line ended with `return`, `break` or `continue`. This keeps method chains, rustfmt-style conditions and `} else {` on a new line working.
- In comma-separated lists inside braces (struct fields, enum variants, struct literals, `match` arms), a newline counts as a comma, and a trailing comma is allowed.
- Identifiers are letters, digits and `_`, not starting with a digit. Types, enum variants and type parameters are `UpperCamelCase`; functions, variables, fields and methods are `snake_case`; constants are `SCREAMING_SNAKE_CASE`. Anything else is R0203, with a rename fix. Names from Go packages keep Go's spelling.
- Keywords: `fn let mut const struct enum impl use go if else match for in while loop break continue return self Self as true false`.
- Literals: integers (`42`, `1_000`, `0xff`), floats (`3.14`, `1e9`), strings (`"..."` with escapes `\n \t \r \\ \" \$ \u{1F600}` and interpolation `${expr}`), `true`, `false`.

### 4.2 Types

| Type | Meaning | Go |
|---|---|---|
| `Int` | 64-bit signed; wraps on overflow; division by zero panics | `int` |
| `Float` | 64-bit IEEE 754 | `float64` |
| `Bool` | | `bool` |
| `String` | Immutable UTF-8 text | `string` |
| `Byte` | 8-bit unsigned | `byte` |
| `()` | Unit. A function without `->` returns it | no result |
| `(A, B, ...)` | Tuple, two or more fields `.0`, `.1`, ... | generated struct |
| `List<T>` | Immutable ordered values (§4.8) | `[]T` |
| `Map<K, V>` | Immutable map, iterated in insertion order; `K` comparable | runtime struct |
| `Option<T>` | `Some(T)` or `None` | runtime struct, no allocation |
| `Result<T, E>` | `Ok(T)` or `Err(E)` | runtime struct, no allocation |
| `Error` | An error message, optionally wrapping another | `error` |
| `fn(A, B) -> R` | Function value | `func(A, B) R` |
| `Never` | The type of `return`, `break`, `continue`, `panic(...)` and `todo()`. Fits wherever any type is expected | |
| Structs, enums | §4.3, §4.4 | §6 |
| Capabilities `Sys`, `Out`, `Env`, `Fs`, `Clock` | §5 | runtime structs |

- No nil, no pointers, no references, no implicit conversions. `as` converts between `Int`, `Float` and `Byte` only (`f as Int` truncates toward zero).
- An integer literal may stand for a `Float` or a `Byte` where one is expected. Mixing `Int` and `Float` in arithmetic is R0301 with an `as Float` fix.
- Type arguments use `<>` and appear only in types. Calls never take explicit type arguments.
- Inference is local and flows forward: a `let` gets its type from its initializer and any annotation, never from later statements. A call's type arguments come from its arguments and the expected type; closure arguments are checked last, using what the other arguments fixed. When that is not enough, R0306 asks for an annotation (`let xs: List<Int> = []`).

### 4.3 Declarations

```
const MAX_RETRIES: Int = 3

/// A point in the plane.
struct Point {
    x: Float,
    y: Float,
}

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

struct Stack<T> {
    items: List<T>,
}

impl<T> Stack<T> {
    fn push(self, x: T) -> Stack<T> {
        Stack { items: self.items.push(x) }
    }

    fn map<U>(self, f: fn(T) -> U) -> Stack<U> {
        Stack { items: self.items.map(f) }
    }
}
```

- Top-level items, in any order: `const`, `struct`, `enum`, `impl`, `fn`, `use go`.
- Function parameters and results are always annotated. Bodies are inferred.
- Functions, structs, enums, `impl` blocks and methods may take unconstrained type parameters. A value of a type parameter can be passed, stored, returned and matched with `_` or a binding. `==` needs a comparable type, so it is not allowed on a type parameter in v0. Constraints come with traits (v0.2).
- `impl Type { ... }` adds methods. The receiver is `self`, by value and immutable. A method that "changes" a value returns a new one. Methods are called as `value.method(args)`. A function in an `impl` block without `self` is an associated function, called as `Point::new(1.0, 2.0)`.
- Enum variants have zero or more fields, either all named (`Circle(radius: Float)`) or all unnamed (`Circle(Float)`). Construction and patterns are positional: `Shape::Circle(2.0)`, `Circle(r)`. Names document the variant and name the Go fields.
- A struct may not contain itself, directly or through `Option`, `Result`, `Map` or a tuple (R0307, which suggests an enum: enums may be recursive, and so may a struct through `List`).
- `const` values are literals or arithmetic on literals and other constants, of a primitive type.

### 4.4 Values, structs and enums

- Struct literals give every field, `Point { x: 1.0, y: 2.0 }`, or copy the rest from a base, `Point { x: 0.0, ..p }`.
- Field access: `p.x`. Tuple fields: `t.0`.
- Enum construction: `Shape::Circle(2.0)`, `Shape::Empty`. `Some`, `None`, `Ok` and `Err` need no prefix.
- `==` and `!=` work on comparable types: the primitives, `String`, and tuples, structs, enums, `Option` and `Result` made only of comparable types. `List`, `Map`, functions, `Error` and capabilities are not comparable (R0305). `<`, `<=`, `>`, `>=` work on `Int`, `Float`, `Byte` and `String`.

### 4.5 Statements and expressions

- `let x = e`, `let x: T = e`, `let (a, b) = pair`. Bindings are immutable. `let mut x = e` may be reassigned (`x = e`, `+=`, `-=`, `*=`, `/=`), and the fields of a `let mut` struct may be assigned (`p.x = 1.0`). Shadowing is allowed.
- Blocks are expressions whose value is their last expression. `if c { a } else { b }`, `match` and blocks may be used as values; `if` without `else` has type `()`.
- Loops: `while c { }`, `loop { }`, `for p in e { }` where `e` is a `List`, a `Map` (yielding `(key, value)`) or a range `a..b` (excludes `b`) or `a..=b` (includes it). `p` is a binding or a tuple pattern. Ranges appear only in `for`. `break` and `continue` work in all three.
- `return e` returns early. A function's body value is its result.
- Operators, highest precedence first: unary `-` and `!`; `as`; `*` `/` `%`; `+` `-`; comparisons; `&&`; `||`. `+` also joins strings. No bitwise operators in v0.
- Closures: `|x| x + 1`, `|a: Int, b: Int| -> Int { a + b }`. Parameter types may be left out when the expected function type gives them. Closures capture values, not variables: a closure sees the value a `let mut` had when the closure was made, and cannot assign to it.
- String interpolation: `"total: ${n}"`, where `n` is any expression of type `Int`, `Float`, `Bool`, `Byte` or `String`.
- `panic(msg)` and `todo()` stop the program. `todo()` raises warning W0003.
- An expression statement of type `Result` must be used: handle it with `?` or `match`, or discard it on purpose with `let _ = e` (R0502).

### 4.6 Patterns and `match`

```
match result {
    Ok(Some(n)) if n > 0 => "positive",
    Ok(Some(_)) => "not positive",
    Ok(None) => "missing",
    Err(e) => e.message(),
}
```

- Patterns: `_`; a `snake_case` binding; `Int`, `String` and `Bool` literals; tuples `(p, q)`; variants with sub-patterns, written bare (`Circle(r)`) or qualified (`Shape::Circle(r)`); `Some(p)`, `None`, `Ok(p)`, `Err(p)`; or-patterns `p | q`, whose sides bind the same names with the same types.
- Guards: `pattern if cond => ...`. A guarded arm does not count toward exhaustiveness.
- `match` must be exhaustive. R0401 lists the missing cases as patterns (`Ok(None)`, `Rect(_, _)`) and offers a fix that adds an arm for each, with `todo()` as the body. An arm that can never match is R0402.
- `if let p = e { } else { }` is shorthand for a two-arm `match`.
- `let` and `for` patterns must be irrefutable: bindings, `_` and tuples of them.

### 4.7 Errors: `Result`, `Option` and `?`

- `e?` on a `Result<T, E>` yields the `T`, or returns the `Err` from the enclosing function, whose result must be `Result<_, E>` with the same `E` (R0504 otherwise, with a `.map_err(...)` hint).
- `e?` on an `Option<T>` yields the `T`, or returns `None` from an enclosing function that returns `Option<_>`. `.ok_or(error("..."))?` turns an `Option` into a `Result` (R0503 suggests it when the enclosing function returns a `Result`).
- Inside a closure, `?` returns from the closure.
- `error(msg)` makes an `Error`. `e.message()` is its text. `e.context(msg)` wraps it; the wrapper's message is `"msg: inner message"`.
- `unwrap()` and `expect(msg)` on `Option` and `Result` panic on the empty case and raise warning W0002.

### 4.8 Values, not references

Every value behaves as if it were copied when it is bound, passed, returned or captured. Nothing can be changed through a second name, so there is no aliasing to reason about, and once concurrency arrives (v0.3), no data race: a task can only receive values.

In v0 this is cheap to keep. `List`, `Map`, `String` and `Error` are immutable, and only local `let mut` bindings change. `xs.push(x)`, `xs.set(i, x)`, `m.insert(k, v)` and the other updates return a new value. They copy in v0 (O(n)); later versions reuse storage when the compiler can prove a value is not shared, as Swift and Hylo do with copy-on-write and Lean and Koka with in-place reuse. A Go function never receives a Riddim list's storage: lists are copied at the Go boundary (§5.3).

### 4.9 Built-in functions and methods

| On | Methods |
|---|---|
| `String` | `len()` (bytes), `is_empty()`, `contains(s)`, `starts_with(s)`, `ends_with(s)`, `find(s) -> Option<Int>`, `split(sep) -> List<String>`, `lines() -> List<String>`, `trim()`, `to_upper()`, `to_lower()`, `replace(old, new)`, `repeat(n)`, `slice(start, end)` (byte offsets; panics off a character boundary), `parse_int() -> Result<Int, Error>`, `parse_float() -> Result<Float, Error>`, `to_string()` |
| `Int`, `Float`, `Bool`, `Byte` | `to_string()`; for `Float`, the shortest text that reads back as the same value (`6`, `0.1`, `1e+21`), as Go's `strconv.FormatFloat(f, 'g', -1, 64)`. `Int`: `abs()`, `min(o)`, `max(o)`. `Float`: `abs()`, `floor()`, `ceil()`, `round()`, `sqrt()` |
| `List<T>` | `len()`, `is_empty()`, `get(i) -> Option<T>`, `xs[i]` (panics out of range), `first() -> Option<T>`, `last() -> Option<T>`, `push(x)`, `set(i, x)`, `concat(ys)`, `slice(start, end)`, `reverse()`, `map(f)`, `filter(f)`, `fold(init, f)`, `any(f)`, `all(f)`, `find(f) -> Option<T>`, `enumerate() -> List<(Int, T)>`. For comparable `T`: `contains(x)`, `index_of(x) -> Option<Int>`. For `Int`, `Float`, `String`: `sort()`, `min() -> Option<T>`, `max() -> Option<T>`. For `Int` and `Float`: `sum()`. For `List<String>`: `join(sep)` |
| `Map<K, V>` | `Map::new()`, `len()`, `is_empty()`, `get(k) -> Option<V>`, `contains_key(k)`, `insert(k, v)` (a new key goes last; an existing key keeps its place), `remove(k)`, `keys() -> List<K>`, `values() -> List<V>`, `entries() -> List<(K, V)>` |
| `Option<T>` | `is_some()`, `is_none()`, `unwrap_or(d)`, `map(f)`, `and_then(f)`, `ok_or(e) -> Result<T, E>`, `unwrap()`, `expect(msg)` |
| `Result<T, E>` | `is_ok()`, `is_err()`, `unwrap_or(d)`, `map(f)`, `map_err(f)`, `and_then(f)`, `ok() -> Option<T>`, `unwrap()`, `expect(msg)` |
| `Error` | `message()`, `context(msg)` |
| Free functions | `error(msg)`, `panic(msg)`, `todo()` |

## 5. Capabilities

### 5.1 The rule

Code can affect the world outside the program only through a capability it was handed. `main` receives the root capability:

```
fn main(sys: Sys) -> Result<(), Error> {
    let text = load(sys.fs, "app.conf")?
    sys.out.println("loaded ${text.len()} bytes")
    Ok(())
}

fn load(fs: Fs, path: String) -> Result<String, Error> {
    fs.read(path).map_err(|e| e.context("reading ${path}"))
}
```

`load` can use files and nothing else, and its signature says so. A function with no capability among its parameters (directly or inside a struct, tuple, list, map, option or result) is pure: the same arguments give the same result.

### 5.2 v0 capabilities

| Capability | From `Sys` | Methods |
|---|---|---|
| `Out` | `sys.out`, `sys.err` | `print(s)`, `println(s)` |
| `Env` | `sys.env` | `get(name) -> Option<String>`, `args() -> List<String>` (without the program name) |
| `Fs` | `sys.fs` | `read(path) -> Result<String, Error>`, `write(path, text) -> Result<(), Error>`, `exists(path) -> Bool`, `list(dir) -> Result<List<String>, Error>` (sorted names) |
| `Clock` | `sys.clock` | `now_ms() -> Int` (Unix time in milliseconds) |

- Capabilities can be passed, stored in structs and returned, but never made: there is no literal or constructor (R0603). Passing one delegates it.
- `main` takes `sys: Sys` or nothing, and returns `()` or `Result<(), Error>`. An `Err` from `main` prints `error: <message>` to standard error and exits with status 1.
- `Net` and `Proc` arrive in v0.2. Tests get fake capabilities (in-memory files, a fixed clock, captured output) in v0.1.

### 5.3 Go packages

- `use go "strings"` imports a Go package; its functions are called as `strings.Fields(s)`, its constants as `math.Pi`.
- v0 allows only packages that cannot reach the outside world: `strings`, `strconv`, `math`, `unicode`, `unicode/utf8`. Any other package is R0601, whose message names the capability to use instead (for `os`: `Fs` or `Env`).
- v0 calls package-level functions and reads package-level constants whose types map to Riddim: `int` to `Int`, `float64` to `Float`, `bool` to `Bool`, `string` to `String`, `byte` to `Byte`, `rune` to `Int`, `[]T` to `List<T>`, `error` to `Error`. A result list of `(T, error)` becomes `Result<T, Error>`; `error` alone becomes `Result<(), Error>`. A function with no results is refused, because it exists only for its side effects. Anything else is R0602.
- v0.2 adds third-party Go modules. The project manifest may mark a module `trusted` for named capabilities; Riddim runs Capslock over every Go dependency and refuses one that reaches further than the manifest grants.

## 6. Lowering to Go

| Riddim | Go |
|---|---|
| A `.rdm` file | One `.go` file in package `main`, with `//line file.rdm:N` directives |
| The runtime (`Option`, `Result`, tuples, `Map`, list helpers, capabilities) | `riddim_rt.go`, embedded in the compiler and written into every build |
| `Option<T>`, `Result<T, E>` | Generic structs, so neither allocates |
| An enum with no fields in any variant | `type Color int` and constants |
| Any other enum | A sealed interface (an unexported marker method) and one struct per variant: `Shape`, `Shape_Circle{radius float64}` |
| A generic method (`fn map<U>` in `impl<T> Stack<T>`) | A Go 1.27 generic method. Go forbids these from implementing interface methods, which v0 never needs |
| `match` | Arms tested in order as an `if` / `else if` chain; enum tests are type assertions; a final `panic("unreachable")` |
| `if`, `match` and blocks used as values | A temporary assigned in each branch |
| `e?` | Test, early `return` of the converted `Err` or `None`, then continue with the value |
| Closures | `func` literals; captured variables are first copied into fresh locals |
| `snake_case` names | Kept. Names that clash with Go keywords or predeclared identifiers get a `_` suffix |
| Shadowed bindings | Renamed `x`, `x_1`, ... |
| Unused bindings | `_ = x`, so Go's unused-variable error never fires (Riddim warns, W0001) |
| `Error` | `error`. `error(m)` is `errors.New(m)`; `context` wraps with `%w` |

The generated Go must pass `go vet`. It is gofmt'd and readable, so a person debugging can read it and step through it.

## 7. Diagnostics

### 7.1 Shape

```json
[{"code": "R0401", "severity": "error",
  "message": "match on Result<Option<Int>, Error> is missing 1 case: Ok(None)",
  "file": "main.rdm", "line": 12, "col": 5, "end_line": 16, "end_col": 6,
  "notes": ["a guarded arm does not count toward exhaustiveness"],
  "fixes": [{"title": "add the missing arm",
             "edits": [{"file": "main.rdm", "line": 15, "col": 1, "end_line": 15, "end_col": 1,
                        "text": "        Ok(None) => todo(),\n"}]}]}]
```

Lines and columns are 1-based; columns count Unicode code points; `end_col` is exclusive. `severity` is `error` or `warning`. `notes` and `fixes` are always present, possibly empty. Text output renders the same data:

```
main.rdm:12:5: error[R0401]: match on Result<Option<Int>, Error> is missing 1 case: Ok(None)
   |
12 |     match r {
   |     ^^^^^^^
   = fix: add the missing arm: `Ok(None) => todo(),`
```

### 7.2 Rules for messages

- Say what is wrong, where, and what to write instead, in that order. Name types as Riddim writes them, never as Go.
- An unknown name comes with the closest known names (edit distance).
- A habit carried over from Rust or Go gets its own code and, where it is mechanical, a fix. At least: `var` and `:=` (write `let mut` or `let`); `nil` (`None`); `i32`, `i64`, `usize`, `u8`, `f64` and friends (`Int`, `Byte`, `Float`); `&`, `&mut`, `*` and `&str` (Riddim has no references; pass the value); lifetimes; `Vec<T>` and `vec![...]` (`List<T>`, `[...]`); `HashMap` (`Map`); macros such as `println!`, `format!`, `panic!`, `todo!` (`sys.out.println("${x}")`, `panic(...)`, `todo()`); `::<>` after a name (delete it); `.iter()`, `.into_iter()`, `.collect()` (delete them: list methods work directly); `.clone()` (delete it: values are copied); `String::from(s)` and `.to_owned()` (the string itself); `if err != nil` (`?`). An agent that writes the habit learns the rule in one round.
- Every code is in `docs/ERRORS.md` with a one-line example of the mistake and of the fix.

### 7.3 Code ranges

| Range | Area |
|---|---|
| R01xx | Lexing and parsing, including the Rust and Go habits |
| R02xx | Names: unknown, duplicate, naming style |
| R03xx | Types: mismatch, arguments, fields, methods, comparability, inference, recursive structs |
| R04xx | Patterns: exhaustiveness, unreachable arms, or-pattern bindings |
| R05xx | Mutability and errors: assigning to an immutable binding, unused `Result`, misused `?` |
| R06xx | Capabilities and Go: refused packages and shapes, making a capability, `main`'s signature |
| R09xx | Internal errors, which are Riddim bugs |
| W00xx | Warnings: unused binding (W0001), `unwrap` and `expect` (W0002), `todo()` (W0003) |

## 8. Tests and examples

- `go vet ./...` and `go test ./...` pass, also with `-race`.
- **Run tests:** `testdata/run/<name>.rdm` with `<name>.out`, the expected standard output, and when needed `<name>.args` and `<name>.exit`. Each is built and run.
- **Error tests:** `testdata/errors/<name>.rdm` with `<name>.diag`, one line per expected diagnostic: `R0401 12:5`. Every code in `docs/ERRORS.md` has at least one.
- **Fix tests:** for each diagnostic with a fix, applying the fix's edits makes that diagnostic go away.
- **Emit tests:** a few `testdata/emit/<name>.rdm` with the expected Go in `<name>.go.golden`; `go test ./... -update` rewrites them.
- **Generated Go:** every run test's Go passes `go vet`.
- **Reference size:** a test fails if `docs/REFERENCE.md` passes 24 KB.
- **Robustness:** a Go fuzz test feeds the lexer, parser and checker arbitrary input; they never panic.
- **Examples**, each also a run test: `hello` (output), `shapes` (enums, methods, `match`), `parse` (`Result`, `?`, `parse_int`), `events` (an append-only event list folded into a `Map` of per-session states, the shape of Irie's core), `files` (`Fs` and `Env`, inside a temporary directory).

## 9. Riddim Bench

- **Tasks:** about 40 small programs in four groups: text and parsing; data modelling with enums and `match`; error-heavy file handling; event folding and state machines. Concurrency tasks join with v0.3. Each task is a prompt plus hidden tests that check standard output for given arguments and files, so the same tests judge the Go, Rust and Riddim versions.
- **Runs:** each task, language and model, with one local model of about 20B parameters and one strong cloud model. Riddim runs get `docs/REFERENCE.md` in the prompt; Go and Rust runs get nothing extra. Up to 3 rounds feed compiler or test output back.
- **Recorded:** pass on the first try and after each round, tokens in and out, wall time, build time, run time, binary size, and defects found by the hidden tests.
- **Rules:** results are published as they come out, losses included, with the prompts, versions and raw logs. §1.1's targets become claims only when RB supports them.

Irie runs RB: each run is a spawn on a profile, and results come back through Irie's journal.

## 10. Milestones

### 10.1 Building v0

Each milestone is built by a fresh session in this repository on its own branch, and reviewed before merge. Rules for build sessions:

- Read AGENTS.md and this document. Use public documentation (Go's spec, standard library docs and source) and record each source in PROVENANCE.md in the same change.
- Work on the milestone's branch; commit in UTC with sign-off (AGENTS.md); do not merge or push.
- A milestone is done when its checks pass. If the design has to change, say how in the commit message and append it to §13.

| Milestone | Delivers | Done when |
|---|---|---|
| **M1. First light** (branch `m1-first-light`) | CLI (§3.1). Lexer with the newline rules. Parser and checker for §4.3 to §4.7 except string interpolation, `if let`, `as` and `Map`: primitives, tuples, structs, enums, methods and associated functions, user generics, closures, `Never`, inference (§4.2), naming rules, immutability, `?` and unused `Result`. Exhaustiveness with missing-case patterns and the add-arms fix. `List` with literals, `for`, `len`, `get`, `[i]`, `push`, `map`, `filter`, `fold`; `Option`, `Result` and `Error` with their §4.9 methods; `to_string()` on primitives. `Sys` with `out` and `err`; `main` returning `Result`. Lowering (§6), the runtime and `//line`. Diagnostics in JSON and text | `riddim run examples/hello` prints `hello, world`; `hello`, `shapes` and `parse` pass as run tests; error tests cover every M1 code; the R0401 fix test passes; `--json` output matches §7.1 (tested); `go vet` and `go test -race ./...` pass |
| M2. Breadth | `Map`; string interpolation; `as`; `if let`; the rest of §4.9; doc comments into Go; the Rust and Go habit diagnostics (§7.2); `events` example | All of §4 implemented; each habit has an error test and, where mechanical, a fix test |
| M3. Capabilities and Go | `Env`, `Fs`, `Clock`; R0603; `main` signature checks; `use go` with the allow list, type mapping and list copying (§5.3); R0601, R0602; `files` example | §5 implemented; `files` passes |
| M4. Hardening | `docs/REFERENCE.md` (with the "differences from Rust" table) and `docs/ERRORS.md` complete; the size test; the fuzz test; `riddim check` on a generated 10,000-line program in under 200 ms | §8 passes in full |

### 10.2 After v0

| Release | Delivers |
|---|---|
| v0.1 | `riddim fmt` (one canonical layout), `riddim fix` (applies diagnostic fixes), `test "name" { }` blocks with fake capabilities |
| v0.2 | Modules and `pub`; traits as Go interfaces; `Net` and `Proc`; third-party Go modules with `trusted` and Capslock; `riddim caps` and `riddim caps --diff` |
| v0.3 | `scope`, `spawn` and channels: structured concurrency, where a task cannot outlive its scope, a failure cancels its siblings, and only values cross between tasks |
| v0.4 | Language server; declaration-level merge driver |
| RB-1 | The bench (§9), run by Irie, results published |
| Later | Irie's state derivation in Riddim; storage reuse for unshared values; a second backend only if RB shows run time is the bottleneck |

## 11. Risks

| Risk | Effect | Mitigation |
|---|---|---|
| Models have seen no Riddim | More errors on the first try than in Go or Rust | Rust-like syntax, the reference in the prompt, a code and a fix for each Rust and Go habit (§7.2). RB measures it |
| Go limits the semantics | No tail calls, GC pauses, Go's scheduler | Fine for Irie's kind of work. A checked program can get another backend later |
| Value semantics cost copies | Slow list-heavy loops in v0 | Measured by RB; storage reuse for unshared values (§4.8) |
| A language is never finished | Scope creep | Each milestone ships with tests and, from RB-1, numbers. Nothing goes in without a use in Irie or RB |
| The name is also used by a small interpreted language | Confusion | Rename before the first push if it matters |

## 12. Open decisions

| # | Decision | Options | Recommendation |
|---|---|---|---|
| 1 | Name and file extension | Riddim and `.rdm`, or another | Keep the working name until the first push |
| 2 | Local models for RB | One ~20B open-weight coding model, or two sizes | Two sizes, to show where Riddim helps most |
| 3 | Integer overflow | Wrap like Go (v0), or panic like Rust's debug builds | Wrap in v0; revisit with RB numbers |

## 13. Changes during the build

Append-only. Each entry: date, milestone, what changed and why.

- **2026-10-27, M1. Constant expressions are checked when compiling.** §4.2 says Int wraps on overflow and division by zero panics. That still holds at run time, but an expression made only of literals and constants is folded by the checker, and if it overflows its type or divides by zero it is error R0308. Go rejects such constant expressions, so without this check they would surface as R0900; Rust rejects them too. Division by a literal zero (`x / 0`) is also R0308 for the same reason. A Float constant expression is emitted so that Go rounds each step as IEEE 754 arithmetic does, keeping `0.1 + 0.2` equal to its run-time value.
- **2026-10-27, M1. Some M3 and M2 pieces come early.** M1 checks `main`'s signature and a missing `main` (R0604) and rejects making a capability (R0603), because the lowering of `Sys` and `main` depends on them. It also has `String.parse_int`, which the `parse` example of §10.1 needs. The rest of §4.9 stays in M2.
- **2026-10-27, M1. R0106 for later features.** Using string interpolation, `as`, `if let`, `use go`, or the `Map`, `Env`, `Fs` and `Clock` types gives R0106, "not supported in this version", so that an agent learns the limit in one round. Each milestone that adds one of them removes it from R0106.
- **2026-10-27, M1. Function values that return ().** §6 lowers a function without `->` to a Go function with no result. Declared functions still do, but function values (closures, `fn(A)` types) return `struct{}` in Go, so that one can instantiate a type parameter: `xs.map(|x| out.println(x))` needs `func(T) U` with `U = ()`. A declared function used as a value is wrapped in a closure that returns `struct{}{}`.
- **2026-10-27, M2. `as` on a Float out of range saturates.** §4.2 says `f as Int` truncates toward zero but not what happens when the value does not fit. Go's conversion is implementation-dependent there, which would break §1.2's determinism, so `as Int` saturates at the ends of Int and `as Byte` at 0 and 255, and NaN gives 0, as Rust's `as` does. `n as Byte` on an Int keeps the low 8 bits, as Go's `byte(n)` does, matching Byte's wrapping arithmetic.
- **2026-10-27, M2. Codes for the Rust and Go habits.** §7.2 gives each habit its own code: R0108 to R0120, in §7.2's order (R0120 is `if err != nil`, which has no mechanical fix). §7.2's list is a minimum, so R0121 to R0124 cover four more habits agents often bring: `pub`, `#[...]` attributes, `++` and `--`, and Go's `func`. All are in R01xx as §7.3 says, but the habits that need names or types to tell (`nil`, `i32` and the other type names, `Vec`, `HashMap`, `.iter()`, `.clone()`, `String::from`, `if err != nil`) are found by the checker and reported with the names-and-types stage of §3.2. After a habit, the parser and checker read on as if its fix were made, so one round reports every habit in a stage. `&` between two values stays R0101, as in M1.
- **2026-10-27, M3. Habits do not stop the pipeline.** §3.2 stops after the first stage that reports errors, and in M2 that stage was often the parser reporting a habit, such as `vec![...]`, so the habits the checker finds (`i64`, `Vec<T>`, `.iter()`, `.clone()`) came a round later. The parser and the checker already read on as if each habit's fix were made, so a habit no longer counts as a stage's error: one round reports every habit, and with them the errors the next stages find. When a real syntax error drops a declaration, a token pre-scan, which runs before parsing, reports every habit the tokens show, at the place and with the code the parser or checker would use; its reports are used only then, and never repeat one of theirs. R0125 is a new habit: Go's `package` and `import` and Rust's `use path;` (the fix writes `use go "path"` for an import). Go's `for i, x := range xs` is R0108, with no fix, since the Riddim loop depends on what `xs` is.
- **2026-10-27, M3. R0106 now means the capabilities of v0.2.** The M1 entry above gave R0106 to features that later milestones add; M3 adds the last of them, `use go` and the `Env`, `Fs` and `Clock` capabilities. R0106 now marks `Net` and `Proc`, and `sys.net` and `sys.proc`, which §5.2 promises for v0.2, so that an agent that reaches for them learns the limit in one round. The checker reports it, in the names-and-types stage, so it no longer hides the errors after it.
