// Package goast adapts the standard go/parser and go/ast packages to Gomu.
// It reads Go source files safely and maps token positions to the domain.
package goast

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/pallat/gomu/internal/mutant"
)

// MaxFileSize is the largest source file, in bytes, that Parse accepts.
const MaxFileSize = 10 << 20

// Errors returned by Parse.
var (
	// ErrOutsideRoot means the file resolves to a location outside the root.
	ErrOutsideRoot = errors.New("goast: path is outside the root")
	// ErrNotGoFile means the file name does not end in .go.
	ErrNotGoFile = errors.New("goast: not a .go file")
	// ErrNotRegular means the path is not a regular file.
	ErrNotRegular = errors.New("goast: not a regular file")
	// ErrTooLarge means the file is bigger than MaxFileSize.
	ErrTooLarge = errors.New("goast: file is too large")
)

// File is a parsed Go source file.
type File struct {
	// Path is the slash-separated path relative to the root.
	Path string
	// Fset holds the position information for AST.
	Fset *token.FileSet
	// AST is the parsed file, including comments.
	AST *ast.File
}

// Position converts pos into a domain position. The file name is relative to
// the root so reports never expose absolute paths.
func (f *File) Position(pos token.Pos) mutant.Position {
	p := f.Fset.Position(pos)
	return mutant.Position{File: f.Path, Line: p.Line, Column: p.Column}
}

// Parse reads and parses the Go file at path. A relative path is resolved
// against root.
//
// The file is rejected unless, after resolving symlinks, it is a regular
// .go file inside root and at most MaxFileSize bytes. Errors do not include
// absolute paths.
func Parse(root, path string) (*File, error) {
	if root == "" {
		return nil, errors.New("goast: empty root")
	}

	resolvedRoot, err := resolve(root)
	if err != nil {
		return nil, fmt.Errorf("goast: root: %w", cause(err))
	}

	if !filepath.IsAbs(path) {
		path = filepath.Join(resolvedRoot, path)
	}
	resolved, err := resolve(path)
	if err != nil {
		return nil, fmt.Errorf("goast: resolve path: %w", cause(err))
	}

	rel, err := filepath.Rel(resolvedRoot, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, ErrOutsideRoot
	}
	rel = filepath.ToSlash(rel)

	if !strings.HasSuffix(rel, ".go") {
		return nil, fmt.Errorf("%w: %s", ErrNotGoFile, rel)
	}

	src, err := readBounded(resolved, rel)
	if err != nil {
		return nil, err
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, rel, src, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("goast: parse: %w", err)
	}
	return &File{Path: rel, Fset: fset, AST: file}, nil
}

// resolve returns the absolute, symlink-free form of p.
func resolve(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// readBounded reads the regular file at path, refusing anything over MaxFileSize.
// rel is used in errors so absolute paths stay out of messages.
func readBounded(path, rel string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("goast: open %s: %w", rel, cause(err))
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("goast: stat %s: %w", rel, cause(err))
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s", ErrNotRegular, rel)
	}
	if info.Size() > MaxFileSize {
		return nil, fmt.Errorf("%w: %s", ErrTooLarge, rel)
	}

	// The size can change after Stat, so bound the read as well.
	src, err := io.ReadAll(io.LimitReader(f, MaxFileSize+1))
	if err != nil {
		return nil, fmt.Errorf("goast: read %s: %w", rel, cause(err))
	}
	if len(src) > MaxFileSize {
		return nil, fmt.Errorf("%w: %s", ErrTooLarge, rel)
	}
	return src, nil
}

// cause strips the absolute path that *fs.PathError carries and keeps the
// underlying error, so errors.Is(err, fs.ErrNotExist) still works.
func cause(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}
