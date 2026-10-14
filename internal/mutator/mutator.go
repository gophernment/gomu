// Package mutator defines the Mutator role and a Registry of mutators.
// Each concrete operator (arithmetic, conditional, ...) lives in its own file
// and is added by registering it, so existing code never changes (Open/Closed).
package mutator

import (
	"errors"
	"fmt"
	"go/ast"
	"go/token"
)

// Errors returned by Registry.Register.
var (
	// ErrInvalid means the mutator is nil or has an empty name.
	ErrInvalid = errors.New("mutator: invalid mutator")
	// ErrDuplicate means a mutator with the same name is already registered.
	ErrDuplicate = errors.New("mutator: duplicate name")
)

// Mutation is one reversible change to an AST node.
//
// Apply changes the node in place and Revert restores it exactly, so one parsed
// file can be reused for every mutant. Both must be non-nil.
// Pos is the position of the token that changes, such as the operator.
// Original and Replacement are the source text before and after, for reports.
type Mutation struct {
	Pos         token.Pos
	Original    string
	Replacement string
	Apply       func()
	Revert      func()
}

// Mutator finds the mutations a single operator can make on an AST node.
type Mutator interface {
	// Name is a short unique identifier such as "arithmetic".
	Name() string
	// Mutations returns the mutations available on n, or nil when the
	// operator does not apply to it. It must not modify n.
	Mutations(n ast.Node) []Mutation
}

// Registry holds mutators in registration order. The zero value is ready to use.
// It is not safe for concurrent Register calls; build it once at startup.
type Registry struct {
	mutators []Mutator
}

// Register adds m. It returns ErrInvalid for a nil mutator or empty name and
// ErrDuplicate when the name is taken.
func (r *Registry) Register(m Mutator) error {
	if m == nil || m.Name() == "" {
		return ErrInvalid
	}
	for _, have := range r.mutators {
		if have.Name() == m.Name() {
			return fmt.Errorf("%w: %q", ErrDuplicate, m.Name())
		}
	}
	r.mutators = append(r.mutators, m)
	return nil
}

// All returns a copy of the registered mutators in registration order.
func (r *Registry) All() []Mutator {
	out := make([]Mutator, len(r.mutators))
	copy(out, r.mutators)
	return out
}
