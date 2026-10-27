package syntax

import (
	"strings"
	"testing"
	"time"
)

// TestPrescanBroken feeds the parser, and so the token pre-scan, the broken
// habits a syntax error leaves: neither may panic.
func TestPrescanBroken(t *testing.T) {
	for _, src := range []string{
		"fn f(b: Box<Int) { }",
		"fn f(b: Box< Int ) { }",
		"fn f() { let b = Box::new( , ) }",
		"fn f() { String::from( , ) }",
		"fn f() { x.clone( , ) }",
		".iter()",
		"for i := ; ; i++ {",
		"vec![; ]",
		"println!(a = )",
		"println!(\"${x\" )",
		"&",
		"*",
		"fn f(x: [Int; ) { }",
		"fn f(x: map[Int) { }",
		"impl<T",
		"use std::{",
		"import (",
		"fn f() { ( ] ) }",
	} {
		fs := NewFileSet()
		Parse(fs, fs.Add("t.rdm", []byte(src)))
	}
}

// TestPrescanLinear checks that the token pre-scan's searches for a closing
// bracket, `>` or `{` do not rescan the file when it is left open: parsing
// four times the input must take well under the sixteen times a quadratic
// search would. The best of three runs keeps other work off the timings.
func TestPrescanLinear(t *testing.T) {
	if testing.Short() {
		t.Skip("timing")
	}
	units := []string{
		"x.clone(\n", "f!(\n", "Box::new(\n", "x::<\n", "pub(\n", "f!(\"a${b}\" \n",
		"for i := 0\n", "vec![1; 2\n", "String::from(Box::new(Vec::new(\n", "&&x\n",
	}
	parse := func(n int) time.Duration {
		best := time.Duration(1 << 62)
		for range 3 {
			start := time.Now()
			for _, u := range units {
				fs := NewFileSet()
				Parse(fs, fs.Add("t.rdm", []byte("fn f() {\n"+strings.Repeat(u, n))))
			}
			best = min(best, time.Since(start))
		}
		return best
	}
	small, large := parse(5000), parse(20000)
	if large > 10*small+20*time.Millisecond {
		t.Errorf("parsing 4 times the input took %v, against %v", large, small)
	}
}

// TestPrescan checks the habits the token pre-scan finds, and that it finds
// none in Riddim that looks like them.
func TestPrescan(t *testing.T) {
	tests := []struct{ src, codes string }{
		{"fn f(x: i32, ys: Vec<u8>, m: HashMap<String, f64>) -> usize { 0 }", "R0110 R0110 R0113 R0110 R0114 R0110"},
		{"struct P { a: &str, b: *Int, c: []int, d: [Int; 3], e: map[string]bool, f: Box<P> }", "R0111 R0111 R0110 R0113 R0113 R0110 R0110 R0114 R0111"},
		{"enum E { A(i64), B(x: char) }", "R0110 R0110"},
		{"fn f() { let x: u64 = y as f32; let g = |a: isize| a }", "R0110 R0110 R0110"},
		{"fn f() { xs.iter().cloned().collect::<Vec<i32>>(); s.to_owned().as_str() }", "R0117 R0118 R0117 R0116 R0119 R0119"},
		{"fn f() { let a = Vec::new(); let b = HashMap::with_capacity(4); let c = Box::new(1); let d = String::new(); let e = String::from(\"x\") }", "R0113 R0114 R0111 R0119 R0119"},
		{"fn f() { var n int = 1; m := 2; a, b := g(); n++; let r = &n; let s = *r }", "R0108 R0110 R0108 R0108 R0123 R0111 R0111"},
		{"fn f() { for i := 0; i < 3; i++ { } }", "R0108"},
		{"fn f() { if x == nil { return nil } if err != nil { } }", "R0109 R0109 R0120"},
		{"pub fn f() { }\nfunc g() { }\nuse std::io;", "R0121 R0124 R0125"},
		{"fn f(&self) { vec![1]; println!(\"{}\", 1) }", "R0111 R0113 R0115"},
		// Riddim that resembles a habit
		{"fn f(x: Int) -> List<Int> { let a = b * c; let d = e && f; let g = |h| h; [x] }", ""},
		{"fn iter(x: Int) -> Int { x.iter() }", ""},
		{"struct Vec<T> { x: T }\nfn f(v: Vec<Int>) { }", ""},
		{"fn f() { let p = Point { x: int, y: string } }", ""},
		{"fn f() { let nil = 1; let x = nil }", ""},
		{"fn f() { match x { A | B => 1 } }", ""},
	}
	for _, tt := range tests {
		fs := NewFileSet()
		src := fs.Add("t.rdm", []byte(tt.src))
		toks, _ := Lex(fs, src)
		var codes []string
		for _, d := range scanHabits(fs, src, toks) {
			codes = append(codes, d.Code)
		}
		if got := strings.Join(codes, " "); got != tt.codes {
			t.Errorf("scanHabits(%q) = %q, want %q", tt.src, got, tt.codes)
		}
	}
}
