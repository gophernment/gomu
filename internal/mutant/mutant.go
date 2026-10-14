// Package mutant holds the pure domain model of mutation testing:
// a Mutant, the Status of its test run, and the Summary score.
// It depends on the standard library only.
package mutant

import "strconv"

// Status is the outcome of running the tests against a Mutant.
// The zero value is Pending.
type Status int

// Mutant statuses.
const (
	// Pending means the mutant has not been evaluated yet.
	Pending Status = iota
	// Killed means the tests failed, so they detected the mutation.
	Killed
	// Survived means the tests passed, so they missed the mutation.
	Survived
	// TimedOut means the tests exceeded the time limit; it counts as detected.
	TimedOut
	// NotCovered means no test executes the mutated line.
	NotCovered
	// NotViable means the mutant does not compile, so it says nothing about the tests.
	NotViable
)

// String returns a human-readable name for s.
func (s Status) String() string {
	switch s {
	case Pending:
		return "pending"
	case Killed:
		return "killed"
	case Survived:
		return "survived"
	case TimedOut:
		return "timed out"
	case NotCovered:
		return "not covered"
	case NotViable:
		return "not viable"
	default:
		return "unknown"
	}
}

// Position is a location in a Go source file. Line and Column are 1-based.
type Position struct {
	File   string
	Line   int
	Column int
}

// String formats p as file:line:column.
func (p Position) String() string {
	return p.File + ":" + strconv.Itoa(p.Line) + ":" + strconv.Itoa(p.Column)
}

// Mutant is one small change to the source code.
type Mutant struct {
	// Operator is the name of the mutator that produced it, e.g. "arithmetic".
	Operator string
	// Pos is where the change is applied.
	Pos Position
	// Original and Replacement are the source text before and after the change.
	Original    string
	Replacement string
	// Status is the result of testing this mutant.
	Status Status
}

// Summary counts mutants per Status.
type Summary struct {
	Pending    int
	Killed     int
	Survived   int
	TimedOut   int
	NotCovered int
	NotViable  int
}

// Summarize counts the statuses of mutants. Unknown statuses are ignored.
func Summarize(mutants []Mutant) Summary {
	var s Summary
	for _, m := range mutants {
		switch m.Status {
		case Pending:
			s.Pending++
		case Killed:
			s.Killed++
		case Survived:
			s.Survived++
		case TimedOut:
			s.TimedOut++
		case NotCovered:
			s.NotCovered++
		case NotViable:
			s.NotViable++
		}
	}
	return s
}

// Score returns the mutation score as a percentage in [0, 100]:
// (killed + timed out) / (killed + timed out + survived).
// Pending, not-covered, and not-viable mutants are excluded.
// It returns 0 when no mutant was evaluated.
func (s Summary) Score() float64 {
	detected := s.Killed + s.TimedOut
	evaluated := detected + s.Survived
	if evaluated == 0 {
		return 0
	}
	return float64(detected) * 100 / float64(evaluated)
}
