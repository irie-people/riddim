package driver

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/riddim-hq/riddim/internal/diag"
	"github.com/riddim-hq/riddim/internal/emit"
	"github.com/riddim-hq/riddim/internal/syntax"
)

const root = "../.."

// A program under test: its sources and what running it should give.
type program struct {
	name  string
	srcs  map[string][]byte // file name -> source
	out   string
	args  []string
	env   []string // NAME=value: the program's whole environment
	exit  int
	where string
}

func readOpt(path string) (string, bool) {
	b, err := os.ReadFile(path)
	return string(b), err == nil
}

// runPrograms collects testdata/run/<name>.rdm, testdata/run/<name>/ and
// examples/<name>/, each with <name>.out and optional .args, .env (one
// NAME=value a line) and .exit.
func runPrograms(t *testing.T) []program {
	var ps []program
	add := func(name, dir, expect string, srcs map[string][]byte) {
		out, ok := readOpt(expect + ".out")
		if !ok {
			t.Errorf("%s: missing %s.out", name, expect)
			return
		}
		p := program{name: name, srcs: srcs, out: out, where: dir}
		if a, ok := readOpt(expect + ".args"); ok {
			p.args = strings.Fields(a)
		}
		if e, ok := readOpt(expect + ".env"); ok {
			p.env = strings.Split(strings.TrimSuffix(e, "\n"), "\n")
		}
		if e, ok := readOpt(expect + ".exit"); ok {
			p.exit, _ = strconv.Atoi(strings.TrimSpace(e))
		}
		ps = append(ps, p)
	}
	dirSrcs := func(dir string) map[string][]byte {
		srcs := map[string][]byte{}
		ms, _ := filepath.Glob(filepath.Join(dir, "*.rdm"))
		for _, m := range ms {
			b, _ := os.ReadFile(m)
			srcs[filepath.Base(m)] = b
		}
		return srcs
	}
	entries, _ := os.ReadDir(filepath.Join(root, "testdata", "run"))
	for _, e := range entries {
		path := filepath.Join(root, "testdata", "run", e.Name())
		switch {
		case e.IsDir():
			add(e.Name(), path, filepath.Join(path, e.Name()), dirSrcs(path))
		case strings.HasSuffix(e.Name(), ".rdm"):
			name := strings.TrimSuffix(e.Name(), ".rdm")
			b, _ := os.ReadFile(path)
			add(name, path, strings.TrimSuffix(path, ".rdm"), map[string][]byte{"main.rdm": b})
		}
	}
	examples, _ := os.ReadDir(filepath.Join(root, "examples"))
	for _, e := range examples {
		if e.IsDir() {
			dir := filepath.Join(root, "examples", e.Name())
			add("example_"+e.Name(), dir, filepath.Join(dir, e.Name()), dirSrcs(dir))
		}
	}
	return ps
}

// TestRun builds every run test and example, checks the generated Go with go
// vet, runs each binary and compares its standard output and exit status.
// Each runs in a new empty directory, with only the environment of its .env
// file, so that what it does with files and the environment is the same on
// every machine.
func TestRun(t *testing.T) {
	ps := runPrograms(t)
	for _, required := range []string{"example_hello", "example_shapes", "example_parse", "example_events", "example_files"} {
		found := false
		for _, p := range ps {
			found = found || p.name == required
		}
		if !found {
			t.Errorf("missing required run test %s", required)
		}
	}
	tmp := t.TempDir()
	gen := filepath.Join(tmp, "gen")
	built := map[string]bool{}
	for _, p := range ps {
		srcDir := filepath.Join(tmp, "src", p.name)
		os.MkdirAll(srcDir, 0o755)
		srcs := map[string][]byte{}
		for name, b := range p.srcs {
			path := filepath.Join(srcDir, name)
			os.WriteFile(path, b, 0o644)
			srcs[path] = b
		}
		u := FromSources(srcDir, srcs)
		if ds := u.Check(); diag.HasErrors(ds) {
			var b bytes.Buffer
			diag.WriteText(&b, ds, u.Fset.LineSource())
			t.Errorf("%s (%s): unexpected errors:\n%s", p.name, p.where, b.String())
			continue
		}
		out := filepath.Join(gen, p.name)
		files, err := emit.Program(u.Prog, func(s *syntax.Source) string {
			rel, _ := filepath.Rel(out, s.Name)
			return filepath.ToSlash(rel)
		})
		if err != nil {
			t.Errorf("%s: %v", p.name, err)
			continue
		}
		os.MkdirAll(out, 0o755)
		for name, b := range files {
			os.WriteFile(filepath.Join(out, name), b, 0o644)
		}
		built[p.name] = true
	}
	if len(built) == 0 {
		t.Fatal("nothing to build")
	}
	os.WriteFile(filepath.Join(gen, "go.mod"), []byte("module runtests\n\ngo 1.27\n"), 0o644)
	goCmd := func(args ...string) {
		cmd := exec.Command("go", args...)
		cmd.Dir = gen
		cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, b)
		}
	}
	goCmd("vet", "./...")
	goCmd("build", "-o", filepath.Join(tmp, "bin")+string(filepath.Separator), "./...")
	for _, p := range ps {
		if !built[p.name] {
			continue
		}
		t.Run(p.name, func(t *testing.T) {
			t.Parallel()
			cmd := exec.Command(filepath.Join(tmp, "bin", p.name), p.args...)
			cmd.Dir = t.TempDir()
			cmd.Env = append([]string{}, p.env...)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			code := 0
			if e, ok := err.(*exec.ExitError); ok {
				code = e.ExitCode()
			} else if err != nil {
				t.Fatal(err)
			}
			if stdout.String() != p.out {
				t.Errorf("%s: stdout differs\n--- got\n%s--- want\n%s--- stderr\n%s", p.where, stdout.String(), p.out, stderr.String())
			}
			if code != p.exit {
				t.Errorf("%s: exit status %d, want %d\nstderr:\n%s", p.where, code, p.exit, stderr.String())
			}
		})
	}
}

// TestGoErrors checks that a Go compile error is reported as R0900 at the
// .rdm line its //line directive names.
func TestGoErrors(t *testing.T) {
	tmp := t.TempDir()
	srcPath := filepath.Join(tmp, "main.rdm")
	src := []byte("fn main() {\n    let x = 1\n}\n")
	os.WriteFile(srcPath, src, 0o644)
	u := FromSources(tmp, map[string][]byte{srcPath: src})
	gen := GenDir(tmp)
	os.MkdirAll(gen, 0o755)
	os.WriteFile(filepath.Join(gen, "go.mod"), []byte(GoMod), 0o644)
	os.WriteFile(filepath.Join(gen, "main.rdm.go"), []byte("package main\n\nfunc main() {\n//line ../../main.rdm:2\n\tvar x int = \"one\"\n\t_ = x\n}\n"), 0o644)
	out, err := GoBuild(gen, filepath.Join(tmp, "prog"))
	if err == nil {
		t.Fatal("broken Go built")
	}
	ds := u.goErrors(gen, out)
	if len(ds) == 0 || ds[0].Code != "R0900" || ds[0].File != srcPath || ds[0].Line != 2 {
		t.Fatalf("got %+v from\n%s", ds, out)
	}
}
