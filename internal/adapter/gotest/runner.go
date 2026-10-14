// Package gotest runs "go test" against a mutant and turns the outcome into a
// mutant.Status. It never uses a shell and passes only an allowlisted
// environment to the child process.
package gotest

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/gophernment/gomu/internal/mutant"
)

const (
	// defaultGrace is extra time on top of the test timeout for compiling and
	// linking. It is only a backstop; "go test" enforces the test timeout.
	defaultGrace = time.Minute
	// defaultWaitDelay bounds how long to wait for a killed process's pipes to
	// close. Without it, a child that outlives its parent would hang us.
	defaultWaitDelay = 5 * time.Second
)

// Errors returned by the Runner.
var (
	// ErrInvalidJob means a Job field is empty, malformed, or unsafe.
	ErrInvalidJob = errors.New("gotest: invalid job")
	// ErrBaselineFailed means the unmodified tests do not pass.
	ErrBaselineFailed = errors.New("gotest: baseline tests failed")
	// ErrUnexpected means go test failed in a way that is not a test result,
	// such as a missing package or a broken toolchain.
	ErrUnexpected = errors.New("gotest: unexpected go test failure")
)

// allowedEnv lists the only variables passed to go test.
var allowedEnv = map[string]bool{
	"PATH": true, "HOME": true, "USER": true, "LOGNAME": true,
	"TMPDIR": true, "TMP": true, "TEMP": true, "SYSTEMROOT": true,
	"GOCACHE": true, "GOMODCACHE": true, "GOPATH": true, "GOROOT": true,
	"GOFLAGS": true, "GOENV": true, "GOTOOLCHAIN": true, "GOWORK": true,
	"GOPROXY": true, "GOSUMDB": true, "GOPRIVATE": true, "GONOPROXY": true, "GONOSUMDB": true,
	"GOOS": true, "GOARCH": true, "CGO_ENABLED": true, "CC": true, "CXX": true,
}

// Runner runs go test inside one module. It is safe for concurrent use.
type Runner struct {
	goBin     string
	dir       string
	timeout   time.Duration
	grace     time.Duration
	waitDelay time.Duration
	tmp       string
}

// WithTempDir returns a copy of r whose go test processes use dir, which must
// be an absolute path, as their temp directory. Whatever the tests leave
// there can then be removed in one go.
func (r *Runner) WithTempDir(dir string) (*Runner, error) {
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("%w: temp directory must be absolute", ErrInvalidJob)
	}
	c := *r
	c.tmp = dir
	return &c, nil
}

// NewRunner returns a Runner for the module at dir (pass a symlink-free path
// if overlay keys are built from it). timeout is the per-mutant
// limit for running the tests. It fails if dir is not a directory, timeout is
// not positive, or no go tool is found on PATH.
func NewRunner(dir string, timeout time.Duration) (*Runner, error) {
	if timeout <= 0 {
		return nil, fmt.Errorf("%w: timeout must be positive", ErrInvalidJob)
	}
	if dir == "" {
		return nil, fmt.Errorf("%w: module dir is empty", ErrInvalidJob)
	}
	resolved, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("gotest: module dir: %w", cause(err))
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("gotest: module dir: %w", cause(err))
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%w: module dir is not a directory", ErrInvalidJob)
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		return nil, fmt.Errorf("gotest: find go tool: %w", err)
	}
	return &Runner{goBin: goBin, dir: resolved, timeout: timeout, grace: defaultGrace, waitDelay: defaultWaitDelay}, nil
}

// Job describes one mutant run.
type Job struct {
	// Pkg is the package directory relative to the module root, slash
	// separated, such as "internal/mutant" or "." for the root package.
	Pkg string
	// Overlay is the absolute path of the overlay.json to compile with.
	Overlay string
}

// Run tests the package in job against the overlay and reports the result:
//
//   - Survived: the tests passed.
//   - Killed: the tests failed.
//   - TimedOut: the tests exceeded the runner's timeout.
//   - NotViable: the mutant did not compile, or its package could not be
//     loaded. The second happens when an earlier mutant made the tests leave
//     a broken file in the package directory of the shared copy; the verdict
//     is then "could not be evaluated", never a false kill.
//
// It returns the context's error if ctx ends first, and ErrUnexpected when
// go test fails without a test result or the package directory is missing.
func (r *Runner) Run(ctx context.Context, job Job) (mutant.Status, error) {
	if !fs.ValidPath(job.Pkg) {
		return mutant.Pending, fmt.Errorf("%w: bad package directory", ErrInvalidJob)
	}
	if !filepath.IsAbs(job.Overlay) {
		return mutant.Pending, fmt.Errorf("%w: overlay path must be absolute", ErrInvalidJob)
	}

	// Without this a typo and a polluted copy would both look like a setup failure.
	if fi, err := os.Stat(filepath.Join(r.dir, filepath.FromSlash(job.Pkg))); err != nil || !fi.IsDir() {
		return mutant.Pending, fmt.Errorf("%w: package directory not found", ErrUnexpected)
	}

	// "./" keeps a directory such as "-x" from being read as a flag.
	pkg := "./" + job.Pkg
	if job.Pkg == "." {
		pkg = "."
	}
	args := []string{
		"test", "-count=1", "-failfast", "-vet=off",
		"-timeout=" + r.timeout.String(),
		"-overlay=" + job.Overlay,
		pkg,
	}

	res, err := r.execGo(ctx, r.timeout+r.grace, args)
	switch {
	case err != nil:
		return mutant.Pending, err
	case res.deadline:
		return mutant.TimedOut, nil
	case res.exitCode == 0:
		return mutant.Survived, nil
	case res.timedOut:
		return mutant.TimedOut, nil
	case res.setupFailed, res.buildFailed: // no verdict on the tests either way
		return mutant.NotViable, nil
	case res.sawFail:
		return mutant.Killed, nil
	default:
		return mutant.Pending, fmt.Errorf("%w: exit code %d", ErrUnexpected, res.exitCode)
	}
}

// Baseline runs every test in the module once, unmodified, and writes a
// coverage profile to coverProfile, which must be an absolute path. It returns
// how long the run took, which callers can use to size the per-mutant timeout.
// It returns ErrBaselineFailed if the tests do not pass.
func (r *Runner) Baseline(ctx context.Context, coverProfile string) (time.Duration, error) {
	if !filepath.IsAbs(coverProfile) {
		return 0, fmt.Errorf("%w: cover profile path must be absolute", ErrInvalidJob)
	}

	start := time.Now()
	res, err := r.execGo(ctx, 0, []string{
		"test", "-count=1", "-vet=off", "-coverprofile=" + coverProfile, "./...",
	})
	elapsed := time.Since(start)

	switch {
	case err != nil:
		return 0, err
	case res.exitCode != 0:
		return elapsed, ErrBaselineFailed
	default:
		return elapsed, nil
	}
}

// execGo runs go with args in the module dir. A positive limit bounds the whole
// run; ctx always applies.
func (r *Runner) execGo(ctx context.Context, limit time.Duration, args []string) (result, error) {
	runCtx := ctx
	if limit > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, limit)
		defer cancel()
	}

	w := &markerWriter{}
	cmd := exec.CommandContext(runCtx, r.goBin, args...)
	cmd.Dir = r.dir
	cmd.Env = r.env()
	cmd.Stdout = w
	cmd.Stderr = w
	cmd.WaitDelay = r.waitDelay

	err := cmd.Run()
	w.flush()
	res := w.result()

	if ctx.Err() != nil {
		return res, ctx.Err()
	}
	if runCtx.Err() != nil { // our own limit, not the caller's
		res.deadline = true
		return res, nil
	}

	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		res.exitCode = exit.ExitCode()
	default:
		return res, fmt.Errorf("%w: %w", ErrUnexpected, cause(err))
	}
	return res, nil
}

// env is the allowlisted environment, with the temp directory replaced when
// one was set.
func (r *Runner) env() []string {
	env := filterEnv(os.Environ())
	if r.tmp == "" {
		return env
	}
	env = slices.DeleteFunc(env, func(kv string) bool {
		name, _, _ := strings.Cut(kv, "=")
		return name == "TMPDIR" || name == "TMP" || name == "TEMP"
	})
	return append(env, "TMPDIR="+r.tmp, "TMP="+r.tmp, "TEMP="+r.tmp)
}

// filterEnv keeps only allowlisted variables, so secrets in the parent
// environment never reach the code under test.
func filterEnv(env []string) []string {
	out := make([]string, 0, len(allowedEnv))
	for _, kv := range env {
		name, _, ok := strings.Cut(kv, "=")
		if ok && allowedEnv[name] {
			out = append(out, kv)
		}
	}
	return out
}

// cause drops the absolute paths carried by *fs.PathError and *exec.Error.
func cause(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}

// result is what Run learns from one go test process.
type result struct {
	exitCode    int
	deadline    bool // our backstop deadline expired
	timedOut    bool // go test reported a test timeout
	buildFailed bool
	setupFailed bool
	sawFail     bool
}

// markerWriter looks for result markers in the output line by line without
// storing it, so a noisy test cannot exhaust memory. Only the first maxLine
// bytes of each line are examined.
type markerWriter struct {
	line []byte
	res  result
}

const maxLine = 4096

func (w *markerWriter) Write(p []byte) (int, error) {
	for _, b := range p {
		if b == '\n' {
			w.flush()
			continue
		}
		if len(w.line) < maxLine {
			w.line = append(w.line, b)
		}
	}
	return len(p), nil
}

func (w *markerWriter) flush() {
	line := strings.TrimRight(string(w.line), "\r ")
	w.line = w.line[:0]

	switch {
	case strings.HasPrefix(line, "FAIL") && strings.HasSuffix(line, "[build failed]"):
		w.res.buildFailed = true
	case strings.HasPrefix(line, "FAIL") && strings.HasSuffix(line, "[setup failed]"):
		w.res.setupFailed = true
	case strings.HasPrefix(line, "FAIL\t"):
		w.res.sawFail = true
	case strings.HasPrefix(line, "panic: test timed out after"):
		w.res.timedOut = true
	}
}

func (w *markerWriter) result() result { return w.res }
