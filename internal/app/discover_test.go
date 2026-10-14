package app_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gophernment/gomu/internal/app"
)

func tree() fstest.MapFS {
	files := []string{
		"a.go", "a_test.go", "README.md", ".dot.go", "_under.go",
		"pkg/b.go", "pkg/b_test.go", "pkg/sub/c.go", "pkg/sub/deep/d.go",
		"vendor/v.go", "vendor/inner/vi.go", "testdata/t.go",
		".hidden/h.go", "_skip/s.go", "pkg/testdata/pt.go",
		"b/x.go",
	}
	fsys := fstest.MapFS{}
	for _, f := range files {
		fsys[f] = &fstest.MapFile{Data: []byte("package p\n")}
	}
	return fsys
}

func discover(t *testing.T, fsys fs.FS, patterns ...string) ([]string, error) {
	t.Helper()

	return app.Discover{FS: fsys, Patterns: patterns}.Run(context.Background())
}

func TestDiscoverSelectsSourceFiles(t *testing.T) {
	t.Parallel()

	all := []string{"a.go", "b/x.go", "pkg/b.go", "pkg/sub/c.go", "pkg/sub/deep/d.go"}

	tests := []struct {
		name     string
		patterns []string
		want     []string
	}{
		{"recursive from root", []string{"./..."}, all},
		{"bare ellipsis", []string{"..."}, all},
		{"recursive subtree", []string{"pkg/..."}, []string{"pkg/b.go", "pkg/sub/c.go", "pkg/sub/deep/d.go"}},
		{"recursive with ./ prefix", []string{"./pkg/sub/..."}, []string{"pkg/sub/c.go", "pkg/sub/deep/d.go"}},
		{"directory is not recursive", []string{"pkg"}, []string{"pkg/b.go"}},
		{"dot is the root directory only", []string{"."}, []string{"a.go"}},
		{"trailing slash", []string{"pkg/"}, []string{"pkg/b.go"}},
		{"single file", []string{"pkg/b.go"}, []string{"pkg/b.go"}},
		{"single file with ./", []string{"./pkg/b.go"}, []string{"pkg/b.go"}},
		{"dot segments are cleaned", []string{"pkg/../b"}, []string{"b/x.go"}},
		{"explicit vendor directory", []string{"vendor"}, []string{"vendor/v.go"}},
		{"explicit vendor recursive skips nested vendor-like dirs only", []string{"vendor/..."}, []string{"vendor/inner/vi.go", "vendor/v.go"}},
		{"explicit testdata directory", []string{"testdata"}, []string{"testdata/t.go"}},
		{"union is sorted and deduplicated", []string{"pkg/b.go", "pkg", "./...", "a.go"}, all},
		{"no patterns", nil, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := discover(t, tree(), tt.patterns...)
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("Run() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDiscoverEmptyTree(t *testing.T) {
	t.Parallel()

	got, err := discover(t, fstest.MapFS{"docs/readme.md": {Data: []byte("x")}}, "./...")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("Run() = %q, want nothing", got)
	}
}

func TestDiscoverRejectsBadPatterns(t *testing.T) {
	t.Parallel()

	for _, p := range []string{
		"", "/", "/abs/path", "..", "../x", "a/../../x", "../...",
		`a\b`, "a\x00b", "pkg/b.go/...",
	} {
		_, err := discover(t, tree(), p)
		if !errors.Is(err, app.ErrInvalidPattern) {
			t.Errorf("pattern %q: error = %v, want %v", p, err, app.ErrInvalidPattern)
		}
	}
}

func TestDiscoverRejectsNonSourceFiles(t *testing.T) {
	t.Parallel()

	for _, p := range []string{"README.md", "a_test.go", ".dot.go", "_under.go", "pkg/b_test.go"} {
		_, err := discover(t, tree(), p)
		if !errors.Is(err, app.ErrNotSource) {
			t.Errorf("pattern %q: error = %v, want %v", p, err, app.ErrNotSource)
		}
	}
}

func TestDiscoverMissingPaths(t *testing.T) {
	t.Parallel()

	for _, p := range []string{"nope", "nope/...", "pkg/nope.go", "nope/deeper/x.go"} {
		_, err := discover(t, tree(), p)
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("pattern %q: error = %v, want %v", p, err, fs.ErrNotExist)
		}
	}
}

func TestDiscoverFailsFastOnAnyBadPattern(t *testing.T) {
	t.Parallel()

	got, err := discover(t, tree(), "pkg", "../x")
	if !errors.Is(err, app.ErrInvalidPattern) {
		t.Fatalf("error = %v, want %v", err, app.ErrInvalidPattern)
	}
	if got != nil {
		t.Errorf("Run() = %q together with an error", got)
	}
}

func TestDiscoverRequiresFS(t *testing.T) {
	t.Parallel()

	if _, err := (app.Discover{Patterns: []string{"./..."}}).Run(context.Background()); !errors.Is(err, app.ErrInvalid) {
		t.Errorf("Run() error = %v, want %v", err, app.ErrInvalid)
	}
}

func TestDiscoverCanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := app.Discover{FS: tree(), Patterns: []string{"./..."}}.Run(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Run() error = %v, want %v", err, context.Canceled)
	}
}

// outsideLayout builds a root with symlinks that point outside of it.
func outsideLayout(t *testing.T) fs.FS {
	t.Helper()

	base := t.TempDir()
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	for _, d := range []string{filepath.Join(root, "pkg"), outside} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p string) {
		if err := os.WriteFile(p, []byte("package p\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "ok.go"))
	write(filepath.Join(root, "pkg", "in.go"))
	write(filepath.Join(outside, "evil.go"))

	link := func(target, name string) {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Skipf("symlinks not supported: %v", err)
		}
	}
	link(filepath.Join(outside, "evil.go"), "link.go")
	link(outside, "linkdir")
	link(filepath.Join(root, "pkg"), "inner_link") // stays inside, still refused

	return os.DirFS(root)
}

func TestDiscoverNeverFollowsSymlinks(t *testing.T) {
	t.Parallel()

	fsys := outsideLayout(t)

	t.Run("walk skips links", func(t *testing.T) {
		t.Parallel()

		got, err := discover(t, fsys, "./...")
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"ok.go", "pkg/in.go"}; !slices.Equal(got, want) {
			t.Errorf("Run() = %q, want %q", got, want)
		}
	})

	for _, p := range []string{"link.go", "linkdir", "linkdir/...", "linkdir/evil.go", "inner_link", "inner_link/in.go"} {
		t.Run("explicit "+p, func(t *testing.T) {
			t.Parallel()

			got, err := discover(t, fsys, p)
			if !errors.Is(err, app.ErrSymlink) {
				t.Errorf("error = %v, want %v", err, app.ErrSymlink)
			}
			if got != nil {
				t.Errorf("Run() = %q together with an error", got)
			}
		})
	}
}

func FuzzDiscoverPattern(f *testing.F) {
	for _, s := range []string{"./...", "pkg", "pkg/b.go", "", "..", "a/../b", "pkg/...", "...", "x\x00y", "//", "pkg//sub/"} {
		f.Add(s)
	}
	fsys := tree()

	f.Fuzz(func(t *testing.T, pattern string) {
		got, err := app.Discover{FS: fsys, Patterns: []string{pattern}}.Run(context.Background())
		if err != nil {
			if got != nil {
				t.Fatal("files and error both returned")
			}
			return
		}
		for _, p := range got {
			if !fs.ValidPath(p) || path.Ext(p) != ".go" || strings.HasSuffix(p, "_test.go") {
				t.Fatalf("pattern %q produced unsafe or ineligible path %q", pattern, p)
			}
		}
		if !slices.IsSorted(got) {
			t.Fatalf("result not sorted: %q", got)
		}
	})
}

// failingListingFS cannot list one directory.
type failingListingFS struct {
	fstest.MapFS
	dir string
}

func (f failingListingFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if name == f.dir {
		return nil, errFailingListing
	}
	return f.MapFS.ReadDir(name)
}

var errFailingListing = errors.New("cannot list")

func TestDiscoverReportsListingErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		dir     string
		pattern string
	}{
		{"the directory itself", "pkg", "pkg"},
		{"a directory below the pattern", "pkg/sub", "pkg/..."},
		{"the root", ".", "./..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := discover(t, failingListingFS{tree(), tt.dir}, tt.pattern)
			if !errors.Is(err, errFailingListing) {
				t.Errorf("error = %v, want %v", err, errFailingListing)
			}
		})
	}
}
