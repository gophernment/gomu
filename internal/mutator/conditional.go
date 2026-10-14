package mutator

import (
	"go/ast"
	"go/token"
)

// Conditional moves comparison operators across their boundary:
// < <-> <=, > <-> >=, and == <-> !=.
//
// Together these catch off-by-one and inverted-condition bugs that tests miss
// when they never probe the boundary value.
//
// The zero value is ready to use.
type Conditional struct{}

// Name implements Mutator.
func (Conditional) Name() string { return "conditional" }

// Mutations implements Mutator.
func (Conditional) Mutations(n ast.Node) []Mutation {
	b, ok := n.(*ast.BinaryExpr)
	if !ok || b == nil {
		return nil
	}

	from := b.Op
	to, ok := conditionalSwap(from)
	if !ok {
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

func conditionalSwap(op token.Token) (token.Token, bool) {
	switch op {
	case token.LSS:
		return token.LEQ, true
	case token.LEQ:
		return token.LSS, true
	case token.GTR:
		return token.GEQ, true
	case token.GEQ:
		return token.GTR, true
	case token.EQL:
		return token.NEQ, true
	case token.NEQ:
		return token.EQL, true
	default:
		return token.ILLEGAL, false
	}
}
