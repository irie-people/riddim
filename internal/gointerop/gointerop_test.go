package gointerop

import (
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
