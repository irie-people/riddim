package driver

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
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
	ds, _, err := u.emit(outDir)
	return ds, err
}

// emit is Emit, and returns the files it wrote, by name relative to outDir.
// A file whose content is unchanged is not written again, and Go files that
// are no longer generated are removed.
func (u *Unit) emit(outDir string) ([]diag.Diagnostic, map[string][]byte, error) {
	ds := u.Check()
	if diag.HasErrors(ds) {
		return ds, nil, nil
	}
	absOut, err := filepath.Abs(outDir)
	if err != nil {
		return ds, nil, err
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
		return append(ds, internalDiag(u, err.Error())), nil, nil
	}
	files["go.mod"] = []byte(GoMod)
	if err := writeFiles(outDir, files); err != nil {
		return ds, nil, err
	}
	return ds, files, nil
}

// writeFiles writes files into dir, leaving alone those whose content is
// already right, and removes the other Go files in dir and in its runtime
// directory rt.
func writeFiles(dir string, files map[string][]byte) error {
	for _, pattern := range []string{"*.go", filepath.Join("rt", "*.go")} {
		old, _ := filepath.Glob(filepath.Join(dir, pattern))
		for _, f := range old {
			rel, _ := filepath.Rel(dir, f)
			if _, ok := files[filepath.ToSlash(rel)]; !ok {
				if err := os.Remove(f); err != nil {
					return err
				}
			}
		}
	}
	for name, src := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, src) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, src, 0o644); err != nil {
			return err
		}
	}
	return nil
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
// When the Go is what the last build of out built, with the same go command
// and settings, and out is still the binary it made, go build is skipped.
func (u *Unit) Build(out string) ([]diag.Diagnostic, error) {
	gen := GenDir(u.Dir)
	ds, files, err := u.emit(gen)
	if err != nil || diag.HasErrors(ds) {
		return ds, err
	}
	absOut, err := filepath.Abs(out)
	if err != nil {
		return ds, err
	}
	stampFile := filepath.Join(filepath.Dir(gen), "build.stamp")
	stamp := buildStamp(files, absOut)
	if old, err := os.ReadFile(stampFile); err == nil && string(old) == stamp+binaryID(absOut) {
		return ds, nil
	}
	os.Remove(stampFile)
	output, err := goBuild(gen, absOut)
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return append(ds, u.goErrors(gen, output)...), nil
		}
		return ds, err
	}
	if id := binaryID(absOut); id != "" {
		os.WriteFile(stampFile, []byte(stamp+id), 0o644)
	}
	return ds, nil
}

// goBuild is GoBuild; tests count its calls.
var goBuild = GoBuild

// buildEnv are the variables that change what go build makes.
var buildEnv = []string{"GOOS", "GOARCH", "GOFLAGS", "CGO_ENABLED", "GOEXPERIMENT", "GOTOOLCHAIN",
	"GOROOT", "GOAMD64", "GOARM", "GOARM64", "GO386", "GOMIPS", "GOPPC64", "GORISCV64", "GOWASM", "CC"}

// buildStamp identifies a build: the generated files, the binary's path,
// the go command and the variables that change its output.
func buildStamp(files map[string][]byte, out string) string {
	h := sha256.New()
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(h, "%s %d\n", n, len(files[n]))
		h.Write(files[n])
	}
	fmt.Fprintf(h, "out %s\n", out)
	if path, err := exec.LookPath("go"); err == nil {
		if real, err := filepath.EvalSymlinks(path); err == nil {
			path = real
		}
		fmt.Fprintf(h, "go %s %s\n", path, fileID(path))
	}
	for _, v := range buildEnv {
		fmt.Fprintf(h, "%s=%s\n", v, os.Getenv(v))
	}
	return fmt.Sprintf("%x\n", h.Sum(nil))
}

// binaryID identifies the binary at path by its size and time, so that a
// stamp no longer matches once it is removed or replaced.
func binaryID(path string) string { return fileID(path) }

func fileID(path string) string {
	fi, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d %d\n", fi.Size(), fi.ModTime().UnixNano())
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
