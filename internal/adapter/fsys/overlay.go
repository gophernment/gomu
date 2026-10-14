// Package fsys holds file-system adapters. Overlay lets a mutated file be
// compiled in place of the original, so the user's source is never modified.
package fsys

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// overlayPrefix names every directory NewOverlay creates. Close refuses to
// delete anything else, so a bug can never turn it into a recursive delete of
// the wrong directory.
const overlayPrefix = "gomu-overlay-"

// Errors returned by Overlay.
var (
	// ErrNotAbsolute means the path to replace is not absolute.
	ErrNotAbsolute = errors.New("fsys: path must be absolute")
	// ErrClosed means the Overlay was already closed.
	ErrClosed = errors.New("fsys: overlay is closed")
	// ErrNotOwned means the directory is not one this package created.
	ErrNotOwned = errors.New("fsys: refusing to remove a directory this package did not create")
)

// Overlay writes a mutated source file and the overlay.json that makes
// "go test -overlay" use it instead of the original.
//
// It owns a private temp directory (mode 0o700) holding files of mode 0o600.
// Create one Overlay per worker: it is not safe for concurrent use, and each
// Write replaces the previous mutation.
type Overlay struct {
	dir    string
	closed bool
}

// NewOverlay creates the temp directory. The caller must call Close.
func NewOverlay() (*Overlay, error) {
	dir, err := os.MkdirTemp("", overlayPrefix+"*")
	if err != nil {
		return nil, fmt.Errorf("fsys: create overlay dir: %w", err)
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("fsys: resolve overlay dir: %w", err)
	}
	return &Overlay{dir: dir}, nil
}

// Write stores src as the replacement for the file at original, which must be
// an absolute path, and returns the path of the overlay.json to pass to
// "go test -overlay". The original file is not touched.
func (o *Overlay) Write(original string, src []byte) (string, error) {
	if o.closed {
		return "", ErrClosed
	}
	if !filepath.IsAbs(original) {
		return "", ErrNotAbsolute
	}

	backing := filepath.Join(o.dir, "src.go")
	if err := os.WriteFile(backing, src, 0o600); err != nil {
		return "", fmt.Errorf("fsys: write mutated source: %w", err)
	}

	data, err := json.Marshal(struct {
		Replace map[string]string
	}{Replace: map[string]string{filepath.Clean(original): backing}})
	if err != nil {
		return "", fmt.Errorf("fsys: encode overlay: %w", err)
	}

	jsonPath := filepath.Join(o.dir, "overlay.json")
	if err := os.WriteFile(jsonPath, data, 0o600); err != nil {
		return "", fmt.Errorf("fsys: write overlay: %w", err)
	}
	return jsonPath, nil
}

// Close removes the temp directory. It is safe to call more than once.
// It returns ErrNotOwned, and removes nothing, if the directory is not one
// NewOverlay would have created.
func (o *Overlay) Close() error {
	if o.closed {
		return nil
	}
	o.closed = true

	if !strings.HasPrefix(filepath.Base(o.dir), overlayPrefix) || !filepath.IsAbs(o.dir) {
		return ErrNotOwned
	}
	if err := os.RemoveAll(o.dir); err != nil {
		return fmt.Errorf("fsys: remove overlay dir: %w", err)
	}
	return nil
}
