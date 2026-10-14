package app_test

import (
	"context"
	"errors"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pallat/gomu/internal/app"
	"github.com/pallat/gomu/internal/mutant"
)

type fakeFinder struct {
	files []string
	err   error
	calls int
}

func (f *fakeFinder) Run(context.Context) ([]string, error) {
	f.calls++
	return f.files, f.err
}

type coverFunc func(mutant.Position) bool

func (c coverFunc) Covered(p mutant.Position) bool { return c(p) }

func allCovered(mutant.Position) bool { return true }

type fakeBaseline struct {
	cov      app.Coverage
	duration time.Duration
	err      error
	calls    int
}

func (b *fakeBaseline) Run(context.Context) (app.Coverage, time.Duration, error) {
	b.calls++
	return b.cov, b.duration, b.err
}

// analysis wires Analyze to one fake world per file and records what it was asked.
type analysis struct {
	finder   *fakeFinder
	baseline *fakeBaseline
	worlds   map[string]*world

	built    []string        // files NewExecute was called for, in order
	timeouts []time.Duration // timeouts it received
	buildErr map[string]error
}

func newAnalysis(sitesPerFile map[string]int, order ...string) *analysis {
	a := &analysis{
		finder:   &fakeFinder{files: order},
		baseline: &fakeBaseline{cov: coverFunc(allCovered), duration: time.Second},
		worlds:   map[string]*world{},
		buildErr: map[string]error{},
	}
	for f, n := range sitesPerFile {
		a.worlds[f] = newWorld(n)
	}
	return a
}

func (a *analysis) analyze(threshold float64) app.Analyze {
	return app.Analyze{
		Files:     a.finder,
		Baseline:  a.baseline,
		Threshold: threshold,
		NewExecute: func(file string, timeout time.Duration) (app.Execute, error) {
			a.built = append(a.built, file)
			a.timeouts = append(a.timeouts, timeout)
			if err := a.buildErr[file]; err != nil {
				return app.Execute{}, err
			}
			return a.worlds[file].execute(2), nil
		},
	}
}

func TestAnalyzeAggregatesFilesInOrder(t *testing.T) {
	t.Parallel()

	a := newAnalysis(map[string]int{"x.go": 3, "y.go": 4}, "x.go", "y.go")
	res, err := a.analyze(0).Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if len(res.Mutants) != 7 {
		t.Fatalf("got %d mutants, want 3 + 4", len(res.Mutants))
	}
	for i, m := range res.Mutants {
		site := i
		if i >= 3 {
			site = i - 3 // second file restarts at its own first site
		}
		if m.Status != wantStatus(site) {
			t.Errorf("mutant %d status = %v, want %v", i, m.Status, wantStatus(site))
		}
	}
	if !slices.Equal(a.built, []string{"x.go", "y.go"}) {
		t.Errorf("files built = %v, want discovery order", a.built)
	}
	if a.baseline.calls != 1 {
		t.Errorf("baseline ran %d times, want exactly 1", a.baseline.calls)
	}

	want := mutant.Summarize(res.Mutants)
	if res.Summary != want {
		t.Errorf("Summary = %+v, want %+v", res.Summary, want)
	}
	if res.Score != want.Score() {
		t.Errorf("Score = %v, want %v", res.Score, want.Score())
	}
	if !res.Passed {
		t.Error("Passed = false with no threshold")
	}
}

func TestAnalyzeUsesCoverageFromBaseline(t *testing.T) {
	t.Parallel()

	a := newAnalysis(map[string]int{"x.go": 6}, "x.go")
	a.baseline.cov = coverFunc(func(p mutant.Position) bool { return p.Line <= 2 })

	res, err := a.analyze(0).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary.NotCovered != 4 {
		t.Errorf("NotCovered = %d, want 4", res.Summary.NotCovered)
	}
	if n := a.worlds["x.go"].runs.Load(); n != 2 {
		t.Errorf("tests ran %d times, want 2 (uncovered mutants must not run)", n)
	}
}

func TestAnalyzeToleratesMissingCoverage(t *testing.T) {
	t.Parallel()

	a := newAnalysis(map[string]int{"x.go": 3}, "x.go")
	a.baseline.cov = nil

	res, err := a.analyze(0).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary.NotCovered != 0 || a.worlds["x.go"].runs.Load() != 3 {
		t.Errorf("nil coverage should mean everything is covered: %+v", res.Summary)
	}
}

func TestAnalyzeDerivesTimeoutFromBaseline(t *testing.T) {
	t.Parallel()

	tests := []struct {
		baseline time.Duration
		want     time.Duration
	}{
		{0, 10 * time.Second},
		{time.Second, 10 * time.Second}, // floor
		{3 * time.Second, 10 * time.Second},
		{4 * time.Second, 12 * time.Second}, // 3x
		{time.Minute, 3 * time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.baseline.String(), func(t *testing.T) {
			t.Parallel()

			a := newAnalysis(map[string]int{"x.go": 1}, "x.go")
			a.baseline.duration = tt.baseline

			if _, err := a.analyze(0).Run(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(a.timeouts) != 1 || a.timeouts[0] != tt.want {
				t.Errorf("timeout = %v, want %v", a.timeouts, tt.want)
			}
		})
	}
}

func TestAnalyzeThreshold(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		threshold float64
		verdict   func(src string) mutant.Status // nil: default 1/3 each
		covered   func(mutant.Position) bool
		want      bool
	}{
		{"no threshold always passes", 0, nil, nil, true},
		{
			"score above threshold", 60,
			func(string) mutant.Status { return mutant.Killed }, nil, true,
		},
		{
			"score exactly at threshold", 100,
			func(string) mutant.Status { return mutant.Killed }, nil, true,
		},
		{
			"score below threshold", 50,
			func(string) mutant.Status { return mutant.Survived }, nil, false,
		},
		{
			"timeouts count as detected", 100,
			func(string) mutant.Status { return mutant.TimedOut }, nil, true,
		},
		{
			"nothing evaluated cannot meet a threshold", 1, nil,
			func(mutant.Position) bool { return false }, false,
		},
		{
			"nothing evaluated without a threshold", 0, nil,
			func(mutant.Position) bool { return false }, true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := newAnalysis(map[string]int{"x.go": 3}, "x.go")
			if tt.verdict != nil {
				a.worlds["x.go"].runnerFn = func(_ context.Context, src string) (mutant.Status, error) {
					return tt.verdict(src), nil
				}
			}
			if tt.covered != nil {
				a.baseline.cov = coverFunc(tt.covered)
			}

			res, err := a.analyze(tt.threshold).Run(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if res.Passed != tt.want {
				t.Errorf("Passed = %v, want %v (score %.1f)", res.Passed, tt.want, res.Score)
			}
		})
	}
}

func TestAnalyzeWithNoFiles(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		threshold float64
		want      bool
	}{{0, true}, {80, false}} {
		a := newAnalysis(nil)
		res, err := a.analyze(tt.threshold).Run(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Mutants) != 0 || res.Passed != tt.want {
			t.Errorf("threshold %v: mutants=%d passed=%v, want 0 and %v", tt.threshold, len(res.Mutants), res.Passed, tt.want)
		}
		if a.baseline.calls != 0 || len(a.built) != 0 {
			t.Error("baseline or execution ran although there was nothing to mutate")
		}
	}
}

func TestAnalyzeErrors(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")

	t.Run("discovery fails", func(t *testing.T) {
		t.Parallel()

		a := newAnalysis(nil)
		a.finder.err = boom
		_, err := a.analyze(0).Run(context.Background())
		if !errors.Is(err, boom) {
			t.Errorf("error = %v, want %v", err, boom)
		}
		if a.baseline.calls != 0 {
			t.Error("baseline ran after discovery failed")
		}
	})

	t.Run("baseline fails", func(t *testing.T) {
		t.Parallel()

		a := newAnalysis(map[string]int{"x.go": 1}, "x.go")
		a.baseline.err = boom
		_, err := a.analyze(0).Run(context.Background())
		if !errors.Is(err, boom) {
			t.Errorf("error = %v, want %v", err, boom)
		}
		if len(a.built) != 0 {
			t.Error("mutants were tested although the baseline failed")
		}
	})

	t.Run("building a file fails", func(t *testing.T) {
		t.Parallel()

		a := newAnalysis(map[string]int{"x.go": 2, "y.go": 2}, "x.go", "y.go")
		a.buildErr["y.go"] = boom
		res, err := a.analyze(0).Run(context.Background())
		if !errors.Is(err, boom) || !strings.Contains(err.Error(), "y.go") {
			t.Errorf("error = %v, want %v naming y.go", err, boom)
		}
		if len(res.Mutants) != 2 {
			t.Errorf("kept %d mutants, want the 2 already tested in x.go", len(res.Mutants))
		}
		if res.Passed {
			t.Error("Passed = true for a failed analysis")
		}
	})

	t.Run("testing a file fails", func(t *testing.T) {
		t.Parallel()

		a := newAnalysis(map[string]int{"x.go": 2, "y.go": 2, "z.go": 2}, "x.go", "y.go", "z.go")
		a.worlds["y.go"].runnerFn = func(context.Context, string) (mutant.Status, error) {
			return mutant.Pending, boom
		}
		res, err := a.analyze(0).Run(context.Background())
		if !errors.Is(err, boom) || !strings.Contains(err.Error(), "y.go") {
			t.Errorf("error = %v, want %v naming y.go", err, boom)
		}
		if slices.Contains(a.built, "z.go") {
			t.Error("kept going after a file failed")
		}
		if res.Passed {
			t.Error("Passed = true for a failed analysis")
		}
	})
}

func TestAnalyzeContext(t *testing.T) {
	t.Parallel()

	t.Run("canceled before start", func(t *testing.T) {
		t.Parallel()

		a := newAnalysis(map[string]int{"x.go": 1}, "x.go")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		if _, err := a.analyze(0).Run(ctx); !errors.Is(err, context.Canceled) {
			t.Errorf("error = %v, want %v", err, context.Canceled)
		}
		if a.finder.calls != 0 {
			t.Error("work started although the context was already canceled")
		}
	})

	t.Run("canceled between files", func(t *testing.T) {
		t.Parallel()

		a := newAnalysis(map[string]int{"x.go": 2, "y.go": 2}, "x.go", "y.go")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		a.worlds["x.go"].runnerFn = func(_ context.Context, src string) (mutant.Status, error) {
			cancel()
			return statusFor(src), nil
		}

		_, err := a.analyze(0).Run(ctx)
		if !errors.Is(err, context.Canceled) {
			t.Errorf("error = %v, want %v", err, context.Canceled)
		}
		if slices.Contains(a.built, "y.go") {
			t.Error("started the next file after cancellation")
		}
	})
}

func TestAnalyzeRejectsBadSetup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		edit func(a *app.Analyze)
	}{
		{"no finder", func(a *app.Analyze) { a.Files = nil }},
		{"no baseline", func(a *app.Analyze) { a.Baseline = nil }},
		{"no NewExecute", func(a *app.Analyze) { a.NewExecute = nil }},
		{"negative threshold", func(a *app.Analyze) { a.Threshold = -1 }},
		{"threshold over 100", func(a *app.Analyze) { a.Threshold = 100.5 }},
		{"NaN threshold", func(a *app.Analyze) { a.Threshold = math.NaN() }},
		{"infinite threshold", func(a *app.Analyze) { a.Threshold = math.Inf(1) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := newAnalysis(map[string]int{"x.go": 1}, "x.go")
			an := a.analyze(0)
			tt.edit(&an)

			if _, err := an.Run(context.Background()); !errors.Is(err, app.ErrInvalid) {
				t.Errorf("error = %v, want %v", err, app.ErrInvalid)
			}
			if a.finder.calls != 0 {
				t.Error("work started with an invalid setup")
			}
		})
	}
}

func TestAnalyzeFailureNeverPassesEvenWithAGoodPartialScore(t *testing.T) {
	t.Parallel()

	a := newAnalysis(map[string]int{"x.go": 2, "y.go": 2}, "x.go", "y.go")
	// x.go: one killed, one survived = 50%, which meets the threshold.
	a.worlds["x.go"].runnerFn = func(_ context.Context, src string) (mutant.Status, error) {
		if src == "mut:0" {
			return mutant.Killed, nil
		}
		return mutant.Survived, nil
	}
	a.worlds["y.go"].runnerFn = func(context.Context, string) (mutant.Status, error) {
		return mutant.Pending, errors.New("boom")
	}

	res, err := a.analyze(50).Run(context.Background())
	if err == nil {
		t.Fatal("Run() returned nil error")
	}
	if res.Score != 50 {
		t.Fatalf("partial Score = %v, want 50 so the threshold would be met", res.Score)
	}
	if res.Passed {
		t.Error("a failed analysis passed because its partial score met the threshold")
	}
}

func TestAnalyzeThresholdCountsKilledAndTimedOutTogether(t *testing.T) {
	t.Parallel()

	a := newAnalysis(map[string]int{"x.go": 2}, "x.go")
	a.worlds["x.go"].runnerFn = func(_ context.Context, src string) (mutant.Status, error) {
		if src == "mut:0" {
			return mutant.Killed, nil
		}
		return mutant.TimedOut, nil
	}

	res, err := a.analyze(100).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Passed || res.Score != 100 {
		t.Errorf("Passed=%v Score=%v, want a pass at 100", res.Passed, res.Score)
	}
}
