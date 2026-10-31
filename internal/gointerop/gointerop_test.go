package gointerop

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"
)

func TestAllowed(t *testing.T) {
	for _, path := range []string{"strings", "strconv", "math", "unicode", "unicode/utf8"} {
		if !Allowed(path) {
			t.Errorf("%s is not allowed", path)
		}
	}
	for _, path := range []string{"os", "fmt", "net/http", "math/rand", "unicode/utf16", "example.com/x", "", "./strings"} {
		if Allowed(path) {
			t.Errorf("%s is allowed", path)
		}
	}
	if got := AllowedList(); got != "`strings`, `strconv`, `math`, `unicode` and `unicode/utf8`" {
		t.Errorf("AllowedList() = %q", got)
	}
}

// TestRefusal checks that R0601 names the capability to use instead of a
// package that reaches outside the program (DESIGN §5.3).
func TestRefusal(t *testing.T) {
	tests := []struct{ path, want string }{
		{"os", "Fs capability"},
		{"os", "Env"},
		{"time", "Clock capability"},
		{"fmt", "Out capability"},
		{"net/http", "Net capability"},
		{"os/exec", "Proc capability"},
		{"example.com/lib", "third-party Go modules come in v0.2"},
		{"sort", "`sort()`"},
		{"unicode/utf16", "not allowed in v0"},
	}
	for _, tt := range tests {
		if got := Refusal(tt.path); !strings.Contains(got, tt.want) {
			t.Errorf("Refusal(%q) = %q, want it to mention %q", tt.path, got, tt.want)
		}
	}
}

// TestImport reads every allowed package from the Go installation's source.
func TestImport(t *testing.T) {
	for _, path := range allowed {
		p, err := Import(path)
		if err != nil {
			t.Fatalf("Import(%q): %v", path, err)
		}
		if !p.Complete() || p.Path() != path {
			t.Errorf("Import(%q) = %s, complete %v", path, p.Path(), p.Complete())
		}
	}
	if p, _ := Import("strings"); p.Scope().Lookup("Fields") == nil {
		t.Error("strings has no Fields")
	}
	if _, err := Import("os"); err == nil {
		t.Error("Import(os) succeeded")
	}
}

// TestReadShallow checks that every allowed package is read with only its
// own imports and that what Riddim uses of it is complete, so that Import
// never reads the whole import tree for it. The check package's
// TestShallowImportMatchesSource compares what is read with go/importer's.
func TestReadShallow(t *testing.T) {
	if err := findGoroot(); err != nil {
		t.Fatal(err)
	}
	for _, path := range allowed {
		p, err := readShallow(token.NewFileSet(), path)
		if err != nil {
			t.Fatalf("readShallow(%q): %v", path, err)
		}
		if p.Path() != path || !complete(p) {
			t.Errorf("readShallow(%q) = %s, complete %v", path, p.Path(), complete(p))
		}
	}
}

// TestComplete checks that an exported function or constant that names a
// package left empty makes a read incomplete, and that variables, types and
// unexported functions, of which Riddim uses at most the name, do not.
func TestComplete(t *testing.T) {
	tests := []struct {
		decls string
		want  bool
	}{
		{"func F(s string) []string { return nil }\nconst K = 1", true},
		{"func f(x e.T) int { return 0 }", true},
		{"var V = e.V\ntype T e.T\ntype S struct{ x e.T }", true},
		{"func F(x e.T) int { return 0 }", false},
		{"func F() (n int, err e.Error) { return }", false},
		{"type A = e.T\nfunc F(xs []A) int { return 0 }", false},
		{"const K = e.K + 1", false},
		{"const K e.Kind = 1", false},
	}
	for _, tt := range tests {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, "p.go", "package p\n\nimport \"e\"\n\n"+tt.decls+"\n", 0)
		if err != nil {
			t.Fatal(err)
		}
		// p was parsed and e was not, so e is left empty.
		s := &shallow{fset: fset, files: map[string][]*ast.File{"p": {f}}, pkgs: map[string]*types.Package{}}
		p, _ := s.Import("p")
		if got := complete(p); got != tt.want {
			t.Errorf("complete(%q) = %v, want %v", tt.decls, got, tt.want)
		}
	}
}
