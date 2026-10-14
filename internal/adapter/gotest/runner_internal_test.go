//go:build unix

package gotest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

// A fake go that prints a failure marker with no trailing newline: the last
// line must still be examined when the process ends.
func TestUnterminatedLastLineIsStillExamined(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "go")
	script := "#!/bin/sh\nprintf 'FAIL\\texample.com/p\\t0.1s'\nexit 1\n"
	if err := os.WriteFile(p, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	r := &Runner{goBin: p, dir: t.TempDir(), timeout: time.Minute, grace: time.Second, waitDelay: time.Second}

	got, err := r.Run(context.Background(), Job{Pkg: ".", Overlay: "/overlay.json"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got != mutant.Killed {
		t.Errorf("Run() = %v, want %v", got, mutant.Killed)
	}
}

// A package that cannot be loaded says nothing about the tests: it must not
// stop the run, and it must not count as a kill either.
func TestSetupFailureIsNotViable(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "go")
	script := "#!/bin/sh\nprintf 'FAIL\\texample.com/p [setup failed]\\nFAIL\\n'\nexit 1\n"
	if err := os.WriteFile(p, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	r := &Runner{goBin: p, dir: t.TempDir(), timeout: time.Minute, grace: time.Second, waitDelay: time.Second}

	got, err := r.Run(context.Background(), Job{Pkg: ".", Overlay: "/overlay.json"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got != mutant.NotViable {
		t.Errorf("Run() = %v, want %v", got, mutant.NotViable)
	}
}

func fakeGoScript(t *testing.T, body string) string {
	t.Helper()

	p := filepath.Join(t.TempDir(), "go")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

// go test that dies without printing a test result is not a verdict.
func TestExitWithoutResultIsUnexpected(t *testing.T) {
	t.Parallel()

	r := &Runner{goBin: fakeGoScript(t, "exit 3"), dir: t.TempDir(), timeout: time.Minute, grace: time.Second, waitDelay: time.Second}

	_, err := r.Run(context.Background(), Job{Pkg: ".", Overlay: "/overlay.json"})
	if !errors.Is(err, ErrUnexpected) || !strings.Contains(err.Error(), "exit code 3") {
		t.Errorf("Run() error = %v, want %v with the exit code", err, ErrUnexpected)
	}
}

func TestMissingGoBinaryIsUnexpected(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "no-such-go")
	r := &Runner{goBin: missing, dir: t.TempDir(), timeout: time.Minute, grace: time.Second, waitDelay: time.Second}

	_, err := r.Run(context.Background(), Job{Pkg: ".", Overlay: "/overlay.json"})
	if !errors.Is(err, ErrUnexpected) {
		t.Fatalf("Run() error = %v, want %v", err, ErrUnexpected)
	}
	if strings.Contains(err.Error(), missing) {
		t.Errorf("error leaks the path of the go tool: %v", err)
	}
}

// Not parallel: t.Setenv changes process-wide state.
func TestNewRunnerWithoutGoTool(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	if _, err := NewRunner(t.TempDir(), time.Second); err == nil {
		t.Error("NewRunner() succeeded without a go tool on PATH")
	}
}
