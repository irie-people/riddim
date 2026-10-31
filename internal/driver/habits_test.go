package driver

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/irie-people/riddim/internal/diag"
)

// TestHabitsOneRound is the review check of M2 for DESIGN §7.2: a program
// that mixes every Rust and Go habit gets every one of their codes from a
// single check, also when a syntax error stops the parser and the checker,
// and no habit is reported twice.
func TestHabitsOneRound(t *testing.T) {
	for _, name := range []string{"habit_all", "habit_all_broken"} {
		path := filepath.Join(root, "testdata", "errors", name+".rdm")
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		ds := checkFile(path, src).Diags
		got := map[string]bool{}
		seen := map[string]bool{}
		for _, d := range ds {
			got[d.Code] = true
			key := fmt.Sprintf("%s %d:%d", d.Code, d.Line, d.Col)
			if seen[key] {
				t.Errorf("%s: %s is reported twice", name, key)
			}
			seen[key] = true
		}
		for _, h := range habits {
			if !got[h.code] {
				t.Errorf("%s: no %s from one check", name, h.code)
			}
		}
		if name == "habit_all" && diag.HasBlocking(ds) {
			t.Errorf("%s: errors other than habits:\n%s", name, summary(ds))
		}
	}
}

// TestPrescanAligned checks the token pre-scan against the parser and the
// checker: on every error test that parses, each habit the pre-scan finds is
// reported by them with the same code at the same place. The pre-scan's
// reports stand in for theirs when a syntax error stops them, so they must
// agree.
func TestPrescanAligned(t *testing.T) {
	checked := 0
	for _, path := range errorCases(t) {
		src, _ := os.ReadFile(path)
		u := checkFile(path, src)
		complete := true
		var pre []diag.Diagnostic
		for _, f := range u.Files {
			complete = complete && f.Complete
			pre = append(pre, f.Habits...)
		}
		if !complete {
			continue
		}
		checked++
		final := map[string]bool{}
		for _, d := range u.Diags {
			final[fmt.Sprintf("%s %d:%d", d.Code, d.Line, d.Col)] = true
		}
		for _, d := range pre {
			if key := fmt.Sprintf("%s %d:%d", d.Code, d.Line, d.Col); !final[key] {
				t.Errorf("%s: the pre-scan reports %s (%s), which the parser and checker do not", filepath.Base(path), key, d.Message)
			}
		}
	}
	if checked == 0 {
		t.Error("no error test parses")
	}
}

// TestPrescanOnly checks the habits the pre-scan finds on its own, in a
// declaration that a syntax error stops the parser in.
func TestPrescanOnly(t *testing.T) {
	src := []byte("fn f(xs: Vec<i64>) -> usize {\n    let ys = xs.iter().clone()\n    let n = )\n    let s: String = String::from(\"a\")\n    var m = HashMap::new()\n    m++\n}\n\nfn main() {}\n")
	u := checkFile("main.rdm", src)
	want := "R0113 1:10\nR0110 1:14\nR0110 1:23\nR0117 2:17\nR0118 2:24\nR0105 3:13\nR0119 4:21\nR0108 5:5\nR0114 5:13\nR0123 6:6\n"
	if got := summary(u.Diags); got != want {
		t.Errorf("got\n%swant\n%s", got, want)
	}
	if u.Files[0].Complete {
		t.Error("a file with a syntax error is complete")
	}
}
