package goast_test

import (
	"errors"
	"go/ast"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pallat/gomu/internal/adapter/goast"
)

const validSrc = `package p

func add(a, b int) int {
	return a + b
}
`

// writeFile writes content to dir/name, creating parent directories.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()

	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func symlink(t *testing.T, target, link string) {
	t.Helper()

	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}
}

func TestParseValid(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	abs := writeFile(t, root, "pkg/add.go", validSrc)

	tests := []struct {
		name string
		path string
	}{
		{"relative path", filepath.Join("pkg", "add.go")},
		{"relative path with dot segments", filepath.Join(".", "pkg", "..", "pkg", "add.go")},
		{"absolute path", abs},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f, err := goast.Parse(root, tt.path)
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if f.Path != "pkg/add.go" {
				t.Errorf("Path = %q, want %q", f.Path, "pkg/add.go")
			}
			if f.AST == nil || f.AST.Name.Name != "p" {
				t.Errorf("AST package name not parsed: %+v", f.AST)
			}
			if f.Fset == nil {
				t.Error("Fset is nil")
			}
		})
	}
}

func TestParsePosition(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, root, "add.go", validSrc)

	f, err := goast.Parse(root, "add.go")
	if err != nil {
		t.Fatal(err)
	}

	var found bool
	ast.Inspect(f.AST, func(n ast.Node) bool {
		b, ok := n.(*ast.BinaryExpr)
		if !ok {
			return true
		}
		found = true

		got := f.Position(b.OpPos)
		if got.File != "add.go" || got.Line != 4 || got.Column != 11 {
			t.Errorf("Position(OpPos) = %v, want add.go:4:11", got)
		}
		return false
	})
	if !found {
		t.Fatal("no BinaryExpr found")
	}
}

func TestParseKeepsComments(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, root, "c.go", "package p\n\n// Hello is documented.\nfunc Hello() {}\n")

	f, err := goast.Parse(root, "c.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.AST.Comments) == 0 {
		t.Error("comments were dropped; printing would lose them")
	}
}

func TestParseRejectsPaths(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	root := filepath.Join(base, "root")
	evilSibling := filepath.Join(base, "root-evil")
	outside := writeFile(t, base, "outside.go", validSrc)
	writeFile(t, root, "inside.go", validSrc)
	siblingFile := writeFile(t, evilSibling, "a.go", validSrc)
	writeFile(t, root, "notes.txt", "hello")
	writeFile(t, root, "a_dir.go/x.txt", "x")

	linkOut := filepath.Join(root, "link_out.go")
	symlink(t, outside, linkOut)
	linkDir := filepath.Join(root, "link_dir")
	symlink(t, evilSibling, linkDir)
	linkIn := filepath.Join(root, "link_in.go")
	symlink(t, filepath.Join(root, "inside.go"), linkIn)

	tests := []struct {
		name    string
		path    string
		wantErr error
	}{
		{"the parent directory itself", "..", goast.ErrOutsideRoot},
		{"the parent directory by absolute path", base, goast.ErrOutsideRoot},
		{"parent traversal", filepath.Join("..", "outside.go"), goast.ErrOutsideRoot},
		{"deep parent traversal", filepath.Join("a", "..", "..", "outside.go"), goast.ErrOutsideRoot},
		{"absolute path outside root", outside, goast.ErrOutsideRoot},
		{"sibling directory sharing root prefix", siblingFile, goast.ErrOutsideRoot},
		{"symlink file pointing outside", "link_out.go", goast.ErrOutsideRoot},
		{"symlink directory pointing outside", filepath.Join("link_dir", "a.go"), goast.ErrOutsideRoot},
		{"not a go file", "notes.txt", goast.ErrNotGoFile},
		{"directory named like go file", "a_dir.go", goast.ErrNotRegular},
		{"root itself", "", goast.ErrNotGoFile},
		{"missing file", "missing.go", fs.ErrNotExist},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f, err := goast.Parse(root, tt.path)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Parse(%q) error = %v, want %v", tt.path, err, tt.wantErr)
			}
			if f != nil {
				t.Errorf("Parse(%q) returned a File together with an error", tt.path)
			}
		})
	}

	t.Run("symlink staying inside root is allowed", func(t *testing.T) {
		t.Parallel()

		f, err := goast.Parse(root, "link_in.go")
		if err != nil {
			t.Fatalf("Parse() error = %v", err)
		}
		if f.Path != "inside.go" {
			t.Errorf("Path = %q, want the resolved path %q", f.Path, "inside.go")
		}
	})
}

func TestParseRejectsOversizedFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, root, "big.go", "package p\n//"+strings.Repeat("a", goast.MaxFileSize))

	if _, err := goast.Parse(root, "big.go"); !errors.Is(err, goast.ErrTooLarge) {
		t.Errorf("Parse() error = %v, want %v", err, goast.ErrTooLarge)
	}
}

func TestParseAcceptsFileAtLimit(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	// The closing brace is the very last byte: reading one byte short would
	// leave a syntax error.
	const head, tail = "package p\nfunc f() {", "}"
	writeFile(t, root, "edge.go", head+strings.Repeat(" ", goast.MaxFileSize-len(head)-len(tail))+tail)

	if _, err := goast.Parse(root, "edge.go"); err != nil {
		t.Errorf("Parse() error = %v, want nil at exactly MaxFileSize", err)
	}
}

func TestParseUnreadableFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	p := writeFile(t, root, "secret.go", validSrc)
	if err := os.Chmod(p, 0); err != nil {
		t.Skipf("chmod not supported: %v", err)
	}
	if f, err := os.Open(p); err == nil {
		f.Close()
		t.Skip("file is readable despite mode 0 (running as root?)")
	}

	_, err := goast.Parse(root, "secret.go")
	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("Parse() error = %v, want %v", err, fs.ErrPermission)
	}
	if err != nil && strings.Contains(err.Error(), root) {
		t.Errorf("error leaks absolute root path: %v", err)
	}
}

func TestParseRootProblems(t *testing.T) {
	t.Parallel()

	t.Run("missing root", func(t *testing.T) {
		t.Parallel()

		_, err := goast.Parse(filepath.Join(t.TempDir(), "nope"), "a.go")
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Parse() error = %v, want %v", err, fs.ErrNotExist)
		}
	})

	t.Run("empty root", func(t *testing.T) {
		t.Parallel()

		if _, err := goast.Parse("", "a.go"); err == nil {
			t.Error("Parse() with empty root returned nil error")
		}
	})
}

func TestParseErrorsDoNotLeakRootPath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, root, "bad.go", "package p\nfunc (")

	tests := []struct {
		name string
		path string
	}{
		{"syntax error", "bad.go"},
		{"missing file", "missing.go"},
		{"not a go file", "x.txt"},
	}
	writeFile(t, root, "x.txt", "x")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := goast.Parse(root, tt.path)
			if err == nil {
				t.Fatal("Parse() returned nil error")
			}
			if strings.Contains(err.Error(), root) {
				t.Errorf("error leaks absolute root path: %v", err)
			}
		})
	}
}

func TestParseSyntaxError(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, root, "bad.go", "package p\nfunc (")

	f, err := goast.Parse(root, "bad.go")
	if err == nil {
		t.Fatal("Parse() returned nil error for invalid syntax")
	}
	if f != nil {
		t.Error("Parse() returned a File together with an error")
	}
	if !strings.Contains(err.Error(), "bad.go") {
		t.Errorf("error %q does not mention the file", err)
	}
}

func FuzzParsePath(f *testing.F) {
	root := f.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ok.go"), []byte(validSrc), 0o600); err != nil {
		f.Fatal(err)
	}

	for _, s := range []string{"ok.go", "", ".", "..", "../x.go", "a/../../x.go", "/etc/passwd", "ok.go\x00", "ok.go/"} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, path string) {
		file, err := goast.Parse(root, path)
		if err != nil {
			if file != nil {
				t.Fatalf("File and error both returned for %q", path)
			}
			return
		}
		if filepath.IsAbs(file.Path) || strings.HasPrefix(file.Path, "..") {
			t.Fatalf("Path %q escapes the root for input %q", file.Path, path)
		}
	})
}
