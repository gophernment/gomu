//go:build unix

package main

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gophernment/gomu/internal/adapter/cover"
	"github.com/gophernment/gomu/internal/adapter/gotest"
)

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("pipe closed") }

func TestReportWriteFailureExitsWithError(t *testing.T) {
	t.Parallel()
	needGo(t)

	dir := fixture(t, map[string]string{"go.mod": "module example.com/e\n\ngo 1.21\n", "doc.go": "package e\n"})

	var stderr bytes.Buffer
	code := run(context.Background(), []string{"-dir", dir}, brokenWriter{}, &stderr)
	if code != exitError || !strings.Contains(stderr.String(), "pipe closed") {
		t.Errorf("code = %d, stderr = %q, want %d and the writer's error", code, stderr.String(), exitError)
	}
}

func TestGoModWithoutModuleDirective(t *testing.T) {
	t.Parallel()

	dir := fixture(t, map[string]string{"go.mod": "go 1.21\n"})

	code, stdout, stderr := gomu(context.Background(), "-dir", dir)
	if code != exitError || stdout != "" || !strings.Contains(stderr, "no module directive") {
		t.Errorf("code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
}

func TestReadModulePathFailures(t *testing.T) {
	t.Parallel()

	missing := t.TempDir() // no go.mod
	isDir := t.TempDir()   // go.mod is a directory: it opens, but cannot be read
	if err := os.Mkdir(filepath.Join(isDir, "go.mod"), 0o700); err != nil {
		t.Fatal(err)
	}

	for name, root := range map[string]string{"missing": missing, "a directory": isDir} {
		_, err := readModulePath(root)
		if err == nil || !strings.Contains(err.Error(), "read go.mod") || strings.Contains(err.Error(), root) {
			t.Errorf("%s: error = %v, want a read error without the path", name, err)
		}
	}
}

func TestUnwrapPathError(t *testing.T) {
	t.Parallel()

	inner := errors.New("denied")
	plain := errors.New("plain")
	if got := unwrapPathError(&fs.PathError{Op: "open", Path: "/secret", Err: inner}); got != inner {
		t.Errorf("unwrapPathError(PathError) = %v, want %v", got, inner)
	}
	if got := unwrapPathError(plain); got != plain {
		t.Errorf("unwrapPathError(plain) = %v, want it unchanged", got)
	}
}

func TestOpenFileFailure(t *testing.T) {
	t.Parallel()

	root := fixture(t, map[string]string{"go.mod": "module example.com/e\n\ngo 1.21\n"})
	if _, err := openFile(root, "nope.go", nil); err == nil || strings.Contains(err.Error(), root) {
		t.Errorf("openFile() error = %v, want an error without the path", err)
	}
}

// fakeGoOnPath puts an executable named go first on PATH. Callers must not be
// parallel: t.Setenv changes process-wide state.
func fakeGoOnPath(t *testing.T, script string) {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go"), []byte("#!/bin/sh\n"+script+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func newBaseline(t *testing.T) baseline {
	t.Helper()

	r, err := gotest.NewRunner(t.TempDir(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return baseline{runner: r, module: "example.com/e"}
}

func TestBaselineFailures(t *testing.T) {
	t.Run("no temp directory", func(t *testing.T) {
		// A mutant may carry on after the failure with an empty directory, which
		// means the current one. Make that harmless.
		t.Chdir(t.TempDir())
		fakeGoOnPath(t, "exit 0")
		b := newBaseline(t)
		missing := filepath.Join(t.TempDir(), "missing")
		t.Setenv("TMPDIR", missing)

		_, _, err := b.Run(context.Background())
		if err == nil || strings.Contains(err.Error(), missing) {
			t.Errorf("error = %v, want an error without the path", err)
		}
	})

	t.Run("tests pass but leave no coverage profile", func(t *testing.T) {
		fakeGoOnPath(t, "exit 0")
		b := newBaseline(t)

		_, _, err := b.Run(context.Background())
		if err == nil || !strings.Contains(err.Error(), "coverage profile") {
			t.Errorf("error = %v, want a coverage profile error", err)
		}
	})

	t.Run("garbage coverage profile", func(t *testing.T) {
		fakeGoOnPath(t, `for a in "$@"; do case $a in -coverprofile=*) echo garbage > "${a#-coverprofile=}";; esac; done`)
		b := newBaseline(t)

		_, _, err := b.Run(context.Background())
		if !errors.Is(err, cover.ErrFormat) {
			t.Errorf("error = %v, want %v", err, cover.ErrFormat)
		}
	})

	t.Run("canceled while running", func(t *testing.T) {
		fakeGoOnPath(t, "sleep 5")
		b := newBaseline(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, _, err := b.Run(ctx)
		if !errors.Is(err, context.Canceled) {
			t.Errorf("error = %v, want %v", err, context.Canceled)
		}
	})
}

func TestNoGoToolOnPath(t *testing.T) {
	needGo(t)
	dir := fixture(t, map[string]string{"go.mod": "module example.com/e\n\ngo 1.21\n", "doc.go": "package e\n"})
	t.Setenv("PATH", t.TempDir())

	code, stdout, stderr := gomu(context.Background(), "-dir", dir)
	if code != exitError || stdout != "" || !strings.Contains(stderr, "go tool") {
		t.Errorf("code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
}
