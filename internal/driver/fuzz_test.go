package driver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/riddim-hq/riddim/internal/diag"
	"github.com/riddim-hq/riddim/internal/emit"
	"github.com/riddim-hq/riddim/internal/syntax"
)

// FuzzCheck feeds arbitrary input to the lexer, parser and checker, and to
// the emitter when the input checks; none may panic. The seeds are every
// program in testdata and examples.
func FuzzCheck(f *testing.F) {
	for _, pattern := range []string{"testdata/*/*.rdm", "testdata/run/*/*.rdm", "examples/*/*.rdm"} {
		ms, _ := filepath.Glob(filepath.Join(root, pattern))
		for _, m := range ms {
			b, err := os.ReadFile(m)
			if err == nil {
				f.Add(b)
			}
		}
	}
	f.Fuzz(func(t *testing.T, src []byte) {
		u := FromSources(".", map[string][]byte{"main.rdm": src})
		if ds := u.Check(); !diag.HasErrors(ds) {
			if _, err := emit.Program(u.Prog, func(s *syntax.Source) string { return s.Name }); err != nil {
				t.Fatalf("emit: %v", err)
			}
		}
	})
}
