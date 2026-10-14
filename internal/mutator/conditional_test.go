package mutator_test

import (
	"go/ast"
	"go/parser"
	"slices"
	"testing"

	"github.com/gophernment/gomu/internal/mutator"
)

func TestConditionalName(t *testing.T) {
	t.Parallel()

	if got, want := (mutator.Conditional{}).Name(), "conditional"; got != want {
		t.Errorf("Name() = %q, want %q", got, want)
	}
}

func TestConditionalMutations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want []string
	}{
		{"lss to leq", "a < b", []string{"a <= b"}},
		{"leq to lss", "a <= b", []string{"a < b"}},
		{"gtr to geq", "a > b", []string{"a >= b"}},
		{"geq to gtr", "a >= b", []string{"a > b"}},
		{"eql to neq", "a == b", []string{"a != b"}},
		{"neq to eql", "a != b", []string{"a == b"}},
		{"nested visits every operator", "a < b == c", []string{"a < b != c", "a <= b == c"}},
		{"mixed with logical", "a < b && c > d", []string{"a <= b && c > d", "a < b && c >= d"}},
		{"arithmetic is not touched", "a + b", []string{}},
		{"logical is not touched", "a && b", []string{}},
		{"no operator", "f(x)", []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := mutateWith(t, mutator.Conditional{}, tt.src)
			if !slices.Equal(got, tt.want) {
				t.Errorf("mutate(%q) = %q, want %q", tt.src, got, tt.want)
			}
		})
	}
}

func TestConditionalMutationText(t *testing.T) {
	t.Parallel()

	root, err := parser.ParseExpr("a < b")
	if err != nil {
		t.Fatal(err)
	}

	got := mutator.Conditional{}.Mutations(root)
	if len(got) != 1 {
		t.Fatalf("got %d mutations, want 1", len(got))
	}
	if got[0].Original != "<" || got[0].Replacement != "<=" {
		t.Errorf("Original/Replacement = %q/%q, want </<=", got[0].Original, got[0].Replacement)
	}
	if want := root.(*ast.BinaryExpr).OpPos; got[0].Pos != want {
		t.Errorf("Pos = %v, want operator position %v", got[0].Pos, want)
	}
}

func TestConditionalIgnoresNilAndOtherNodes(t *testing.T) {
	t.Parallel()

	for _, n := range []ast.Node{nil, &ast.Ident{Name: "x"}, (*ast.BinaryExpr)(nil)} {
		if got := (mutator.Conditional{}).Mutations(n); len(got) != 0 {
			t.Errorf("Mutations(%T) = %d mutations, want 0", n, len(got))
		}
	}
}

func TestOperatorsRegisterTogether(t *testing.T) {
	t.Parallel()

	var r mutator.Registry
	for _, m := range []mutator.Mutator{mutator.Arithmetic{}, mutator.Conditional{}} {
		if err := r.Register(m); err != nil {
			t.Fatalf("Register(%s) = %v", m.Name(), err)
		}
	}
}
