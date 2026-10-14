package fsys

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Regression: running Gomu on itself once deleted this very package. A mutant
// made NewOverlay carry on after MkdirTemp failed, with dir "", which
// filepath.Abs turned into the current directory, and Close then removed it.
func TestCloseRefusesDirectoriesItDidNotCreate(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	victim := filepath.Join(base, "precious")
	if err := os.Mkdir(victim, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(victim, "keep.go"), []byte("package p\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, dir := range []string{victim, base, "", ".", "relative/gomu-overlay-x", filepath.Join(base, "gomu-overlay")} {
		o := &Overlay{dir: dir}
		if err := o.Close(); !errors.Is(err, ErrNotOwned) {
			t.Errorf("Close() with dir %q error = %v, want %v", dir, err, ErrNotOwned)
		}
	}

	if _, err := os.Stat(filepath.Join(victim, "keep.go")); err != nil {
		t.Errorf("Close removed a directory it did not create: %v", err)
	}
}

func TestCloseRemovesItsOwnDirectory(t *testing.T) {
	t.Parallel()

	o, err := NewOverlay()
	if err != nil {
		t.Fatal(err)
	}
	dir := o.dir

	if err := o.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("directory still exists: %v", err)
	}
}
