package mutator_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strings"
	"testing"

	"github.com/pallat/gomu/internal/mutator"
)

func returnEach(t *testing.T, src string) ([]string, []mutator.Mutation) {
	t.Helper()
	return mutateFile(t, mutator.Return{}, src)
}

func TestReturnReplacesValuesByDeclaredType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		sig  string // results of f, e.g. "(int, error)"
		ret  string // the return statement
		want []string
	}{
		{"bool expression", "bool", "return ok", []string{"return !(ok)"}},
		{"bool true", "bool", "return true", []string{"return false"}},
		{"bool false", "bool", "return false", []string{"return true"}},
		{"int", "int", "return n + 1", []string{"return 0"}},
		{"int already zero", "int", "return 0", nil},
		{"every number type", "(int8, uint, uintptr, byte, rune, float32, float64, complex128)", "return a, b, c, d, e, f, g, h",
			[]string{"return 0, b, c, d, e, f, g, h", "return a, 0, c, d, e, f, g, h", "return a, b, 0, d, e, f, g, h", "return a, b, c, 0, e, f, g, h",
				"return a, b, c, d, 0, f, g, h", "return a, b, c, d, e, 0, g, h", "return a, b, c, d, e, f, 0, h", "return a, b, c, d, e, f, g, 0"}},
		{"string", "string", "return s + \"x\"", []string{`return ""`}},
		{"string already empty", "string", `return ""`, nil},
		{"raw empty string", "string", "return ``", nil},
		{"error", "error", "return err", []string{"return nil"}},
		{"error already nil", "error", "return nil", nil},
		{"value and error", "(int, error)", "return n, err", []string{"return 0, err", "return n, nil"}},
		{"value and nil error", "(int, error)", "return n, nil", []string{"return 0, nil"}},
		{"grouped names", "(a, b int)", "return x, y", []string{"return 0, y", "return x, 0"}},
		{"pointer", "*T", "return p", []string{"return nil"}},
		{"slice", "[]string", "return xs", []string{"return nil"}},
		{"map", "map[string]int", "return m", []string{"return nil"}},
		{"channel", "chan int", "return c", []string{"return nil"}},
		{"func", "func()", "return fn", []string{"return nil"}},
		{"interface", "interface{ M() }", "return v", []string{"return nil"}},
		{"any", "any", "return v", []string{"return nil"}},
		{"array has no nil", "[2]int", "return a", nil},
		{"struct", "struct{}", "return s", nil},
		{"named type", "T", "return t", nil},
		{"generic type", "G[int]", "return g", nil},
		{"passing on several values", "(int, error)", "return g()", nil},
		{"naked return", "(n int, err error)", "return", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			src := "package p\n\nfunc f() " + tt.sig + " {\n" + tt.ret + "\n}\n"
			got, _ := returnEach(t, src)

			want := make([]string, len(tt.want))
			for i, w := range tt.want {
				want[i] = "package p func f() " + strings.Join(strings.Fields(tt.sig), " ") + " { " + w + " }"
			}
			if !slices.Equal(got, want) {
				t.Errorf("got\n%q\nwant\n%q", got, want)
			}
		})
	}
}

func TestReturnReadsTheSignatureOfTheFunctionItIsIn(t *testing.T) {
	t.Parallel()

	const src = `package p

func outer() int {
	f := func() bool {
		return ok
	}
	g := func() {
		return
	}
	_, _ = f, g
	return 1
}

var lit = func() string { return s }

func (T) method() error { return err }

func none() { return }

func bodyless() int
`
	got, muts := returnEach(t, src)

	// The nested literal's return is bool, not int: one mutation per return, no double counting.
	if len(got) != 4 {
		t.Fatalf("got %d mutations, want 4:\n%q", len(got), got)
	}
	var texts []string
	for _, mu := range muts {
		texts = append(texts, mu.Original+" -> "+mu.Replacement)
	}
	want := []string{"bool -> negated", "int -> 0", `string -> ""`, "error -> nil"}
	slices.Sort(texts)
	slices.Sort(want)
	if !slices.Equal(texts, want) {
		t.Errorf("got %q, want %q", texts, want)
	}
}

func TestReturnReports(t *testing.T) {
	t.Parallel()

	_, muts := returnEach(t, "package p\n\nfunc f() (int, []string, bool, bool, error) {\n\treturn secretValue(1), xs, true, ok, err\n}\n")

	var got [][2]string
	for _, mu := range muts {
		got = append(got, [2]string{mu.Original, mu.Replacement})
	}
	want := [][2]string{{"int", "0"}, {"[]string", "nil"}, {"true", "false"}, {"bool", "negated"}, {"error", "nil"}}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestReturnPointsAtTheReturnedValue(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "p.go", "package p\n\nfunc f() (int, error) {\n\treturn 7, err\n}\n", 0)
	if err != nil {
		t.Fatal(err)
	}
	var cols []int
	ast.Inspect(file, func(n ast.Node) bool {
		for _, mu := range (mutator.Return{}).Mutations(n) {
			if p := fset.Position(mu.Pos); p.Line == 4 {
				cols = append(cols, p.Column)
			}
		}
		return true
	})
	if want := []int{9, 12}; !slices.Equal(cols, want) {
		t.Errorf("columns = %v, want %v", cols, want)
	}
}

func TestReturnTruncatesLongTypes(t *testing.T) {
	t.Parallel()

	typ := "map[string]" + strings.Repeat("a", 80)
	_, muts := returnEach(t, "package p\n\nfunc f() "+typ+" {\n\treturn m\n}\n")

	if len(muts) != 1 || !strings.HasSuffix(muts[0].Original, "…") || len([]rune(muts[0].Original)) != 61 {
		t.Errorf("got %+v, want the type cut at 60 runes plus an ellipsis", muts)
	}
}

func TestReturnIgnoresOtherNodesAndNil(t *testing.T) {
	t.Parallel()

	var (
		decl *ast.FuncDecl
		lit  *ast.FuncLit
	)
	inputs := []ast.Node{
		nil, decl, lit, ast.NewIdent("x"), &ast.ReturnStmt{},
		&ast.FuncDecl{Name: ast.NewIdent("f"), Type: &ast.FuncType{}}, // no body
		&ast.FuncLit{Body: &ast.BlockStmt{}},                          // no signature
	}
	for _, n := range inputs {
		if got := (mutator.Return{}).Mutations(n); got != nil {
			t.Errorf("Mutations(%T) = %v, want nil", n, got)
		}
	}
}

func TestReturnName(t *testing.T) {
	t.Parallel()

	if got := (mutator.Return{}).Name(); got != "return" {
		t.Errorf("Name() = %q", got)
	}
}
