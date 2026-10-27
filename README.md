# Riddim

> **rid·dim** /ˈrɪd.ɪm/ · *noun, Jamaican Patois*
> The rhythm a song is built on. One riddim carries many voices.

A small language for code that agents write and people review. It reads like a small Rust, compiles to readable Go, and stops what agents most often get wrong before it reaches you: nil, ignored errors, missed cases, and code that touches files or the network without being handed the right to.

```
enum Shape {
    Circle(radius: Float),
    Rect(w: Float, h: Float),
}

fn area(s: Shape) -> Float {
    match s {
        Circle(r) => 3.14159 * r * r,
        Rect(w, h) => w * h,
    }
}

fn main(sys: Sys) {
    let shapes = [Shape::Circle(1.0), Shape::Rect(2.0, 3.0)]
    for s in shapes {
        sys.out.println(area(s).to_string())
    }
}
```

## Why

**Cheap to check.** Every diagnostic has a code, an exact place and, where it can, a fix a tool can apply. `riddim check --json` gives an agent everything it needs to fix its own code, in milliseconds, before a reviewer spends a token on it.

**Small enough to learn from the prompt.** The whole language reference is kept under about 6,000 tokens, so even a small local model can carry it.

**Code can only touch what it's handed.** Files, the environment and the clock reach a function only as parameters. A signature tells you what the code can do; a change that widens it stands out in review.

**Go underneath.** Riddim compiles to readable Go: Go's speed, tooling, cross-compiling and packages.

Riddim is built for [Irie](https://github.com/irie-hq/irie), which runs many coding agents side by side.

## Status

Early and unreleased. Nothing to install yet. The plan is [docs/DESIGN.md](docs/DESIGN.md).

## Free, and paid back

**Get the value you'd pay for, free, today.** If Riddim ever makes money
(donations, sponsorships, grants), it all goes back to the people who build it,
after running costs, in proportion to the value of what they contributed. The
founder started with zero Steak and earns it the same way as everyone else. The
record is public: [riddim-hq/stake](https://github.com/riddim-hq/stake). It runs on
[Steak](https://github.com/steak-hq/steak) v0.1.0.

## License

[Apache-2.0](LICENSE). Built from public documentation; every source is listed in [PROVENANCE.md](PROVENANCE.md).
