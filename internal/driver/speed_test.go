package driver

import (
	"testing"

	"github.com/irie-people/riddim/internal/diag"
	"github.com/irie-people/riddim/internal/gen"
)

// BenchmarkCheck checks a generated 10,000-line program, in one process, so
// the Go packages it uses are read once.
func BenchmarkCheck(b *testing.B) {
	src := gen.Program(10000)
	b.SetBytes(int64(len(src)))
	for b.Loop() {
		u := FromSources(".", map[string][]byte{"main.rdm": src})
		if ds := u.Check(); diag.HasErrors(ds) {
			b.Fatalf("errors: %s", summary(ds))
		}
	}
}

// TestGeneratedProgram checks that the generated program is clean, so the
// timings measure a program that passes.
func TestGeneratedProgram(t *testing.T) {
	src := gen.Program(10000)
	u := FromSources(".", map[string][]byte{"main.rdm": src})
	if ds := u.Check(); len(ds) > 0 {
		t.Fatalf("diagnostics: %s", summary(ds))
	}
}
