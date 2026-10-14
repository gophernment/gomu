package mutator_test

import (
	"errors"
	"go/ast"
	"go/token"
	"slices"
	"testing"

	"github.com/pallat/gomu/internal/mutator"
)

// flipAdd is a minimal Mutator used to exercise the contract: it turns + into -.
type flipAdd struct{ name string }

func (f flipAdd) Name() string { return f.name }

func (f flipAdd) Mutations(n ast.Node) []mutator.Mutation {
	b, ok := n.(*ast.BinaryExpr)
	if !ok || b.Op != token.ADD {
		return nil
	}
	return []mutator.Mutation{{
		Original:    "+",
		Replacement: "-",
		Apply:       func() { b.Op = token.SUB },
		Revert:      func() { b.Op = token.ADD },
	}}
}

func names(ms []mutator.Mutator) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Name()
	}
	return out
}

func TestMutationApplyAndRevert(t *testing.T) {
	t.Parallel()

	expr := &ast.BinaryExpr{Op: token.ADD}

	got := flipAdd{name: "flip"}.Mutations(expr)
	if len(got) != 1 {
		t.Fatalf("Mutations() returned %d mutations, want 1", len(got))
	}

	got[0].Apply()
	if expr.Op != token.SUB {
		t.Errorf("after Apply, Op = %v, want %v", expr.Op, token.SUB)
	}

	got[0].Revert()
	if expr.Op != token.ADD {
		t.Errorf("after Revert, Op = %v, want %v", expr.Op, token.ADD)
	}
}

func TestMutatorIgnoresOtherNodes(t *testing.T) {
	t.Parallel()

	nodes := []ast.Node{
		&ast.BinaryExpr{Op: token.MUL},
		&ast.Ident{Name: "x"},
		nil,
	}
	for _, n := range nodes {
		if got := (flipAdd{name: "flip"}).Mutations(n); len(got) != 0 {
			t.Errorf("Mutations(%T) = %d mutations, want 0", n, len(got))
		}
	}
}

func TestRegistryRegister(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   []mutator.Mutator
		wantErr error
		want    []string
	}{
		{
			name:  "empty registry",
			input: nil,
			want:  []string{},
		},
		{
			name:  "keeps registration order",
			input: []mutator.Mutator{flipAdd{"b"}, flipAdd{"a"}, flipAdd{"c"}},
			want:  []string{"b", "a", "c"},
		},
		{
			name:    "nil mutator",
			input:   []mutator.Mutator{nil},
			wantErr: mutator.ErrInvalid,
			want:    []string{},
		},
		{
			name:    "empty name",
			input:   []mutator.Mutator{flipAdd{""}},
			wantErr: mutator.ErrInvalid,
			want:    []string{},
		},
		{
			name:    "duplicate name",
			input:   []mutator.Mutator{flipAdd{"a"}, flipAdd{"a"}},
			wantErr: mutator.ErrDuplicate,
			want:    []string{"a"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				r   mutator.Registry
				err error
			)
			for _, m := range tt.input {
				if e := r.Register(m); e != nil {
					err = e
				}
			}

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Register() error = %v, want %v", err, tt.wantErr)
			}
			if got := names(r.All()); !slices.Equal(got, tt.want) {
				t.Errorf("All() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRegistryAllReturnsCopy(t *testing.T) {
	t.Parallel()

	var r mutator.Registry
	if err := r.Register(flipAdd{"a"}); err != nil {
		t.Fatal(err)
	}

	all := r.All()
	all[0] = flipAdd{"hacked"}

	if got := names(r.All()); !slices.Equal(got, []string{"a"}) {
		t.Errorf("registry mutated through All(): %v", got)
	}
}
