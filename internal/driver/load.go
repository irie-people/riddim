// Package driver runs the pipeline: load, parse, check, lower to Go, build
// and run (DESIGN §3.2).
package driver

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/riddim-hq/riddim/internal/check"
	"github.com/riddim-hq/riddim/internal/diag"
	"github.com/riddim-hq/riddim/internal/syntax"
)

// A Unit is one program: the .rdm files of a directory.
type Unit struct {
	Dir   string
	Fset  *syntax.FileSet
	Files []*syntax.File
	Prog  *check.Program
	Diags []diag.Diagnostic
}

// Load reads the .rdm files of dir. File names in diagnostics are dir joined
// with the file name, so they are relative to the working directory when dir
// is.
func Load(dir string) (*Unit, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".rdm") {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("no .rdm files in %s", dir)
	}
	sort.Strings(names)
	srcs := map[string][]byte{}
	for _, n := range names {
		b, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			return nil, err
		}
		srcs[filepath.Join(dir, n)] = b
	}
	return FromSources(dir, srcs), nil
}

// FromSources makes a unit from file contents keyed by path.
func FromSources(dir string, srcs map[string][]byte) *Unit {
	u := &Unit{Dir: dir, Fset: syntax.NewFileSet()}
	var names []string
	for n := range srcs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		u.Fset.Add(n, srcs[n])
	}
	return u
}

// Check parses and checks the unit. It stops after the first stage that
// reports errors: syntax, then names and types, then patterns. The Rust and
// Go habits (DESIGN §7.2) do not stop it: the parser and the checker read on
// as if their fixes were made, so one round reports every habit. When a
// syntax error stops it after parsing, the token pre-scan's reports stand in
// for the habits the parser did not reach and the checker would have found.
func (u *Unit) Check() []diag.Diagnostic {
	var ds, habits []diag.Diagnostic
	u.Files = nil
	complete := true
	for _, src := range u.Fset.Files() {
		f, fd := syntax.Parse(u.Fset, src)
		u.Files = append(u.Files, f)
		ds = append(ds, fd...)
		habits = append(habits, f.Habits...)
		complete = complete && f.Complete
	}
	if !complete {
		ds = addHabits(ds, habits)
		diag.Sort(ds)
		u.Diags = ds
		return ds
	}
	prog, cd := check.Check(u.Fset, u.Files)
	u.Prog = prog
	if diag.HasErrors(ds) {
		// Unused bindings wait until there are no errors.
		for _, d := range cd {
			if d.Code != "W0001" {
				ds = append(ds, d)
			}
		}
	} else {
		ds = append(ds, cd...)
	}
	diag.Sort(ds)
	u.Diags = ds
	return ds
}

// addHabits adds to ds each habit that ds does not already report: the same
// code at the same place.
func addHabits(ds, habits []diag.Diagnostic) []diag.Diagnostic {
	type key struct {
		code, file string
		line, col  int
	}
	seen := map[key]bool{}
	for _, d := range ds {
		seen[key{d.Code, d.File, d.Line, d.Col}] = true
	}
	for _, h := range habits {
		k := key{h.Code, h.File, h.Line, h.Col}
		if !seen[k] {
			seen[k] = true
			ds = append(ds, h)
		}
	}
	return ds
}
