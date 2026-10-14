//go:build unix

package fsys

import (
	"context"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
)

func TestSandboxSkipsSpecialFiles(t *testing.T) {
	t.Parallel()

	src := tree(t, map[string]string{"a.go": "package p\n"})
	if err := syscall.Mkfifo(filepath.Join(src, "pipe"), 0o600); err != nil {
		t.Skipf("mkfifo not supported: %v", err)
	}

	s, err := NewSandbox(context.Background(), src)
	if err != nil {
		t.Fatalf("NewSandbox() error = %v (a pipe must be skipped, not opened)", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if got := listing(t, s.Dir()); !slices.Equal(got, []string{"a.go"}) {
		t.Errorf("sandbox contents = %q, want only a.go", got)
	}
	if s.Skipped() != 1 {
		t.Errorf("Skipped() = %d, want 1", s.Skipped())
	}
}
