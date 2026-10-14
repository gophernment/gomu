package fsys_test

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gophernment/gomu/internal/adapter/fsys"
)

type overlayJSON struct {
	Replace map[string]string
}

func newOverlay(t *testing.T) *fsys.Overlay {
	t.Helper()

	o, err := fsys.NewOverlay()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = o.Close() })
	return o
}

func readOverlay(t *testing.T, jsonPath string) overlayJSON {
	t.Helper()

	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	var got overlayJSON
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("overlay.json is not valid JSON: %v\n%s", err, data)
	}
	return got
}

func absPath(t *testing.T, name string) string {
	t.Helper()

	return filepath.Join(t.TempDir(), name)
}

func TestOverlayWrite(t *testing.T) {
	t.Parallel()

	o := newOverlay(t)
	orig := absPath(t, "add.go")

	jsonPath, err := o.Write(orig, []byte("package p\n"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if !filepath.IsAbs(jsonPath) {
		t.Errorf("overlay path %q is not absolute", jsonPath)
	}

	got := readOverlay(t, jsonPath)
	backing, ok := got.Replace[orig]
	if !ok || len(got.Replace) != 1 {
		t.Fatalf("Replace = %v, want exactly %q", got.Replace, orig)
	}
	if !filepath.IsAbs(backing) {
		t.Errorf("backing path %q is not absolute", backing)
	}

	data, err := os.ReadFile(backing)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "package p\n" {
		t.Errorf("backing file = %q, want the written source", data)
	}

	if _, err := os.Stat(orig); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Write touched the original path: Stat error = %v", err)
	}
}

func TestOverlayPermissions(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("unix permissions")
	}

	o := newOverlay(t)
	jsonPath, err := o.Write(absPath(t, "a.go"), []byte("package p\n"))
	if err != nil {
		t.Fatal(err)
	}
	backing := readOverlay(t, jsonPath).Replace
	var backingPath string
	for _, v := range backing {
		backingPath = v
	}

	for _, tt := range []struct {
		path string
		want os.FileMode
	}{
		{filepath.Dir(jsonPath), 0o700},
		{jsonPath, 0o600},
		{backingPath, 0o600},
	} {
		info, err := os.Stat(tt.path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != tt.want {
			t.Errorf("mode of %s = %o, want %o", filepath.Base(tt.path), got, tt.want)
		}
	}
}

func TestOverlayWriteReplacesPrevious(t *testing.T) {
	t.Parallel()

	o := newOverlay(t)
	first, second := absPath(t, "a.go"), absPath(t, "b.go")

	if _, err := o.Write(first, []byte("package first\n")); err != nil {
		t.Fatal(err)
	}
	jsonPath, err := o.Write(second, []byte("package second\n"))
	if err != nil {
		t.Fatal(err)
	}

	got := readOverlay(t, jsonPath)
	if _, stale := got.Replace[first]; stale || len(got.Replace) != 1 {
		t.Fatalf("Replace = %v, want only %q", got.Replace, second)
	}
	data, err := os.ReadFile(got.Replace[second])
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "package second\n" {
		t.Errorf("backing file = %q, want the latest source", data)
	}
}

func TestOverlayPathRoundTrip(t *testing.T) {
	t.Parallel()

	o := newOverlay(t)
	orig := absPath(t, `we"ird \ name é.go`)

	jsonPath, err := o.Write(orig, []byte("package p\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := readOverlay(t, jsonPath).Replace[orig]; !ok {
		t.Errorf("path %q did not survive JSON encoding", orig)
	}
}

func TestOverlayWriteRejectsRelativePath(t *testing.T) {
	t.Parallel()

	o := newOverlay(t)

	for _, p := range []string{"", "add.go", "./add.go", "../add.go"} {
		jsonPath, err := o.Write(p, []byte("package p\n"))
		if !errors.Is(err, fsys.ErrNotAbsolute) {
			t.Errorf("Write(%q) error = %v, want %v", p, err, fsys.ErrNotAbsolute)
		}
		if jsonPath != "" {
			t.Errorf("Write(%q) returned path %q together with an error", p, jsonPath)
		}
	}
}

func TestOverlayClose(t *testing.T) {
	t.Parallel()

	o, err := fsys.NewOverlay()
	if err != nil {
		t.Fatal(err)
	}
	jsonPath, err := o.Write(absPath(t, "a.go"), []byte("package p\n"))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(jsonPath)

	if err := o.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("temp dir still exists after Close: Stat error = %v", err)
	}

	if err := o.Close(); err != nil {
		t.Errorf("second Close() error = %v, want nil", err)
	}
	if _, err := o.Write(absPath(t, "a.go"), nil); !errors.Is(err, fsys.ErrClosed) {
		t.Errorf("Write() after Close error = %v, want %v", err, fsys.ErrClosed)
	}
}

func TestOverlaysDoNotShareDirectories(t *testing.T) {
	t.Parallel()

	a, b := newOverlay(t), newOverlay(t)
	pa, err := a.Write(absPath(t, "x.go"), []byte("package a\n"))
	if err != nil {
		t.Fatal(err)
	}
	pb, err := b.Write(absPath(t, "x.go"), []byte("package b\n"))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(pa) == filepath.Dir(pb) {
		t.Errorf("two overlays share %s; parallel workers would race", filepath.Dir(pa))
	}
}

// TestOverlayWithGoTest proves the file is accepted by the real go tool and
// that the original source on disk is never modified.
func TestOverlayWithGoTest(t *testing.T) {
	t.Parallel()

	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go tool not found")
	}

	mod := t.TempDir()
	const orig = "package p\n\nfunc Add(a, b int) int { return a + b }\n"
	files := map[string]string{
		"go.mod":      "module example.com/p\n\ngo 1.21\n",
		"add.go":      orig,
		"add_test.go": "package p\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(2, 3) != 5 {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(mod, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Resolve symlinks (macOS /var -> /private/var) so the overlay key matches
	// the path the go tool computes.
	mod, err = filepath.EvalSymlinks(mod)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(mod, "add.go")

	run := func(overlay string) (string, error) {
		args := []string{"test", "-count=1"}
		if overlay != "" {
			args = append(args, "-overlay="+overlay)
		}
		cmd := exec.Command(goBin, append(args, "./...")...)
		cmd.Dir = mod
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	if out, err := run(""); err != nil {
		t.Fatalf("baseline go test failed: %v\n%s", err, out)
	}

	o := newOverlay(t)
	jsonPath, err := o.Write(target, []byte(strings.Replace(orig, "a + b", "a - b", 1)))
	if err != nil {
		t.Fatal(err)
	}

	out, err := run(jsonPath)
	if err == nil {
		t.Fatalf("mutant survived but Add(2,3) != 5 should fail:\n%s", out)
	}
	if !strings.Contains(out, "FAIL") {
		t.Errorf("expected a test failure, got:\n%s", out)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != orig {
		t.Errorf("original source was modified:\n%s", got)
	}
}

func TestOverlayWriteFailsWhenDirIsGone(t *testing.T) {
	t.Parallel()

	o := newOverlay(t)
	jsonPath, err := o.Write(absPath(t, "a.go"), []byte("package p\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Dir(jsonPath)); err != nil {
		t.Fatal(err)
	}

	if p, err := o.Write(absPath(t, "a.go"), []byte("package p\n")); err == nil || p != "" {
		t.Errorf("Write() = (%q, %v), want an error and no path", p, err)
	}
}

// Not parallel: t.Setenv changes the process environment.
func TestNewOverlayFailsWithoutTempDir(t *testing.T) {
	// A mutant may carry on after the failure with an empty directory, which
	// means the current one. Make that harmless.
	t.Chdir(t.TempDir())
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
	t.Setenv("TMP", os.Getenv("TMPDIR"))
	t.Setenv("TEMP", os.Getenv("TMPDIR"))

	if o, err := fsys.NewOverlay(); err == nil {
		_ = o.Close()
		t.Error("NewOverlay() succeeded with an unusable temp dir")
	}
}
