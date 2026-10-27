package syntax

// Inspect calls f for n and, while f returns true, for every node inside n:
// statements, expressions and patterns (not type expressions).
func Inspect(n Node, f func(Node) bool) {
	if n == nil || !f(n) {
		return
	}
	each := func(ns ...Node) {
		for _, c := range ns {
			if c != nil {
				Inspect(c, f)
			}
		}
	}
	switch n := n.(type) {
	case *LetStmt:
		each(n.Pat, n.Value)
	case *AssignStmt:
		each(n.Target, n.Value)
	case *ExprStmt:
		each(n.X)
	case *CallExpr:
		each(n.Fun)
		for _, a := range n.Args {
			each(a)
		}
	case *MethodCallExpr:
		each(n.Recv)
		for _, a := range n.Args {
			each(a)
		}
	case *FieldExpr:
		each(n.X)
	case *IndexExpr:
		each(n.X, n.Index)
	case *TryExpr:
		each(n.X)
	case *InterpLit:
		for _, x := range n.Exprs {
			each(x)
		}
	case *UnaryExpr:
		each(n.X)
	case *AsExpr:
		each(n.X)
	case *BinaryExpr:
		each(n.X, n.Y)
	case *ParenExpr:
		each(n.X)
	case *TupleExpr:
		for _, x := range n.Elems {
			each(x)
		}
	case *ListExpr:
		for _, x := range n.Elems {
			each(x)
		}
	case *StructLit:
		for _, fi := range n.Fields {
			each(fi.Value)
		}
		if n.Base != nil {
			each(n.Base)
		}
	case *BlockExpr:
		for _, s := range n.Stmts {
			each(s)
		}
	case *IfExpr:
		each(n.Cond, n.Then)
		if n.Else != nil {
			each(n.Else)
		}
	case *MatchExpr:
		each(n.Subject)
		for _, a := range n.Arms {
			each(a.Pat)
			if a.Guard != nil {
				each(a.Guard)
			}
			each(a.Body)
		}
	case *WhileExpr:
		each(n.Cond, n.Body)
	case *LoopExpr:
		each(n.Body)
	case *ForExpr:
		each(n.Pat, n.Iter)
		if n.End != nil {
			each(n.End)
		}
		each(n.Body)
	case *ClosureExpr:
		for _, p := range n.Params {
			each(p.Pat)
		}
		each(n.Body)
	case *ReturnExpr:
		if n.Value != nil {
			each(n.Value)
		}
	case *TuplePat:
		for _, p := range n.Elems {
			each(p)
		}
	case *VariantPat:
		for _, p := range n.Args {
			each(p)
		}
	case *OrPat:
		for _, p := range n.Alts {
			each(p)
		}
	case *LitPat:
		each(n.Lit)
	}
}
