package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
)

// Errors returned by Discover.
var (
	// ErrInvalidPattern means a pattern is empty, absolute, escapes the root,
	// or is otherwise malformed.
	ErrInvalidPattern = errors.New("app: invalid pattern")
	// ErrNotSource means a file named on its own is not a mutable .go file.
	ErrNotSource = errors.New("app: not a mutable Go source file")
	// ErrSymlink means a path named on its own goes through a symbolic link.
	ErrSymlink = errors.New("app: path goes through a symbolic link")
)

// Discover is the context that finds the Go files to mutate. Its one role is
// the file system, a plain fs.FS rooted at the module, so tests need no disk.
//
// Patterns are slash-separated paths relative to the root:
//
//	pkg/b.go     one file
//	pkg          the files directly in a directory
//	pkg/...      a directory and everything below it
//	./...        the whole tree ("..." alone means the same)
//
// Test files are never selected. While walking a tree it also skips
// directories named vendor or testdata, and anything starting with "." or "_",
// like the go tool does. A file or directory named explicitly is taken as
// given, except that test files and non-.go files are refused.
//
// Symbolic links are never followed, so a link cannot lead outside the root.
// Generated files are listed; they simply have no mutation sites later.
type Discover struct {
	FS       fs.FS
	Patterns []string
}

// Run returns the matching files, as sorted, de-duplicated, slash-separated
// paths relative to the root. No patterns, or no matches, give an empty result.
// The first problem with any pattern fails the whole call.
func (d Discover) Run(ctx context.Context) ([]string, error) {
	if d.FS == nil {
		return nil, ErrInvalid
	}

	found := make(map[string]struct{})
	for _, pattern := range d.Patterns {
		if err := d.collect(ctx, pattern, found); err != nil {
			return nil, err
		}
	}

	files := make([]string, 0, len(found))
	for f := range found {
		files = append(files, f)
	}
	slices.Sort(files)
	return files, nil
}

func (d Discover) collect(ctx context.Context, pattern string, found map[string]struct{}) error {
	base, recursive, err := parsePattern(pattern)
	if err != nil {
		return err
	}
	if err := d.rejectSymlinks(base); err != nil {
		return err
	}

	info, err := fs.Stat(d.FS, base)
	if err != nil {
		return err
	}

	if !info.IsDir() {
		if recursive {
			return fmt.Errorf("%w: %q is a file, not a directory", ErrInvalidPattern, pattern)
		}
		if !isSource(path.Base(base)) || !info.Mode().IsRegular() {
			return fmt.Errorf("%w: %s", ErrNotSource, base)
		}
		found[base] = struct{}{}
		return nil
	}
	return d.walk(ctx, base, recursive, found)
}

// walk adds the eligible files of dir, descending when recursive.
func (d Discover) walk(ctx context.Context, dir string, recursive bool, found map[string]struct{}) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	entries, err := fs.ReadDir(d.FS, dir)
	if err != nil {
		return err
	}

	for _, e := range entries {
		name := e.Name()
		full := path.Join(dir, name)

		switch {
		case e.Type()&fs.ModeSymlink != 0:
			// Never followed: it could point outside the root.
		case e.IsDir():
			if recursive && !skipDir(name) {
				if err := d.walk(ctx, full, true, found); err != nil {
					return err
				}
			}
		case e.Type().IsRegular() && isSource(name):
			found[full] = struct{}{}
		}
	}
	return nil
}

// rejectSymlinks refuses base if it, or any directory above it, is a link.
func (d Discover) rejectSymlinks(base string) error {
	if base == "." {
		return nil
	}
	parts := strings.Split(base, "/")
	for i := range parts {
		prefix := strings.Join(parts[:i+1], "/")
		info, err := fs.Lstat(d.FS, prefix)
		if err != nil {
			return err
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%w: %s", ErrSymlink, prefix)
		}
	}
	return nil
}

// parsePattern turns a pattern into a cleaned, root-relative path and whether
// it asks for a recursive walk.
func parsePattern(pattern string) (base string, recursive bool, err error) {
	bad := func(why string) (string, bool, error) {
		return "", false, fmt.Errorf("%w: %s", ErrInvalidPattern, why)
	}

	switch {
	case pattern == "":
		return bad("empty")
	case strings.ContainsRune(pattern, 0):
		return bad("contains a NUL byte")
	case strings.Contains(pattern, `\`):
		return bad("use / as the separator")
	}

	base = pattern
	if base == "..." || strings.HasSuffix(base, "/...") {
		recursive = true
		base = strings.TrimSuffix(base, "...")
	}
	if base == "" {
		base = "."
	}
	base = path.Clean(base)

	if !fs.ValidPath(base) {
		return bad("outside the root")
	}
	return base, recursive, nil
}

func isSource(name string) bool {
	return strings.HasSuffix(name, ".go") &&
		!strings.HasSuffix(name, "_test.go") &&
		!strings.HasPrefix(name, ".") &&
		!strings.HasPrefix(name, "_")
}

func skipDir(name string) bool {
	return name == "vendor" || name == "testdata" ||
		strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}
