package mutator

import (
	"go/ast"
	"go/types"
	"slices"
	"unicode/utf8"
)

// Deletion removes a call statement: `cache.Invalidate(key)` becomes nothing.
//
// It catches side effects no test checks: if the tests still pass without the
// call, nothing asserts what the call does.
//
// Only statements that are a bare call are removed. Assignments, declarations,
// return, defer and go statements are left alone, and so is panic, because
// removing it breaks the "missing return" rule and would only make a mutant
// that does not compile. A removed call can still leave an unused variable or
// import behind; such a mutant fails to compile and is reported as not viable.
//
// The zero value is ready to use.
type Deletion struct{}

// Name implements Mutator.
func (Deletion) Name() string { return "deletion" }

// Mutations implements Mutator. It looks at the statement lists of blocks and
// of case and select clauses, so every statement is considered exactly once.
func (Deletion) Mutations(n ast.Node) []Mutation {
	list := stmtList(n)
	if list == nil {
		return nil
	}
	stmts := *list

	var out []Mutation
	for i, s := range stmts {
		es, ok := s.(*ast.ExprStmt)
		if !ok {
			continue
		}
		call, ok := es.X.(*ast.CallExpr)
		if !ok || isPanic(call) {
			continue
		}
		out = append(out, Mutation{
			Pos:         s.Pos(),
			Original:    callName(call),
			Replacement: "(removed)",
			// Concat builds a new slice, so the original stays intact for Revert.
			Apply:  func() { *list = slices.Concat(stmts[:i:i], stmts[i+1:]) },
			Revert: func() { *list = stmts },
		})
	}
	return out
}

// stmtList returns the address of the statement list n owns, or nil.
func stmtList(n ast.Node) *[]ast.Stmt {
	switch n := n.(type) {
	case *ast.BlockStmt:
		if n != nil {
			return &n.List
		}
	case *ast.CaseClause:
		if n != nil {
			return &n.Body
		}
	case *ast.CommClause:
		if n != nil {
			return &n.Body
		}
	}
	return nil
}

func isPanic(call *ast.CallExpr) bool {
	id, ok := call.Fun.(*ast.Ident)
	return ok && id.Name == "panic"
}

// callName describes a call for a report without printing its arguments.
func callName(call *ast.CallExpr) string {
	return truncate(types.ExprString(call.Fun)) + "(...)"
}

// maxShown caps how much of a source expression a report shows.
const maxShown = 60

// truncate shortens s to maxShown runes, marking the cut with an ellipsis.
func truncate(s string) string {
	if utf8.RuneCountInString(s) <= maxShown {
		return s
	}
	return string([]rune(s)[:maxShown]) + "…"
}
