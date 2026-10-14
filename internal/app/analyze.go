package app

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/pallat/gomu/internal/mutant"
)

const (
	// timeoutFactor scales the baseline duration into the per-mutant limit.
	// The baseline covers the whole module and a run covers one package, so
	// this is generous on purpose: a false timeout would hide a weak test.
	timeoutFactor = 3
	// minTimeout is the lowest per-mutant limit, so a fast baseline does not
	// make the limit tighter than process start-up and compilation noise.
	minTimeout = 10 * time.Second
)

// Finder is the role of whatever lists the files to mutate, as paths relative
// to the module root. Discover plays it.
type Finder interface {
	Run(ctx context.Context) ([]string, error)
}

// Coverage is the role of the coverage data from the baseline run.
type Coverage interface {
	// Covered reports whether any test executes pos. pos.File is relative to
	// the module root.
	Covered(pos mutant.Position) bool
}

// Baseline is the role of the unmodified test run. It returns coverage, and
// how long the run took so the per-mutant timeout can be derived. It must fail
// if the tests do not pass: mutating code with failing tests proves nothing.
type Baseline interface {
	Run(ctx context.Context) (Coverage, time.Duration, error)
}

// Result is the outcome of Analyze.
type Result struct {
	// Mutants lists every mutant in file order, then site order.
	Mutants []mutant.Mutant
	// Summary counts Mutants per status.
	Summary mutant.Summary
	// Score is the mutation score in percent.
	Score float64
	// Passed is true when the score meets the threshold. A failed analysis is
	// never Passed.
	Passed bool
}

// Analyze is the context for the whole scenario: find files, run the baseline
// once, then test every mutant of every file and judge the score.
type Analyze struct {
	// Files lists the files to mutate.
	Files Finder
	// Baseline runs the unmodified tests.
	Baseline Baseline
	// NewExecute builds the Execute context for one file with the given
	// per-mutant timeout. Analyze sets its Covered field from the baseline.
	NewExecute func(file string, timeout time.Duration) (Execute, error)
	// Threshold is the minimum score in percent, in [0, 100]. Zero disables
	// the check. With a threshold, a run that evaluated no mutant fails: no
	// evidence is not a pass.
	Threshold float64
}

// Run performs the analysis. On error it returns the mutants tested so far
// (never Passed) together with the error.
func (a Analyze) Run(ctx context.Context) (Result, error) {
	if a.Files == nil || a.Baseline == nil || a.NewExecute == nil {
		return Result{}, ErrInvalid
	}
	if math.IsNaN(a.Threshold) || a.Threshold < 0 || a.Threshold > 100 {
		return Result{}, fmt.Errorf("%w: threshold must be within 0..100", ErrInvalid)
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	files, err := a.Files.Run(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("app: find files: %w", err)
	}
	if len(files) == 0 {
		return a.result(nil, nil), nil
	}

	cov, took, err := a.Baseline.Run(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("app: baseline: %w", err)
	}
	timeout := max(took*timeoutFactor, minTimeout)

	var all []mutant.Mutant
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return a.result(all, err), err
		}

		ex, err := a.NewExecute(file, timeout)
		if err != nil {
			err = fmt.Errorf("app: prepare %s: %w", file, err)
			return a.result(all, err), err
		}
		if cov != nil {
			ex.Covered = cov.Covered
		}

		mutants, err := ex.Run(ctx)
		all = append(all, mutants...)
		if err != nil {
			err = fmt.Errorf("app: test %s: %w", file, err)
			return a.result(all, err), err
		}
	}
	return a.result(all, nil), nil
}

// result summarizes mutants. Any non-nil failure means the run did not pass.
func (a Analyze) result(mutants []mutant.Mutant, failure error) Result {
	s := mutant.Summarize(mutants)

	// With nothing evaluated Score is 0, so any threshold above 0 fails: no
	// evidence is not a pass. A threshold of 0 is always met.
	passed := failure == nil && s.Score() >= a.Threshold
	return Result{Mutants: mutants, Summary: s, Score: s.Score(), Passed: passed}
}
