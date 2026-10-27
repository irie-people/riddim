package syntax

import (
	"strings"
	"testing"
)

// tokens renders the token stream compactly: TERM as `;`, identifiers and
// literals by their text.
func tokens(t *testing.T, src string) (string, []string) {
	t.Helper()
	fs := NewFileSet()
	toks, diags := Lex(fs, fs.Add("t.rdm", []byte(src)))
	var parts []string
	for _, tok := range toks {
		switch tok.Kind {
		case EOF:
		case TERM:
			parts = append(parts, ";")
		case IDENT, INT, FLOAT:
			parts = append(parts, tok.Text)
		case STRING:
			parts = append(parts, `"`+tok.Text+`"`)
		case STRHEAD:
			parts = append(parts, `"`+tok.Text+`${`)
		case STRMID:
			parts = append(parts, `}`+tok.Text+`${`)
		case STRTAIL:
			parts = append(parts, `}`+tok.Text+`"`)
		default:
			parts = append(parts, tok.Kind.String())
		}
	}
	var codes []string
	for _, d := range diags {
		codes = append(codes, d.Code)
	}
	return strings.Join(parts, " "), codes
}

func TestNewlineRules(t *testing.T) {
	tests := []struct{ src, want string }{
		{"let x = 1\nlet y = 2\n", "let x = 1 ; let y = 2 ;"},
		{"let x = 1; let y = 2", "let x = 1 ; let y = 2 ;"},
		// newlines inside ( ) and [ ] are ignored
		{"f(a,\n  b)\n", "f ( a , b ) ;"},
		{"[1,\n2\n]\n", "[ 1 , 2 ] ;"},
		// a line ending in an operator continues
		{"let x = a +\n  b\n", "let x = a + b ;"},
		// a line starting with `.`, `&&`, `||`, `|`, `=>`, `else` or a binary operator continues
		{"xs\n  .map(f)\n  .len()\n", "xs . map ( f ) . len ( ) ;"},
		{"a\n  && b\n  || c\n", "a && b || c ;"},
		{"a\n  + b\n  * c\n  == d\n", "a + b * c == d ;"},
		{"if c {\n} else {\n}\n", "if c { } else { } ;"},
		{"if c {\n}\nelse {\n}\n", "if c { } else { } ;"},
		{"match x {\n  A\n  | B => 1\n}\n", "match x { A | B => 1 ; } ;"},
		// but not `-`
		{"a\n-b\n", "a ; - b ;"},
		// nor after return, break or continue
		{"return\n.5\n", "return ; . 5 ;"},
		{"break\n+ 1\n", "break ; + 1 ;"},
		// braces: newlines separate statements and list items
		{"Point {\n  x: 1.0\n  y: 2.0\n}\n", "Point { x : 1.0 ; y : 2.0 ; } ;"},
		{"f(|x| {\n  let y = x\n  y\n})\n", "f ( | x | { let y = x ; y ; } ) ;"},
		// tokens that cannot end a statement
		{"let x =\n  1\n", "let x = 1 ;"},
		{"fn f() -> Int {\n  1\n}", "fn f ( ) -> Int { 1 ; } ;"},
		// comments
		{"a // one\n/// doc\nb\n", "a ; b ;"},
		// tuple fields
		{"t.0.1\n", "t . 0 . 1 ;"},
		{"1..10 1..=2 3.5 1e9 0xff 1_000", "1 .. 10 1 ..= 2 3.5 1e9 0xff 1_000 ;"},
		{`"a\n\t\"\\\$\u{48}"`, "\"a\n\t\"\\$H\" ;"},
		{"a -> b => c :: d", "a -> b => c :: d ;"},
		// interpolation
		{`"a ${x} b ${y.z(1)} c"` + "\n", `"a ${ x } b ${ y . z ( 1 ) } c" ;`},
		{`"${n}"`, `"${ n }" ;`},
		{`"${ "in ${m}" } ${ { 1 } }"`, `"${ "in ${ m }" } ${ { 1 } }" ;`},
		{`"\${not} $ alone"`, `"${not} $ alone" ;`},
	}
	for _, tt := range tests {
		got, codes := tokens(t, tt.src)
		if got != tt.want || len(codes) != 0 {
			t.Errorf("Lex(%q)\n got  %s %v\n want %s", tt.src, got, codes, tt.want)
		}
	}
}

func TestLexErrors(t *testing.T) {
	tests := []struct{ src, code string }{
		{"let x = a & y", "R0101"},
		{"let c = 'a' 'static", "R0101 R0112"},
		{"#[derive(Debug)]", "R0122"},
		{"let c = 'a'", "R0101"},
		{"let s = \"abc\n", "R0102"},
		{`"\q"`, "R0103"},
		{`"\u{110000}"`, "R0103"},
		{"1__0", "R0104"},
		{"12abc", "R0104"},
		{"0x", "R0104"},
		{"\"n = ${n\n", "R0102"},
		{"\"n = ${n} \n", "R0102"},
		{"\"n = ${\"in", "R0102"},
	}
	for _, tt := range tests {
		_, codes := tokens(t, tt.src)
		if strings.Join(codes, " ") != tt.code {
			t.Errorf("Lex(%q) codes = %v, want [%s]", tt.src, codes, tt.code)
		}
	}
}

func parse(t *testing.T, src string) (*File, []string) {
	t.Helper()
	fs := NewFileSet()
	f, diags := Parse(fs, fs.Add("t.rdm", []byte(src)))
	var codes []string
	for _, d := range diags {
		codes = append(codes, d.Code)
	}
	return f, codes
}

func TestParse(t *testing.T) {
	src := `
const MAX: Int = 3

/// A point.
struct Point {
    x: Float,
    y: Float,
}

enum Shape {
    Circle(radius: Float),
    Rect(Float, Float)
    Empty,
}

impl<T> Stack<T> {
    fn push(self, x: T) -> Stack<T> {
        Stack { items: self.items.push(x) }
    }
    fn map<U>(self, f: fn(T) -> U) -> Stack<U> {
        Stack { items: self.items.map(f), ..self }
    }
}

fn main(sys: Sys) -> Result<(), Error> {
    let mut n = 0
    let (a, b) = (1, "x")
    let xs: List<Int> = [1, 2, 3]
    for x in xs {
        n += x
    }
    for i in 0..=10 { n = n - i }
    while n > 0 && true { n -= 1 }
    let r = match parse(a) {
        Ok(Some(v)) if v > 0 => v,
        Ok(None) | Err(_) => -1
        _ => 0,
    }
    let f = |x: Int| -> Int { x + 1 }
    let g = || 1
    let s = if a == 1 { "one" } else if a == 2 { "two" } else { "many" }
    sys.out.println(s)
    loop {
        if n > 3 { break }
        n += 1
        continue
    }
    Ok(())
}
`
	f, codes := parse(t, src)
	if len(codes) != 0 {
		t.Fatalf("unexpected diagnostics %v", codes)
	}
	if len(f.Decls) != 5 {
		t.Fatalf("got %d declarations, want 5", len(f.Decls))
	}
	e := f.Decls[2].(*EnumDecl)
	if len(e.Variants) != 3 || e.Variants[0].Fields[0].Name.Name != "radius" || e.Variants[1].Fields[0].Name != nil {
		t.Errorf("enum variants parsed wrong: %+v", e.Variants)
	}
	main := f.Decls[4].(*FuncDecl)
	if got := len(main.Body.Stmts); got != 13 {
		t.Errorf("main has %d statements, want 13", got)
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		src   string
		codes string
	}{
		{"fn f() { let x }", "R0105"},
		{"fn f() { x = }", "R0105"},
		{"fn f() { break }", "R0107"},
		{"fn f() { loop { xs.map(|x| { continue }) } }", "R0107"},
		{"fn f() -> Float { 1 as }", "R0105"},
		{"fn f() { if let Some(x) = y { } else if let None = y { } }", ""},
		{"fn f() { if let Some(x) y { } }", "R0105"},
		{"fn f() { while let Some(x) = y { } }", "R0105"},
		{"use go \"strings\"", ""},
		{"use go strings", "R0105"},
		{"use go unicode/utf8\nfn f() { }", "R0105"},
		{"fn f(m: Map<String, Int>) { let n = Map::new() }", ""},
		{"fn f(c: Clock) { }", ""},
		{`fn f() { let s = "${}" }`, "R0105"},
		{`fn f() { let s = "${1 2}" }`, "R0105"},
		{`fn f() { match s { "a${b}" => 1 } }`, "R0105"},
		{"fn f() { a < b < c }", "R0105"},
		{"fn f(x) { }", "R0105"},
		{"struct P(Int)", "R0105"},
		{"fn f() { foo::<Int>() }", "R0116"},
		// Rust and Go habits: each has its own code, and parsing goes on
		{"fn f() { var x = 1; y := 2; z++ }", "R0108 R0108 R0123"},
		{"fn f() { a, b := g() }\nfn h() { }", "R0108"},
		{"fn f() { for i := 0; i < 3; i++ { } }", "R0108"},
		{"fn f(s: &str, t: &mut T, u: *T) { let r = &s; let v = *r; g(&mut x) }", "R0111 R0111 R0111 R0111 R0111 R0111"},
		{"impl P { fn m(&self) { }; fn n(&mut self) { } }", "R0111 R0111"},
		{"fn f<'a>(x: &'a str) { 'outer: loop { break 'outer } }", "R0112 R0112 R0112 R0112 R0111"},
		{"fn f() { let v = vec![1, 2]; println!(\"{}\", v); x.collect::<Vec<_>>() }", "R0113 R0115 R0116"},
		{"fn f(xs: []int, m: map[string]int) { }", "R0113 R0114"},
		{"pub fn f() { }\npub(crate) struct S { pub x: Int }", "R0121 R0121 R0121"},
		{"#[derive(Debug)]\nstruct S { }", "R0122"},
		{"func f() { }", "R0124"},
		{"fn f() { for { } for x < 3 { } }", "R0105 R0105"},
		{"fn f(xs: &[Int]) { }", "R0113 R0111"},
		{"package main\nimport \"strings\"\nimport (\n\"a\"\n\"b\"\n)\nuse std::io;\nfn f() { use std::fmt::{self,\nWrite}; }", "R0125 R0125 R0125 R0125 R0125"},
		{"fn f() { for i := range xs { } }\nfn g() { }", "R0108"},
		{"fn f() { for _, x := range xs { } }\nfn g() { }", "R0108"},
		{"fn f() { println!(\"{}\", x); assert!(x) }", "R0115 R0115"},
		// errors inside type arguments are reported, and parsing goes on
		{"fn f() { g::<,>() }\nfn h() { ) }", "R0105 R0105"},
		{"fn f() { g::<&str>() }", "R0111 R0116"},
		{"fn f() { match x { A => y = 1 } }", "R0105"},
		// one error per declaration: recovery finds the next one
		{"fn f() { ) }\nfn g() { ] }\nfn h() { }", "R0105 R0105"},
	}
	for _, tt := range tests {
		_, codes := parse(t, tt.src)
		if got := strings.Join(codes, " "); got != tt.codes {
			t.Errorf("Parse(%q) codes = %q, want %q", tt.src, got, tt.codes)
		}
	}
}

func TestPositions(t *testing.T) {
	fs := NewFileSet()
	a := fs.Add("a.rdm", []byte("ab\ncdé f\n"))
	b := fs.Add("b.rdm", []byte("x"))
	if got := fs.Source(b.Pos(0)); got != b {
		t.Errorf("Source(b) = %s", got.Name)
	}
	line, col := a.LineCol(a.Pos(8)) // after "cdé " (é is two bytes)
	if line != 2 || col != 5 {
		t.Errorf("LineCol = %d:%d, want 2:5", line, col)
	}
	if s, _ := a.Line(2); s != "cdé f" {
		t.Errorf("Line(2) = %q", s)
	}
}

// TestParseComplete checks File.Complete: a file whose only errors are
// habits is read as if they were fixed, and is complete.
func TestParseComplete(t *testing.T) {
	tests := []struct {
		src      string
		complete bool
	}{
		{"fn f() { var x = vec![1]; x++ }", true},
		{"package main\nfn f(s: &str) -> i32 { 0 }", true},
		{"fn f() { n, err := g() }", false},
		{"fn f() { for i := range xs { } }", false},
		{"fn f() { let x = ) }", false},
		{"fn f() { let c = 'a' }", false},
	}
	for _, tt := range tests {
		fs := NewFileSet()
		f, _ := Parse(fs, fs.Add("t.rdm", []byte(tt.src)))
		if f.Complete != tt.complete {
			t.Errorf("Parse(%q).Complete = %v", tt.src, f.Complete)
		}
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
