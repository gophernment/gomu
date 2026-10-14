package fsys

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

var errBoom = errors.New("boom")

// failingReadDirFS fails every directory listing.
type failingReadDirFS struct{ fstest.MapFS }

func (failingReadDirFS) ReadDir(string) ([]fs.DirEntry, error) { return nil, errBoom }

// badInfoFS hands out entries whose Info fails.
type badInfoFS struct{ fstest.MapFS }

type badInfoEntry struct{ fs.DirEntry }

func (badInfoEntry) Info() (fs.FileInfo, error) { return nil, errBoom }

func (f badInfoFS) ReadDir(name string) ([]fs.DirEntry, error) {
	entries, err := f.MapFS.ReadDir(name)
	for i, e := range entries {
		entries[i] = badInfoEntry{e}
	}
	return entries, err
}

// unreadableFS opens files that fail to read.
type unreadableFS struct{ fstest.MapFS }

type unreadableFile struct{ fs.File }

func (unreadableFile) Read([]byte) (int, error) { return 0, errBoom }

func (f unreadableFS) Open(name string) (fs.File, error) {
	file, err := f.MapFS.Open(name)
	if err != nil || name == "." {
		return file, err
	}
	return unreadableFile{file}, nil
}

func oneFile() fstest.MapFS {
	return fstest.MapFS{"a.go": {Data: []byte("package p\n")}}
}

func TestCopyTreeFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		src   fs.FS
		setup func(t *testing.T, dir string) // runs before the copy
		want  error
	}{
		{"listing fails", failingReadDirFS{oneFile()}, nil, errBoom},
		{"stat fails", badInfoFS{oneFile()}, nil, errBoom},
		{"read fails", unreadableFS{oneFile()}, nil, errBoom},
		{
			"directory already exists",
			fstest.MapFS{"pkg/a.go": {Data: []byte("package p\n")}},
			func(t *testing.T, dir string) {
				if err := os.Mkdir(filepath.Join(dir, "pkg"), 0o700); err != nil {
					t.Fatal(err)
				}
			},
			fs.ErrExist,
		},
		{
			"file already exists",
			oneFile(),
			func(t *testing.T, dir string) {
				if err := os.WriteFile(filepath.Join(dir, "a.go"), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			},
			fs.ErrExist,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			if tt.setup != nil {
				tt.setup(t, dir)
			}
			s := &Sandbox{dir: dir}

			err := s.copyTree(context.Background(), tt.src, limits{files: 100, bytes: 1 << 20})
			if !errors.Is(err, tt.want) {
				t.Fatalf("copyTree() error = %v, want %v", err, tt.want)
			}
			if strings.Contains(err.Error(), dir) {
				t.Errorf("error leaks the sandbox path: %v", err)
			}
		})
	}
}

func TestCauseKeepsErrorsThatCarryNoPath(t *testing.T) {
	t.Parallel()

	if got := cause(errBoom); got != errBoom {
		t.Errorf("cause(plain error) = %v, want it unchanged", got)
	}
	if got := cause(&fs.PathError{Op: "open", Path: "/secret", Err: errBoom}); got != errBoom {
		t.Errorf("cause(PathError) = %v, want the inner error", got)
	}
}

// A directory below a regular file can never be removed or created.
func belowAFile(t *testing.T, name string) string {
	t.Helper()

	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(file, name)
}

func TestSandboxTempDirFailure(t *testing.T) {
	t.Parallel()

	s := &Sandbox{dir: belowAFile(t, sandboxPrefix+"x")}
	if _, err := s.TempDir(); err == nil || strings.Contains(err.Error(), s.dir) {
		t.Errorf("TempDir() error = %v, want an error without the path", err)
	}
}

func TestCloseFailureIsReported(t *testing.T) {
	t.Parallel()

	s := &Sandbox{dir: belowAFile(t, sandboxPrefix+"x")}
	if err := s.Close(); err == nil || errors.Is(err, ErrNotOwned) || strings.Contains(err.Error(), s.dir) {
		t.Errorf("Sandbox.Close() error = %v, want a removal error without the path", err)
	}

	o := &Overlay{dir: belowAFile(t, overlayPrefix+"x")}
	if err := o.Close(); err == nil || errors.Is(err, ErrNotOwned) || strings.Contains(err.Error(), o.dir) {
		t.Errorf("Overlay.Close() error = %v, want a removal error without the path", err)
	}
}

// Not parallel: t.Setenv changes process-wide state.
func TestNewSandboxWithoutTempDirectory(t *testing.T) {
	// A mutant may carry on after the failure with an empty directory, which
	// means the current one. Make that harmless.
	t.Chdir(t.TempDir())
	src := tree(t, map[string]string{"a.go": "package p\n"})
	missing := filepath.Join(t.TempDir(), "missing")
	t.Setenv("TMPDIR", missing)

	if _, err := NewSandbox(context.Background(), src); err == nil || strings.Contains(err.Error(), missing) {
		t.Errorf("NewSandbox() error = %v, want an error without the path", err)
	}
}

func TestOverlayWriteFailsWhenJSONCannotBeWritten(t *testing.T) {
	t.Parallel()

	o, err := NewOverlay()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = o.Close() })

	original := filepath.Join(t.TempDir(), "a.go")
	jsonPath, err := o.Write(original, []byte("package p\n"))
	if err != nil {
		t.Fatal(err)
	}

	// Something that is not a file now sits where overlay.json goes.
	if err := os.Remove(jsonPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(jsonPath, 0o700); err != nil {
		t.Fatal(err)
	}

	if _, err := o.Write(original, []byte("package p\n")); err == nil || strings.Contains(err.Error(), o.dir) {
		t.Errorf("Write() error = %v, want an error without the path", err)
	}
}
