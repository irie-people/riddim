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

### Ideas from prior knowledge

These shaped the design but were not re-read for it. A build session that relies on one reads it and adds a row above.

| Source | Idea |
|---|---|
| The Rust Reference, https://doc.rust-lang.org/reference/ | Syntax that models already know: `match`, `Option`, `Result`, `?`, `impl`, closures (DESIGN §4) |
| The Go Programming Language Specification, https://go.dev/ref/spec | Semicolons from newlines, the model for §4.1 |
| Go compiler directives, https://pkg.go.dev/cmd/compile | `//line` directives (DESIGN §6) |
| L. Maranget, "Warnings for pattern matching", Journal of Functional Programming, 2007 | Exhaustiveness and unreachable-arm checking with example missing patterns (DESIGN §4.6) |
| Hylo, https://www.hylo-lang.org/ ; Swift's copy-on-write collections | Mutable value semantics: values instead of shared references (DESIGN §4.8) |
| Reinking, Xie, de Moura, Leijen, "Perceus: Garbage Free Reference Counting with Reuse", PLDI 2021 | Reusing storage in place when a value is not shared (DESIGN §4.8) |
| Pony, https://www.ponylang.io/ ; object-capability security | The root capability handed to `main` (DESIGN §5) |
| N. J. Smith, "Notes on structured concurrency", https://vorpus.org/blog/notes-on-structured-concurrency-or-go-statement-considered-harmful/ | Tasks that cannot outlive their scope (DESIGN §10.2) |

## Third-party code

| Module | Version | License | Why |
|---|---|---|---|
