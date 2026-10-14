package mutator_test

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"slices"
	"strings"
	"testing"

	"github.com/pallat/gomu/internal/mutator"
)

// deleteEach parses a Go file, applies every Deletion mutation one at a time
// and returns the whole file after each, with whitespace collapsed. It fails
// the test if Revert does not restore the file or a result does not parse.
func deleteEach(t *testing.T, src string) (results []string, muts []mutator.Mutation) {
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
		for _, mu := range (mutator.Deletion{}).Mutations(n) {
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

func TestDeletionRemovesBareCalls(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want []string
	}{
		{"one call", `a()`, []string{`func f() { }`}},
		{"first of two", "a()\nb()", []string{`func f() { b() }`, `func f() { a() }`}},
		{"middle", "a()\nb()\nc()", []string{`func f() { b() c() }`, `func f() { a() c() }`, `func f() { a() b() }`}},
		{"method call", `x.Do(1)`, []string{`func f() { }`}},
		{"nested block", "if ok {\n\ta()\n}", []string{`func f() { if ok { } }`}},
		{"else block", "if ok {\n\ta()\n} else {\n\tb()\n}", []string{`func f() { if ok { } else { b() } }`, `func f() { if ok { a() } else { } }`}},
		{"for body", "for {\n\ta()\n}", []string{`func f() { for { } }`}},
		{"case body", "switch x {\ncase 1:\n\ta()\ndefault:\n\tb()\n}", []string{`func f() { switch x { case 1: default: b() } }`, `func f() { switch x { case 1: a() default: } }`}},
		{"select body", "select {\ncase <-c:\n\ta()\n}", []string{`func f() { select { case <-c: } }`}},
		{"func literal", "g := func() {\n\ta()\n}\n_ = g", []string{`func f() { g := func() { } _ = g }`}},
		{"only calls: others untouched", "x := a()\ny = b()\ndefer c()\ngo d()\nreturn", nil},
		{"panic untouched", `panic("boom")`, nil},
		{"conversion-like and builtins are calls", "close(c)\ndelete(m, k)", []string{`func f() { delete(m, k) }`, `func f() { close(c) }`}},
		{"nothing", ``, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, _ := deleteEach(t, "package p\n\nfunc f() {\n"+tt.body+"\n}\n")
			want := make([]string, len(tt.want))
			for i, w := range tt.want {
				want[i] = "package p " + w
			}
			if !slices.Equal(got, want) {
				t.Errorf("got\n%q\nwant\n%q", got, want)
			}
		})
	}
}

func TestDeletionReportsTheCallWithoutArguments(t *testing.T) {
	t.Parallel()

	src := "package p\n\nfunc f() {\n\tcache.Invalidate(\"secret-token\")\n\tfn()\n\ta.b(1).c(2)\n}\n"
	_, muts := deleteEach(t, src)

	var got [][2]string
	for _, mu := range muts {
		got = append(got, [2]string{mu.Original, mu.Replacement})
	}
	want := [][2]string{
		{"cache.Invalidate(...)", "(removed)"},
		{"fn(...)", "(removed)"},
		{"a.b(1).c(...)", "(removed)"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDeletionTruncatesLongNames(t *testing.T) {
	t.Parallel()

	name := strings.Repeat("a", 200)
	_, muts := deleteEach(t, "package p\n\nfunc f() {\n\t"+name+"()\n}\n")

	if len(muts) != 1 {
		t.Fatalf("got %d mutations, want 1", len(muts))
	}
	want := strings.Repeat("a", 60) + "…(...)"
	if muts[0].Original != want {
		t.Errorf("Original = %q, want %q", muts[0].Original, want)
	}
}

func TestDeletionPointsAtTheStatement(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	src := "package p\n\nfunc f() {\n\ta()\n}\n"
	file, err := parser.ParseFile(fset, "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var pos token.Pos
	ast.Inspect(file, func(n ast.Node) bool {
		for _, mu := range (mutator.Deletion{}).Mutations(n) {
			pos = mu.Pos
		}
		return true
	})
	if p := fset.Position(pos); p.Line != 4 || p.Column != 2 {
		t.Errorf("position = %d:%d, want 4:2", p.Line, p.Column)
	}
}

func TestDeletionIgnoresOtherNodesAndNil(t *testing.T) {
	t.Parallel()

	var (
		block  *ast.BlockStmt
		cas    *ast.CaseClause
		comm   *ast.CommClause
		expr   = &ast.CallExpr{Fun: ast.NewIdent("a")}
		stmt   = &ast.ExprStmt{X: expr}
		inputs = []ast.Node{nil, block, cas, comm, expr, stmt, ast.NewIdent("x")}
	)
	for _, n := range inputs {
		if got := (mutator.Deletion{}).Mutations(n); got != nil {
			t.Errorf("Mutations(%T) = %v, want nil", n, got)
		}
	}
}

func TestDeletionName(t *testing.T) {
	t.Parallel()

	if got := (mutator.Deletion{}).Name(); got != "deletion" {
		t.Errorf("Name() = %q", got)
	}
}

func TestDeletionKeepsANameOfExactlyTheLimit(t *testing.T) {
	t.Parallel()

	name := strings.Repeat("a", 60)
	_, muts := deleteEach(t, "package p\n\nfunc f() {\n\t"+name+"()\n}\n")

	if len(muts) != 1 || muts[0].Original != name+"(...)" {
		t.Errorf("got %+v, want the full name and no ellipsis", muts)
	}
}
