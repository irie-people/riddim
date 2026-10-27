// Package gointerop reads the Go packages that `use go` imports, and keeps
// the list of those v0 allows (DESIGN §5.3).
package gointerop

import (
	"fmt"
	"go/build"
	"go/importer"
	"go/token"
	"go/types"
	"os"
	"os/exec"
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
	imp      types.Importer
	packages = map[string]*types.Package{}
	errs     = map[string]error{}
)

// Import reads the exported API of an allowed package from the source of the
// Go installation, with go/importer in source mode (DESIGN §3.3). Packages
// are read once per process.
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
	if imp == nil {
		if err := findGoroot(); err != nil {
			errs[path] = err
			return nil, err
		}
		imp = importer.ForCompiler(token.NewFileSet(), "source", nil)
	}
	p, err := imp.Import(path)
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
