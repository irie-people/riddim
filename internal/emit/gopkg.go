package emit

import (
	"fmt"
	"go/types"
	"strings"

	"github.com/riddim-hq/riddim/internal/check"
	"github.com/riddim-hq/riddim/internal/syntax"
)

// Go packages (DESIGN §5.3): a call converts each argument to the Go
// function's parameter type and the result back to Riddim's. Lists are
// copied both ways, so a Go function never holds a Riddim list's storage
// (DESIGN §4.8), and a Go rune is Riddim's Int.

// goCall lowers a call of a Go function.
func (f *fn) goCall(args []syntax.Expr, gu *check.GoUse) value {
	vals, never := f.list(args)
	if never {
		return value{never: true}
	}
	f.imports[gu.Pkg.Path] = true
	conv := make([]string, len(vals))
	for i, v := range vals {
		conv[i] = toGo(v.s, gu.Func.Params[i])
	}
	call := gu.Pkg.Name + "." + gu.Name + "(" + strings.Join(conv, ", ") + ")"
	switch gf := gu.Func; gf.Result {
	case check.GoErr:
		return value{s: "goError(" + call + ")"}
	case check.GoValueErr:
		if !needsConv(gf.Value) {
			return value{s: "goResult(" + call + ")"}
		}
		v, err := f.temp("v"), f.temp("err")
		f.printf("%s, %s := %s\n", v, err, call)
		return value{s: "goResult(" + fromGo(v, gf.Value) + ", " + err + ")"}
	}
	return value{s: fromGo(call, gu.Func.Value)}
}

// goFuncValue lowers a Go function used as a value. When Riddim's function
// type lowers to the Go function's own type, it is the Go function itself;
// otherwise a function literal converts on the way in and out.
func (f *fn) goFuncValue(gu *check.GoUse) value {
	f.imports[gu.Pkg.Path] = true
	name := gu.Pkg.Name + "." + gu.Name
	gf := gu.Func
	direct := gf.Result == check.GoValue && !needsConv(gf.Value)
	for _, p := range gf.Params {
		direct = direct && !needsConv(p)
	}
	if direct {
		return value{s: name, stable: true}
	}
	ft := gu.Type.(*check.Func)
	var params, args []string
	for i, p := range gf.Params {
		params = append(params, fmt.Sprintf("p%d %s", i, f.goType(ft.Params[i])))
		args = append(args, toGo(fmt.Sprintf("p%d", i), p))
	}
	call := name + "(" + strings.Join(args, ", ") + ")"
	var body string
	switch {
	case gf.Result == check.GoErr:
		body = "return goError(" + call + ")"
	case gf.Result == check.GoValueErr && needsConv(gf.Value):
		body = "v, err := " + call + "\nreturn goResult(" + fromGo("v", gf.Value) + ", err)"
	case gf.Result == check.GoValueErr:
		body = "return goResult(" + call + ")"
	default:
		body = "return " + fromGo(call, gf.Value)
	}
	return value{s: fmt.Sprintf("func(%s) %s {\n%s\n}", strings.Join(params, ", "), f.goType(ft.Result), body), stable: true}
}

// goConst lowers a Go constant. One of rune type is converted to int, the
// Go type of Riddim's Int, so that `let r = utf8.RuneError` is an Int in Go
// too.
func (f *fn) goConst(gu *check.GoUse) value {
	f.imports[gu.Pkg.Path] = true
	s := gu.Pkg.Name + "." + gu.Name
	if isRune(gu.ConstType) {
		s = "int(" + s + ")"
	}
	return value{s: s, stable: true}
}

func isRune(t types.Type) bool {
	b, ok := types.Unalias(t).(*types.Basic)
	return ok && (b.Kind() == types.Int32 || b.Kind() == types.UntypedRune)
}

// needsConv reports whether a value of Go type t needs converting or copying
// on its way between Riddim and Go: a rune, or a list.
func needsConv(t types.Type) bool {
	if isRune(t) {
		return true
	}
	_, ok := types.Unalias(t).(*types.Slice)
	return ok
}

// toGo converts the Riddim value s to Go type t.
func toGo(s string, t types.Type) string {
	if isRune(t) {
		return "rune(" + s + ")"
	}
	sl, ok := types.Unalias(t).(*types.Slice)
	if !ok {
		return s
	}
	if !needsConv(sl.Elem()) {
		return "listCopy(" + s + ")"
	}
	return fmt.Sprintf("listMap(%s, func(x %s) %s {\nreturn %s\n})", s, riddimGo(sl.Elem()), goTypeName(sl.Elem()), toGo("x", sl.Elem()))
}

// fromGo converts the Go value s of type t to Riddim.
func fromGo(s string, t types.Type) string {
	if isRune(t) {
		return "int(" + s + ")"
	}
	sl, ok := types.Unalias(t).(*types.Slice)
	if !ok {
		return s
	}
	if !needsConv(sl.Elem()) {
		return "listCopy(" + s + ")"
	}
	return fmt.Sprintf("listMap(%s, func(x %s) %s {\nreturn %s\n})", s, goTypeName(sl.Elem()), riddimGo(sl.Elem()), fromGo("x", sl.Elem()))
}

// goTypeName writes a Go type as Go source.
func goTypeName(t types.Type) string { return types.TypeString(t, nil) }

// riddimGo returns the Go type of the Riddim type that the Go type t maps
// to: int for a rune, and the same type for the others.
func riddimGo(t types.Type) string {
	if isRune(t) {
		return "int"
	}
	if sl, ok := types.Unalias(t).(*types.Slice); ok {
		return "[]" + riddimGo(sl.Elem())
	}
	return goTypeName(t)
}
