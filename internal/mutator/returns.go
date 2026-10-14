package mutator

import (
	"go/ast"
	"go/token"
	"go/types"
)

// Return replaces a returned value with a wrong one: a bool is negated, an
// error or other nil-able value becomes nil, a number becomes 0 and a string
// becomes "".
//
// It catches tests that call a function but never check what it gives back.
//
// There is no type information, so only results whose declared type is plainly
// one of those kinds are changed: bool, string, the numeric types, error, any,
// and pointer, slice, map, channel, func and interface types. Arrays, structs
// and named types are left alone, and so are returns that are already the
// replacement (`return nil` is not turned into nil), `return f()` that passes
// on several values, and naked returns.
//
// The zero value is ready to use.
type Return struct{}

// Name implements Mutator.
func (Return) Name() string { return "return" }

// Mutations implements Mutator. It reads the signature of a function
// declaration or literal and looks at the returns in its own body; returns in
// a nested function literal belong to that literal.
func (Return) Mutations(n ast.Node) []Mutation {
	var (
		sig  *ast.FuncType
		body *ast.BlockStmt
	)
	switch fn := n.(type) {
	case *ast.FuncDecl:
		if fn != nil {
			sig, body = fn.Type, fn.Body
		}
	case *ast.FuncLit:
		if fn != nil {
			sig, body = fn.Type, fn.Body
		}
	}
	if sig == nil || body == nil {
		return nil
	}
	want := resultTypes(sig)
	if len(want) == 0 {
		return nil
	}

	var out []Mutation
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.ReturnStmt:
			if len(n.Results) == len(want) {
				for i, res := range n.Results {
					if mu, ok := wrongValue(want[i], res); ok {
						mu.Apply = func() { n.Results[i] = mu.repl }
						mu.Revert = func() { n.Results[i] = res }
						out = append(out, mu.Mutation)
					}
				}
			}
		}
		return true
	})
	return out
}

// resultTypes lists the declared type of every result, one entry per value.
func resultTypes(sig *ast.FuncType) []ast.Expr {
	if sig.Results == nil {
		return nil
	}
	var out []ast.Expr
	for _, f := range sig.Results.List {
		for range max(len(f.Names), 1) {
			out = append(out, f.Type)
		}
	}
	return out
}

type replacement struct {
	Mutation
	repl ast.Expr
}

// wrongValue builds the mutation for one returned expression of declared type
// typ, or reports false when there is nothing safe to change.
func wrongValue(typ, res ast.Expr) (replacement, bool) {
	var (
		repl ast.Expr
		text string
	)
	switch kindOf(typ) {
	case kindBool:
		return negate(typ, res), true
	case kindNumber:
		if lit, ok := res.(*ast.BasicLit); ok && (lit.Value == "0" || lit.Value == "0.0") {
			return replacement{}, false
		}
		repl, text = &ast.BasicLit{Kind: token.INT, Value: "0"}, "0"
	case kindString:
		if lit, ok := res.(*ast.BasicLit); ok && (lit.Value == `""` || lit.Value == "``") {
			return replacement{}, false
		}
		repl, text = &ast.BasicLit{Kind: token.STRING, Value: `""`}, `""`
	case kindNilable:
		if id, ok := res.(*ast.Ident); ok && id.Name == "nil" {
			return replacement{}, false
		}
		repl, text = ast.NewIdent("nil"), "nil"
	default:
		return replacement{}, false
	}
	return replacement{
		Mutation: Mutation{Pos: res.Pos(), Original: truncate(types.ExprString(typ)), Replacement: text},
		repl:     repl,
	}, true
}

// negate flips a true/false literal and wraps anything else in !(...).
func negate(typ, res ast.Expr) replacement {
	if id, ok := res.(*ast.Ident); ok && (id.Name == "true" || id.Name == "false") {
		flipped := "true"
		if id.Name == "true" {
			flipped = "false"
		}
		return replacement{
			Mutation: Mutation{Pos: res.Pos(), Original: id.Name, Replacement: flipped},
			repl:     ast.NewIdent(flipped),
		}
	}
	return replacement{
		Mutation: Mutation{Pos: res.Pos(), Original: "bool", Replacement: "negated"},
		repl:     &ast.UnaryExpr{Op: token.NOT, X: &ast.ParenExpr{X: res}},
	}
}

type kind int

const (
	kindOther kind = iota
	kindBool
	kindNumber
	kindString
	kindNilable
)

// kindOf classifies a declared type from its syntax alone.
func kindOf(typ ast.Expr) kind {
	switch t := typ.(type) {
	case *ast.Ident:
		switch t.Name {
		case "bool":
			return kindBool
		case "string":
			return kindString
		case "error", "any":
			return kindNilable
		case "int", "int8", "int16", "int32", "int64",
			"uint", "uint8", "uint16", "uint32", "uint64", "uintptr",
			"byte", "rune", "float32", "float64", "complex64", "complex128":
			return kindNumber
		}
	case *ast.StarExpr, *ast.MapType, *ast.ChanType, *ast.FuncType, *ast.InterfaceType:
		return kindNilable
	case *ast.ArrayType:
		if t.Len == nil { // a slice; an array has no nil
			return kindNilable
		}
	}
	return kindOther
}
