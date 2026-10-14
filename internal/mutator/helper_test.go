package mutator_test

import (
	"go/ast"
	"go/parser"
	"go/types"
	"testing"

	"github.com/pallat/gomu/internal/mutator"
)

// mutateWith parses src as an expression, applies every mutation of m one at a
// time, and returns the resulting expressions in AST pre-order. It fails the
// test if Revert does not restore the original expression.
func mutateWith(t *testing.T, m mutator.Mutator, src string) []string {
	t.Helper()

	root, err := parser.ParseExpr(src)
	if err != nil {
		t.Fatalf("ParseExpr(%q): %v", src, err)
	}
	before := types.ExprString(root)

	got := []string{}
	ast.Inspect(root, func(n ast.Node) bool {
		for _, mu := range m.Mutations(n) {
			mu.Apply()
			got = append(got, types.ExprString(root))
			mu.Revert()

			if after := types.ExprString(root); after != before {
				t.Fatalf("Revert did not restore %q, got %q", before, after)
			}
		}
		return true
	})
	return got
}
