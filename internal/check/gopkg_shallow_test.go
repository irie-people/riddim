package check

import (
	"fmt"
	"go/importer"
	"go/token"
	"go/types"
	"slices"
	"testing"

	"github.com/irie-people/riddim/internal/gointerop"
)

// TestShallowImportMatchesSource checks that the packages of `use go`, which
// gointerop reads with only their own imports, look to Riddim exactly as
// they do when go/importer reads their whole import tree: the same exported
// names, and for each function and constant the same signature Riddim
// accepts, refusal message or value. gointerop's TestReadShallow checks
// that Import uses that read and not the whole tree.
func TestShallowImportMatchesSource(t *testing.T) {
	source := importer.ForCompiler(token.NewFileSet(), "source", nil)
	for _, path := range []string{"strings", "strconv", "math", "unicode", "unicode/utf8"} {
		shallow, err := gointerop.Import(path)
		if err != nil {
			t.Fatal(err)
		}
		whole, err := source.Import(path)
		if err != nil {
			t.Fatal(err)
		}
		got, want := apiOf(shallow), apiOf(whole)
		for _, d := range got {
			if !slices.Contains(want, d) {
				t.Errorf("%s, read with its imports only: %s", path, d)
			}
		}
		for _, d := range want {
			if !slices.Contains(got, d) {
				t.Errorf("%s, read with go/importer: %s", path, d)
			}
		}
	}
}

// apiOf writes what Riddim can use of a Go package, one line per exported
// name: a function's Riddim signature or why it is refused, a constant's
// type and value. Of a type or a variable Riddim uses only the name.
func apiOf(pkg *types.Package) []string {
	var out []string
	for _, n := range pkg.Scope().Names() {
		obj := pkg.Scope().Lookup(n)
		if !obj.Exported() {
			continue
		}
		d := n + ": "
		switch o := obj.(type) {
		case *types.Func:
			gf, f, why := GoSignature(o.Type().(*types.Signature))
			d += fmt.Sprintf("func %q", why)
			if gf != nil {
				d += fmt.Sprintf(" %v %d %v %v", gf.Params, gf.Result, gf.Value, f.Result)
			}
		case *types.Const:
			d += fmt.Sprintf("const %s %s", o.Type(), o.Val().ExactString())
		case *types.TypeName:
			d += "type"
		case *types.Var:
			d += "var"
		}
		out = append(out, d)
	}
	return out
}
