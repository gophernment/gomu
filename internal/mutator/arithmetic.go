package mutator

import (
	"go/ast"
	"go/token"
)

// Arithmetic swaps arithmetic operators in binary expressions:
// + <-> -, * <-> /, and % -> *.
//
// Without type information it cannot tell string concatenation from addition,
// so it skips a + that has a string literal operand. Other string + cases may
// still yield mutants that fail to compile; the runner must treat build errors
// as invalid, not killed.
//
// The zero value is ready to use.
type Arithmetic struct{}

// Name implements Mutator.
func (Arithmetic) Name() string { return "arithmetic" }

// Mutations implements Mutator.
func (Arithmetic) Mutations(n ast.Node) []Mutation {
	b, ok := n.(*ast.BinaryExpr)
	if !ok || b == nil {
		return nil
	}

	from := b.Op
	to, ok := arithmeticSwap(from)
	if !ok {
		return nil
	}
	if from == token.ADD && (isStringLit(b.X) || isStringLit(b.Y)) {
		return nil
	}

	return []Mutation{{
		Pos:         b.OpPos,
		Original:    from.String(),
		Replacement: to.String(),
		Apply:       func() { b.Op = to },
		Revert:      func() { b.Op = from },
	}}
}

func arithmeticSwap(op token.Token) (token.Token, bool) {
	switch op {
	case token.ADD:
		return token.SUB, true
	case token.SUB:
		return token.ADD, true
	case token.MUL:
		return token.QUO, true
	case token.QUO:
		return token.MUL, true
	case token.REM:
		return token.MUL, true
	default:
		return token.ILLEGAL, false
	}
}

func isStringLit(e ast.Expr) bool {
	lit, ok := e.(*ast.BasicLit)
	return ok && lit.Kind == token.STRING
}
