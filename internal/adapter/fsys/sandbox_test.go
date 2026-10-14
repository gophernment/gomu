package fsys

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

// tree writes files (slash-separated name -> content) under a new directory.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()

	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// listing returns every path under dir, slash-separated and sorted.
func listing(t *testing.T, dir string) []string {
	t.Helper()

	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if rel != "." {
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return out
}

func newTestSandbox(t *testing.T, src string) *Sandbox {
	t.Helper()

	s, err := NewSandbox(context.Background(), src)
	if err != nil {
		t.Fatalf("NewSandbox() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestSandboxCopiesTheModule(t *testing.T) {
	t.Parallel()

	src := tree(t, map[string]string{
		"go.mod":              "module example.com/p\n",
		"a.go":                "package p\n",
		"pkg/b.go":            "package pkg\n",
		"pkg/testdata/in.txt": "data",
		"vendor/v/v.go":       "package v\n",
		"empty/.keep":         "",
	})
	if err := os.Mkdir(filepath.Join(src, "emptydir"), 0o755); err != nil {
		t.Fatal(err)
	}

	s := newTestSandbox(t, src)

	if got, want := listing(t, s.Dir()), listing(t, src); !slices.Equal(got, want) {
		t.Errorf("sandbox contents =\n%q\nwant\n%q", got, want)
	}
	for name, want := range map[string]string{"a.go": "package p\n", "pkg/testdata/in.txt": "data", "vendor/v/v.go": "package v\n"} {
		got, err := os.ReadFile(filepath.Join(s.Dir(), filepath.FromSlash(name)))
		if err != nil || string(got) != want {
			t.Errorf("%s = %q, %v; want %q", name, got, err, want)
		}
	}
	if s.Skipped() != 0 {
		t.Errorf("Skipped() = %d, want 0", s.Skipped())
	}
}

func TestSandboxLocation(t *testing.T) {
	t.Parallel()

	src := tree(t, map[string]string{"a.go": "package p\n"})
	a, b := newTestSandbox(t, src), newTestSandbox(t, src)

	if !filepath.IsAbs(a.Dir()) {
		t.Errorf("Dir() = %q is not absolute", a.Dir())
	}
	if resolved, err := filepath.EvalSymlinks(a.Dir()); err != nil || resolved != a.Dir() {
		t.Errorf("Dir() = %q is not symlink-free (%q, %v); the go tool compares real paths", a.Dir(), resolved, err)
	}
	if !strings.HasPrefix(filepath.Base(a.Dir()), sandboxPrefix) {
		t.Errorf("Dir() = %q does not carry the sandbox prefix", a.Dir())
	}
	if a.Dir() == b.Dir() {
		t.Error("two sandboxes share a directory")
	}
	if rel, err := filepath.Rel(src, a.Dir()); err == nil && !strings.HasPrefix(rel, "..") {
		t.Errorf("sandbox %q lies inside the source tree %q", a.Dir(), src)
	}
}

func TestSandboxIsPrivateAndKeepsExecutableBit(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("unix permissions")
	}

	src := tree(t, map[string]string{"run.sh": "#!/bin/sh\n", "pkg/a.go": "package p\n"})
	if err := os.Chmod(filepath.Join(src, "run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := newTestSandbox(t, src)

	for name, want := range map[string]os.FileMode{
		".":        0o700,
		"pkg":      0o700,
		"run.sh":   0o700,
		"pkg/a.go": 0o600,
	} {
		info, err := os.Stat(filepath.Join(s.Dir(), filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("mode of %s = %o, want %o", name, got, want)
		}
	}
}

func TestSandboxSkipsGit(t *testing.T) {
	t.Parallel()

	src := tree(t, map[string]string{
		"a.go":                  "package p\n",
		".git/HEAD":             "ref: refs/heads/main\n",
		".git/objects/ab/cdef":  "blob",
		"sub/module/.git":       "gitdir: ../../.git/modules/m\n", // a submodule's .git is a file
		"sub/module/m.go":       "package m\n",
		"sub/.github/ci.yml":    "name: ci\n", // similar name, must be kept
		"sub/dir.git/config.go": "package g\n",
	})
	s := newTestSandbox(t, src)

	got := listing(t, s.Dir())
	for _, p := range got {
		if p == ".git" || strings.HasPrefix(p, ".git/") || p == "sub/module/.git" {
			t.Errorf("sandbox contains %s", p)
		}
	}
	for _, want := range []string{"a.go", "sub/module/m.go", "sub/.github/ci.yml", "sub/dir.git/config.go"} {
		if !slices.Contains(got, want) {
			t.Errorf("sandbox lacks %s; got %q", want, got)
		}
	}
}

func TestSandboxNeverCopiesSymlinks(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	secret := filepath.Join(base, "secret.txt")
	if err := os.WriteFile(secret, []byte("do not copy"), 0o600); err != nil {
		t.Fatal(err)
	}
	outsideDir := filepath.Join(base, "outside")
	if err := os.Mkdir(outsideDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outsideDir, "x.go"), []byte("package x\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	src := filepath.Join(base, "module")
	if err := os.MkdirAll(filepath.Join(src, "pkg"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.go"), []byte("package p\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	links := map[string]string{
		"file_link":    secret,                      // file outside
		"dir_link":     outsideDir,                  // directory outside
		"inside_link":  filepath.Join(src, "a.go"),  // even a link inside is not recreated
		"pkg/rel_link": filepath.Join("..", "a.go"), // relative link
		"dangling":     filepath.Join(base, "nowhere"),
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(src, filepath.FromSlash(name))); err != nil {
			t.Skipf("symlinks not supported: %v", err)
		}
	}

	s := newTestSandbox(t, src)

	if got, want := listing(t, s.Dir()), []string{"a.go", "pkg"}; !slices.Equal(got, want) {
		t.Errorf("sandbox contents = %q, want %q", got, want)
	}
	if s.Skipped() != len(links) {
		t.Errorf("Skipped() = %d, want %d", s.Skipped(), len(links))
	}
}

func TestSandboxIsIsolatedFromTheSource(t *testing.T) {
	t.Parallel()

	src := tree(t, map[string]string{"a.go": "package p\n", "pkg/b.go": "package b\n"})
	s := newTestSandbox(t, src)

	// Never write below an empty or relative Dir(): that is the package directory
	// of whoever runs this test, which may be a copy being mutation tested.
	if !filepath.IsAbs(s.Dir()) {
		t.Fatalf("Dir() = %q is not absolute", s.Dir())
	}

	// A mutant doing its worst inside the sandbox.
	for _, name := range []string{"a.go", "pkg"} {
		if err := os.RemoveAll(filepath.Join(s.Dir(), name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(s.Dir(), "new.go"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got, want := listing(t, src), []string{"a.go", "pkg", "pkg/b.go"}; !slices.Equal(got, want) {
		t.Errorf("source tree changed: %q, want %q", got, want)
	}
	if b, err := os.ReadFile(filepath.Join(src, "a.go")); err != nil || string(b) != "package p\n" {
		t.Errorf("source file changed: %q, %v", b, err)
	}
}

func TestSandboxClose(t *testing.T) {
	t.Parallel()

	s, err := NewSandbox(context.Background(), tree(t, map[string]string{"a.go": "package p\n"}))
	if err != nil {
		t.Fatal(err)
	}
	dir := s.Dir()

	if err := s.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("sandbox still exists after Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("second Close() error = %v, want nil", err)
	}
}

func TestSandboxCloseRefusesDirectoriesItDidNotCreate(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	victim := filepath.Join(base, "precious")
	if err := os.Mkdir(victim, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(victim, "keep.go"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, dir := range []string{victim, base, "", ".", "relative/gomu-sandbox-x", filepath.Join(base, "gomu-sandbox")} {
		s := &Sandbox{dir: dir}
		if err := s.Close(); !errors.Is(err, ErrNotOwned) {
			t.Errorf("Close() with dir %q error = %v, want %v", dir, err, ErrNotOwned)
		}
	}
	if _, err := os.Stat(filepath.Join(victim, "keep.go")); err != nil {
		t.Errorf("Close removed a directory it did not create: %v", err)
	}
}

func TestSandboxRejectsBadSources(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "nope")

	tests := []struct {
		name string
		src  string
	}{
		{"empty", ""},
		{"missing", missing},
		{"a file", file},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s, err := NewSandbox(context.Background(), tt.src)
			if err == nil {
				_ = s.Close()
				t.Fatal("NewSandbox() returned nil error")
			}
			if s != nil {
				t.Error("NewSandbox() returned a Sandbox together with an error")
			}
			if tt.src != "" && strings.Contains(err.Error(), filepath.Dir(tt.src)) {
				t.Errorf("error leaks an absolute path: %v", err)
			}
		})
	}

	t.Run("missing source reports not-exist", func(t *testing.T) {
		t.Parallel()

		if _, err := NewSandbox(context.Background(), missing); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("error = %v, want %v", err, fs.ErrNotExist)
		}
	})
}

func TestSandboxLimits(t *testing.T) {
	t.Parallel()

	src := tree(t, map[string]string{"a.go": "12345", "b.go": "12345", "c.go": "12345"})

	tests := []struct {
		name    string
		lim     limits
		wantErr bool
	}{
		{"within both limits", limits{files: 3, bytes: 15}, false},
		{"too many files", limits{files: 2, bytes: 1 << 20}, true},
		{"too many bytes", limits{files: 10, bytes: 14}, true},
		{"nothing allowed", limits{}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s, err := newSandbox(context.Background(), src, tt.lim)
			if s != nil {
				t.Cleanup(func() { _ = s.Close() })
			}
			if tt.wantErr != errors.Is(err, ErrTooLarge) {
				t.Fatalf("error = %v, want ErrTooLarge: %v", err, tt.wantErr)
			}
			if tt.wantErr && s != nil {
				t.Error("a Sandbox was returned together with an error")
			}
			if !tt.wantErr && err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Not parallel: t.Setenv changes the process environment. The temp directory
// is private to the test, so it shows whether a failed copy cleans up.
func TestSandboxFailureLeavesNothingBehind(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	src := tree(t, map[string]string{"a.go": "package p\n", "pkg/b.go": "package b\n"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cases := map[string]func() error{
		"canceled context": func() error { _, err := NewSandbox(ctx, src); return err },
		"over the limit": func() error {
			_, err := newSandbox(context.Background(), src, limits{files: 1, bytes: 1 << 20})
			return err
		},
	}
	for name, try := range cases {
		if err := try(); err == nil {
			t.Fatalf("%s: expected an error", name)
		}
		entries, err := os.ReadDir(tmp)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Errorf("%s left %d entries in the temp directory", name, len(entries))
		}
	}
}

func TestSandboxUnreadableFile(t *testing.T) {
	t.Parallel()

	src := tree(t, map[string]string{"a.go": "package p\n", "secret.go": "package p\n"})
	secret := filepath.Join(src, "secret.go")
	if err := os.Chmod(secret, 0); err != nil {
		t.Skipf("chmod not supported: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(secret, 0o600) })
	if f, err := os.Open(secret); err == nil {
		f.Close()
		t.Skip("file is readable despite mode 0 (running as root?)")
	}

	s, err := NewSandbox(context.Background(), src)
	if err == nil {
		_ = s.Close()
		t.Fatal("NewSandbox() returned nil error for an unreadable file")
	}
	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("error = %v, want %v", err, fs.ErrPermission)
	}
	if strings.Contains(err.Error(), src) {
		t.Errorf("error leaks the source path: %v", err)
	}
}

// Not parallel: t.Setenv and t.Chdir change process-wide state.
func TestSandboxDirIsAbsoluteWithRelativeTmpdir(t *testing.T) {
	src := tree(t, map[string]string{"a.go": "package p\n"})

	t.Chdir(t.TempDir())
	if err := os.Mkdir("tmp", 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", "tmp")

	s, err := NewSandbox(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if !filepath.IsAbs(s.Dir()) {
		t.Errorf("Dir() = %q is not absolute", s.Dir())
	}
	if _, err := os.Stat(filepath.Join(s.Dir(), "a.go")); err != nil {
		t.Errorf("copy missing: %v", err)
	}
}

// Not parallel: t.Setenv changes process-wide state. TMPDIR is reached through
// a symbolic link, like /var -> /private/var on macOS, and the go tool compares
// real paths, so Dir() must have the link resolved.
func TestSandboxDirResolvesSymlinkedTmpdir(t *testing.T) {
	src := tree(t, map[string]string{"a.go": "package p\n"})

	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(base, "real")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}
	t.Setenv("TMPDIR", link)

	s, err := NewSandbox(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if filepath.Dir(s.Dir()) != real {
		t.Errorf("Dir() = %q, want a directory directly inside the resolved %q", s.Dir(), real)
	}
}

// liarFS reports a smaller size for one file than it really has, like a file
// that grows between Stat and read.
type liarFS struct {
	fs.FS
	name    string
	claimed int64
}

func (l liarFS) ReadDir(dir string) ([]fs.DirEntry, error) {
	entries, err := fs.ReadDir(l.FS, dir)
	for i, e := range entries {
		if e.Name() == l.name {
			entries[i] = liarEntry{DirEntry: e, claimed: l.claimed}
		}
	}
	return entries, err
}

type liarEntry struct {
	fs.DirEntry
	claimed int64
}

func (e liarEntry) Info() (fs.FileInfo, error) {
	info, err := e.DirEntry.Info()
	return liarInfo{FileInfo: info, claimed: e.claimed}, err
}

type liarInfo struct {
	fs.FileInfo
	claimed int64
}

func (i liarInfo) Size() int64 { return i.claimed }

func TestSandboxBoundsFilesThatGrowWhileCopying(t *testing.T) {
	t.Parallel()

	// a.go is honest (1 byte). b.go claims 1 byte but holds 6, so the copy
	// would reach 7 bytes against a limit of 6.
	files := fstest.MapFS{
		"a.go": {Data: []byte("1")},
		"b.go": {Data: []byte("123456")},
	}

	tests := []struct {
		name    string
		limit   int64
		wantErr bool
	}{
		{"the file outgrew the remaining budget", 6, true},
		{"enough room for the real size", 7, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := &Sandbox{dir: filepath.Join(t.TempDir(), sandboxPrefix+"x")}
			if err := os.Mkdir(s.dir, 0o700); err != nil {
				t.Fatal(err)
			}

			err := s.copyTree(context.Background(), liarFS{FS: files, name: "b.go", claimed: 1}, limits{files: 10, bytes: tt.limit})
			if tt.wantErr != errors.Is(err, ErrTooLarge) {
				t.Fatalf("error = %v, want ErrTooLarge: %v", err, tt.wantErr)
			}
			if !tt.wantErr && err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSandboxTempDir(t *testing.T) {
	t.Parallel()

	s := newTestSandbox(t, tree(t, map[string]string{"a.go": "package p\n"}))

	tmp, err := s.TempDir()
	if err != nil {
		t.Fatalf("TempDir() error = %v", err)
	}
	if !filepath.IsAbs(tmp) || filepath.Dir(tmp) != s.Dir() {
		t.Fatalf("TempDir() = %q, want an absolute directory directly inside %q", tmp, s.Dir())
	}
	if !strings.HasPrefix(filepath.Base(tmp), ".") {
		t.Errorf("TempDir() = %q: the go tool would see it as a package directory", tmp)
	}
	if info, err := os.Stat(tmp); err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Errorf("temp dir stat = %v, %v, want a 0700 directory", info, err)
	}
	if again, err := s.TempDir(); err != nil || again != tmp {
		t.Errorf("second TempDir() = %q, %v, want %q", again, err, tmp)
	}

	// What the code under test leaves there goes away with the sandbox.
	if err := os.WriteFile(filepath.Join(tmp, "junk"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := s.Dir()
	if err := s.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("sandbox still exists after Close: %v", err)
	}
	if _, err := s.TempDir(); !errors.Is(err, ErrClosed) {
		t.Errorf("TempDir() after Close error = %v, want %v", err, ErrClosed)
	}
}
