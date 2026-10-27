package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/riddim-hq/riddim/internal/gen"
)

// TestCheckSpeed is the speed check of milestone M4: `riddim check` on a
// generated 10,000-line program finishes in under 200 ms. It times the
// riddim command itself, a new process each time, so the time includes
// starting it and reading the Go package the program uses; the best of
// three runs is taken. The race detector slows everything down, so the test
// does not run under it.
func TestCheckSpeed(t *testing.T) {
	if raceEnabled || testing.Short() {
		t.Skip("timing")
	}
	bin := filepath.Join(t.TempDir(), "riddim")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.rdm"), gen.Program(10000), 0o644); err != nil {
		t.Fatal(err)
	}
	best := time.Duration(1 << 62)
	for range 3 {
		start := time.Now()
		out, err := exec.Command(bin, "check", dir).CombinedOutput()
		best = min(best, time.Since(start))
		if err != nil {
			t.Fatalf("riddim check: %v\n%s", err, out)
		}
	}
	t.Logf("riddim check on 10,000 lines: %v", best)
	if best > 200*time.Millisecond {
		t.Errorf("riddim check on 10,000 lines took %v; the limit is 200 ms", best)
	}
}
