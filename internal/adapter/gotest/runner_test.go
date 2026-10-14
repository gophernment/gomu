package gotest_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gophernment/gomu/internal/adapter/fsys"
	"github.com/gophernment/gomu/internal/adapter/gotest"
	"github.com/gophernment/gomu/internal/mutant"
)

const (
	addSrc = "package p\n\nfunc Add(a, b int) int { return a + b }\n"

	addTest = `package p

import "testing"

func TestAdd(t *testing.T) {
	if Add(2, 3) != 5 {
		t.Fatal("bad")
	}
}
`
)

func needGo(t *testing.T) {
	t.Helper()

	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not found")
	}
}

// module creates a one-package module and returns its resolved directory.
func module(t *testing.T, src, test string) string {
	t.Helper()

	dir := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":      "module example.com/p\n\ngo 1.21\n",
		"add.go":      src,
		"add_test.go": test,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func newRunner(t *testing.T, dir string, timeout time.Duration) *gotest.Runner {
	t.Helper()

	r, err := gotest.NewRunner(dir, timeout)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// overlayFor makes an overlay that replaces dir/add.go with src.
func overlayFor(t *testing.T, dir, src string) string {
	t.Helper()

	o, err := fsys.NewOverlay()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = o.Close() })

	p, err := o.Write(filepath.Join(dir, "add.go"), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRunStatuses(t *testing.T) {
	t.Parallel()
	needGo(t)

	tests := []struct {
		name    string
		mutated string
		timeout time.Duration
		want    mutant.Status
	}{
		{
			name:    "tests pass so the mutant survives",
			mutated: addSrc,
			timeout: time.Minute,
			want:    mutant.Survived,
		},
		{
			name:    "tests fail so the mutant is killed",
			mutated: "package p\n\nfunc Add(a, b int) int { return a - b }\n",
			timeout: time.Minute,
			want:    mutant.Killed,
		},
		{
			name:    "type error is not viable",
			mutated: "package p\n\nfunc Add(a, b int) int { return a + \"x\" }\n",
			timeout: time.Minute,
			want:    mutant.NotViable,
		},
		{
			name:    "syntax error is not viable",
			mutated: "package p\n\nfunc Add(a, b int) int { return a + }\n",
			timeout: time.Minute,
			want:    mutant.NotViable,
		},
		{
			name:    "infinite loop times out",
			mutated: "package p\n\nfunc Add(a, b int) int {\n\tfor {\n\t}\n}\n",
			timeout: 2 * time.Second,
			want:    mutant.TimedOut,
		},
		{
			name:    "vet findings do not kill a mutant",
			mutated: "package p\n\nimport \"fmt\"\n\nfunc Add(a, b int) int {\n\tfmt.Sprintf(\"%d\")\n\treturn a + b\n}\n",
			timeout: time.Minute,
			want:    mutant.Survived,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := module(t, addSrc, addTest)
			r := newRunner(t, dir, tt.timeout)

			got, err := r.Run(context.Background(), gotest.Job{
				Pkg:     ".",
				Overlay: overlayFor(t, dir, tt.mutated),
			})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("Run() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRunKilledWithHugeOutput(t *testing.T) {
	t.Parallel()
	needGo(t)

	noisy := `package p

import (
	"strings"
	"testing"
)

func TestAdd(t *testing.T) {
	t.Log(strings.Repeat("x", 1<<20))
	println(strings.Repeat("y", 1<<20))
	if Add(2, 3) != 5 {
		t.Fatal("bad")
	}
}
`
	dir := module(t, addSrc, noisy)
	r := newRunner(t, dir, time.Minute)

	got, err := r.Run(context.Background(), gotest.Job{
		Pkg:     ".",
		Overlay: overlayFor(t, dir, "package p\n\nfunc Add(a, b int) int { return a - b }\n"),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got != mutant.Killed {
		t.Errorf("Run() = %v, want %v", got, mutant.Killed)
	}
}

func TestRunSubPackage(t *testing.T) {
	t.Parallel()
	needGo(t)

	dir := module(t, addSrc, addTest)
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"s.go":      "package sub\n\nfunc One() int { return 1 }\n",
		"s_test.go": "package sub\n\nimport \"testing\"\n\nfunc TestOne(t *testing.T) {\n\tif One() != 1 {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n",
	} {
		if err := os.WriteFile(filepath.Join(sub, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	o, err := fsys.NewOverlay()
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	overlay, err := o.Write(filepath.Join(sub, "s.go"), []byte("package sub\n\nfunc One() int { return 2 }\n"))
	if err != nil {
		t.Fatal(err)
	}

	got, err := newRunner(t, dir, time.Minute).Run(context.Background(), gotest.Job{Pkg: "sub", Overlay: overlay})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got != mutant.Killed {
		t.Errorf("Run() = %v, want %v", got, mutant.Killed)
	}
}

func TestRunRejectsBadJobs(t *testing.T) {
	t.Parallel()
	needGo(t)

	dir := module(t, addSrc, addTest)
	r := newRunner(t, dir, time.Minute)
	overlay := overlayFor(t, dir, addSrc)

	tests := []struct {
		name string
		job  gotest.Job
	}{
		{"empty package", gotest.Job{Pkg: "", Overlay: overlay}},
		{"parent directory", gotest.Job{Pkg: "..", Overlay: overlay}},
		{"escaping package", gotest.Job{Pkg: "../x", Overlay: overlay}},
		{"absolute package", gotest.Job{Pkg: "/tmp", Overlay: overlay}},
		{"dot segments", gotest.Job{Pkg: "a/../b", Overlay: overlay}},
		{"relative overlay", gotest.Job{Pkg: ".", Overlay: "overlay.json"}},
		{"empty overlay", gotest.Job{Pkg: "."}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := r.Run(context.Background(), tt.job)
			if !errors.Is(err, gotest.ErrInvalidJob) {
				t.Fatalf("Run() error = %v, want %v", err, gotest.ErrInvalidJob)
			}
			if got != mutant.Pending {
				t.Errorf("Run() status = %v, want %v with an error", got, mutant.Pending)
			}
		})
	}
}

func TestRunDirectoryNamedLikeFlagIsNotAFlag(t *testing.T) {
	t.Parallel()
	needGo(t)

	dir := module(t, addSrc, addTest)
	r := newRunner(t, dir, time.Minute)

	// "-v" must be treated as a missing directory, never as a go test flag.
	_, err := r.Run(context.Background(), gotest.Job{Pkg: "-v", Overlay: overlayFor(t, dir, addSrc)})
	if !errors.Is(err, gotest.ErrUnexpected) {
		t.Errorf("Run() error = %v, want %v", err, gotest.ErrUnexpected)
	}
}

func TestRunMissingPackageIsUnexpected(t *testing.T) {
	t.Parallel()
	needGo(t)

	dir := module(t, addSrc, addTest)
	r := newRunner(t, dir, time.Minute)

	_, err := r.Run(context.Background(), gotest.Job{Pkg: "nope", Overlay: overlayFor(t, dir, addSrc)})
	if !errors.Is(err, gotest.ErrUnexpected) {
		t.Errorf("Run() error = %v, want %v", err, gotest.ErrUnexpected)
	}
}

func TestRunCanceledContext(t *testing.T) {
	t.Parallel()
	needGo(t)

	dir := module(t, addSrc, addTest)
	r := newRunner(t, dir, time.Minute)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, err := r.Run(ctx, gotest.Job{Pkg: ".", Overlay: overlayFor(t, dir, addSrc)})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Run() error = %v, want %v", err, context.Canceled)
	}
	if got != mutant.Pending {
		t.Errorf("Run() status = %v, want %v with an error", got, mutant.Pending)
	}
}

func TestBaseline(t *testing.T) {
	t.Parallel()
	needGo(t)

	t.Run("passing tests write a profile", func(t *testing.T) {
		t.Parallel()

		dir := module(t, addSrc, addTest)
		profile := filepath.Join(t.TempDir(), "cover.out")

		d, err := newRunner(t, dir, time.Minute).Baseline(context.Background(), profile)
		if err != nil {
			t.Fatalf("Baseline() error = %v", err)
		}
		if d <= 0 {
			t.Errorf("Baseline() duration = %v, want > 0", d)
		}
		data, err := os.ReadFile(profile)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(string(data), "mode: ") {
			t.Errorf("profile does not look like a coverage profile: %q", data)
		}
	})

	t.Run("failing tests", func(t *testing.T) {
		t.Parallel()

		dir := module(t, "package p\n\nfunc Add(a, b int) int { return a - b }\n", addTest)
		profile := filepath.Join(t.TempDir(), "cover.out")

		_, err := newRunner(t, dir, time.Minute).Baseline(context.Background(), profile)
		if !errors.Is(err, gotest.ErrBaselineFailed) {
			t.Errorf("Baseline() error = %v, want %v", err, gotest.ErrBaselineFailed)
		}
	})

	t.Run("relative profile path", func(t *testing.T) {
		t.Parallel()

		dir := module(t, addSrc, addTest)
		_, err := newRunner(t, dir, time.Minute).Baseline(context.Background(), "cover.out")
		if !errors.Is(err, gotest.ErrInvalidJob) {
			t.Errorf("Baseline() error = %v, want %v", err, gotest.ErrInvalidJob)
		}
	})

	t.Run("canceled context", func(t *testing.T) {
		t.Parallel()

		dir := module(t, addSrc, addTest)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := newRunner(t, dir, time.Minute).Baseline(ctx, filepath.Join(t.TempDir(), "c.out"))
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Baseline() error = %v, want %v", err, context.Canceled)
		}
	})
}

// Not parallel: t.Setenv changes the process environment.
func TestSecretsDoNotReachTests(t *testing.T) {
	needGo(t)
	t.Setenv("GOMU_TEST_SECRET", "hunter2")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "hunter2")

	probe := `package p

import (
	"os"
	"testing"
)

func TestEnv(t *testing.T) {
	for _, k := range []string{"GOMU_TEST_SECRET", "AWS_SECRET_ACCESS_KEY"} {
		if os.Getenv(k) != "" {
			t.Fatalf("%s leaked into the test process", k)
		}
	}
}
`
	dir := module(t, addSrc, probe)

	if _, err := newRunner(t, dir, time.Minute).Baseline(context.Background(), filepath.Join(t.TempDir(), "c.out")); err != nil {
		t.Errorf("Baseline() error = %v; a secret reached the test process", err)
	}
}

func TestNewRunnerRejectsBadInput(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		dir     string
		timeout time.Duration
	}{
		{"zero timeout", t.TempDir(), 0},
		{"negative timeout", t.TempDir(), -time.Second},
		{"missing dir", filepath.Join(t.TempDir(), "nope"), time.Second},
		{"file instead of dir", file, time.Second},
		{"empty dir name", "", time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r, err := gotest.NewRunner(tt.dir, tt.timeout)
			if err == nil {
				t.Fatal("NewRunner() returned nil error")
			}
			if r != nil {
				t.Error("NewRunner() returned a Runner together with an error")
			}
		})
	}
}

func TestNewRunnerErrorsDoNotLeakPaths(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "nope")
	_, err := gotest.NewRunner(dir, time.Second)
	if err == nil {
		t.Fatal("NewRunner() returned nil error")
	}
	if strings.Contains(err.Error(), dir) {
		t.Errorf("error leaks the directory path: %v", err)
	}
}
