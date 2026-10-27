package check

import (
	"fmt"
	"go/constant"
	"go/types"
	"math"
	"math/big"
	"strings"

	"github.com/riddim-hq/riddim/internal/gointerop"
	"github.com/riddim-hq/riddim/internal/syntax"
)

// Go packages (DESIGN §5.3). `use go "strings"` makes the package's name a
// name of the whole program, like a declaration, through which its functions
// are called and its constants read: `strings.Fields(s)`, `math.Pi`. A local
// of the same name hides it. Only functions and constants whose types map to
// Riddim's can be used.

// A GoPkg is a Go package imported with `use go`. Pkg is nil for a package
// that was refused: its name is still declared, so that its uses report
// nothing more.
type GoPkg struct {
	Name  string
	Path  string
	Pkg   *types.Package
	Ident *syntax.Ident // the package's name in the path of its first `use go`
}

// GoResult says how a Go function's results become a Riddim value.
type GoResult int

// The results Riddim takes from a Go function.
const (
	GoValue    GoResult = iota // T
	GoValueErr                 // (T, error): Result<T, Error>
	GoErr                      // error: Result<(), Error>
)

// A GoFunc is a Go function whose types map to Riddim's. The emitter
// converts each argument to Go's parameter type, copying lists, and the
// result back.
type GoFunc struct {
	Params []types.Type
	Result GoResult
	Value  types.Type // T, for GoValue and GoValueErr
}

// A GoUse is a member of a Go package that the program uses: a function,
// called or used as a value, or a constant.
type GoUse struct {
	Pkg       *GoPkg
	Name      string
	Type      Type    // the Riddim type: a function's *Func, or a constant's
	Func      *GoFunc // a function
	Const     *Const  // a constant's value
	ConstType types.Type
}

// goImport reads an allowed Go package; tests may replace it.
var goImport = gointerop.Import

var goErrorType = types.Universe.Lookup("error").Type()

// collectUses imports the packages of the `use go` declarations and declares
// their names. A package may be imported in several files.
func (c *checker) collectUses(files []*syntax.File) {
	byPath := map[string]bool{}
	for _, f := range files {
		for _, d := range f.Decls {
			u, ok := d.(*syntax.UseDecl)
			if !ok || byPath[u.Path] {
				continue
			}
			byPath[u.Path] = true
			var p *types.Package
			if !gointerop.Allowed(u.Path) {
				d := c.errorf("R0601", u.PathSpan, "%s", gointerop.Refusal(u.Path))
				d.Notes = append(d.Notes, "`use go` allows "+gointerop.AllowedList()+", which cannot reach outside the program")
			} else if pkg, err := goImport(u.Path); err != nil {
				d := c.errorf("R0601", u.PathSpan, "%v", err)
				d.Notes = append(d.Notes, "Riddim reads the packages of `use go` from the Go installation that `go env GOROOT` names")
			} else {
				p = pkg
			}
			name := pathName(u.Path)
			if p != nil {
				name = p.Name()
			}
			if name == "" {
				continue
			}
			// The name, inside the quotes of the path: "unicode/utf8" names utf8.
			pos := u.PathSpan.Start + 1
			if i := strings.LastIndex(u.Path, name); i >= 0 {
				pos += syntax.Pos(i)
			}
			gp := &GoPkg{Name: name, Path: u.Path, Pkg: p, Ident: &syntax.Ident{NamePos: pos, Name: name}}
			if prev := c.globals[gp.Name]; prev != nil {
				if p == nil {
					continue
				}
				c.errorf("R0202", gp.Ident.Span(), "the Go package `%s` has the name of the declaration at %s; rename the declaration", gp.Name, c.where(declIdent(prev)))
				continue
			}
			c.declareGlobal(gp.Ident, gp)
		}
	}
}

// pathName returns the name a Go package of path usually has: its last
// element, or the one before a major version such as v2. It is "" when that
// is not a name.
func pathName(path string) string {
	parts := strings.Split(path, "/")
	name := parts[len(parts)-1]
	if len(parts) > 1 && len(name) > 1 && name[0] == 'v' && strings.Trim(name[1:], "0123456789") == "" {
		name = parts[len(parts)-2]
	}
	for i, r := range name {
		if !(r == '_' || 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || i > 0 && '0' <= r && r <= '9') {
			return ""
		}
	}
	return name
}

// goPkg returns the Go package that x names, if x is a name that no local
// hides.
func (c *checker) goPkg(x syntax.Expr) *GoPkg {
	id, ok := x.(*syntax.Ident)
	if !ok || c.lookupLocal(id.Name) != nil {
		return nil
	}
	gp, _ := c.globals[id.Name].(*GoPkg)
	return gp
}

// goObject looks up an exported member of a Go package, reporting an
// unknown one (R0201).
func (c *checker) goObject(gp *GoPkg, name *syntax.Ident) types.Object {
	obj := gp.Pkg.Scope().Lookup(name.Name)
	if obj != nil && obj.Exported() {
		return obj
	}
	var d = c.errorf("R0201", name.Span(), "Go package %s has no `%s`", gp.Name, name.Name)
	if obj != nil {
		d.Message = fmt.Sprintf("`%s.%s` is not exported: Go exports only names that start with an uppercase letter", gp.Name, name.Name)
		return nil
	}
	var cands []string
	for _, n := range gp.Pkg.Scope().Names() {
		if gp.Pkg.Scope().Lookup(n).Exported() {
			cands = append(cands, n)
		}
	}
	if close := closest(name.Name, cands); len(close) > 0 {
		d.Message += "; did you mean " + orList(close) + "?"
	}
	return nil
}

// goCall checks a call of a Go function, `strings.Fields(s)`.
func (c *checker) goCall(e *syntax.MethodCallExpr, gp *GoPkg, want Type) Type {
	fail := func() Type {
		for _, a := range e.Args {
			c.expr(a, nil)
		}
		return invalid
	}
	if gp.Pkg == nil {
		return fail() // refused: reported at its `use go`
	}
	obj := c.goObject(gp, e.Name)
	if obj == nil {
		return fail()
	}
	fn, ok := obj.(*types.Func)
	if !ok {
		if k, ok := obj.(*types.Const); ok {
			c.errorf("R0301", e.Name.Span(), "`%s.%s` is a constant, not a function: write it without `(...)`", gp.Name, k.Name())
		} else {
			c.goRefused(gp, obj, e.Name.Span())
		}
		return fail()
	}
	gf, sig := c.goFunc(gp, fn, e.Name.Span())
	if gf == nil {
		return fail()
	}
	c.info.Go[e] = &GoUse{Pkg: gp, Name: fn.Name(), Type: sig, Func: gf}
	return c.callWith("`"+gp.Name+"."+fn.Name()+"`", e.Span(), e.Args, sig.Params, sig.Result, want)
}

// goMember checks a Go package's member that is not called: a constant,
// `math.Pi`, or a function used as a value, `xs.map(strings.ToUpper)`.
func (c *checker) goMember(e *syntax.FieldExpr, gp *GoPkg) Type {
	if gp.Pkg == nil {
		return invalid // refused: reported at its `use go`
	}
	obj := c.goObject(gp, e.Name)
	if obj == nil {
		return invalid
	}
	switch o := obj.(type) {
	case *types.Const:
		t, k := c.goConst(gp, o, e.Name.Span())
		if k == nil {
			return invalid
		}
		c.info.Go[e] = &GoUse{Pkg: gp, Name: o.Name(), Type: t, Const: k, ConstType: o.Type()}
		c.consts[e] = k
		return t
	case *types.Func:
		gf, sig := c.goFunc(gp, o, e.Name.Span())
		if gf == nil {
			return invalid
		}
		c.info.Go[e] = &GoUse{Pkg: gp, Name: o.Name(), Type: sig, Func: gf}
		return sig
	}
	c.goRefused(gp, obj, e.Name.Span())
	return invalid
}

// goTypesNote says which Go functions and constants Riddim can use.
const goTypesNote = "Riddim uses the Go functions and constants whose types are int, float64, bool, string, byte, rune, error or []T of them, and whose results are T, (T, error) or error"

func (c *checker) refuse(sp syntax.Span, format string, args ...any) {
	d := c.errorf("R0602", sp, format, args...)
	d.Notes = append(d.Notes, goTypesNote)
}

// goRefused reports a Go variable or type (R0602): Riddim only calls Go
// functions and reads Go constants.
func (c *checker) goRefused(gp *GoPkg, obj types.Object, sp syntax.Span) {
	if _, ok := obj.(*types.TypeName); ok {
		c.refuse(sp, "`%s.%s` is a Go type: Riddim cannot use Go types in v0", gp.Name, obj.Name())
		return
	}
	c.refuse(sp, "`%s.%s` is a Go variable: Riddim reads Go constants and calls Go functions, but not variables, which can change", gp.Name, obj.Name())
}

// goFunc maps a Go function's signature to a Riddim function type, or
// reports why it cannot (R0602).
func (c *checker) goFunc(gp *GoPkg, fn *types.Func, sp syntax.Span) (*GoFunc, *Func) {
	gf, f, why := GoSignature(fn.Type().(*types.Signature))
	if gf == nil {
		c.refuse(sp, "`%s.%s` cannot be used from Riddim: %s", gp.Name, fn.Name(), why)
	}
	return gf, f
}

// GoSignature maps a Go function's signature to Riddim's types (DESIGN
// §5.3): its parameters and its results T, (T, error) or error. When it
// cannot, it says why.
func GoSignature(sig *types.Signature) (*GoFunc, *Func, string) {
	switch {
	case sig.TypeParams().Len() > 0:
		return nil, nil, "it is generic"
	case sig.Variadic():
		return nil, nil, "it takes a variable number of arguments"
	}
	gf := &GoFunc{}
	f := &Func{}
	for i := 0; i < sig.Params().Len(); i++ {
		p := sig.Params().At(i)
		t, ok := GoType(p.Type())
		if !ok {
			return nil, nil, fmt.Sprintf("its parameter %s is %s, which has no Riddim type", p.Name(), aGo(p.Type()))
		}
		gf.Params = append(gf.Params, p.Type())
		f.Params = append(f.Params, t)
	}
	res := sig.Results()
	var value types.Type
	switch {
	case res.Len() == 0:
		return nil, nil, "it returns nothing, so it is there only for what else it does"
	case res.Len() == 1 && types.Identical(res.At(0).Type(), goErrorType):
		gf.Result, f.Result = GoErr, &Result{Unit, ErrorT}
		return gf, f, ""
	case res.Len() == 1:
		gf.Result, value = GoValue, res.At(0).Type()
	case res.Len() == 2 && types.Identical(res.At(1).Type(), goErrorType):
		gf.Result, value = GoValueErr, res.At(0).Type()
	default:
		return nil, nil, fmt.Sprintf("it returns %d results %s; Riddim takes a Go function's results as T, (T, error) or error", res.Len(), goTypeString(res))
	}
	t, ok := GoType(value)
	if !ok {
		return nil, nil, fmt.Sprintf("its result is %s, which has no Riddim type", aGo(value))
	}
	gf.Value, f.Result = value, t
	if gf.Result == GoValueErr {
		f.Result = &Result{t, ErrorT}
	}
	return gf, f, ""
}

// GoType maps a Go type to Riddim's (DESIGN §5.3): int, rune and untyped
// integer constants to Int, float64 to Float, bool to Bool, string to
// String, byte to Byte, error to Error and []T to List<T>. Go's byte and
// rune are the same types as its uint8 and int32, so those map too.
func GoType(t types.Type) (Type, bool) {
	t = types.Unalias(t)
	if types.Identical(t, goErrorType) {
		return ErrorT, true
	}
	switch t := t.(type) {
	case *types.Basic:
		switch t.Kind() {
		case types.Int, types.Int32, types.UntypedInt, types.UntypedRune:
			return Int, true
		case types.Float64, types.UntypedFloat:
			return Float, true
		case types.Bool, types.UntypedBool:
			return Bool, true
		case types.String, types.UntypedString:
			return String, true
		case types.Uint8:
			return Byte, true
		}
	case *types.Slice:
		if e, ok := GoType(t.Elem()); ok {
			return &List{e}, true
		}
	}
	return nil, false
}

// goTypeString writes a Go type as Go does, its packages by name.
func goTypeString(t types.Type) string {
	return types.TypeString(t, func(p *types.Package) string { return p.Name() })
}

// aGo writes a Go type with its article: "an int64", "a *strings.Reader".
func aGo(t types.Type) string {
	s := goTypeString(t)
	if strings.ContainsRune("aeiou", rune(s[0])) {
		return "an " + s
	}
	return "a " + s
}

// goConst maps a Go constant to a Riddim one, or reports why it cannot
// (R0602): its type has no Riddim type, or its value does not fit.
func (c *checker) goConst(gp *GoPkg, k *types.Const, sp syntax.Span) (Type, *Const) {
	name := gp.Name + "." + k.Name()
	t, ok := GoType(k.Type())
	if !ok || t == ErrorT {
		c.refuse(sp, "`%s` is %s, which has no Riddim type", name, aGo(k.Type()))
		return nil, nil
	}
	v := k.Val()
	switch t {
	case Int, Byte:
		n, exact := constant.Int64Val(constant.ToInt(v))
		if !exact || t == Byte && (n < 0 || n > 255) {
			c.refuse(sp, "`%s` is %s, which does not fit in %s", name, v.ExactString(), aType(t))
			return nil, nil
		}
		return t, &Const{Int: big.NewInt(n)}
	case Float:
		f, _ := constant.Float64Val(constant.ToFloat(v))
		if math.IsInf(f, 0) {
			c.refuse(sp, "`%s` does not fit in a Float", name)
			return nil, nil
		}
		return Float, &Const{Float: f}
	case Bool:
		return Bool, &Const{Bool: constant.BoolVal(v)}
	}
	return String, &Const{Str: constant.StringVal(v)}
}

func aType(t Type) string {
	if t == Int {
		return "an Int"
	}
	return "a " + t.String()
}
