package driver

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestBuildSkips checks that riddim build skips go build when the generated
// Go, the binary and the go command are as the last build left them, and
// builds again when any of them changes.
func TestBuildSkips(t *testing.T) {
	builds := 0
	goBuild = func(dir, out string) (string, error) {
		builds++
		return GoBuild(dir, out)
	}
	defer func() { goBuild = GoBuild }()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.rdm")
	write := func(text string) {
		if err := os.WriteFile(src, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	bin := filepath.Join(t.TempDir(), "prog")
	build := func(out string, want int, what string) {
		t.Helper()
		u, err := Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		if ds, err := u.Build(out); err != nil || len(ds) > 0 {
			t.Fatalf("%s: %v %s", what, err, summary(ds))
		}
		if builds != want {
			t.Errorf("%s: %d go builds, want %d", what, builds, want)
		}
		if _, err := os.Stat(out); err != nil {
			t.Errorf("%s: %v", what, err)
		}
	}
	write("fn main(sys: Sys) {\n    sys.out.println(\"one\")\n}\n")
	build(bin, 1, "first build")
	build(bin, 1, "unchanged")
	write("fn main(sys: Sys) {\n    sys.out.println(\"one\")\n}\n\n// a comment changes no Go\n")
	build(bin, 1, "a comment")
	write("fn main(sys: Sys) {\n    sys.out.println(\"two\")\n}\n")
	build(bin, 2, "an edit")
	os.Remove(bin)
	build(bin, 3, "binary removed")
	os.Chtimes(bin, time.Now(), time.Now().Add(time.Hour))
	build(bin, 4, "binary touched")
	other := filepath.Join(t.TempDir(), "other")
	build(other, 5, "another output")
	t.Setenv("CGO_ENABLED", "0")
	build(other, 6, "other CGO_ENABLED")
}
