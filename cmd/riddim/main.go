// Command riddim checks, builds and runs Riddim programs (DESIGN §3.1).
//
//	riddim check [dir] [--json]   check every .rdm file in dir
//	riddim build [dir] [-o out]   check, write Go into dir/.riddim/go, go build
//	riddim run [dir] [-- args]    build, then run with args
//	riddim emit [dir] [-o gen]    write the generated Go and stop
//	riddim version                print the version
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/riddim-hq/riddim/internal/diag"
	"github.com/riddim-hq/riddim/internal/driver"
)

// Version is the compiler's version.
const Version = "0.0.0-m1"

const usage = `usage:
  riddim check [dir] [--json]   check every .rdm file in dir (default .)
  riddim build [dir] [-o out]   check, write Go into dir/.riddim/go, then go build
  riddim run [dir] [-- args]    build, then run the program with args
  riddim emit [dir] [-o gen]    write the generated Go and stop
  riddim version                print the version
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run is the command line; it returns the exit status: 0 when there are no
// errors, 1 on errors, 2 on usage or internal errors. riddim run exits with
// the program's status.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "version":
		fmt.Fprintf(stdout, "riddim %s\n", Version)
		return 0
	case "help", "-h", "-help", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	case "check", "build", "run", "emit":
	default:
		fmt.Fprintf(stderr, "riddim: unknown command %q\n%s", cmd, usage)
		return 2
	}
	opts, err := parseArgs(cmd, rest)
	if err != nil {
		fmt.Fprintf(stderr, "riddim %s: %v\n%s", cmd, err, usage)
		return 2
	}
	u, err := driver.Load(opts.dir)
	if err != nil {
		fmt.Fprintf(stderr, "riddim %s: %v\n", cmd, err)
		return 2
	}
	report := func(ds []diag.Diagnostic) {
		if opts.json {
			diag.WriteJSON(stdout, ds)
		} else {
			diag.WriteText(stderr, ds, u.Fset.LineSource())
		}
	}
	var ds []diag.Diagnostic
	switch cmd {
	case "check":
		ds = u.Check()
	case "emit":
		out := opts.out
		if out == "" {
			out = driver.GenDir(opts.dir)
		}
		ds, err = u.Emit(out)
	case "build":
		out := opts.out
		if out == "" {
			out = driver.BinName(opts.dir)
		}
		ds, err = u.Build(out)
	case "run":
		var status int
		status, ds, err = u.Run(opts.progArgs, stdin, stdout, stderr)
		if err == nil && !diag.HasErrors(ds) {
			report(ds)
			return status
		}
	}
	if err != nil {
		report(ds)
		fmt.Fprintf(stderr, "riddim %s: %v\n", cmd, err)
		return 2
	}
	report(ds)
	if diag.HasErrors(ds) {
		return 1
	}
	return 0
}

type options struct {
	dir      string
	json     bool
	out      string
	progArgs []string
}

func parseArgs(cmd string, args []string) (options, error) {
	opts := options{dir: "."}
	dirSet := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--" && cmd == "run":
			opts.progArgs = args[i+1:]
			return opts, nil
		case (a == "--json" || a == "-json") && cmd == "check":
			opts.json = true
		case a == "-o" && (cmd == "build" || cmd == "emit"):
			if i+1 >= len(args) {
				return opts, fmt.Errorf("-o needs a path")
			}
			i++
			opts.out = args[i]
		case strings.HasPrefix(a, "-o=") && (cmd == "build" || cmd == "emit"):
			opts.out = strings.TrimPrefix(a, "-o=")
		case strings.HasPrefix(a, "-"):
			return opts, fmt.Errorf("unknown flag %q", a)
		case !dirSet:
			opts.dir = filepath.Clean(a)
			dirSet = true
		default:
			return opts, fmt.Errorf("unexpected argument %q", a)
		}
	}
	return opts, nil
}
