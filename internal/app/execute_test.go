package app_test

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pallat/gomu/internal/app"
	"github.com/pallat/gomu/internal/mutant"
	"github.com/pallat/gomu/internal/mutator"
)

const (
	original = "/mod/pkg/a.go"
	pkgDir   = "pkg"
)

// world holds the fakes of one test and counts what the context did to them.
type world struct {
	sites int

	opens, applies, reverts, closes, runs atomic.Int32

	mu        sync.Mutex
	written   map[string]string // overlay path -> last source written
	originals map[string]bool   // original paths passed to Overlay.Write
	pkgs      map[string]bool   // package dirs passed to the runner
	ran       map[string]int    // source -> times the runner saw it

	openErr    func(n int) error // n is the 1-based call number
	renderErr  int               // site index whose Render fails, -1 for none
	overlayErr error
	runnerFn   func(ctx context.Context, src string) (mutant.Status, error)
}

func newWorld(sites int) *world {
	return &world{
		sites:     sites,
		written:   map[string]string{},
		originals: map[string]bool{},
		pkgs:      map[string]bool{},
		ran:       map[string]int{},
		renderErr: -1,
		runnerFn: func(_ context.Context, src string) (mutant.Status, error) {
			return statusFor(src), nil
		},
	}
}

// statusFor gives a stable verdict per mutation index so results can be checked.
func statusFor(src string) mutant.Status {
	i, err := strconv.Atoi(strings.TrimPrefix(src, "mut:"))
	if err != nil {
		return mutant.Pending
	}
	return []mutant.Status{mutant.Killed, mutant.Survived, mutant.TimedOut}[i%3]
}

func wantStatus(i int) mutant.Status { return statusFor("mut:" + strconv.Itoa(i)) }

type fakeFile struct {
	w       *world
	applied int
	sites   []app.Site
}

func (w *world) openFile() (app.File, error) {
	n := int(w.opens.Add(1))
	if w.openErr != nil {
		if err := w.openErr(n); err != nil {
			return nil, err
		}
	}

	f := &fakeFile{w: w, applied: -1}
	for i := range w.sites {
		f.sites = append(f.sites, app.Site{
			Mutant: mutant.Mutant{
				Operator:    "fake",
				Pos:         mutant.Position{File: "pkg/a.go", Line: i + 1, Column: 1},
				Original:    "o",
				Replacement: "r",
			},
			Mutation: mutator.Mutation{
				Apply: func() {
					if f.applied != -1 {
						panic("Apply while another mutation is applied")
					}
					f.applied = i
					w.applies.Add(1)
				},
				Revert: func() {
					f.applied = -1
					w.reverts.Add(1)
				},
			},
		})
	}
	return f, nil
}

func (f *fakeFile) Sites() []app.Site { return f.sites }

func (f *fakeFile) Render() ([]byte, error) {
	if f.applied == f.w.renderErr {
		return nil, errors.New("render failed")
	}
	if f.applied < 0 {
		return []byte("orig"), nil
	}
	return []byte("mut:" + strconv.Itoa(f.applied)), nil
}

type fakeOverlay struct {
	w  *world
	id int
}

func (w *world) newOverlay() (app.Overlay, error) {
	if w.overlayErr != nil {
		return nil, w.overlayErr
	}
	return &fakeOverlay{w: w, id: int(w.opens.Load())*1000 + int(w.closes.Load())}, nil
}

func (o *fakeOverlay) Write(path string, src []byte) (string, error) {
	o.w.mu.Lock()
	defer o.w.mu.Unlock()

	name := fmt.Sprintf("overlay-%p.json", o)
	o.w.written[name] = string(src)
	o.w.originals[path] = true
	return name, nil
}

func (o *fakeOverlay) Close() error {
	o.w.closes.Add(1)
	return nil
}

type fakeRunner struct{ w *world }

func (r fakeRunner) Run(ctx context.Context, pkg, overlay string) (mutant.Status, error) {
	r.w.runs.Add(1)

	r.w.mu.Lock()
	src := r.w.written[overlay]
	r.w.pkgs[pkg] = true
	r.w.ran[src]++
	r.w.mu.Unlock()

	return r.w.runnerFn(ctx, src)
}

func (w *world) execute(workers int) app.Execute {
	return app.Execute{
		OpenFile:   w.openFile,
		NewOverlay: w.newOverlay,
		Runner:     fakeRunner{w},
		Original:   original,
		Pkg:        pkgDir,
		Workers:    workers,
	}
}

func TestRunEvaluatesEverySiteInOrder(t *testing.T) {
	t.Parallel()

	for _, workers := range []int{1, 2, 4, 100, 0, -3} {
		t.Run(fmt.Sprintf("workers=%d", workers), func(t *testing.T) {
			t.Parallel()

			w := newWorld(25)
			got, err := w.execute(workers).Run(context.Background())
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if len(got) != 25 {
				t.Fatalf("got %d mutants, want 25", len(got))
			}
			for i, m := range got {
				if m.Pos.Line != i+1 {
					t.Errorf("mutant %d is at line %d; results are out of site order", i, m.Pos.Line)
				}
				if m.Status != wantStatus(i) {
					t.Errorf("mutant %d status = %v, want %v", i, m.Status, wantStatus(i))
				}
				if m.Operator != "fake" || m.Original != "o" || m.Replacement != "r" {
					t.Errorf("mutant %d lost its description: %+v", i, m)
				}
			}

			if n := w.runs.Load(); n != 25 {
				t.Errorf("runner called %d times, want 25 (once per mutant)", n)
			}
			for i := range 25 {
				if c := w.ran["mut:"+strconv.Itoa(i)]; c != 1 {
					t.Errorf("mutation %d ran %d times, want 1", i, c)
				}
			}
			if a, r := w.applies.Load(), w.reverts.Load(); a != 25 || r != 25 {
				t.Errorf("applies/reverts = %d/%d, want 25/25", a, r)
			}
			if o, c := w.opens.Load(), w.closes.Load(); c == 0 || int(c) > int(o) {
				t.Errorf("opens=%d closes=%d; every overlay must be closed", o, c)
			}
		})
	}
}

func TestRunPassesPathsToCollaborators(t *testing.T) {
	t.Parallel()

	w := newWorld(3)
	if _, err := w.execute(2).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(w.originals) != 1 || !w.originals[original] {
		t.Errorf("overlay keyed by %v, want only %q", w.originals, original)
	}
	if len(w.pkgs) != 1 || !w.pkgs[pkgDir] {
		t.Errorf("runner got packages %v, want only %q", w.pkgs, pkgDir)
	}
}

func TestRunSkipsUncoveredMutants(t *testing.T) {
	t.Parallel()

	w := newWorld(6)
	e := w.execute(3)
	e.Covered = func(p mutant.Position) bool { return p.Line%2 == 0 } // lines 2,4,6

	got, err := e.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	for i, m := range got {
		if (i+1)%2 == 0 {
			if m.Status != wantStatus(i) {
				t.Errorf("covered mutant %d status = %v, want %v", i, m.Status, wantStatus(i))
			}
		} else if m.Status != mutant.NotCovered {
			t.Errorf("uncovered mutant %d status = %v, want %v", i, m.Status, mutant.NotCovered)
		}
	}
	if n := w.runs.Load(); n != 3 {
		t.Errorf("runner called %d times, want 3 (uncovered mutants must not run)", n)
	}
}

func TestRunWithNothingToRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		sites   int
		covered func(mutant.Position) bool
		want    int
	}{
		{"no sites", 0, nil, 0},
		{"nothing covered", 4, func(mutant.Position) bool { return false }, 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			w := newWorld(tt.sites)
			e := w.execute(4)
			e.Covered = tt.covered

			got, err := e.Run(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != tt.want {
				t.Errorf("got %d mutants, want %d", len(got), tt.want)
			}
			if w.runs.Load() != 0 || w.closes.Load() != 0 {
				t.Errorf("runs=%d closes=%d, want no work and no overlays", w.runs.Load(), w.closes.Load())
			}
		})
	}
}

func TestRunCapsWorkersAtQueueSize(t *testing.T) {
	t.Parallel()

	w := newWorld(3)
	if _, err := w.execute(64).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := w.opens.Load(); n > 3 {
		t.Errorf("opened %d files for 3 mutants", n)
	}
}

func TestRunRevertsWhenRenderFails(t *testing.T) {
	t.Parallel()

	w := newWorld(4)
	w.renderErr = 1
	got, err := w.execute(1).Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "render") {
		t.Fatalf("Run() error = %v, want a render error", err)
	}
	if a, r := w.applies.Load(), w.reverts.Load(); a != r {
		t.Errorf("applies=%d reverts=%d; a failed render must still revert", a, r)
	}
	if got[0].Status != wantStatus(0) {
		t.Errorf("partial result lost: mutant 0 status = %v", got[0].Status)
	}
}

func TestRunStopsOnRunnerError(t *testing.T) {
	t.Parallel()

	boom := errors.New("go test exploded")
	w := newWorld(50)
	w.runnerFn = func(_ context.Context, src string) (mutant.Status, error) {
		if src == "mut:2" {
			return mutant.Pending, boom
		}
		return statusFor(src), nil
	}

	_, err := w.execute(1).Run(context.Background())
	if !errors.Is(err, boom) {
		t.Fatalf("Run() error = %v, want %v", err, boom)
	}
	if n := w.runs.Load(); n != 3 {
		t.Errorf("runner called %d times, want 3 (stop at the failure)", n)
	}
	if w.closes.Load() != 1 {
		t.Errorf("overlay closed %d times, want 1", w.closes.Load())
	}
}

func TestRunKeepsFirstErrorWhenWorkersAreCanceled(t *testing.T) {
	t.Parallel()

	boom := errors.New("first failure")
	w := newWorld(40)
	w.runnerFn = func(ctx context.Context, src string) (mutant.Status, error) {
		if src == "mut:0" {
			return mutant.Pending, boom
		}
		return mutant.Pending, waitForCancel(ctx) // the others only stop because of the failure
	}

	_, err := w.execute(4).Run(context.Background())
	if !errors.Is(err, boom) {
		t.Errorf("Run() error = %v, want the first failure %v", err, boom)
	}
}

func TestRunContext(t *testing.T) {
	t.Parallel()

	t.Run("canceled before start", func(t *testing.T) {
		t.Parallel()

		w := newWorld(5)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		if _, err := w.execute(2).Run(ctx); !errors.Is(err, context.Canceled) {
			t.Errorf("Run() error = %v, want %v", err, context.Canceled)
		}
		if w.opens.Load() != 0 || w.runs.Load() != 0 {
			t.Error("work started although the context was already canceled")
		}
	})

	t.Run("canceled while running", func(t *testing.T) {
		t.Parallel()

		w := newWorld(40)
		started := make(chan struct{}, 40)
		w.runnerFn = func(ctx context.Context, _ string) (mutant.Status, error) {
			started <- struct{}{}
			return mutant.Pending, waitForCancel(ctx)
		}

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			_, err := w.execute(4).Run(ctx)
			done <- err
		}()

		<-started
		cancel()

		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("Run() error = %v, want %v", err, context.Canceled)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("Run() did not return after cancellation")
		}
		if o, c := w.opens.Load(), w.closes.Load(); c == 0 || int(c) > int(o) {
			t.Errorf("opens=%d closes=%d; overlays must be closed on cancel", o, c)
		}
	})
}

func TestRunSetupErrors(t *testing.T) {
	t.Parallel()

	boom := errors.New("setup failed")

	tests := []struct {
		name  string
		setup func(w *world)
		check func(t *testing.T, w *world)
	}{
		{
			name:  "first open fails",
			setup: func(w *world) { w.openErr = func(int) error { return boom } },
			check: func(t *testing.T, w *world) {
				if w.runs.Load() != 0 {
					t.Error("runner called after the file failed to open")
				}
			},
		},
		{
			name: "a later worker cannot open its file",
			setup: func(w *world) {
				w.openErr = func(n int) error {
					if n == 2 {
						return boom
					}
					return nil
				}
			},
		},
		{
			name:  "overlay cannot be created",
			setup: func(w *world) { w.overlayErr = boom },
			check: func(t *testing.T, w *world) {
				if w.runs.Load() != 0 {
					t.Error("runner called without an overlay")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			w := newWorld(10)
			tt.setup(w)

			_, err := w.execute(3).Run(context.Background())
			if !errors.Is(err, boom) {
				t.Fatalf("Run() error = %v, want %v", err, boom)
			}
			if tt.check != nil {
				tt.check(t, w)
			}
		})
	}
}

func TestRunDetectsFilesThatDisagree(t *testing.T) {
	t.Parallel()

	w := newWorld(5)
	e := w.execute(2)

	var n atomic.Int32
	e.OpenFile = func() (app.File, error) {
		f, _ := w.openFile()
		if n.Add(1) == 1 {
			return f, nil
		}
		ff := f.(*fakeFile)
		ff.sites = ff.sites[:3] // the second copy differs
		return ff, nil
	}

	if _, err := e.Run(context.Background()); !errors.Is(err, app.ErrSiteMismatch) {
		t.Errorf("Run() error = %v, want %v", err, app.ErrSiteMismatch)
	}
}

func TestRunRejectsIncompleteSetup(t *testing.T) {
	t.Parallel()

	w := newWorld(1)
	tests := []struct {
		name string
		edit func(e *app.Execute)
	}{
		{"no OpenFile", func(e *app.Execute) { e.OpenFile = nil }},
		{"no NewOverlay", func(e *app.Execute) { e.NewOverlay = nil }},
		{"no Runner", func(e *app.Execute) { e.Runner = nil }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e := w.execute(1)
			tt.edit(&e)
			if _, err := e.Run(context.Background()); !errors.Is(err, app.ErrInvalid) {
				t.Errorf("Run() error = %v, want %v", err, app.ErrInvalid)
			}
		})
	}
}

func TestRunIsRepeatable(t *testing.T) {
	t.Parallel()

	w := newWorld(7)
	e := w.execute(3)

	first, err := e.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("mutant %d differs between runs: %+v vs %+v", i, first[i], second[i])
		}
	}
}

func TestRunReportsCancellationEvenWhenAllWorkIsDone(t *testing.T) {
	t.Parallel()

	w := newWorld(1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.runnerFn = func(_ context.Context, src string) (mutant.Status, error) {
		cancel() // the only job finishes normally, but the caller has given up
		return statusFor(src), nil
	}

	got, err := w.execute(1).Run(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Run() error = %v, want %v", err, context.Canceled)
	}
	if len(got) != 1 || got[0].Status != wantStatus(0) {
		t.Errorf("the finished result was lost: %+v", got)
	}
}

// waitForCancel blocks until ctx ends. It gives up after a while, so a bug in
// the code under test fails the test quickly instead of hanging it.
func waitForCancel(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(5 * time.Second):
		return errors.New("the context was never canceled")
	}
}
