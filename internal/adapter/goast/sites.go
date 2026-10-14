package goast

import (
	"go/ast"
	"go/token"
	"slices"

	"github.com/gophernment/gomu/internal/mutant"
	"github.com/gophernment/gomu/internal/mutator"
)

// Site is one place in a file where a mutation can be applied.
type Site struct {
	// Mutant describes the change for reports; its Status is Pending.
	Mutant mutant.Mutant
	// Mutation changes the parsed AST in place and reverts it.
	Mutation mutator.Mutation
}

// Sites returns every mutation the mutators can make in f, ordered by source
// position. Mutators that report the same position keep their given order,
// so the result is deterministic.
//
// Only code inside function bodies (including function literals) is
// considered; signatures and package-level declarations are not. Mutators are
// also offered each function declaration and literal as a whole, so one that
// needs the signature (the types a return must have) can find it; it must
// still mutate only inside the body. A file marked
// as generated ("Code generated ... DO NOT EDIT.") yields no sites. Nil
// mutators are skipped.
//
// The returned mutations refer to f.AST. Apply only one at a time and Revert it
// before applying the next; do not share f between goroutines.
func (f *File) Sites(mutators []mutator.Mutator) []Site {
	if ast.IsGenerated(f.AST) {
		return nil
	}

	type found struct {
		pos token.Pos
		Site
	}
	var all []found

	collect := func(n ast.Node) bool {
		// ast.Inspect also calls with nil at the end of each node.
		for _, m := range mutators {
			if m == nil || n == nil {
				continue
			}
			for _, mu := range m.Mutations(n) {
				all = append(all, found{
					pos: mu.Pos,
					Site: Site{
						Mutant: mutant.Mutant{
							Operator:    m.Name(),
							Pos:         f.Position(mu.Pos),
							Original:    mu.Original,
							Replacement: mu.Replacement,
						},
						Mutation: mu,
					},
				})
			}
		}
		return true
	}

	// Only function bodies are mutated: that is the code the compiler
	// instruments for coverage, so a "not covered" verdict is trustworthy.
	// Package-level initializers run at init and would look uncovered.
	ast.Inspect(f.AST, func(n ast.Node) bool {
		switch fn := n.(type) {
		case *ast.FuncDecl:
			if fn.Body != nil {
				collect(fn) // the signature tells a mutator what the returns must be
				ast.Inspect(fn.Body, collect)
			}
			return false
		case *ast.FuncLit: // a literal outside any function, e.g. var f = func() {...}
			collect(fn)
			ast.Inspect(fn.Body, collect)
			return false
		}
		return true
	})

	// Stable: equal positions keep mutator registration order.
	slices.SortStableFunc(all, func(a, b found) int { return int(a.pos) - int(b.pos) })

	sites := make([]Site, len(all))
	for i, s := range all {
		sites[i] = s.Site
	}
	return sites
}
