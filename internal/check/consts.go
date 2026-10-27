package check

import (
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/riddim-hq/riddim/internal/syntax"
)

// Constant expressions (literals, constants and operators on them) become Go
// constant expressions, which Go evaluates at compile time and rejects when
// they overflow or divide by zero. The checker folds them first and reports
// those cases itself (R0308).

var (
	minInt = big.NewInt(math.MinInt64)
	maxInt = big.NewInt(math.MaxInt64)
)

// ParseIntLit returns the value of an integer literal.
func ParseIntLit(text string) (*big.Int, bool) {
	text = strings.ReplaceAll(text, "_", "")
	base := 10
	if strings.HasPrefix(text, "0x") || strings.HasPrefix(text, "0X") {
		text, base = text[2:], 16
	}
	return new(big.Int).SetString(text, base)
}

func inRange(v *big.Int, t Type) bool {
	switch Resolve(t) {
	case Byte:
		return v.Sign() >= 0 && v.Cmp(big.NewInt(255)) <= 0
	case Int:
		return v.Cmp(minInt) >= 0 && v.Cmp(maxInt) <= 0
	}
	return true
}

// intLit checks an integer literal, possibly negated, where want is expected.
// It may stand for a Float or a Byte.
func (c *checker) intLit(e *syntax.IntLit, want Type, neg bool, sp syntax.Span) Type {
	v, ok := ParseIntLit(e.Text)
	if !ok {
		return invalid // the lexer reported it
	}
	if neg {
		v.Neg(v)
	}
	switch Resolve(want) {
	case Float:
		f, _ := new(big.Float).SetInt(v).Float64()
		c.consts[e] = &Const{Float: f}
		return Float
	case Byte:
		if !inRange(v, Byte) {
			c.errorf("R0308", sp, "%s does not fit in a Byte, which holds 0 to 255", v)
			return invalid
		}
		c.consts[e] = &Const{Int: v}
		return Byte
	}
	if !inRange(v, Int) {
		c.errorf("R0308", sp, "%s does not fit in an Int, which holds -9223372036854775808 to 9223372036854775807", v)
		return invalid
	}
	c.consts[e] = &Const{Int: v}
	return Int
}

func (c *checker) floatLit(e *syntax.FloatLit) Type {
	f, err := strconv.ParseFloat(strings.ReplaceAll(e.Text, "_", ""), 64)
	if err != nil {
		c.errorf("R0308", e.Span(), "%s is out of range for a Float", e.Text)
		return invalid
	}
	c.consts[e] = &Const{Float: f}
	return Float
}

func isZero(k *Const, t Type) bool {
	switch Resolve(t) {
	case Int, Byte:
		return k.Int != nil && k.Int.Sign() == 0
	case Float:
		return k.Float == 0
	}
	return false
}

// foldUnary records the value of -x or !x when x is constant.
func (c *checker) foldUnary(e *syntax.UnaryExpr, t Type) {
	x := c.consts[e.X]
	if x == nil {
		return
	}
	switch {
	case e.Op == syntax.NOT:
		c.consts[e] = &Const{Bool: !x.Bool}
	case Resolve(t) == Float:
		c.consts[e] = &Const{Float: -x.Float}
	case x.Int != nil:
		v := new(big.Int).Neg(x.Int)
		if !inRange(v, t) {
			c.errorf("R0308", e.Span(), "the constant %s overflows %s", v, Resolve(t))
			return
		}
		c.consts[e] = &Const{Int: v}
	}
}

// foldBinary records the value of x op y when both are constant, and reports
// overflow and division by zero.
func (c *checker) foldBinary(e *syntax.BinaryExpr, operand Type) {
	x, y := c.consts[e.X], c.consts[e.Y]
	if (e.Op == syntax.SLASH || e.Op == syntax.PERCENT) && y != nil && isZero(y, operand) {
		c.errorf("R0308", e.Span(), "division by zero")
		return
	}
	if x == nil || y == nil {
		return
	}
	switch e.Op {
	case syntax.ANDAND:
		c.consts[e] = &Const{Bool: x.Bool && y.Bool}
		return
	case syntax.OROR:
		c.consts[e] = &Const{Bool: x.Bool || y.Bool}
		return
	case syntax.EQ, syntax.NE, syntax.LT, syntax.LE, syntax.GT, syntax.GE:
		if r, ok := compareConsts(e.Op, x, y, operand); ok {
			c.consts[e] = &Const{Bool: r}
		}
		return
	}
	switch Resolve(operand) {
	case Int, Byte:
		r := new(big.Int)
		switch e.Op {
		case syntax.PLUS:
			r.Add(x.Int, y.Int)
		case syntax.MINUS:
			r.Sub(x.Int, y.Int)
		case syntax.STAR:
			r.Mul(x.Int, y.Int)
		case syntax.SLASH:
			r.Quo(x.Int, y.Int)
		case syntax.PERCENT:
			r.Rem(x.Int, y.Int)
		}
		if !inRange(r, operand) {
			c.errorf("R0308", e.Span(), "the constant %s overflows %s", r, Resolve(operand))
			return
		}
		c.consts[e] = &Const{Int: r}
	case Float:
		var r float64
		switch e.Op {
		case syntax.PLUS:
			r = x.Float + y.Float
		case syntax.MINUS:
			r = x.Float - y.Float
		case syntax.STAR:
			r = x.Float * y.Float
		case syntax.SLASH:
			r = x.Float / y.Float
		default:
			return // % on Float is math.Mod in Go, not a constant
		}
		if math.IsInf(r, 0) || math.IsNaN(r) {
			c.errorf("R0308", e.Span(), "the constant overflows Float")
			return
		}
		c.consts[e] = &Const{Float: r}
	case String:
		if e.Op == syntax.PLUS {
			c.consts[e] = &Const{Str: x.Str + y.Str}
		}
	}
}

func compareConsts(op syntax.Kind, x, y *Const, t Type) (bool, bool) {
	var cmp int
	switch Resolve(t) {
	case Int, Byte:
		cmp = x.Int.Cmp(y.Int)
	case Float:
		switch {
		case x.Float < y.Float:
			cmp = -1
		case x.Float > y.Float:
			cmp = 1
		}
	case String:
		cmp = strings.Compare(x.Str, y.Str)
	case Bool:
		if op != syntax.EQ && op != syntax.NE {
			return false, false
		}
		if x.Bool != y.Bool {
			cmp = 1
		}
	default:
		return false, false
	}
	switch op {
	case syntax.EQ:
		return cmp == 0, true
	case syntax.NE:
		return cmp != 0, true
	case syntax.LT:
		return cmp < 0, true
	case syntax.LE:
		return cmp <= 0, true
	case syntax.GT:
		return cmp > 0, true
	}
	return cmp >= 0, true
}

// constSyntax reports whether e is built only of literals, names and
// operators, as a const initializer must be.
func constSyntax(e syntax.Expr) bool {
	switch e := e.(type) {
	case *syntax.IntLit, *syntax.FloatLit, *syntax.StringLit, *syntax.BoolLit, *syntax.Ident:
		return true
	case *syntax.ParenExpr:
		return constSyntax(e.X)
	case *syntax.UnaryExpr:
		return constSyntax(e.X)
	case *syntax.BinaryExpr:
		return constSyntax(e.X) && constSyntax(e.Y)
	}
	return false
}

// checkConst checks a const declaration and computes its value.
func (c *checker) checkConst(k *ConstObj) {
	switch k.state {
	case 2:
		return
	case 1:
		if !k.cyclic {
			c.errorf("R0308", k.Decl.Name.Span(), "constant `%s` refers to itself", k.Name)
			k.cyclic = true
		}
		return
	}
	k.state = 1
	defer func() { k.state = 2 }()
	switch Resolve(k.Type) {
	case Int, Float, Bool, String, Byte:
	default:
		if !isInvalid(k.Type) {
			c.errorf("R0308", k.Decl.Type.Span(), "a constant must be an Int, Float, Bool, String or Byte, not %s", k.Type)
		}
		return
	}
	v := k.Decl.Value
	if !constSyntax(v) {
		c.errorf("R0308", v.Span(), "the value of a constant must be literals and constants combined with operators")
		return
	}
	saved := saveCtx(c)
	c.fn, c.scope, c.loops, c.tparams = nil, nil, nil, nil
	errs := len(c.errs)
	t := c.expr(v, k.Type)
	saved.restore(c)
	if isInvalid(t) {
		return
	}
	k.Value = c.consts[v]
	if k.Value == nil && !k.cyclic && len(c.errs) == errs {
		c.errorf("R0308", v.Span(), "the value of a constant must be literals and constants combined with operators")
	}
}

type ctxState struct {
	fn      *funcCtx
	scope   *scope
	loops   []*loopCtx
	tparams []map[string]*TypeParam
	dead    bool
}

func saveCtx(c *checker) ctxState {
	return ctxState{c.fn, c.scope, c.loops, c.tparams, c.dead}
}

func (s ctxState) restore(c *checker) {
	c.fn, c.scope, c.loops, c.tparams, c.dead = s.fn, s.scope, s.loops, s.tparams, s.dead
}
