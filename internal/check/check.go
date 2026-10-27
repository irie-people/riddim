package check

import (
	"fmt"
	"sort"
	"strings"

	"github.com/riddim-hq/riddim/internal/diag"
	"github.com/riddim-hq/riddim/internal/syntax"
)

// Check checks a parsed program. It reports the errors of the first stage
// that has any (names and types, then patterns), together with warnings.
func Check(fset *syntax.FileSet, files []*syntax.File) (*Program, []diag.Diagnostic) {
	c := &checker{
		fset:    fset,
		globals: map[string]any{},
		info: &Info{
			Types:     map[syntax.Expr]Type{},
			Uses:      map[syntax.Expr]*Use{},
			Methods:   map[*syntax.MethodCallExpr]*MethodUse{},
			Fields:    map[*syntax.FieldExpr]*FieldUse{},
			Structs:   map[*syntax.StructLit]*StructUse{},
			Pats:      map[syntax.Pattern]*PatInfo{},
			Defs:      map[*syntax.Ident]*Local{},
			Closures:  map[*syntax.ClosureExpr]*ClosureInfo{},
			Tries:     map[*syntax.TryExpr]*TryInfo{},
			Casts:     map[*syntax.AsExpr]*CastInfo{},
			Funcs:     map[*syntax.FuncDecl]*FuncObj{},
			Consts:    map[*syntax.ConstDecl]*ConstObj{},
			TypeDecls: map[syntax.Decl]*TypeDecl{},
		},
		consts: map[syntax.Expr]*Const{},
	}
	c.prog = &Program{Fset: fset, Files: files, Info: c.info}
	c.collect(files)
	c.resolveTypeDecls()
	c.collectImpls()
	c.resolveSignatures()
	c.declsDone = true
	c.checkKeys()
	c.checkRecursive()
	for _, k := range c.prog.Consts {
		c.checkConst(k)
	}
	c.checkMain(files)
	for _, f := range c.allFuncs {
		c.checkFunc(f)
	}
	c.finishNames()
	return c.prog, c.result()
}

type checker struct {
	fset    *syntax.FileSet
	info    *Info
	prog    *Program
	globals map[string]any // *TypeDecl, *FuncObj or *ConstObj

	errs   []diag.Diagnostic // names and types
	pats   []diag.Diagnostic // exhaustiveness and unreachable arms
	warns  []diag.Diagnostic // W0002, W0003
	unused []diag.Diagnostic // W0001

	allFuncs []*FuncObj
	impls    []implInfo
	styles   []styled

	// per function
	fn       *funcCtx
	scope    *scope
	tparams  []map[string]*TypeParam
	selfType Type
	selfDecl *TypeDecl
	loops    []*loopCtx
	dead     bool
	vars     []*Var
	matches  []*syntax.MatchExpr
	fnLocals []*Local
	consts   map[syntax.Expr]*Const

	trail  []*Var
	trying int

	// Map key types to check once their types are known: those written in
	// declarations wait until every declaration is resolved, those inferred
	// for Map::new() until the end of the function.
	keyChecks []keyCheck
	declsDone bool
}

type keyCheck struct {
	key Type
	sp  syntax.Span
}

type implInfo struct {
	decl    *syntax.ImplDecl
	td      *TypeDecl
	tparams []*TypeParam
	self    Type
}

type funcCtx struct {
	result   Type
	parent   *funcCtx
	captures []*Local
	pending  []*Var // parts of a closure's result type, known by the end of its body
}

type loopCtx struct{ hasBreak bool }

type scope struct {
	parent *scope
	names  map[string]*Local
}

func (c *checker) errorf(code string, sp syntax.Span, format string, args ...any) *diag.Diagnostic {
	c.errs = append(c.errs, c.fset.Diag(code, sp, format, args...))
	return &c.errs[len(c.errs)-1]
}

func (c *checker) warnf(code string, sp syntax.Span, format string, args ...any) {
	c.warns = append(c.warns, c.fset.Diag(code, sp, format, args...))
}

// result returns the errors of the names-and-types stage, or, when there
// are none but the Rust and Go habits, which do not stop the stages after
// theirs, those of the patterns stage too. Unused bindings wait until there
// are no errors at all.
func (c *checker) result() []diag.Diagnostic {
	var out []diag.Diagnostic
	switch {
	case diag.HasBlocking(c.errs):
		out = append(append(out, c.errs...), c.warns...)
	case len(c.errs) > 0:
		out = append(append(append(out, c.errs...), c.pats...), c.warns...)
	default:
		out = append(append(append(out, c.pats...), c.warns...), c.unused...)
	}
	diag.Sort(out)
	return out
}

func (c *checker) where(id *syntax.Ident) string {
	s := c.fset.Source(id.NamePos)
	line, col := s.LineCol(id.NamePos)
	return fmt.Sprintf("%s:%d:%d", s.Name, line, col)
}

// reserved names cannot be declared.
var reserved = map[string]string{
	"Int": "type", "Float": "type", "Bool": "type", "String": "type", "Byte": "type",
	"List": "type", "Map": "type", "Option": "type", "Result": "type", "Error": "type",
	"Never": "type", "Sys": "capability", "Out": "capability", "Env": "capability",
	"Fs": "capability", "Clock": "capability", "Some": "constructor", "None": "constructor",
	"Ok": "constructor", "Err": "constructor", "error": "function", "panic": "function",
	"todo": "function",
}

func (c *checker) declareGlobal(id *syntax.Ident, obj any) bool {
	if kind, ok := reserved[id.Name]; ok {
		c.errorf("R0202", id.Span(), "`%s` is a built-in %s; choose another name", id.Name, kind)
		return false
	}
	if prev, ok := c.globals[id.Name]; ok {
		c.errorf("R0202", id.Span(), "`%s` is already declared at %s", id.Name, c.where(declIdent(prev)))
		return false
	}
	c.globals[id.Name] = obj
	return true
}

func declIdent(obj any) *syntax.Ident {
	switch o := obj.(type) {
	case *TypeDecl:
		return o.Ident
	case *FuncObj:
		return o.Decl.Name
	case *ConstObj:
		return o.Decl.Name
	}
	return nil
}

func (c *checker) collect(files []*syntax.File) {
	for _, f := range files {
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *syntax.StructDecl:
				td := &TypeDecl{Name: d.Name.Name, Ident: d.Name, Decl: d, byName: map[string]*FuncObj{}}
				c.info.TypeDecls[d] = td
				c.style(d.Name, upperCamel, &td.refs)
				if c.declareGlobal(d.Name, td) {
					c.prog.Types = append(c.prog.Types, td)
				}
			case *syntax.EnumDecl:
				td := &TypeDecl{Name: d.Name.Name, Ident: d.Name, Decl: d, IsEnum: true, byName: map[string]*FuncObj{}}
				c.info.TypeDecls[d] = td
				c.style(d.Name, upperCamel, &td.refs)
				if c.declareGlobal(d.Name, td) {
					c.prog.Types = append(c.prog.Types, td)
				}
			case *syntax.FuncDecl:
				fo := &FuncObj{Name: d.Name.Name, Decl: d}
				c.info.Funcs[d] = fo
				c.style(d.Name, snake, &fo.refs)
				if c.declareGlobal(d.Name, fo) {
					c.prog.Funcs = append(c.prog.Funcs, fo)
				}
				c.allFuncs = append(c.allFuncs, fo)
			case *syntax.ConstDecl:
				k := &ConstObj{Name: d.Name.Name, Decl: d}
				c.info.Consts[d] = k
				c.style(d.Name, screaming, &k.refs)
				if c.declareGlobal(d.Name, k) {
					c.prog.Consts = append(c.prog.Consts, k)
				}
				k.Type = c.resolveType(d.Type)
			case *syntax.ImplDecl:
				c.impls = append(c.impls, implInfo{decl: d})
			case *syntax.UseDecl:
				c.errorf("R0106", d.Span(), "`use go` is not supported in this version")
			}
		}
	}
}

// typeParams declares type parameters, checking names.
func (c *checker) typeParams(ids []*syntax.Ident, outer map[string]*TypeParam) ([]*TypeParam, map[string]*TypeParam) {
	m := map[string]*TypeParam{}
	var out []*TypeParam
	for _, id := range ids {
		tp := &TypeParam{Name: id.Name, Ident: id}
		c.style(id, upperCamel, &tp.refs)
		if _, dup := m[id.Name]; dup {
			c.errorf("R0202", id.Span(), "type parameter `%s` is declared twice", id.Name)
			continue
		}
		if _, dup := outer[id.Name]; dup {
			c.errorf("R0202", id.Span(), "type parameter `%s` is already declared by the impl block", id.Name)
			continue
		}
		if kind, ok := reserved[id.Name]; ok {
			c.errorf("R0202", id.Span(), "`%s` is a built-in %s; choose another name", id.Name, kind)
			continue
		}
		m[id.Name] = tp
		out = append(out, tp)
	}
	return out, m
}

func (c *checker) resolveTypeDecls() {
	for _, td := range c.prog.Types {
		switch d := td.Decl.(type) {
		case *syntax.StructDecl:
			var m map[string]*TypeParam
			td.TParams, m = c.typeParams(d.TypeParams, nil)
			c.tparams = append(c.tparams, m)
			seen := map[string]bool{}
			for i, fd := range d.Fields {
				f := &Field{Name: fd.Name.Name, Ident: fd.Name, Index: i, Type: c.resolveType(fd.Type)}
				c.style(fd.Name, snake, &f.refs)
				if seen[f.Name] {
					c.errorf("R0202", fd.Name.Span(), "field `%s` is declared twice", f.Name)
				}
				seen[f.Name] = true
				td.Fields = append(td.Fields, f)
			}
			c.tparams = c.tparams[:len(c.tparams)-1]
		case *syntax.EnumDecl:
			var m map[string]*TypeParam
			td.TParams, m = c.typeParams(d.TypeParams, nil)
			c.tparams = append(c.tparams, m)
			seen := map[string]bool{}
			for i, vd := range d.Variants {
				v := &Variant{Name: vd.Name.Name, Ident: vd.Name, Enum: td, Index: i}
				c.style(vd.Name, upperCamel, &v.refs)
				if seen[v.Name] {
					c.errorf("R0202", vd.Name.Span(), "variant `%s` is declared twice", v.Name)
				}
				seen[v.Name] = true
				fseen := map[string]bool{}
				for j, vf := range vd.Fields {
					f := &Field{Index: j, Type: c.resolveType(vf.Type)}
					if vf.Name != nil {
						v.Named = true
						f.Name, f.Ident = vf.Name.Name, vf.Name
						c.style(vf.Name, snake, &f.refs)
						if fseen[f.Name] {
							c.errorf("R0202", vf.Name.Span(), "field `%s` is declared twice", f.Name)
						}
						fseen[f.Name] = true
					}
					v.Fields = append(v.Fields, f)
				}
				td.Variants = append(td.Variants, v)
			}
			c.tparams = c.tparams[:len(c.tparams)-1]
		}
	}
}

func (c *checker) collectImpls() {
	for i := range c.impls {
		im := &c.impls[i]
		d := im.decl
		im.tparams, _ = c.typeParams(d.TypeParams, nil)
		name := d.Type.Name.Name
		td, _ := c.globals[name].(*TypeDecl)
		if td == nil {
			if _, ok := reserved[name]; ok || name == "Self" {
				c.errorf("R0304", d.Type.Name.Span(), "methods can only be added to structs and enums declared in the program, not to `%s`", name)
			} else {
				c.unknown(d.Type.Name, "type", c.typeNames())
			}
			continue
		}
		td.refs = append(td.refs, d.Type.Name.Span())
		// The impl names the type with its own type parameters, in order.
		ok := len(d.Type.Args) == len(td.TParams) && len(im.tparams) == len(td.TParams)
		var args []Type
		for j, a := range d.Type.Args {
			nt, isNamed := a.(*syntax.NamedType)
			if !ok || !isNamed || len(nt.Args) > 0 || j >= len(im.tparams) || nt.Name.Name != im.tparams[j].Name {
				ok = false
				break
			}
			im.tparams[j].refs = append(im.tparams[j].refs, nt.Name.Span())
			args = append(args, im.tparams[j])
		}
		if !ok {
			want := td.Name
			if len(td.TParams) > 0 {
				names := make([]string, len(td.TParams))
				for j, tp := range td.TParams {
					names[j] = tp.Name
				}
				want = fmt.Sprintf("impl<%s> %s<%s>", strings.Join(names, ", "), td.Name, strings.Join(names, ", "))
			} else {
				want = "impl " + td.Name
			}
			c.errorf("R0302", d.Type.Span(), "an impl block names the type with its own type parameters: write `%s`", want)
			continue
		}
		im.td = td
		im.self = &Named{Decl: td, Args: args}
		for _, md := range d.Methods {
			fo := &FuncObj{Name: md.Name.Name, Decl: md, Recv: td, ImplTParams: im.tparams}
			fo.HasSelf = len(md.Params) > 0 && md.Params[0].Self
			c.info.Funcs[md] = fo
			c.style(md.Name, snake, &fo.refs)
			c.allFuncs = append(c.allFuncs, fo)
			if prev := td.byName[fo.Name]; prev != nil {
				c.errorf("R0202", md.Name.Span(), "`%s` already has a method `%s`, declared at %s", td.Name, fo.Name, c.where(prev.Decl.Name))
				continue
			}
			if f := td.Field(fo.Name); f != nil {
				c.errorf("R0202", md.Name.Span(), "`%s` has both a field and a method named `%s`; rename one", td.Name, fo.Name)
				continue
			}
			td.byName[fo.Name] = fo
			td.Methods = append(td.Methods, fo)
		}
	}
}

func (c *checker) implOf(fo *FuncObj) *implInfo {
	for i := range c.impls {
		for _, md := range c.impls[i].decl.Methods {
			if md == fo.Decl {
				return &c.impls[i]
			}
		}
	}
	return nil
}

func (c *checker) resolveSignatures() {
	for _, fo := range c.allFuncs {
		c.enterSig(fo, true)
		d := fo.Decl
		seen := map[string]bool{}
		for _, p := range d.Params {
			if p.Self {
				continue
			}
			if seen[p.Name.Name] {
				c.errorf("R0202", p.Name.Span(), "parameter `%s` is declared twice", p.Name.Name)
			}
			seen[p.Name.Name] = true
			fo.Params = append(fo.Params, c.resolveType(p.Type))
		}
		fo.Result = Unit
		if d.Result != nil {
			fo.Result = c.resolveType(d.Result)
		}
		c.leaveSig()
	}
}

// enterSig makes a function's type parameters, and those of its impl block,
// visible to type resolution. The first time, it declares them.
func (c *checker) enterSig(fo *FuncObj, first bool) {
	c.selfType, c.selfDecl = nil, nil
	var implParams map[string]*TypeParam
	if fo.Recv != nil {
		im := c.implOf(fo)
		implParams = map[string]*TypeParam{}
		for _, tp := range im.tparams {
			implParams[tp.Name] = tp
		}
		c.tparams = append(c.tparams, implParams)
		c.selfType, c.selfDecl = im.self, im.td
	}
	if first {
		var m map[string]*TypeParam
		fo.TParams, m = c.typeParams(fo.Decl.TypeParams, implParams)
		c.tparams = append(c.tparams, m)
	} else {
		m := map[string]*TypeParam{}
		for _, tp := range fo.TParams {
			m[tp.Name] = tp
		}
		c.tparams = append(c.tparams, m)
	}
}

func (c *checker) leaveSig() {
	c.tparams = nil
	c.selfType, c.selfDecl = nil, nil
}

func (c *checker) typeNames() []string {
	names := []string{"Int", "Float", "Bool", "String", "Byte", "List", "Map", "Option", "Result", "Error", "Never", "Sys", "Out"}
	for name, g := range c.globals {
		if _, ok := g.(*TypeDecl); ok {
			names = append(names, name)
		}
	}
	for _, m := range c.tparams {
		for name := range m {
			names = append(names, name)
		}
	}
	return names
}

func (c *checker) lookupTParam(name string) *TypeParam {
	for i := len(c.tparams) - 1; i >= 0; i-- {
		if tp := c.tparams[i][name]; tp != nil {
			return tp
		}
	}
	return nil
}

// resolveType turns a written type into a Type.
func (c *checker) resolveType(t syntax.TypeExpr) Type {
	switch t := t.(type) {
	case *syntax.TupleType:
		if len(t.Elems) == 0 {
			return Unit
		}
		elems := make([]Type, len(t.Elems))
		for i, e := range t.Elems {
			elems[i] = c.resolveType(e)
		}
		return &Tuple{elems}
	case *syntax.FuncType:
		params := make([]Type, len(t.Params))
		for i, p := range t.Params {
			params[i] = c.resolveType(p)
		}
		var res Type = Unit
		if t.Result != nil {
			res = c.resolveType(t.Result)
		}
		return &Func{params, res}
	case *syntax.NamedType:
		return c.resolveNamed(t)
	}
	return invalid
}

func (c *checker) resolveNamed(t *syntax.NamedType) Type {
	name := t.Name.Name
	args := make([]Type, len(t.Args))
	for i, a := range t.Args {
		args[i] = c.resolveType(a)
	}
	arity := func(n int) bool {
		if len(args) != n {
			c.errorf("R0302", t.Span(), "`%s` takes %s, found %d", name, plural(n, "type argument"), len(args))
			return false
		}
		return true
	}
	if tp := c.lookupTParam(name); tp != nil {
		tp.refs = append(tp.refs, t.Name.Span())
		if !arity(0) {
			return invalid
		}
		return tp
	}
	switch name {
	case "Int", "Float", "Bool", "String", "Byte", "Error", "Never", "Sys", "Out":
		if !arity(0) {
			return invalid
		}
		return map[string]Type{"Int": Int, "Float": Float, "Bool": Bool, "String": String, "Byte": Byte,
			"Error": ErrorT, "Never": Never, "Sys": SysT, "Out": OutT}[name]
	case "List", "Option":
		if !arity(1) {
			return invalid
		}
		if name == "List" {
			return &List{args[0]}
		}
		return &Option{args[0]}
	case "Map":
		if !arity(2) {
			return invalid
		}
		if !c.declsDone {
			c.keyChecks = append(c.keyChecks, keyCheck{args[0], t.Args[0].Span()})
		} else if !c.mapKey(args[0], t.Args[0].Span()) {
			return invalid
		}
		return &Map{args[0], args[1]}
	case "Result":
		if !arity(2) {
			return invalid
		}
		return &Result{args[0], args[1]}
	case "Env", "Fs", "Clock":
		c.errorf("R0106", t.Name.Span(), "`%s` is not supported in this version", name)
		return invalid
	case "Self":
		if c.selfType == nil {
			c.errorf("R0201", t.Name.Span(), "`Self` is only available inside an impl block")
			return invalid
		}
		if !arity(0) {
			return invalid
		}
		return c.selfType
	}
	td, ok := c.globals[name].(*TypeDecl)
	if !ok {
		if c.globals[name] != nil {
			c.errorf("R0201", t.Name.Span(), "`%s` is not a type", name)
			return invalid
		}
		if ht := c.habitType(t, args); ht != nil {
			return ht
		}
		c.unknown(t.Name, "type", c.typeNames())
		return invalid
	}
	td.refs = append(td.refs, t.Name.Span())
	if !arity(len(td.TParams)) {
		return invalid
	}
	return &Named{Decl: td, Args: args}
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// unknown reports an unknown name with the closest known names.
func (c *checker) unknown(id *syntax.Ident, what string, candidates []string) *diag.Diagnostic {
	msg := fmt.Sprintf("unknown %s `%s`", what, id.Name)
	if close := closest(id.Name, candidates); len(close) > 0 {
		msg += "; did you mean " + orList(close) + "?"
	}
	return c.errorf("R0201", id.Span(), "%s", msg)
}

func orList(names []string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = "`" + n + "`"
	}
	if len(q) == 1 {
		return q[0]
	}
	return strings.Join(q[:len(q)-1], ", ") + " or " + q[len(q)-1]
}

// checkRecursive reports structs that contain themselves by value (R0307).
func (c *checker) checkRecursive() {
	for _, td := range c.prog.Types {
		if td.IsEnum {
			continue
		}
		d := td.Decl.(*syntax.StructDecl)
		for i, f := range td.Fields {
			if containsByValue(f.Type, td, map[string]bool{}, 0) {
				c.errorf("R0307", d.Fields[i].Type.Span(),
					"struct `%s` contains itself through field `%s` (%s); a struct cannot contain itself by value. Use an enum, which may be recursive, or a List",
					td.Name, f.Name, f.Type)
				break
			}
		}
	}
}

func containsByValue(t Type, target *TypeDecl, seen map[string]bool, depth int) bool {
	if depth > 64 {
		return false
	}
	switch t := Resolve(t).(type) {
	case *Named:
		if t.Decl.IsEnum {
			return false
		}
		if t.Decl == target {
			return true
		}
		key := t.String()
		if seen[key] {
			return false
		}
		seen[key] = true
		for _, f := range t.Decl.Fields {
			if containsByValue(FieldType(f, t.Decl, t.Args), target, seen, depth+1) {
				return true
			}
		}
	case *Tuple:
		for _, e := range t.Elems {
			if containsByValue(e, target, seen, depth+1) {
				return true
			}
		}
	case *Option:
		return containsByValue(t.Elem, target, seen, depth+1)
	case *Map:
		return containsByValue(t.Key, target, seen, depth+1) || containsByValue(t.Val, target, seen, depth+1)
	case *Result:
		return containsByValue(t.Ok, target, seen, depth+1) || containsByValue(t.Err, target, seen, depth+1)
	}
	return false
}

// mapKey reports a Map key type that == does not work on (R0305): the
// runtime's map needs comparable keys.
func (c *checker) mapKey(k Type, sp syntax.Span) bool {
	t := Deep(k)
	if isInvalid(t) || hasVars(t) || comparable(t, nil) {
		return true
	}
	d := c.errorf("R0305", sp, "the keys of a Map must be comparable with ==, and %s is not", t)
	d.Notes = append(d.Notes, "keys may be Int, Float, Bool, String or Byte, or tuples, structs, enums, Option and Result made of them; a type parameter is not comparable in this version")
	return false
}

// checkKeys checks the Map key types waiting in c.keyChecks.
func (c *checker) checkKeys() {
	for _, k := range c.keyChecks {
		c.mapKey(k.key, k.sp)
	}
	c.keyChecks = nil
}

// checkMain checks that there is exactly one main with a supported
// signature (R0604).
func (c *checker) checkMain(files []*syntax.File) {
	g, ok := c.globals["main"]
	if !ok {
		if len(files) == 0 {
			return
		}
		src := files[0].Source
		d := c.errorf("R0604", syntax.Span{Start: src.Base, End: src.Base}, "the program has no `fn main`")
		d.Notes = append(d.Notes, "write `fn main(sys: Sys) { ... }` in one of its files")
		return
	}
	fo, ok := g.(*FuncObj)
	if !ok {
		c.errorf("R0604", declIdent(g).Span(), "`main` must be a function")
		return
	}
	good := len(fo.TParams) == 0
	if len(fo.Params) > 1 || len(fo.Params) == 1 && Resolve(fo.Params[0]) != SysT {
		good = false
	}
	switch r := Resolve(fo.Result).(type) {
	case *Result:
		if Resolve(r.Ok) != Unit || Resolve(r.Err) != ErrorT {
			good = false
		}
	default:
		if r != Unit {
			good = false
		}
	}
	if !good {
		d := c.errorf("R0604", fo.Decl.Name.Span(), "`main` must take `sys: Sys` or nothing, and return nothing or `Result<(), Error>`")
		d.Notes = append(d.Notes, "for example `fn main(sys: Sys) -> Result<(), Error>`")
	}
	c.prog.Main = fo
}

// checkFunc checks a function or method body.
func (c *checker) checkFunc(fo *FuncObj) {
	if fo.Decl.Body == nil {
		return
	}
	c.enterSig(fo, false)
	defer c.leaveSig()
	c.fn = &funcCtx{result: fo.Result}
	c.scope = &scope{names: map[string]*Local{}}
	c.loops, c.dead, c.vars, c.matches, c.fnLocals = nil, false, nil, nil, nil
	if fo.HasSelf && c.selfType != nil {
		self := fo.Decl.Params[0]
		fo.SelfLocal = &Local{Name: "self", Decls: []*syntax.Ident{self.Name}, Type: c.selfType, Param: true, fn: c.fn}
		c.info.Defs[self.Name] = fo.SelfLocal
		c.scope.names["self"] = fo.SelfLocal
	}
	i := 0
	for _, p := range fo.Decl.Params {
		if p.Self {
			continue
		}
		var t Type = invalid
		if i < len(fo.Params) {
			t = fo.Params[i]
		}
		i++
		l := &Local{Name: p.Name.Name, Decls: []*syntax.Ident{p.Name}, Type: t, Param: true, fn: c.fn}
		c.style(p.Name, snake, &l.refs)
		c.info.Defs[p.Name] = l
		c.scope.names[l.Name] = l
		fo.ParamLocals = append(fo.ParamLocals, l)
	}
	c.body(fo.Decl.Body, fo.Result, "`"+fo.Name+"`")
	c.endStmt(0)
	c.checkKeys()
	for _, m := range c.matches {
		c.checkMatch(m)
	}
	c.reportUnused()
	c.fn, c.scope = nil, nil
}

// body checks the body of a function or closure against its result type.
func (c *checker) body(b syntax.Expr, result Type, what string) {
	if isUnit(result) {
		c.unitBody(b, what)
		return
	}
	blk, ok := b.(*syntax.BlockExpr)
	if !ok {
		c.expr(b, result)
		return
	}
	t := c.block(blk, result, false)
	c.info.Types[blk] = t
	if !isNever(t) && !isInvalid(t) && isUnit(t) && !isUnit(result) && !hasTail(blk) {
		d := c.errorf("R0301", syntax.Span{Start: blk.Rbrace, End: blk.Rbrace + 1}, "%s returns %s, but its body ends without a value", what, Resolve(result))
		d.Notes = append(d.Notes, "the last line of a body is its value; end with an expression or `return`")
	}
}

// unitBody checks the body of a function that returns (): its value, the
// last expression, must be () too. A Result there is R0502.
func (c *checker) unitBody(b syntax.Expr, what string) {
	blk, ok := b.(*syntax.BlockExpr)
	if !ok {
		c.unitTail(b, what)
		return
	}
	c.pushScope()
	defer c.popScope()
	saved := c.dead
	defer func() { c.dead = saved }()
	for i, s := range blk.Stmts {
		if es, ok := s.(*syntax.ExprStmt); ok && i == len(blk.Stmts)-1 {
			mark := len(c.vars)
			c.unitTail(es.X, what)
			c.endStmt(mark)
			break
		}
		if isNever(c.stmt(s)) {
			c.dead = true
		}
	}
	c.info.Types[blk] = Unit
}

func (c *checker) unitTail(e syntax.Expr, what string) {
	var t Type
	switch x := e.(type) {
	case *syntax.BlockExpr:
		c.unitBody(x, what)
		return
	case *syntax.IfExpr:
		if x.Else == nil {
			c.discard(e)
			return
		}
	}
	t = c.expr(e, nil)
	switch r := Resolve(t).(type) {
	case *Result:
		c.unusedResult(e)
	case *Var:
		c.bind(r, Unit)
	case *Basic:
		if r == Unit || r == Never {
			return
		}
		c.unitMismatch(e, what, t)
	case *Invalid:
	default:
		c.unitMismatch(e, what, t)
	}
}

func (c *checker) unitMismatch(e syntax.Expr, what string, t Type) {
	d := c.errorf("R0301", e.Span(), "%s returns (), but its last expression is a %s", what, Deep(t))
	d.Notes = append(d.Notes, fmt.Sprintf("to return it, declare the result: `-> %s`; to drop it, write `let _ = ...`", Deep(t)))
}

func hasTail(b *syntax.BlockExpr) bool {
	if len(b.Stmts) == 0 {
		return false
	}
	_, ok := b.Stmts[len(b.Stmts)-1].(*syntax.ExprStmt)
	return ok
}

func (c *checker) pushScope() { c.scope = &scope{parent: c.scope, names: map[string]*Local{}} }
func (c *checker) popScope()  { c.scope = c.scope.parent }

func (c *checker) lookupLocal(name string) *Local {
	for s := c.scope; s != nil; s = s.parent {
		if l := s.names[name]; l != nil {
			return l
		}
	}
	return nil
}

func (c *checker) localNames() []string {
	var names []string
	for s := c.scope; s != nil; s = s.parent {
		for n := range s.names {
			names = append(names, n)
		}
	}
	return names
}

// newVar makes an inference variable; endStmt reports it if it is still
// unknown at the end of the statement that made it (R0306).
func (c *checker) newVar(sp syntax.Span, what string) *Var {
	v := &Var{origin: sp, what: what}
	c.vars = append(c.vars, v)
	return v
}

// endStmt reports the variables made since mark that are still unknown.
func (c *checker) endStmt(mark int) {
	if mark > len(c.vars) {
		return
	}
	var bad []*Var
	for _, v := range c.vars[mark:] {
		if Resolve(v) == v {
			bad = append(bad, v)
		}
	}
	if len(bad) > 0 {
		sort.SliceStable(bad, func(i, j int) bool { return bad[i].origin.Start < bad[j].origin.Start })
		v := bad[0]
		d := c.errorf("R0306", v.origin, "cannot infer %s", v.what)
		example := "`let xs: List<Int> = []`"
		switch {
		case strings.Contains(v.what, "`Map::new()`"):
			example = "`let m: Map<String, Int> = Map::new()`"
		case strings.Contains(v.what, "`None`"):
			example = "`let x: Option<Int> = None`"
		case strings.Contains(v.what, "Ok") || strings.Contains(v.what, "Err"):
			example = "`let r: Result<Int, Error> = Ok(1)`"
		}
		d.Notes = append(d.Notes, "Riddim infers types forward only; annotate the binding, as in "+example)
		for _, v := range bad {
			v.ref = invalid
		}
	}
	c.vars = c.vars[:mark]
}

// reportUnused warns about bindings that are never read (W0001).
func (c *checker) reportUnused() {
	for _, l := range c.fnLocals {
		if l.Used || l.Param || strings.HasPrefix(l.Name, "_") {
			continue
		}
		d := c.fset.Diag("W0001", l.Decls[0].Span(), "`%s` is never used", l.Name)
		if !l.Assigned {
			fix := diag.Fix{Title: "replace it with _"}
			for _, id := range l.Decls {
				fix.Edits = append(fix.Edits, c.fset.Edit(id.Span(), "_"))
			}
			d.Fixes = append(d.Fixes, fix)
		} else {
			d.Notes = append(d.Notes, "it is assigned but never read")
		}
		c.unused = append(c.unused, d)
	}
}
