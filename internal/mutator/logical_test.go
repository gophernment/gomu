package mutator_test

import (
	"go/ast"
	"go/parser"
	"slices"
	"testing"

	"github.com/pallat/gomu/internal/mutator"
)

func TestLogicalName(t *testing.T) {
	t.Parallel()

	if got, want := (mutator.Logical{}).Name(), "logical"; got != want {
		t.Errorf("Name() = %q, want %q", got, want)
	}
}

func TestLogicalMutations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want []string
	}{
		{"and to or", "a && b", []string{"a || b"}},
		{"or to and", "a || b", []string{"a && b"}},
		{"nested visits every operator", "a && b || c", []string{"a && b && c", "a || b || c"}},
		{"parentheses are kept", "a && (b || c)", []string{"a || (b || c)", "a && (b && c)"}},
		{"comparison is not touched", "a < b", []string{}},
		{"bitwise is not touched", "a & b | c", []string{}},
		{"no operator", "f(x)", []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := mutateWith(t, mutator.Logical{}, tt.src)
			if !slices.Equal(got, tt.want) {
				t.Errorf("mutate(%q) = %q, want %q", tt.src, got, tt.want)
			}
		})
	}
}

func TestLogicalMutationText(t *testing.T) {
	t.Parallel()

	root, err := parser.ParseExpr("a && b")
	if err != nil {
		t.Fatal(err)
	}

	got := mutator.Logical{}.Mutations(root)
	if len(got) != 1 {
		t.Fatalf("got %d mutations, want 1", len(got))
	}
	if got[0].Original != "&&" || got[0].Replacement != "||" {
		t.Errorf("Original/Replacement = %q/%q, want &&/||", got[0].Original, got[0].Replacement)
	}
	if want := root.(*ast.BinaryExpr).OpPos; got[0].Pos != want {
		t.Errorf("Pos = %v, want operator position %v", got[0].Pos, want)
	}
}

func TestLogicalIgnoresNilAndOtherNodes(t *testing.T) {
	t.Parallel()

	for _, n := range []ast.Node{nil, &ast.Ident{Name: "x"}, (*ast.BinaryExpr)(nil)} {
		if got := (mutator.Logical{}).Mutations(n); len(got) != 0 {
			t.Errorf("Mutations(%T) = %d mutations, want 0", n, len(got))
		}
	}
}
