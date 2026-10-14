// Package app holds Gomu's use cases as DCI contexts. Each context is a struct
// that is handed its collaborators, casts them in roles (small interfaces
// declared here), and runs one scenario. It knows nothing about go/parser,
// os/exec, or the file system.
package app

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"

	"github.com/pallat/gomu/internal/mutant"
	"github.com/pallat/gomu/internal/mutator"
)

// Errors shared by the contexts in this package.
var (
	// ErrInvalid means the context was not given all of its collaborators.
	ErrInvalid = errors.New("app: missing collaborator")
	// ErrSiteMismatch means two copies of the same file produced different
	// mutation sites, so results could not be matched up.
	ErrSiteMismatch = errors.New("app: mutation sites differ between file copies")
)

// Site is one mutation that can be applied to a File.
type Site struct {
	// Mutant describes the change; its Status is Pending.
	Mutant mutant.Mutant
	// Mutation changes the owning File in place and reverts it.
	Mutation mutator.Mutation
}

// File is the role of a parsed source file that one worker may mutate.
// Sites must come back in the same order for every copy of the same source.
type File interface {
	// Sites lists the mutations available in this copy of the file.
	Sites() []Site
	// Render returns the file's current source, including an applied mutation.
	Render() ([]byte, error)
}

// Overlay is the role of a private place where a mutated file is written so
// the test runner can use it instead of the original. One per worker.
type Overlay interface {
	// Write stores src as the replacement of the file at original and returns
	// the path the test runner needs.
	Write(original string, src []byte) (path string, err error)
	// Close removes whatever the overlay created.
	Close() error
}

// TestRunner is the role of whatever runs the tests of a package against an
// overlay and reports the verdict.
type TestRunner interface {
	Run(ctx context.Context, pkg, overlay string) (mutant.Status, error)
}

// Execute is the context that tests every mutant of one source file.
//
// Each worker gets its own File copy (mutations edit the AST in place) and its
// own Overlay, so workers share nothing but the results slice, in which each
// writes its own slots.
type Execute struct {
	// OpenFile parses a fresh, independent copy of the file.
	OpenFile func() (File, error)
	// NewOverlay creates a private Overlay for a worker.
	NewOverlay func() (Overlay, error)
	// Runner runs the tests.
	Runner TestRunner
	// Covered reports whether a position should be tested: false only if it
	// is known that no test executes it. Mutants at such positions are marked
	// NotCovered and never run. Nil means everything is covered.
	Covered func(mutant.Position) bool

	// Original is the absolute path of the file, exactly as the go tool sees it.
	Original string
	// Pkg is the package directory, relative to the module root.
	Pkg string
	// Workers is the number of parallel test runs. Less than 1 means GOMAXPROCS.
	Workers int
}

// Run tests every mutant and returns them in site order with their Status set.
// If it fails, it returns the first error together with the mutants as far
// as they were evaluated; the rest stay Pending.
func (e Execute) Run(ctx context.Context) ([]mutant.Mutant, error) {
	if e.OpenFile == nil || e.NewOverlay == nil || e.Runner == nil {
		return nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	first, err := e.OpenFile()
	if err != nil {
		return nil, fmt.Errorf("app: open file: %w", err)
	}
	sites := first.Sites()

	mutants := make([]mutant.Mutant, len(sites))
	queue := make([]int, 0, len(sites))
	for i, s := range sites {
		mutants[i] = s.Mutant
		if e.Covered != nil && !e.Covered(s.Mutant.Pos) {
			mutants[i].Status = mutant.NotCovered
			continue
		}
		queue = append(queue, i)
	}
	if len(queue) == 0 {
		return mutants, nil
	}

	workers := e.Workers
	if workers < 1 {
		workers = runtime.GOMAXPROCS(0)
	}
	workers = min(workers, len(queue))

	parent := ctx
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		wg       sync.WaitGroup
		once     sync.Once
		firstErr error
	)
	fail := func(err error) {
		once.Do(func() {
			firstErr = err
			cancel()
		})
	}

	jobs := make(chan int)
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()

			file := first
			if w > 0 {
				f, err := e.OpenFile()
				if err != nil {
					fail(fmt.Errorf("app: open file: %w", err))
					return
				}
				file = f
			}
			if err := e.work(ctx, file, mutants, jobs); err != nil {
				fail(err)
			}
		}()
	}

	// If every worker has stopped, nobody is left to take jobs; stop feeding
	// rather than block forever.
	workersDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(workersDone)
	}()

feed:
	for _, i := range queue {
		select {
		case jobs <- i:
		case <-ctx.Done():
			break feed
		case <-workersDone:
			break feed
		}
	}
	close(jobs)
	<-workersDone

	if firstErr != nil {
		return mutants, firstErr
	}
	if err := parent.Err(); err != nil {
		return mutants, err
	}
	return mutants, nil
}

// work is one worker: it takes site indexes from jobs until they run out or
// ctx ends, and records each verdict in mutants.
func (e Execute) work(ctx context.Context, file File, mutants []mutant.Mutant, jobs <-chan int) error {
	sites := file.Sites()
	if len(sites) != len(mutants) {
		return ErrSiteMismatch
	}
	for i, s := range sites {
		if s.Mutant.Pos != mutants[i].Pos {
			return ErrSiteMismatch
		}
	}

	overlay, err := e.NewOverlay()
	if err != nil {
		return fmt.Errorf("app: create overlay: %w", err)
	}
	// Close only removes temp files; there is nothing useful to do if it fails.
	defer func() { _ = overlay.Close() }()

	for i := range jobs {
		if ctx.Err() != nil {
			break // Run reports why the context ended
		}
		status, err := e.evaluate(ctx, file, sites[i], overlay)
		if err != nil {
			return err
		}
		mutants[i].Status = status
	}
	return nil
}

// evaluate applies one mutation, writes it to the overlay, and runs the tests.
func (e Execute) evaluate(ctx context.Context, file File, site Site, overlay Overlay) (mutant.Status, error) {
	src, err := render(file, site)
	if err != nil {
		return mutant.Pending, err
	}
	path, err := overlay.Write(e.Original, src)
	if err != nil {
		return mutant.Pending, fmt.Errorf("app: write overlay: %w", err)
	}
	status, err := e.Runner.Run(ctx, e.Pkg, path)
	if err != nil {
		return mutant.Pending, fmt.Errorf("app: run tests at %v: %w", site.Mutant.Pos, err)
	}
	return status, nil
}

// render returns the source with site applied and always reverts it, so the
// File is clean for the next mutation even when rendering fails.
func render(file File, site Site) ([]byte, error) {
	site.Mutation.Apply()
	defer site.Mutation.Revert()

	src, err := file.Render()
	if err != nil {
		return nil, fmt.Errorf("app: render %v: %w", site.Mutant.Pos, err)
	}
	return src, nil
}
