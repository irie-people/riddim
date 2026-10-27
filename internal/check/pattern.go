package check

import (
	"fmt"
	"sort"

	"github.com/riddim-hq/riddim/internal/syntax"
)

// pat checks pattern p against a value of type t, binding names through b.
func (c *checker) pat(p syntax.Pattern, t Type, b *binder) {
	info := &PatInfo{Type: t}
	c.info.Pats[p] = info
	rt := Resolve(t)
	switch p := p.(type) {
	case *syntax.WildcardPat:
		info.Kind = PWild
	case *syntax.BindPat:
		info.Kind = PBind
		info.Local = b.bind(c, p.Name, t)
	case *syntax.LitPat:
		c.litPat(p, rt, info)
	case *syntax.TuplePat:
		info.Kind = PTuple
		tup, ok := rt.(*Tuple)
		if !ok || len(tup.Elems) != len(p.Elems) {
			if !isInvalid(rt) {
				c.errorf("R0301", p.Span(), "mismatched types: this tuple pattern has %d fields, but the value is %s", len(p.Elems), Deep(rt))
			}
			for _, e := range p.Elems {
				c.pat(e, invalid, b)
			}
			return
		}
		for i, e := range p.Elems {
			c.pat(e, tup.Elems[i], b)
		}
	case *syntax.VariantPat:
		c.variantPat(p, rt, b, info)
	case *syntax.OrPat:
		info.Kind = POr
		before := b.bound()
		c.pat(p.Alts[0], t, b)
		names := map[string]*Local{}
		for name, l := range b.bound() {
			if before[name] == nil {
				names[name] = l
			}
		}
		for _, alt := range p.Alts[1:] {
			side := &binder{mut: b.mut, reuse: names, seen: map[string]bool{}}
			c.pat(alt, t, side)
			missing := make([]string, 0, len(names))
			for name := range names {
				missing = append(missing, name)
			}
			sort.Strings(missing)
			for _, name := range missing {
				if !side.seen[name] {
					c.errorf("R0403", alt.Span(), "`%s` is bound on only one side of `|`: each side must bind the same names", name)
				}
			}
		}
	}
}

func (c *checker) litPat(p *syntax.LitPat, rt Type, info *PatInfo) {
	switch lit := p.Lit.(type) {
	case *syntax.IntLit:
		info.Kind = PInt
		v, ok := ParseIntLit(lit.Text)
		if !ok {
			return
		}
		if p.Minus != syntax.NoPos {
			v.Neg(v)
		}
		info.Int = v
		switch rt {
		case Int, Byte:
			if !inRange(v, rt) {
				c.errorf("R0308", p.Span(), "%s does not fit in %s", v, rt)
			}
			c.info.Types[lit] = rt
			return
		}
	case *syntax.StringLit:
		info.Kind = PString
		info.Str = lit.Value
		c.info.Types[lit] = String
		if rt == String {
			return
		}
	case *syntax.BoolLit:
		info.Kind = PBool
		info.Bool = lit.Value
		c.info.Types[lit] = Bool
		if rt == Bool {
			return
		}
	}
	if !isInvalid(rt) {
		c.errorf("R0301", p.Span(), "mismatched types: this literal pattern cannot match a value of type %s", Deep(rt))
	}
}

func (c *checker) variantPat(p *syntax.VariantPat, rt Type, b *binder, info *PatInfo) {
	name := p.Name.Name
	argsOr := func(types []Type) {
		for i, a := range p.Args {
			var t Type = invalid
			if i < len(types) {
				t = types[i]
			}
			c.pat(a, t, b)
		}
	}
	arity := func(want int, show string) bool {
		switch {
		case want == 0 && p.Rparen != syntax.NoPos:
			c.errorf("R0302", p.Span(), "`%s` has no fields: write `%s` without parentheses", name, show)
		case want > 0 && p.Rparen == syntax.NoPos:
			c.errorf("R0302", p.Span(), "`%s` has %s: write `%s(%s)`", name, plural(want, "field"), show, underscores(want))
		case len(p.Args) != want:
			c.errorf("R0302", p.Span(), "`%s` has %s, but the pattern has %d", name, plural(want, "field"), len(p.Args))
		default:
			return true
		}
		return false
	}
	// Some, None, Ok and Err, unless the enum being matched has a variant of
	// that name.
	if p.Type == nil {
		named, isNamed := rt.(*Named)
		builtinCase := !isNamed || named.Decl.Variant(name) == nil
		switch o := rt.(type) {
		case *Option:
			if builtinCase && (name == "Some" || name == "None") {
				if name == "Some" {
					info.Kind = PSome
					if arity(1, "Some") {
						c.pat(p.Args[0], o.Elem, b)
						return
					}
				} else {
					info.Kind = PNone
					arity(0, "None")
				}
				argsOr(nil)
				return
			}
		case *Result:
			if builtinCase && (name == "Ok" || name == "Err") {
				t := o.Ok
				info.Kind = POk
				if name == "Err" {
					t, info.Kind = o.Err, PErr
				}
				if arity(1, name) {
					c.pat(p.Args[0], t, b)
					return
				}
				argsOr(nil)
				return
			}
		}
	}
	var td *TypeDecl
	if p.Type != nil {
		var ok bool
		td, _, ok = c.pathType(p.Type)
		if !ok {
			argsOr(nil)
			return
		}
		if !td.IsEnum {
			c.errorf("R0301", p.Type.Span(), "`%s` is a struct; only enum variants, Option and Result have variant patterns", td.Name)
			argsOr(nil)
			return
		}
	}
	named, ok := rt.(*Named)
	if !ok || !named.Decl.IsEnum {
		if !isInvalid(rt) {
			c.errorf("R0301", p.Span(), "mismatched types: the pattern `%s` cannot match a value of type %s", name, Deep(rt))
		}
		argsOr(nil)
		return
	}
	if td != nil && td != named.Decl {
		c.errorf("R0301", p.Span(), "mismatched types: `%s::%s` cannot match a value of type %s", td.Name, name, Deep(rt))
		argsOr(nil)
		return
	}
	v := named.Decl.Variant(name)
	if v == nil {
		var cands []string
		for _, v := range named.Decl.Variants {
			cands = append(cands, v.Name)
		}
		msg := fmt.Sprintf("%s has no variant `%s`", named.Decl.Name, name)
		if close := closest(name, cands); len(close) > 0 {
			msg += "; did you mean " + orList(close) + "?"
		}
		c.errorf("R0201", p.Name.Span(), "%s", msg)
		argsOr(nil)
		return
	}
	v.refs = append(v.refs, p.Name.Span())
	info.Kind = PVariant
	info.Variant = v
	info.TypeArgs = named.Args
	types := make([]Type, len(v.Fields))
	for i, f := range v.Fields {
		types[i] = FieldType(f, named.Decl, named.Args)
	}
	show := name
	if p.Type != nil {
		show = p.Type.Name + "::" + name
	}
	arity(len(v.Fields), show)
	argsOr(types)
}

func underscores(n int) string {
	s := "_"
	for i := 1; i < n; i++ {
		s += ", _"
	}
	return s
}
