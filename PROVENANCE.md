# Provenance

Riddim is written from public documentation and public open-source projects. Every source that shaped the code is listed here: what it is, when it was read, and what it was used for.

## Sources

| Date | Source | Used for |
|---|---|---|
| 2026-10-27 | https://github.com/ubavic/compiles-to-go | The list of languages that compile to Go, and which are maintained: prior art (DESIGN §1) |
| 2026-10-27 | https://github.com/borgo-lang/borgo ; https://borgo-lang.github.io/ | Prior art: Rust-like syntax lowered to Go, Go's interfaces in place of traits. Ideas only, no code (DESIGN §1, §6) |
| 2026-10-27 | https://github.com/MadAppGang/dingo (README) | Prior art: `Result`, `Option` and `?` added to Go as syntax, with gopls doing the type checking. Riddim checks its own types instead (DESIGN §3.2). Ideas only, no code |
| 2026-10-27 | https://lisette.run/ ; https://github.com/ivov/lisette | Prior art: Rust syntax on Go's runtime, typedef files for Go packages. Ideas only, no code (DESIGN §5.3) |
| 2026-10-27 | https://github.com/google/capslock ; https://capslock-project.github.io/ | Capability classes for Go packages (files, network, exec, environment) and the `analyzer` library: the plan for checking Go dependencies (DESIGN §5.3) |
| 2026-10-27 | https://go.dev/doc/go1.27 | Methods may declare their own type parameters, but cannot implement interface methods: lowering of generic methods (DESIGN §6) |
| 2026-10-27 | https://www.apache.org/licenses/LICENSE-2.0.txt | License text (LICENSE) |
| 2026-10-27 | Go standard library: `go doc encoding/json.Marshal`; `$GOROOT/src/encoding/json/encode.go` (fields sorted by index) | Diagnostics JSON keys come from struct tags in struct order, so `internal/diag` keeps the field order of DESIGN §7.1 |
| 2026-10-27 | Go standard library: `go doc unicode/utf8` (`RuneCount`, `DecodeRune`) | Columns counted in code points and invalid UTF-8 detection in the lexer (`internal/syntax`, `internal/diag`) |
| 2026-10-27 | The Go Programming Language Specification, `$GOROOT/doc/go_spec.html` (Method declarations) | A struct's method and field names must be distinct in Go, so the checker reports a field and a method with one name (R0202) instead of emitting Go that cannot compile (`internal/check`) |
| 2026-10-27 | The Go Programming Language Specification, `$GOROOT/doc/go_spec.html` (Arithmetic operators, Constant expressions, Representability) | Go rejects division by a constant zero and typed constants that overflow, so the checker folds Riddim constant expressions and reports those cases as R0308 (`internal/check/consts.go`) |
| 2026-10-27 | The Go Programming Language Specification, `$GOROOT/doc/go_spec.html` (Method declarations: generic methods, Go 1.27) | Methods of generic structs with their own type parameters lower to Go 1.27 generic methods, as DESIGN §6 plans (`internal/emit`) |
| 2026-10-27 | The Go Programming Language Specification, `$GOROOT/doc/go_spec.html` (Constant expressions, Representability), checked with a small program | Untyped float constant expressions fold exactly, typed ones round at each step; the emitter types one operand of a constant Float operation so it rounds like IEEE 754 arithmetic (`0.1 + 0.2` prints 0.30000000000000004) |
| 2026-10-27 | Go source, `$GOROOT/src/cmd/compile/doc.go` (Line Directives) | The `//line file:line` form, at the start of a line, with file names relative to the directory of the generated file; how R0900 maps go build's messages back to `.rdm` lines (`internal/emit`, `internal/driver`) |
| 2026-10-27 | Go source, `$GOROOT/src/go/printer/printer.go` (`writeComment`) | gofmt keeps a `//line` comment that starts in column 1 in column 1, so the generated Go can be gofmt'd and keep its directives |
| 2026-10-27 | `go doc go/format.Source`; `go help build` (`-o`, `-trimpath`) | Formatting the generated Go; building it with `go build -trimpath -o`, and, in the run tests, building every test program at once into one directory |

### Ideas from prior knowledge

These shaped the design but were not re-read for it. A build session that relies on one reads it and adds a row above.

| Source | Idea |
|---|---|
| The Rust Reference, https://doc.rust-lang.org/reference/ | Syntax that models already know: `match`, `Option`, `Result`, `?`, `impl`, closures (DESIGN §4) |
| The Go Programming Language Specification, https://go.dev/ref/spec | Semicolons from newlines, the model for §4.1 |
| Go compiler directives, https://pkg.go.dev/cmd/compile | `//line` directives (DESIGN §6) |
| L. Maranget, "Warnings for pattern matching", Journal of Functional Programming, 2007 | Exhaustiveness and unreachable-arm checking with example missing patterns (DESIGN §4.6). M1 implements the usefulness algorithm in `internal/check/exhaust.go` from prior knowledge: the M1 build session could read only this repository and Go's own docs and source |
| Hylo, https://www.hylo-lang.org/ ; Swift's copy-on-write collections | Mutable value semantics: values instead of shared references (DESIGN §4.8) |
| Reinking, Xie, de Moura, Leijen, "Perceus: Garbage Free Reference Counting with Reuse", PLDI 2021 | Reusing storage in place when a value is not shared (DESIGN §4.8) |
| Pony, https://www.ponylang.io/ ; object-capability security | The root capability handed to `main` (DESIGN §5) |
| N. J. Smith, "Notes on structured concurrency", https://vorpus.org/blog/notes-on-structured-concurrency-or-go-statement-considered-harmful/ | Tasks that cannot outlive their scope (DESIGN §10.2) |

## Third-party code

| Module | Version | License | Why |
|---|---|---|---|
