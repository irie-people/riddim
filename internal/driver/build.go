package driver

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/riddim-hq/riddim/internal/diag"
	"github.com/riddim-hq/riddim/internal/emit"
	"github.com/riddim-hq/riddim/internal/syntax"
)

// GoMod is the go.mod written next to the generated Go.
const GoMod = "module riddimprog\n\ngo 1.27\n"

// GenDir is where build and run write the generated Go for a program.
func GenDir(dir string) string { return filepath.Join(dir, ".riddim", "go") }

// Emit checks the unit and, when it has no errors, writes the generated Go,
// the runtime and a go.mod into outDir.
func (u *Unit) Emit(outDir string) ([]diag.Diagnostic, error) {
	ds := u.Check()
	if diag.HasErrors(ds) {
		return ds, nil
	}
	absOut, err := filepath.Abs(outDir)
	if err != nil {
		return ds, err
	}
	files, err := emit.Program(u.Prog, func(src *syntax.Source) string {
		abs, err := filepath.Abs(src.Name)
		if err != nil {
			return src.Name
		}
		rel, err := filepath.Rel(absOut, abs)
		if err != nil {
			return abs
		}
		return filepath.ToSlash(rel)
	})
	if err != nil {
		return append(ds, internalDiag(u, err.Error())), nil
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return ds, err
	}
	old, _ := filepath.Glob(filepath.Join(outDir, "*.go"))
	for _, f := range old {
		if err := os.Remove(f); err != nil {
			return ds, err
		}
	}
	files["go.mod"] = []byte(GoMod)
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(outDir, name), src, 0o644); err != nil {
			return ds, err
		}
	}
	return ds, nil
}

func internalDiag(u *Unit, msg string) diag.Diagnostic {
	file := ""
	if fs := u.Fset.Files(); len(fs) > 0 {
		file = fs[0].Name
	}
	return diag.Diagnostic{
		Code: "R0900", Severity: diag.Error, File: file, Line: 1, Col: 1, EndLine: 1, EndCol: 1,
		Message: "generated Go failed to compile, which is a Riddim bug: " + msg,
		Notes:   []string{"please report it with the program that caused it"}, Fixes: []diag.Fix{},
	}
}

// Build checks the unit, writes the Go into GenDir and builds the binary out.
func (u *Unit) Build(out string) ([]diag.Diagnostic, error) {
	gen := GenDir(u.Dir)
	ds, err := u.Emit(gen)
	if err != nil || diag.HasErrors(ds) {
		return ds, err
	}
	absOut, err := filepath.Abs(out)
	if err != nil {
		return ds, err
	}
	output, err := GoBuild(gen, absOut)
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return append(ds, u.goErrors(gen, output)...), nil
		}
		return ds, err
	}
	return ds, nil
}

// GoBuild runs `go build -trimpath -o out` in dir and returns its output.
func GoBuild(dir, out string) (string, error) {
	cmd := exec.Command("go", "build", "-trimpath", "-o", out, ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
	b, err := cmd.CombinedOutput()
	return string(b), err
}

var goErrLine = regexp.MustCompile(`^(.+?):(\d+)(?::(\d+))?: (.*)$`)

// goErrors turns go build's messages into R0900 diagnostics. Thanks to the
// //line directives most of them name .rdm lines.
func (u *Unit) goErrors(gen, output string) []diag.Diagnostic {
	var ds []diag.Diagnostic
	for _, line := range strings.Split(output, "\n") {
		m := goErrLine.FindStringSubmatch(line)
		if m == nil || strings.HasPrefix(line, "#") {
			continue
		}
		file := m[1]
		if !filepath.IsAbs(file) {
			file = filepath.Join(gen, file)
		}
		for _, src := range u.Fset.Files() {
			if a, b := absPath(src.Name), absPath(file); a == b {
				file = src.Name
			}
		}
		l, _ := strconv.Atoi(m[2])
		c, _ := strconv.Atoi(m[3])
		if c == 0 {
			c = 1
		}
		ds = append(ds, diag.Diagnostic{
			Code: "R0900", Severity: diag.Error, File: file, Line: l, Col: c, EndLine: l, EndCol: c + 1,
			Message: "generated Go failed to compile, which is a Riddim bug: " + m[4],
			Notes:   []string{"please report it with the program that caused it"}, Fixes: []diag.Fix{},
		})
	}
	if len(ds) == 0 {
		ds = append(ds, internalDiag(u, strings.TrimSpace(output)))
	}
	return ds
}

func absPath(p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return filepath.Clean(a)
}

// BinName is the default binary name for a program directory.
func BinName(dir string) string {
	return filepath.Base(absPath(dir))
}

// Run builds the unit into GenDir's parent and runs it with args. It returns
// the program's exit status.
func (u *Unit) Run(args []string, stdin io.Reader, stdout, stderr io.Writer) (int, []diag.Diagnostic, error) {
	bin := filepath.Join(u.Dir, ".riddim", "bin", BinName(u.Dir))
	ds, err := u.Build(bin)
	if err != nil || diag.HasErrors(ds) {
		return 1, ds, err
	}
	cmd := exec.Command(bin, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	err = cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0, ds, nil
	case errors.As(err, &exit):
		return exit.ExitCode(), ds, nil
	}
	return 1, ds, fmt.Errorf("running %s: %v", bin, err)
}
