package driver

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/riddim-hq/riddim/internal/diag"
)

// TestCodesCovered checks that every code the compiler emits is documented in
// docs/ERRORS.md and has an error test (R0900, a compiler bug, is tested by
// TestGoErrors instead).
func TestCodesCovered(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join(root, "docs", "ERRORS.md"))
	if err != nil {
		t.Fatal(err)
	}
	tested := map[string]bool{"R0900": true}
	ds, _ := filepath.Glob(filepath.Join(root, "testdata", "errors", "*.diag"))
	for _, path := range ds {
		b, _ := os.ReadFile(path)
		for _, line := range strings.Split(string(b), "\n") {
			if f := strings.Fields(line); len(f) > 0 {
				tested[f[0]] = true
				if _, ok := diag.Codes[f[0]]; !ok {
					t.Errorf("%s: unknown code %s", path, f[0])
				}
			}
		}
	}
	var codes []string
	for c := range diag.Codes {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	for _, c := range codes {
		if !strings.Contains(string(doc), "## "+c) {
			t.Errorf("%s is not documented in docs/ERRORS.md", c)
		}
		if !tested[c] {
			t.Errorf("%s has no error test in testdata/errors", c)
		}
	}
}

// TestReferenceSize keeps docs/REFERENCE.md within an agent's prompt.
func TestReferenceSize(t *testing.T) {
	fi, err := os.Stat(filepath.Join(root, "docs", "REFERENCE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() > 24*1024 {
		t.Errorf("docs/REFERENCE.md is %d bytes; keep it under 24 KB", fi.Size())
	}
}
