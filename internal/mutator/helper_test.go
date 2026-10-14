package mutator_test

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
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

// mutateFile parses src as a Go file, applies every mutation of m one at a
// time and returns the whole file after each, with whitespace collapsed,
// together with the mutations. It fails the test if Revert does not restore
// the file or a result does not parse.
func mutateFile(t *testing.T, m mutator.Mutator, src string) (results []string, muts []mutator.Mutation) {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "p.go", src, 0)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	print := func() string {
		var buf bytes.Buffer
		if err := format.Node(&buf, fset, file); err != nil {
			t.Fatalf("format: %v", err)
		}
		return buf.String()
	}
	before := print()

	results = []string{}
	ast.Inspect(file, func(n ast.Node) bool {
		for _, mu := range m.Mutations(n) {
			mu.Apply()
			out := print()
			if _, err := parser.ParseFile(token.NewFileSet(), "p.go", out, 0); err != nil {
				t.Fatalf("mutant does not parse: %v\n%s", err, out)
			}
			results = append(results, strings.Join(strings.Fields(out), " "))
			mu.Revert()

			if after := print(); after != before {
				t.Fatalf("Revert did not restore the file:\n%s\nwant\n%s", after, before)
			}
			muts = append(muts, mu)
		}
		return true
	})
	return results, muts
}
