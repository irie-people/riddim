// Package gointerop reads the Go packages that `use go` imports, and keeps
// the list of those v0 allows (DESIGN §5.3).
package gointerop

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/constant"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"strings"
	"sync"
)

// allowed are the Go packages v0 lets a program use: none of them can reach
// the world outside the program.
var allowed = []string{"strings", "strconv", "math", "unicode", "unicode/utf8"}

// Allowed reports whether `use go` may import path.
func Allowed(path string) bool {
	for _, a := range allowed {
		if path == a {
			return true
		}
	}
	return false
}

// PathOf returns the path of the allowed package called name, or "".
func PathOf(name string) string {
	for _, a := range allowed {
		if a == name || strings.HasSuffix(a, "/"+name) {
			return a
		}
	}
	return ""
}

// AllowedList names the allowed packages, for messages.
func AllowedList() string {
	q := make([]string, len(allowed))
	for i, a := range allowed {
		q[i] = "`" + a + "`"
	}
	return strings.Join(q[:len(q)-1], ", ") + " and " + q[len(q)-1]
}

// instead says what to use in place of the Go packages agents reach for
// most: for a package that reaches outside the program, the capability that
// does it (DESIGN §5.3); for the others, Riddim's own way.
var instead = map[string]string{
	"os":            "use the Fs capability for files, Env for the environment and arguments, and Out (`sys.out`, `sys.err`) for output",
	"io/ioutil":     "use the Fs capability for files",
	"io/fs":         "use the Fs capability for files",
	"path/filepath": "use the Fs capability for files; join paths with `+`",
	"io":            "read and write files with the Fs capability, and print with Out",
	"bufio":         "read files with the Fs capability, and split the text with `lines()`",
	"fmt":           "print with the Out capability, as in `sys.out.println(\"n = ${n}\")`, and build text with interpolation",
	"log":           "print with the Out capability: `sys.err.println(...)`",
	"log/slog":      "print with the Out capability: `sys.err.println(...)`",
	"time":          "read the time with the Clock capability: `sys.clock.now_ms()`",
	"os/exec":       "running other programs needs the Proc capability, which comes in v0.2",
	"os/signal":     "signals need the Proc capability, which comes in v0.2",
	"syscall":       "use the capabilities main receives in `sys: Sys`",
	"math/rand":     "v0 has no capability for random numbers",
	"math/rand/v2":  "v0 has no capability for random numbers",
	"crypto/rand":   "v0 has no capability for random numbers",
	"sort":          "a List of Int, Float or String has `sort()`",
	"slices":        "use the List methods, such as `sort()`, `contains(x)` and `index_of(x)`",
	"maps":          "use the Map methods, such as `keys()` and `values()`",
	"errors":        "make an Error with `error(msg)` and wrap one with `e.context(msg)`",
	"bytes":         "work on Strings with their methods and the `strings` package",
	"sync":          "concurrency comes in v0.3",
	"sync/atomic":   "concurrency comes in v0.3",
	"context":       "concurrency comes in v0.3",
	"unsafe":        "Riddim has no pointers",
	"reflect":       "Riddim has no reflection",
	"runtime":       "Riddim's runtime is not a package",
}

// Refusal explains why `use go` may not import path (R0601), naming the
// capability to use instead where the package reaches outside the program.
func Refusal(path string) string {
	msg := fmt.Sprintf("Go package `%s` is not allowed in v0", path)
	first, _, _ := strings.Cut(path, "/")
	switch {
	case instead[path] != "":
		msg += ": " + instead[path]
	case path == "net" || strings.HasPrefix(path, "net/"):
		msg += ": the network needs the Net capability, which comes in v0.2"
	case strings.Contains(first, "."):
		msg += ": third-party Go modules come in v0.2"
	}
	return msg
}

var (
	mu       sync.Mutex
	rooted   bool // findGoroot has succeeded
	fset     = token.NewFileSet()
	whole    types.Importer // go/importer in source mode, for a package readShallow cannot read
	packages = map[string]*types.Package{}
	errs     = map[string]error{}
)

// Import reads the exported API of an allowed package from the source of the
// Go installation (DESIGN §3.3). It reads the package and the packages it
// imports, but not theirs (readShallow); should that not be enough for what
// Riddim uses of the package, it reads the whole import tree with
// go/importer in source mode. Packages are read once per process.
func Import(path string) (*types.Package, error) {
	if !Allowed(path) {
		return nil, fmt.Errorf("package %q is not allowed", path)
	}
	mu.Lock()
	defer mu.Unlock()
	if p := packages[path]; p != nil {
		return p, nil
	}
	if err := errs[path]; err != nil {
		return nil, err
	}
	if !rooted {
		if err := findGoroot(); err != nil {
			errs[path] = err
			return nil, err
		}
		rooted = true
	}
	p, err := readShallow(fset, path)
	if err != nil || !complete(p) {
		if whole == nil {
			whole = importer.ForCompiler(fset, "source", nil)
		}
		p, err = whole.Import(path)
	}
	if err != nil {
		err = fmt.Errorf("cannot read Go package %q from %s: %v", path, build.Default.GOROOT, err)
		errs[path] = err
		return nil, err
	}
	packages[path] = p
	return p, nil
}

// findGoroot makes sure go/build knows where the Go installation is. A
// binary built with -trimpath does not record it, so ask the go command, as
// the documentation of runtime.GOROOT advises.
func findGoroot() error {
	if root := build.Default.GOROOT; root != "" {
		if fi, err := os.Stat(filepath.Join(root, "src")); err == nil && fi.IsDir() {
			return nil
		}
	}
	out, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		return fmt.Errorf("cannot find the Go installation to read Go packages from: go env GOROOT: %v", err)
	}
	build.Default.GOROOT = strings.TrimSpace(string(out))
	return nil
}

// readShallow reads the package path from the source of the Go installation
// as go/importer's source mode does, with function bodies ignored, but reads
// in full only the package and the packages it imports: the packages that
// those import are left empty. Reading the whole import tree, down to
// runtime, costs several times as much, while what Riddim uses of a package,
// the signatures of its functions and its constants, rarely names more than
// its own imports; complete checks that it does not.
//
// Each package is read on its own, so what is read does not depend on which
// packages a program used before. The packages are found and parsed in
// parallel, as go/importer parses the files of a package.
func readShallow(fset *token.FileSet, path string) (*types.Package, error) {
	bp, err := build.Default.Import(path, "", 0)
	if err != nil {
		return nil, err
	}
	paths := []string{path}
	for _, imp := range bp.Imports {
		if imp != "unsafe" {
			paths = append(paths, imp)
		}
	}
	files := make([][]*ast.File, len(paths))
	failed := make([]error, len(paths))
	var wg sync.WaitGroup
	for i, p := range paths {
		wg.Go(func() {
			pkg := bp
			if i > 0 {
				// Found from the directory of path, as the compiler finds imports.
				if pkg, failed[i] = build.Default.Import(p, bp.Dir, 0); failed[i] != nil {
					return
				}
			}
			files[i], failed[i] = parseFiles(fset, pkg)
		})
	}
	wg.Wait()
	s := &shallow{
		fset:  fset,
		sizes: types.SizesFor(build.Default.Compiler, build.Default.GOARCH),
		files: map[string][]*ast.File{},
		pkgs:  map[string]*types.Package{},
	}
	for i, p := range paths {
		if failed[i] != nil {
			return nil, failed[i]
		}
		s.files[p] = files[i]
	}
	var first error
	conf := types.Config{
		IgnoreFuncBodies: true,
		Importer:         s,
		Sizes:            s.sizes,
		Error: func(err error) {
			if first == nil && !err.(types.Error).Soft {
				first = err
			}
		},
	}
	pkg, _ := conf.Check(path, fset, files[0], nil)
	if first != nil {
		return nil, first
	}
	return pkg, nil
}

// parseFiles parses the Go files of a package in parallel. A package that
// uses cgo is refused: go/importer runs cgo to read it.
func parseFiles(fset *token.FileSet, bp *build.Package) ([]*ast.File, error) {
	if len(bp.CgoFiles) > 0 {
		return nil, fmt.Errorf("package %q uses cgo", bp.ImportPath)
	}
	files := make([]*ast.File, len(bp.GoFiles))
	failed := make([]error, len(bp.GoFiles))
	var wg sync.WaitGroup
	for i, name := range bp.GoFiles {
		wg.Go(func() {
			files[i], failed[i] = parser.ParseFile(fset, filepath.Join(bp.Dir, name), nil, parser.SkipObjectResolution)
		})
	}
	wg.Wait()
	for _, err := range failed {
		if err != nil {
			return nil, err
		}
	}
	return files, nil
}

// shallow is the importer of readShallow: it type-checks a package that was
// parsed, the one asked for or one of its imports, and leaves any other
// package empty. The errors of an import are dropped, since they come from
// the empty packages it imports.
type shallow struct {
	fset  *token.FileSet
	sizes types.Sizes
	files map[string][]*ast.File    // by import path
	pkgs  map[string]*types.Package // the packages checked or left empty
}

func (s *shallow) Import(path string) (*types.Package, error) {
	if path == "unsafe" {
		return types.Unsafe, nil
	}
	if pkg := s.pkgs[path]; pkg != nil {
		return pkg, nil
	}
	var pkg *types.Package
	if files, ok := s.files[path]; ok {
		conf := types.Config{IgnoreFuncBodies: true, Importer: s, Sizes: s.sizes, Error: func(error) {}}
		pkg, _ = conf.Check(path, s.fset, files, nil)
	} else {
		pkg = types.NewPackage(path, pathpkg.Base(path))
		pkg.MarkComplete()
	}
	s.pkgs[path] = pkg
	return pkg, nil
}

// complete reports whether what Riddim uses of pkg (DESIGN §5.3), the
// signatures of its exported functions and its exported constants, was read
// as the whole import tree would read it. A type or constant of a package
// that readShallow left empty is invalid, and a constant that depends on one
// has no value.
func complete(pkg *types.Package) bool {
	scope := pkg.Scope()
	for _, name := range scope.Names() {
		switch obj := scope.Lookup(name).(type) {
		case *types.Func:
			if obj.Exported() && invalid(obj.Type()) {
				return false
			}
		case *types.Const:
			if obj.Exported() && (invalid(obj.Type()) || obj.Val().Kind() == constant.Unknown) {
				return false
			}
		}
	}
	return true
}

// invalid reports whether t is or holds an invalid type. It looks through
// aliases, as Riddim does, but not into a named type, which Riddim knows by
// its name.
func invalid(t types.Type) bool {
	switch t := types.Unalias(t).(type) {
	case *types.Basic:
		return t.Kind() == types.Invalid
	case *types.Pointer:
		return invalid(t.Elem())
	case *types.Slice:
		return invalid(t.Elem())
	case *types.Array:
		return t.Len() < 0 || invalid(t.Elem())
	case *types.Map:
		return invalid(t.Key()) || invalid(t.Elem())
	case *types.Chan:
		return invalid(t.Elem())
	case *types.Signature:
		return invalid(t.Params()) || invalid(t.Results())
	case *types.Tuple:
		for v := range t.Variables() {
			if invalid(v.Type()) {
				return true
			}
		}
	case *types.Struct:
		for f := range t.Fields() {
			if invalid(f.Type()) {
				return true
			}
		}
	case *types.Named:
		for a := range t.TypeArgs().Types() {
			if invalid(a) {
				return true
			}
		}
	}
	return false
}
