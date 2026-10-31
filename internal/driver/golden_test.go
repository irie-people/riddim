package driver

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/irie-people/riddim/internal/diag"
	"github.com/irie-people/riddim/internal/emit"
	"github.com/irie-people/riddim/internal/syntax"
)

var update = flag.Bool("update", false, "rewrite the golden files of the emit tests")

// errorCases returns testdata/errors/*.rdm.
func errorCases(t *testing.T) []string {
	ms, err := filepath.Glob(filepath.Join(root, "testdata", "errors", "*.rdm"))
	if err != nil || len(ms) == 0 {
		t.Fatal("no error tests")
	}
	return ms
}

func checkFile(path string, src []byte) *Unit {
	u := FromSources(".", map[string][]byte{filepath.Base(path): src})
	u.Check()
	return u
}

func summary(ds []diag.Diagnostic) string {
	var b strings.Builder
	for _, d := range ds {
		fmt.Fprintf(&b, "%s %d:%d\n", d.Code, d.Line, d.Col)
	}
	return b.String()
}

// TestErrors checks testdata/errors/<name>.rdm against <name>.diag: one line
// per expected diagnostic, `CODE line:col`.
func TestErrors(t *testing.T) {
	for _, path := range errorCases(t) {
		name := strings.TrimSuffix(filepath.Base(path), ".rdm")
		t.Run(name, func(t *testing.T) {
			src, _ := os.ReadFile(path)
			want, ok := readOpt(strings.TrimSuffix(path, ".rdm") + ".diag")
			if !ok {
				t.Fatalf("missing %s.diag", name)
			}
			u := checkFile(path, src)
			if got := summary(u.Diags); got != want {
				var b bytes.Buffer
				diag.WriteText(&b, u.Diags, u.Fset.LineSource())
				t.Errorf("diagnostics differ\n--- got\n%s--- want\n%s--- text\n%s", got, want, b.String())
			}
		})
	}
}

func countCode(ds []diag.Diagnostic, code string) (n, errs int) {
	for _, d := range ds {
		if d.Code == code {
			n++
		}
		if d.Severity == diag.Error {
			errs++
		}
	}
	return n, errs
}

// applyFix applies the first fix of d to src and checks the result.
func applyFix(t *testing.T, path string, src []byte, d diag.Diagnostic) []diag.Diagnostic {
	t.Helper()
	fixed, err := diag.Apply(string(src), d.Fixes[0].Edits)
	if err != nil {
		t.Fatalf("applying the fix for %s: %v", d.Code, err)
	}
	return checkFile(path, []byte(fixed)).Diags
}

// TestFixes applies each fix in the error tests: the diagnostic it fixes goes
// away and no new error appears (DESIGN §8).
func TestFixes(t *testing.T) {
	fixes := 0
	for _, path := range errorCases(t) {
		src, _ := os.ReadFile(path)
		before := checkFile(path, src).Diags
		for _, d := range before {
			if len(d.Fixes) == 0 {
				continue
			}
			fixes++
			after := applyFix(t, path, src, d)
			n0, e0 := countCode(before, d.Code)
			n1, e1 := countCode(after, d.Code)
			if n1 >= n0 || e1 > e0 {
				t.Errorf("%s: the fix for %s at %d:%d did not remove it (%d -> %d %s, %d -> %d errors):\n%s",
					filepath.Base(path), d.Code, d.Line, d.Col, n0, n1, d.Code, e0, e1, summary(after))
			}
		}
	}
	if fixes == 0 {
		t.Error("no fixes were tested")
	}
	t.Logf("applied %d fixes", fixes)
}

// TestFixR0401 is the R0401 fix test of DESIGN §10.1: adding the missing arms
// makes the match exhaustive.
func TestFixR0401(t *testing.T) {
	path := filepath.Join(root, "testdata", "errors", "match_missing.rdm")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ds := checkFile(path, src).Diags
	var r *diag.Diagnostic
	for i := range ds {
		if ds[i].Code == "R0401" {
			r = &ds[i]
		}
	}
	if r == nil || len(r.Fixes) == 0 {
		t.Fatalf("no R0401 with a fix in %s:\n%s", path, summary(ds))
	}
	after := applyFix(t, path, src, *r)
	if n, errs := countCode(after, "R0401"); n != 0 || errs != 0 {
		t.Errorf("after the fix: %s", summary(after))
	}
	for _, d := range after {
		if d.Code != "W0003" {
			t.Errorf("unexpected %s after the fix", d.Code)
		}
	}
}

// TestJSONShape checks `check --json` against DESIGN §7.1.
func TestJSONShape(t *testing.T) {
	src := []byte(`fn classify(result: Result<Option<Int>, Error>) -> String {
    match result {
        Ok(Some(n)) if n > 0 => "positive",
        Ok(Some(_)) => "not positive",
        Err(e) => e.message(),
    }
}

fn main(sys: Sys) {
    sys.out.println(classify(Ok(Some(1))))
}
`)
	u := FromSources(".", map[string][]byte{"main.rdm": src})
	var b bytes.Buffer
	if err := diag.WriteJSON(&b, u.Check()); err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := json.Unmarshal(b.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, b.String())
	}
	want := []map[string]any{{
		"code": "R0401", "severity": "error",
		"message": "match on Result<Option<Int>, Error> is missing 1 case: Ok(None)",
		"file":    "main.rdm", "line": 2.0, "col": 5.0, "end_line": 6.0, "end_col": 6.0,
		"notes": []any{"a guarded arm does not count toward exhaustiveness"},
		"fixes": []any{map[string]any{"title": "add the missing arm",
			"edits": []any{map[string]any{"file": "main.rdm", "line": 6.0, "col": 1.0, "end_line": 6.0, "end_col": 1.0,
				"text": "        Ok(None) => todo(),\n"}}}},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got\n%s\nwant\n%v", b.String(), want)
	}
	// The keys come in the order of §7.1.
	keys := []string{`"code"`, `"severity"`, `"message"`, `"file"`, `"line"`, `"col"`, `"end_line"`, `"end_col"`, `"notes"`, `"fixes"`}
	last := -1
	for _, k := range keys {
		i := strings.Index(b.String(), k)
		if i <= last {
			t.Errorf("key %s out of order in\n%s", k, b.String())
		}
		last = i
	}
	// A clean program gives [].
	b.Reset()
	clean := FromSources(".", map[string][]byte{"main.rdm": []byte("fn main() {}\n")})
	diag.WriteJSON(&b, clean.Check())
	if b.String() != "[]\n" {
		t.Errorf("clean program: got %q, want []", b.String())
	}
}

// TestEmit compares the generated Go of testdata/emit/<name>.rdm with
// <name>.go.golden. Run with -update to rewrite the golden files.
func TestEmit(t *testing.T) {
	ms, _ := filepath.Glob(filepath.Join(root, "testdata", "emit", "*.rdm"))
	if len(ms) == 0 {
		t.Fatal("no emit tests")
	}
	for _, path := range ms {
		name := strings.TrimSuffix(filepath.Base(path), ".rdm")
		t.Run(name, func(t *testing.T) {
			src, _ := os.ReadFile(path)
			u := FromSources(".", map[string][]byte{name + ".rdm": src})
			if ds := u.Check(); diag.HasErrors(ds) {
				t.Fatalf("errors: %s", summary(ds))
			}
			files, err := emit.Program(u.Prog, func(s *syntax.Source) string { return s.Name }, RuntimePath)
			if err != nil {
				t.Fatal(err)
			}
			got := files[emit.GoFileName(name+".rdm")]
			golden := strings.TrimSuffix(path, ".rdm") + ".go.golden"
			if *update {
				os.WriteFile(golden, got, 0o644)
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run go test -update)", err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("generated Go differs from %s (run go test -update to accept)\n%s", golden, got)
			}
		})
	}
}

// TestDocComments checks that /// comments reach the generated Go as doc
// comments that Go itself reads, without the //line directives.
func TestDocComments(t *testing.T) {
	path := filepath.Join(root, "testdata", "emit", "docs.rdm")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	u := FromSources(".", map[string][]byte{"docs.rdm": src})
	if ds := u.Check(); diag.HasErrors(ds) {
		t.Fatalf("errors: %s", summary(ds))
	}
	files, err := emit.Program(u.Prog, func(s *syntax.Source) string { return s.Name }, RuntimePath)
	if err != nil {
		t.Fatal(err)
	}
	gf, err := parser.ParseFile(token.NewFileSet(), "docs.rdm.go", files["docs.rdm.go"], parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, d := range gf.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			got[d.Name.Name] = d.Doc.Text()
		case *ast.GenDecl:
			for _, s := range d.Specs {
				switch s := s.(type) {
				case *ast.TypeSpec:
					got[s.Name.Name] = d.Doc.Text()
					if st, ok := s.Type.(*ast.StructType); ok {
						for _, f := range st.Fields.List {
							got[s.Name.Name+"."+f.Names[0].Name] = f.Doc.Text()
						}
					}
				case *ast.ValueSpec:
					got[s.Names[0].Name] = d.Doc.Text() + s.Doc.Text()
				}
			}
		}
	}
	want := map[string]string{
		"MAX_RETRIES":  "The largest number of retries.\n",
		"Point":        "A point in the plane.\n\nBoth coordinates are in metres.\n",
		"Point.x":      "Distance east of the origin.\n",
		"Shape":        "A shape.\n",
		"Shape_Circle": "A circle around the origin.\n",
		"Shape_Empty":  "Nothing at all.\n",
		"Color":        "Primary colours.\n",
		"Color_Red":    "The colour of blood.\n",
		"Color_Blue":   "",
		"norm":         "The distance from the origin.\n",
		"main_":        "Runs the example.\nline this is not a directive\n",
	}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("doc of %s = %q, want %q", name, got[name], w)
		}
	}
}
