// Package gen writes large Riddim programs for timing the compiler (DESIGN
// §10.1, M4: riddim check on a 10,000-line program in under 200 ms).
package gen

import (
	"bytes"
	"fmt"
)

// Program writes a program of at least lines lines, made of blocks that
// use most of the language: structs, enums, impls, generics, match,
// closures, loops, lists, maps, Results with ?, interpolation and capabilities.
func Program(lines int) []byte {
	var b bytes.Buffer
	b.WriteString("use go \"strings\"\n\n")
	n := 0
	for i := 0; bytes.Count(b.Bytes(), []byte("\n")) < lines-12; i++ {
		fmt.Fprintf(&b, `/// Block %[1]d: a shape, a stack and a parser.
enum Shape%[1]d {
    Circle(radius: Float),
    Rect(w: Float, h: Float),
    Empty,
}

struct Point%[1]d {
    x: Float,
    y: Float,
}

struct Stack%[1]d<T> {
    items: List<T>,
}

impl<T> Stack%[1]d<T> {
    fn push(self, x: T) -> Stack%[1]d<T> {
        Stack%[1]d { items: self.items.push(x) }
    }

    fn size(self) -> Int {
        self.items.len()
    }
}

impl Shape%[1]d {
    fn area(self) -> Float {
        match self {
            Circle(r) => 3.14159 * r * r,
            Rect(w, h) => w * h,
            Empty => 0.0,
        }
    }
}

fn parse_all_%[1]d(texts: List<String>) -> Result<List<Int>, Error> {
    let mut out: List<Int> = []
    for t in texts {
        let n = t.trim().parse_int().map_err(|e| e.context("reading ${t}"))?
        out = out.push(n)
    }
    Ok(out)
}

fn count_%[1]d(words: List<String>) -> Map<String, Int> {
    let mut counts: Map<String, Int> = Map::new()
    for w in words {
        counts = counts.insert(w, counts.get(w).unwrap_or(0) + 1)
    }
    counts
}

fn report_%[1]d(out: Out, p: Point%[1]d) -> Result<Int, Error> {
    let shapes = [Shape%[1]d::Circle(p.x), Shape%[1]d::Rect(p.x, p.y), Shape%[1]d::Empty]
    let total = shapes.fold(0.0, |acc, s| acc + s.area())
    let nums = parse_all_%[1]d(strings.Fields("1 2 3"))?
    let s: Stack%[1]d<Int> = Stack%[1]d { items: [] }
    let s2 = s.push(nums.sum()).push(%[1]d)
    let counts = count_%[1]d("a b a".split(" "))
    let mut i = 0
    while i < s2.size() {
        if let Some(n) = counts.get("a") {
            i += n
        } else {
            break
        }
    }
    out.println("total ${total}, ${counts.len()} words, ${i}")
    Ok(i)
}

`, i)
		n = i + 1
	}
	fmt.Fprintf(&b, "fn main(sys: Sys) -> Result<(), Error> {\n")
	for i := 0; i < n; i += max(1, n/8) {
		fmt.Fprintf(&b, "    let _ = report_%d(sys.out, Point%d { x: 1.0, y: 2.0 })?\n", i, i)
	}
	b.WriteString("    Ok(())\n}\n")
	return b.Bytes()
}
