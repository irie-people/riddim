package driver

import (
	"bytes"
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

// habits are the codes of the Rust and Go habits (DESIGN §7.2) and whether
// each is mechanical, so that its diagnostics can carry a fix.
var habits = []struct {
	code       string
	mechanical bool
}{
	{"R0108", true}, {"R0109", true}, {"R0110", true}, {"R0111", true}, {"R0112", true},
	{"R0113", true}, {"R0114", true}, {"R0115", true}, {"R0116", true}, {"R0117", true},
	{"R0118", true}, {"R0119", true}, {"R0120", false}, {"R0121", true}, {"R0122", true},
	{"R0123", true}, {"R0124", true}, {"R0125", true},
}

// TestHabits is the M2 check of DESIGN §10.1: each habit has an error test
// and, where mechanical, a fix that removes it without adding an error.
func TestHabits(t *testing.T) {
	seen, fixed := map[string]bool{}, map[string]bool{}
	for _, path := range errorCases(t) {
		src, _ := os.ReadFile(path)
		before := checkFile(path, src).Diags
		for _, d := range before {
			seen[d.Code] = true
			if len(d.Fixes) == 0 {
				continue
			}
			after := applyFix(t, path, src, d)
			n0, e0 := countCode(before, d.Code)
			n1, e1 := countCode(after, d.Code)
			if n1 < n0 && e1 <= e0 {
				fixed[d.Code] = true
			}
		}
	}
	listed := map[string]bool{}
	for _, h := range habits {
		listed[h.code] = true
		if !seen[h.code] {
			t.Errorf("habit %s has no error test", h.code)
		}
		if h.mechanical && !fixed[h.code] {
			t.Errorf("habit %s has no fix test: no fix in testdata/errors removes it", h.code)
		}
	}
	for code := range diag.Codes {
		if code >= "R0108" && code < "R0200" && !listed[code] {
			t.Errorf("%s is in the habit range but not in the habits list", code)
		}
	}
}

// TestReferencePrograms checks the code in docs/REFERENCE.md that agents
// copy: each block that is a program, or a list of declarations, checks
// without a diagnostic.
func TestReferencePrograms(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join(root, "docs", "REFERENCE.md"))
	if err != nil {
		t.Fatal(err)
	}
	blocks := strings.Split(string(doc), "```")
	checked := 0
	for i := 1; i < len(blocks); i += 2 {
		src := strings.TrimPrefix(blocks[i], "\n")
		first := strings.Fields(src)
		if len(first) == 0 {
			continue
		}
		switch {
		case strings.Contains(src, "fn main"):
		case first[0] == "const" || first[0] == "struct" || first[0] == "enum" || first[0] == "///":
			src += "\nfn main() {}\n"
		default:
			continue
		}
		checked++
		u := FromSources(".", map[string][]byte{"ref.rdm": []byte(src)})
		if ds := u.Check(); len(ds) > 0 {
			var b bytes.Buffer
			diag.WriteText(&b, ds, u.Fset.LineSource())
			t.Errorf("block %d of docs/REFERENCE.md:\n%s", i/2+1, b.String())
		}
	}
	if checked < 3 {
		t.Errorf("checked %d blocks, want at least 3", checked)
	}
}
