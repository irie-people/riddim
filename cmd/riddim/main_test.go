package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func riddim(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errb bytes.Buffer
	code = run(args, strings.NewReader(""), &out, &errb)
	return out.String(), errb.String(), code
}

func writeProgram(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.rdm"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestRunHello is the first check of milestone M1.
func TestRunHello(t *testing.T) {
	t.Chdir("../..")
	out, errs, code := riddim(t, "run", "examples/hello")
	if out != "hello, world\n" || code != 0 {
		t.Errorf("riddim run examples/hello: stdout %q, exit %d\n%s", out, code, errs)
	}
}

func TestVersion(t *testing.T) {
	out, _, code := riddim(t, "version")
	if out != "riddim "+Version+"\n" || code != 0 {
		t.Errorf("got %q, exit %d", out, code)
	}
}

func TestCheck(t *testing.T) {
	clean := writeProgram(t, "fn main(sys: Sys) {\n    sys.out.println(\"hi\")\n}\n")
	if out, _, code := riddim(t, "check", clean, "--json"); out != "[]\n" || code != 0 {
		t.Errorf("clean --json: %q, exit %d", out, code)
	}
	if out, errs, code := riddim(t, "check", clean); out != "" || errs != "" || code != 0 {
		t.Errorf("clean: %q %q, exit %d", out, errs, code)
	}
	bad := writeProgram(t, "enum E { A, B }\n\nfn main() {\n    let x = match E::A {\n        A => 1,\n    }\n    let _ = x\n}\n")
	_, errs, code := riddim(t, "check", bad)
	if code != 1 || !strings.Contains(errs, "error[R0401]: match on E is missing 1 case: B") ||
		!strings.Contains(errs, "= fix: add the missing arm: `B => todo(),`") {
		t.Errorf("exit %d, stderr:\n%s", code, errs)
	}
	out, _, code := riddim(t, "check", "--json", bad)
	if code != 1 || !strings.HasPrefix(out, "[\n{\"code\":\"R0401\"") {
		t.Errorf("--json: exit %d\n%s", code, out)
	}
	warn := writeProgram(t, "fn main() {\n    todo()\n}\n")
	if _, errs, code := riddim(t, "check", warn); code != 0 || !strings.Contains(errs, "warning[W0003]") {
		t.Errorf("warnings only: exit %d\n%s", code, errs)
	}
}

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{{}, {"frobnicate"}, {"check", "--nope"}, {"check", "a", "b"}, {"check", t.TempDir()}} {
		if _, _, code := riddim(t, args...); code != 2 {
			t.Errorf("riddim %v: exit %d, want 2", args, code)
		}
	}
}

func TestBuildEmitRun(t *testing.T) {
	dir := writeProgram(t, "fn main(sys: Sys) -> Result<(), Error> {\n    sys.out.println(\"start\")\n    let n = \"x\".parse_int()?\n    sys.out.println(n.to_string())\n    Ok(())\n}\n")
	bin := filepath.Join(t.TempDir(), "prog")
	if _, errs, code := riddim(t, "build", dir, "-o", bin); code != 0 {
		t.Fatalf("build: exit %d\n%s", code, errs)
	}
	cmd := exec.Command(bin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if e, ok := err.(*exec.ExitError); !ok || e.ExitCode() != 1 {
		t.Errorf("binary: %v", err)
	}
	if stdout.String() != "start\n" || stderr.String() != "error: cannot parse \"x\" as Int\n" {
		t.Errorf("binary: stdout %q, stderr %q", stdout.String(), stderr.String())
	}
	out, errs, code := riddim(t, "run", dir, "--", "ignored")
	if out != "start\n" || code != 1 || !strings.Contains(errs, "error: cannot parse") {
		t.Errorf("run: %q %q exit %d", out, errs, code)
	}
	gen := filepath.Join(t.TempDir(), "gen")
	if _, errs, code := riddim(t, "emit", dir, "-o", gen); code != 0 {
		t.Fatalf("emit: exit %d\n%s", code, errs)
	}
	for _, f := range []string{"main.rdm.go", "riddim_rt.go", "riddim_main.go", "go.mod"} {
		if _, err := os.Stat(filepath.Join(gen, f)); err != nil {
			t.Errorf("emit did not write %s", f)
		}
	}
}
