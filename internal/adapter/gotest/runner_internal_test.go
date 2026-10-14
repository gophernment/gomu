//go:build unix

package gotest

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pallat/gomu/internal/mutant"
)

func TestDefaultsAreSet(t *testing.T) {
	t.Parallel()

	r, err := NewRunner(t.TempDir(), time.Second)
	if err != nil {
		t.Skipf("no go tool: %v", err)
	}
	// Zero would remove the backstop, or hang on a child that keeps our pipes.
	if r.grace <= 0 {
		t.Errorf("grace = %v, want a positive default", r.grace)
	}
	if r.waitDelay <= 0 {
		t.Errorf("waitDelay = %v, want a positive default", r.waitDelay)
	}
}

// fakeGo writes an executable that ignores its arguments and sleeps, standing
// in for a "go test" that hangs and leaves a child holding the output pipe.
func fakeGo(t *testing.T) string {
	t.Helper()

	p := filepath.Join(t.TempDir(), "go")
	script := "#!/bin/sh\nsleep 3 &\nsleep 3\n"
	if err := os.WriteFile(p, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

// The backstop must end a run that "go test -timeout" did not stop, and only
// after timeout+grace, not before and not by waiting for orphaned children.
func TestBackstopEndsAHungRun(t *testing.T) {
	t.Parallel()

	r := &Runner{
		goBin:     fakeGo(t),
		dir:       t.TempDir(),
		timeout:   time.Second,
		grace:     300 * time.Millisecond,
		waitDelay: 100 * time.Millisecond,
	}

	start := time.Now()
	got, err := r.Run(context.Background(), Job{Pkg: ".", Overlay: "/overlay.json"})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got != mutant.TimedOut {
		t.Errorf("Run() = %v, want %v", got, mutant.TimedOut)
	}
	if elapsed < time.Second {
		t.Errorf("gave up after %v, before the %v test timeout", elapsed, time.Second)
	}
	if elapsed > 2500*time.Millisecond {
		t.Errorf("took %v: the orphaned child kept us waiting (the children sleep for 3s)", elapsed)
	}
}

func TestMarkerWriterBoundsMemory(t *testing.T) {
	t.Parallel()

	var w markerWriter
	long := make([]byte, 10*maxLine)
	for i := range long {
		long[i] = 'x'
	}
	if n, err := w.Write(long); n != len(long) || err != nil {
		t.Fatalf("Write() = %d, %v; want all %d bytes accepted", n, err, len(long))
	}
	if len(w.line) != maxLine {
		t.Errorf("kept %d bytes of a long line, want exactly maxLine = %d", len(w.line), maxLine)
	}
}

func TestMarkerWriterFindsMarkersAcrossWrites(t *testing.T) {
	t.Parallel()

	var w markerWriter
	for _, chunk := range []string{"ok  \tpkg\t0.1s\nFA", "IL\tpkg [build ", "failed]\n", "panic: test timed out after 1s\n", "FAIL\tother\t1s"} {
		_, _ = w.Write([]byte(chunk))
	}
	w.flush()

	res := w.result()
	if !res.buildFailed || !res.timedOut || !res.sawFail {
		t.Errorf("result = %+v, want buildFailed, timedOut and sawFail all set", res)
	}
	if res.setupFailed {
		t.Error("setupFailed set without a [setup failed] line")
	}
}
