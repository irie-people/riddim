package check

import (
	"fmt"
	"strings"
	"testing"

	"github.com/irie-people/riddim/internal/diag"
	"github.com/irie-people/riddim/internal/syntax"
)

func checkSrc(t *testing.T, src string) []diag.Diagnostic {
	t.Helper()
	fs := syntax.NewFileSet()
	f, ds := syntax.Parse(fs, fs.Add("t.rdm", []byte(src)))
	if diag.HasErrors(ds) {
		t.Fatalf("syntax errors: %v", ds)
	}
	_, cds := Check(fs, []*syntax.File{f})
	return cds
}

func codes(ds []diag.Diagnostic) string {
	var out []string
	for _, d := range ds {
		out = append(out, fmt.Sprintf("%s %d:%d", d.Code, d.Line, d.Col))
	}
	return strings.Join(out, ", ")
}

func TestCleanPrograms(t *testing.T) {
	progs := []string{
		`fn main() {}`,
		`
struct Stack<T> { items: List<T> }
impl<T> Stack<T> {
    fn empty() -> Stack<T> { Stack { items: [] } }
    fn push(self, x: T) -> Stack<T> { Stack { items: self.items.push(x) } }
    fn map<U>(self, f: fn(T) -> U) -> Stack<U> { Stack { items: self.items.map(f) } }
    fn size(self) -> Int { self.items.len() }
}
fn main(sys: Sys) {
    let s: Stack<Int> = Stack::empty()
    let t = s.push(1).push(2).map(|x| x.to_string() + "!")
    sys.out.println(t.size().to_string())
}`,
		`
fn first<T>(xs: List<T>) -> Option<T> { xs.get(0) }
fn main(sys: Sys) -> Result<(), Error> {
    let n = "42".parse_int()?
    let xs = [1.5, 2, 3]
    let total = xs.fold(0.0, |a, b| a + b)
    let f = first(["a", "b"]).unwrap_or("none")
    let r: Result<Int, String> = Err("bad")
    let m = match r { Ok(v) => v, Err(_) => n }
    sys.out.println(f + total.to_string() + m.to_string())
    Ok(())
}`,
	}
	for i, src := range progs {
		if ds := checkSrc(t, src); len(ds) != 0 {
			t.Errorf("program %d: unexpected diagnostics: %s\n%v", i, codes(ds), ds)
		}
	}
}

func TestErrors(t *testing.T) {
	tests := []struct{ src, want string }{
		{"fn main() { let x = y }", "R0201 1:21"},
		{"fn main() {}\nfn main() {}", "R0202 2:4"},
		{"struct point {}\nfn main() {}", "R0203 1:8"},
		{"fn main() { let x: Int = \"s\"\n let _ = x }", "R0301 1:26"},
		{"fn f(a: Int) {}\nfn main() { f(1, 2) }", "R0302 2:13"},
		{"struct P { x: Int }\nfn main() { let p = P { y: 1 }\n let _ = p }", "R0303 2:21, R0303 2:25"},
		{"fn main() { let x = 1.to_strin()\n let _ = x }", "R0304 1:23"},
		{"fn main() { let b = [1] == [1]\n let _ = b }", "R0305 1:21"},
		{"fn main() { let xs = []\n let _ = xs }", "R0306 1:22"},
		{"struct N { n: Option<N> }\nfn main() {}", "R0307 1:15"},
		{"fn main() { let x = 1 / 0\n let _ = x }", "R0308 1:21"},
		{"enum E { A, B }\nfn main() { let x = match E::A { A => 1 }\n let _ = x }", "R0401 2:21"},
		{"fn main() { let x = match 1 { _ => 1, 2 => 2 }\n let _ = x }", "R0402 1:39"},
		{"fn main() { let x = match (1, 2) { (a, _) | (_, b) => 1 }\n let _ = x }", "R0403 1:45, R0403 1:49"},
		{"fn main() { let Some(x) = Some(1)\n let _ = x }", "R0404 1:17"},
		{"fn main() { let x = 1\n x = 2 }", "R0501 2:2"},
		{"fn f() -> Result<Int, Error> { Ok(1) }\nfn main() { f() }", "R0502 2:13"},
		{"fn f() -> Result<Int, Error> { let x = [1].get(0)?\n Ok(x) }\nfn main() {}", "R0503 1:40"},
		{"fn f() -> Result<Int, String> { let x = \"1\".parse_int()?\n Ok(x) }\nfn main() {}", "R0504 1:41"},
		{"fn main() { let x = \"1\".parse_int()?\n let _ = x }", "R0505 1:21"},
		{"fn main() { let s = Sys {}\n let _ = s }", "R0603 1:21"},
		{"fn main(x: Int) {}", "R0604 1:4"},
		{"fn main() { let x = 1 }", "W0001 1:17"},
		{"fn main() { let x = Some(1).unwrap()\n let _ = x }", "W0002 1:29"},
		{"fn main() { todo() }", "W0003 1:13"},
	}
	for _, tt := range tests {
		if got := codes(checkSrc(t, tt.src)); got != tt.want {
			t.Errorf("%q:\n got  %s\n want %s", tt.src, got, tt.want)
		}
	}
}

func TestRenameStyles(t *testing.T) {
	tests := []struct {
		name  string
		style nameStyle
		want  string
	}{
		{"myVar", snake, "my_var"},
		{"MyVar", snake, "my_var"},
		{"HTTPServer", snake, "http_server"},
		{"my_type", upperCamel, "MyType"},
		{"maxRetries", screaming, "MAX_RETRIES"},
		{"_unused", snake, "_unused"},
	}
	for _, tt := range tests {
		if got := convert(tt.name, tt.style); got != tt.want {
			t.Errorf("convert(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}
