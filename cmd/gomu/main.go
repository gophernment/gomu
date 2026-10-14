// Command gomu runs mutation testing on a Go module.
//
// It changes your code in small ways (a + becomes a -, a < becomes a <=) and
// runs the tests on each change. A mutant the tests still pass on has survived:
// your tests did not notice the change.
//
// This file only wires adapters to the use cases in internal/app; it holds no
// mutation logic of its own.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"io"
	"math"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pallat/gomu/internal/adapter/cover"
	"github.com/pallat/gomu/internal/adapter/fsys"
	"github.com/pallat/gomu/internal/adapter/goast"
	"github.com/pallat/gomu/internal/adapter/gotest"
	"github.com/pallat/gomu/internal/adapter/report"
	"github.com/pallat/gomu/internal/app"
	"github.com/pallat/gomu/internal/mutant"
	"github.com/pallat/gomu/internal/mutator"
)

// Exit codes.
const (
	exitOK             = 0
	exitBelowThreshold = 1
	exitError          = 2
)

// maxGoMod is the most bytes read from go.mod.
const maxGoMod = 1 << 20

// errUsage means the flag package already told the user what was wrong.
var errUsage = errors.New("usage")

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

type config struct {
	dir       string
	patterns  []string
	threshold float64
	workers   int
	verbose   bool
}

func parseArgs(args []string, stderr io.Writer) (config, error) {
	var cfg config

	fl := flag.NewFlagSet("gomu", flag.ContinueOnError)
	fl.SetOutput(stderr)
	fl.StringVar(&cfg.dir, "dir", ".", "module `directory` (must contain go.mod)")
	fl.Float64Var(&cfg.threshold, "threshold", 0, "minimum mutation score in `percent` (0-100); exit 1 below it. 0 disables")
	fl.IntVar(&cfg.workers, "workers", 0, "parallel test runs per file; 0 means GOMAXPROCS")
	fl.BoolVar(&cfg.verbose, "v", false, "list every mutant, not only the survivors")
	fl.Usage = func() {
		fmt.Fprint(stderr, `usage: gomu [flags] [pattern ...]

Patterns select the files to mutate, relative to -dir:
  pkg/file.go   one file
  pkg           the files directly in a directory
  pkg/...       a directory and everything below it
  ./...         the whole module (default)

Test files, vendor, testdata, and hidden directories are skipped.
Exit codes: 0 ok, 1 score below -threshold, 2 error.

flags:
`)
		fl.PrintDefaults()
	}

	if err := fl.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return config{}, err
		}
		return config{}, errUsage
	}

	cfg.patterns = fl.Args()
	if len(cfg.patterns) == 0 {
		cfg.patterns = []string{"./..."}
	}
	if math.IsNaN(cfg.threshold) || cfg.threshold < 0 || cfg.threshold > 100 {
		return config{}, errors.New("-threshold must be between 0 and 100")
	}
	if cfg.workers < 0 {
		return config{}, errors.New("-workers must not be negative")
	}
	return cfg, nil
}

// run is main without the process: it returns the exit code.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	cfg, err := parseArgs(args, stderr)
	switch {
	case errors.Is(err, flag.ErrHelp):
		return exitOK
	case errors.Is(err, errUsage):
		return exitError
	case err != nil:
		fmt.Fprintln(stderr, "gomu:", err)
		return exitError
	}

	res, err := analyze(ctx, cfg)
	if err != nil {
		fmt.Fprintln(stderr, "gomu:", err)
		return exitError
	}

	if err := (report.Text{Verbose: cfg.verbose}).Write(stdout, res.Mutants); err != nil {
		fmt.Fprintln(stderr, "gomu:", err)
		return exitError
	}
	if !res.Passed {
		return exitBelowThreshold
	}
	return exitOK
}

// analyze wires the adapters into the Analyze context and runs it.
func analyze(ctx context.Context, cfg config) (app.Result, error) {
	root, err := moduleRoot(cfg.dir)
	if err != nil {
		return app.Result{}, err
	}
	mod, err := readModulePath(root)
	if err != nil {
		return app.Result{}, err
	}

	var reg mutator.Registry
	for _, m := range []mutator.Mutator{mutator.Arithmetic{}, mutator.Conditional{}, mutator.Logical{}} {
		if err := reg.Register(m); err != nil {
			return app.Result{}, err
		}
	}
	mutators := reg.All()

	// The baseline does not use the per-mutant timeout, so any value will do.
	baselineRunner, err := gotest.NewRunner(root, time.Minute)
	if err != nil {
		return app.Result{}, err
	}

	return app.Analyze{
		Files:     app.Discover{FS: os.DirFS(root), Patterns: cfg.patterns},
		Baseline:  baseline{runner: baselineRunner, module: mod},
		Threshold: cfg.threshold,
		NewExecute: func(file string, timeout time.Duration) (app.Execute, error) {
			runner, err := gotest.NewRunner(root, timeout)
			if err != nil {
				return app.Execute{}, err
			}
			return app.Execute{
				OpenFile: func() (app.File, error) { return openFile(root, file, mutators) },
				NewOverlay: func() (app.Overlay, error) {
					o, err := fsys.NewOverlay()
					if err != nil {
						return nil, err // not wrapped in the interface: avoids a typed nil
					}
					return o, nil
				},
				Runner:   testRunner{runner},
				Original: filepath.Join(root, filepath.FromSlash(file)),
				Pkg:      path.Dir(file),
				Workers:  cfg.workers,
			}, nil
		},
	}.Run(ctx)
}

// moduleRoot returns dir as an absolute, symlink-free path holding a go.mod.
func moduleRoot(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err == nil {
		abs, err = filepath.EvalSymlinks(abs)
	}
	if err != nil {
		return "", fmt.Errorf("module directory: %w", unwrapPathError(err))
	}
	info, err := os.Stat(filepath.Join(abs, "go.mod"))
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("no go.mod in the module directory; use -dir to point at the module root")
	}
	return abs, nil
}

func unwrapPathError(err error) error {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}

func readModulePath(root string) (string, error) {
	f, err := os.Open(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("read go.mod: %w", unwrapPathError(err))
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxGoMod+1))
	if err != nil {
		return "", fmt.Errorf("read go.mod: %w", unwrapPathError(err))
	}
	if len(data) > maxGoMod {
		return "", errors.New("go.mod is too large")
	}
	return modulePath(data)
}

// modulePath extracts the path from the module directive of go.mod data.
func modulePath(data []byte) (string, error) {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		rest, ok := strings.CutPrefix(line, "module")
		if !ok || rest == "" || (rest[0] != ' ' && rest[0] != '\t') {
			continue
		}
		if i := strings.Index(rest, "//"); i >= 0 {
			rest = rest[:i]
		}
		p := strings.TrimSpace(rest)
		if len(p) >= 2 && (p[0] == '"' || p[0] == '`') {
			unquoted, err := strconv.Unquote(p)
			if err != nil {
				return "", errors.New("go.mod: malformed module path")
			}
			p = unquoted
		}
		if p == "" || strings.ContainsAny(p, " \t\r") {
			return "", errors.New("go.mod: malformed module path")
		}
		return p, nil
	}
	return "", errors.New("go.mod: no module directive")
}

// testRunner lets gotest.Runner play the app.TestRunner role.
type testRunner struct{ r *gotest.Runner }

func (t testRunner) Run(ctx context.Context, pkg, overlay string) (mutant.Status, error) {
	return t.r.Run(ctx, gotest.Job{Pkg: pkg, Overlay: overlay})
}

// baseline plays app.Baseline: it runs the module's tests once and turns the
// coverage profile into a Coverage.
type baseline struct {
	runner *gotest.Runner
	module string
}

func (b baseline) Run(ctx context.Context) (app.Coverage, time.Duration, error) {
	dir, err := os.MkdirTemp("", "gomu-cover-*")
	if err == nil {
		dir, err = filepath.Abs(dir)
	}
	if err != nil {
		return nil, 0, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	profile := filepath.Join(dir, "cover.out")
	took, err := b.runner.Baseline(ctx, profile)
	if err != nil {
		if errors.Is(err, gotest.ErrBaselineFailed) {
			return nil, 0, errors.New("the tests fail before any change; fix them first (go test ./...)")
		}
		return nil, 0, err
	}

	f, err := os.Open(profile)
	if err != nil {
		return nil, 0, fmt.Errorf("read coverage profile: %w", unwrapPathError(err))
	}
	defer f.Close()

	p, err := cover.Parse(f)
	if err != nil {
		return nil, 0, err
	}
	return coverage{module: b.module, profile: p}, took, nil
}

// coverage plays app.Coverage. Profile names are import path plus file name.
type coverage struct {
	module  string
	profile *cover.Profile
}

func (c coverage) Covered(pos mutant.Position) bool {
	return c.profile.Covered(c.module+"/"+pos.File, pos.Line, pos.Column)
}

// sourceFile plays app.File on top of a parsed goast.File.
type sourceFile struct {
	file  *goast.File
	sites []app.Site
}

func openFile(root, rel string, mutators []mutator.Mutator) (app.File, error) {
	f, err := goast.Parse(root, rel)
	if err != nil {
		return nil, err
	}
	found := f.Sites(mutators)

	sites := make([]app.Site, len(found))
	for i, s := range found {
		sites[i] = app.Site{Mutant: s.Mutant, Mutation: s.Mutation}
	}
	return &sourceFile{file: f, sites: sites}, nil
}

func (s *sourceFile) Sites() []app.Site { return s.sites }

func (s *sourceFile) Render() ([]byte, error) {
	var buf bytes.Buffer
	if err := format.Node(&buf, s.file.Fset, s.file.AST); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
