package mutator

import (
	"go/ast"
	"go/token"
)

// Logical swaps the boolean connectives: && <-> ||.
//
// It catches conditions whose tests never exercise each operand independently.
//
// The zero value is ready to use.
type Logical struct{}

// Name implements Mutator.
func (Logical) Name() string { return "logical" }

// Mutations implements Mutator.
func (Logical) Mutations(n ast.Node) []Mutation {
	b, ok := n.(*ast.BinaryExpr)
	if !ok || b == nil {
		return nil
	}

	from := b.Op
	var to token.Token
	switch from {
	case token.LAND:
		to = token.LOR
	case token.LOR:
		to = token.LAND
	default:
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
