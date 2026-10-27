package check

import (
	"go/token"
	"go/types"
	"strings"
	"testing"
)

// TestGoSignature checks the mapping of Go signatures to Riddim types
// (DESIGN §5.3), with signatures made for the test: the allowed packages
// have no function that returns only an error, or is generic.
func TestGoSignature(t *testing.T) {
	v := func(name string, t types.Type) *types.Var { return types.NewParam(token.NoPos, nil, name, t) }
	tuple := func(vs ...*types.Var) *types.Tuple { return types.NewTuple(vs...) }
	basic := func(k types.BasicKind) types.Type { return types.Typ[k] }
	errT := types.Universe.Lookup("error").Type()
	rune := types.Universe.Lookup("rune").Type()
	byteT := types.Universe.Lookup("byte").Type()
	tp := types.NewTypeParam(types.NewTypeName(token.NoPos, nil, "T", nil), types.NewInterfaceType(nil, nil))
	tests := []struct {
		sig  *types.Signature
		want string // the Riddim type, or what the refusal says
	}{
		{types.NewSignatureType(nil, nil, nil, tuple(v("s", basic(types.String))), tuple(v("", types.NewSlice(basic(types.String)))), false), "fn(String) -> List<String>"},
		{types.NewSignatureType(nil, nil, nil, tuple(v("path", basic(types.String))), tuple(v("", errT)), false), "fn(String) -> Result<(), Error>"},
		{types.NewSignatureType(nil, nil, nil, tuple(v("r", rune), v("b", byteT)), tuple(v("", basic(types.Float64)), v("", errT)), false), "fn(Int, Byte) -> Result<Float, Error>"},
		{types.NewSignatureType(nil, nil, nil, tuple(v("xs", types.NewSlice(types.NewSlice(rune)))), tuple(v("", basic(types.Bool))), false), "fn(List<List<Int>>) -> Bool"},
		{types.NewSignatureType(nil, nil, nil, nil, tuple(v("", basic(types.Int))), false), "fn() -> Int"},
		{types.NewSignatureType(nil, nil, nil, tuple(v("s", basic(types.String))), nil, false), "returns nothing"},
		{types.NewSignatureType(nil, nil, nil, tuple(v("xs", types.NewSlice(basic(types.String)))), tuple(v("", basic(types.Int))), true), "variable number of arguments"},
		{types.NewSignatureType(nil, nil, []*types.TypeParam{tp}, tuple(v("x", tp)), tuple(v("", tp)), false), "generic"},
		{types.NewSignatureType(nil, nil, nil, tuple(v("n", basic(types.Int64))), tuple(v("", basic(types.Int))), false), "parameter n is an int64"},
		{types.NewSignatureType(nil, nil, nil, nil, tuple(v("", basic(types.Float32))), false), "result is a float32"},
		{types.NewSignatureType(nil, nil, nil, nil, tuple(v("a", basic(types.Int)), v("b", basic(types.Bool))), false), "returns 2 results"},
	}
	for _, tt := range tests {
		gf, f, why := GoSignature(tt.sig)
		got := why
		if gf != nil {
			got = f.String()
		}
		if !strings.Contains(got, tt.want) {
			t.Errorf("GoSignature(%s) = %q, want %q", tt.sig, got, tt.want)
		}
	}
}
