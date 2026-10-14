package fsys

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// sandboxPrefix names every directory NewSandbox creates. Close refuses to
// delete anything else.
const sandboxPrefix = "gomu-sandbox-"

// Copy limits, so a huge or runaway tree cannot fill the disk.
const (
	maxSandboxFiles = 200_000
	maxSandboxBytes = 2 << 30
)

// ErrTooLarge means the tree to copy exceeds the sandbox limits.
var ErrTooLarge = errors.New("fsys: module is too large to copy")

type limits struct {
	files int
	bytes int64
}

// Sandbox is a private copy of a module in a temp directory. Mutated code and
// its tests run there, so whatever a mutant does, such as deleting files, can
// only damage the copy and never the user's tree.
//
// Not copied: .git, symbolic links, and special files (sockets, devices,
// pipes). Links are skipped rather than recreated because a link pointing out
// of the module would let a mutant write through it into the real tree.
type Sandbox struct {
	dir     string
	skipped int
	closed  bool
}

// NewSandbox copies the module at src into a new private temp directory.
// The caller must call Close. It checks ctx while copying.
func NewSandbox(ctx context.Context, src string) (*Sandbox, error) {
	return newSandbox(ctx, src, limits{files: maxSandboxFiles, bytes: maxSandboxBytes})
}

func newSandbox(ctx context.Context, src string, lim limits) (*Sandbox, error) {
	if src == "" {
		return nil, errors.New("fsys: empty source directory")
	}
	abs, err := filepath.Abs(src)
	if err != nil {
		return nil, fmt.Errorf("fsys: source directory: %w", cause(err))
	}

	// A Root refuses to follow a symlink out of the tree, even if one is
	// swapped in while we copy.
	root, err := os.OpenRoot(abs)
	if err != nil {
		return nil, fmt.Errorf("fsys: source directory: %w", cause(err))
	}
	defer root.Close()

	dir, err := os.MkdirTemp("", sandboxPrefix+"*")
	if err == nil {
		// Resolved, because the go tool compares real paths.
		dir, err = filepath.EvalSymlinks(dir)
	}
	if err != nil {
		return nil, fmt.Errorf("fsys: create sandbox: %w", cause(err))
	}

	s := &Sandbox{dir: dir}
	if err := s.copyTree(ctx, root.FS(), lim); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

// Dir returns the absolute, symlink-free path of the copy.
func (s *Sandbox) Dir() string { return s.dir }

// Skipped returns how many symbolic links and special files were not copied.
func (s *Sandbox) Skipped() int { return s.skipped }

// Close removes the copy. It is safe to call more than once. It returns
// ErrNotOwned, and removes nothing, if the directory is not one NewSandbox
// would have created.
func (s *Sandbox) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true

	if !strings.HasPrefix(filepath.Base(s.dir), sandboxPrefix) || !filepath.IsAbs(s.dir) {
		return ErrNotOwned
	}
	if err := os.RemoveAll(s.dir); err != nil {
		return fmt.Errorf("fsys: remove sandbox: %w", cause(err))
	}
	return nil
}

func (s *Sandbox) copyTree(ctx context.Context, src fs.FS, lim limits) error {
	var files int
	var bytes int64

	return fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("fsys: copy %s: %w", p, cause(err))
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		if p != "." && d.Name() == ".git" {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil // a submodule's .git file
		}

		dst := filepath.Join(s.dir, filepath.FromSlash(p))

		if d.IsDir() {
			if p == "." {
				return nil
			}
			if err := os.Mkdir(dst, 0o700); err != nil {
				return fmt.Errorf("fsys: copy %s: %w", p, cause(err))
			}
			return nil
		}

		if !d.Type().IsRegular() { // symlink or special file
			s.skipped++
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("fsys: copy %s: %w", p, cause(err))
		}
		files++
		bytes += info.Size()
		if files > lim.files || bytes > lim.bytes {
			return ErrTooLarge
		}

		// Keep the owner's permission bits (notably the executable bit); the
		// copy is private, so group and other get nothing.
		perm := info.Mode().Perm()&0o700 | 0o600
		return copyFile(src, p, dst, perm, lim.bytes-(bytes-info.Size()))
	})
}

// copyFile copies src/name to dst, reading at most limit bytes.
func copyFile(src fs.FS, name, dst string, perm fs.FileMode, limit int64) (err error) {
	in, err := src.Open(name)
	if err != nil {
		return fmt.Errorf("fsys: copy %s: %w", name, cause(err))
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return fmt.Errorf("fsys: copy %s: %w", name, cause(err))
	}
	defer func() {
		if cerr := out.Close(); err == nil && cerr != nil {
			err = fmt.Errorf("fsys: copy %s: %w", name, cause(cerr))
		}
	}()

	// The file may have grown since Stat; bound the copy as well.
	n, err := io.Copy(out, io.LimitReader(in, limit+1))
	if err != nil {
		return fmt.Errorf("fsys: copy %s: %w", name, cause(err))
	}
	if n > limit {
		return ErrTooLarge
	}
	return nil
}

// cause drops the absolute paths that *fs.PathError carries and keeps the
// underlying error, so errors.Is(err, fs.ErrNotExist) still works.
func cause(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}
