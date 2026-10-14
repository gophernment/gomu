package mutator_test

import (
	"go/ast"
	"go/parser"
	"slices"
	"testing"

	"github.com/pallat/gomu/internal/mutator"
)

func TestArithmeticName(t *testing.T) {
	t.Parallel()

	if got, want := (mutator.Arithmetic{}).Name(), "arithmetic"; got != want {
		t.Errorf("Name() = %q, want %q", got, want)
	}
}

func TestArithmeticMutations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want []string
	}{
		{"add to sub", "a + b", []string{"a - b"}},
		{"sub to add", "a - b", []string{"a + b"}},
		{"mul to quo", "a * b", []string{"a / b"}},
		{"quo to mul", "a / b", []string{"a * b"}},
		{"rem to mul", "a % b", []string{"a * b"}},
		{"nested visits every operator", "a + b*c", []string{"a - b * c", "a + b / c"}},
		{"parentheses are kept", "(a + b) * c", []string{"(a + b) / c", "(a - b) * c"}},
		{"unrelated operators", "a < b && c == d", []string{}},
		{"no operator", "f(x)", []string{}},
		{"string literal on the left", `"x" + s`, []string{}},
		{"string literal on the right", `s + "x"`, []string{}},
		{"string literal does not hide other sites", `("x" + s) + n`, []string{`("x" + s) - n`}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := mutateWith(t, mutator.Arithmetic{}, tt.src)
			if !slices.Equal(got, tt.want) {
				t.Errorf("mutate(%q) = %q, want %q", tt.src, got, tt.want)
			}
		})
	}
}

func TestArithmeticMutationText(t *testing.T) {
	t.Parallel()

	root, err := parser.ParseExpr("a + b")
	if err != nil {
		t.Fatal(err)
	}

	got := mutator.Arithmetic{}.Mutations(root)
	if len(got) != 1 {
		t.Fatalf("got %d mutations, want 1", len(got))
	}
	if got[0].Original != "+" || got[0].Replacement != "-" {
		t.Errorf("Original/Replacement = %q/%q, want +/-", got[0].Original, got[0].Replacement)
	}
	if want := root.(*ast.BinaryExpr).OpPos; got[0].Pos != want {
		t.Errorf("Pos = %v, want operator position %v", got[0].Pos, want)
	}
}

func TestArithmeticIgnoresNilAndOtherNodes(t *testing.T) {
	t.Parallel()

	for _, n := range []ast.Node{nil, &ast.Ident{Name: "x"}, (*ast.BinaryExpr)(nil)} {
		if got := (mutator.Arithmetic{}).Mutations(n); len(got) != 0 {
			t.Errorf("Mutations(%T) = %d mutations, want 0", n, len(got))
		}
	}
}

func TestArithmeticSatisfiesMutator(t *testing.T) {
	t.Parallel()

	var r mutator.Registry
	if err := r.Register(mutator.Arithmetic{}); err != nil {
		t.Fatalf("Register(Arithmetic{}) = %v", err)
	}
}
