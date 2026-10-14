// Package report writes mutation results for people and tools to read.
package report

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode"

	"github.com/pallat/gomu/internal/mutant"
)

// Text writes a plain-text report.
//
// By default it lists the survived mutants, the ones that need attention, then
// a summary. Verbose lists every mutant. Output is ordered by file, line,
// column, then operator, so equal input always gives equal output.
//
// Names and code fragments are escaped when they contain control characters,
// so a hostile file name cannot inject terminal escape sequences.
type Text struct {
	Verbose bool
}

// Write renders mutants to w in a single Write call. It does not modify mutants.
func (t Text) Write(w io.Writer, mutants []mutant.Mutant) error {
	if w == nil {
		return errors.New("report: nil writer")
	}
	if len(mutants) == 0 {
		return write(w, "No mutants found.\n")
	}

	sorted := slices.Clone(mutants)
	slices.SortStableFunc(sorted, compare)

	var b strings.Builder
	listed := 0
	for _, mu := range sorted {
		if !t.Verbose && mu.Status != mutant.Survived {
			continue
		}
		listed++
		fmt.Fprintf(&b, "%-11s %s:%d:%d  %-12s %s -> %s\n",
			strings.ToUpper(mu.Status.String()),
			clean(mu.Pos.File), mu.Pos.Line, mu.Pos.Column,
			clean(mu.Operator), clean(mu.Original), clean(mu.Replacement))
	}
	if listed > 0 {
		b.WriteByte('\n')
	}

	s := mutant.Summarize(mutants)
	if evaluated := s.Killed + s.TimedOut + s.Survived; evaluated == 0 {
		b.WriteString("Mutation score: n/a (no mutants were evaluated)\n")
	} else {
		fmt.Fprintf(&b, "Mutation score: %.1f%% (%d of %d evaluated mutants detected)\n",
			s.Score(), s.Killed+s.TimedOut, evaluated)
	}

	fmt.Fprintf(&b, "Killed %d | Survived %d | Timed out %d | Not covered %d | Not viable %d",
		s.Killed, s.Survived, s.TimedOut, s.NotCovered, s.NotViable)
	if s.Pending > 0 {
		fmt.Fprintf(&b, " | Pending %d", s.Pending)
	}
	b.WriteByte('\n')

	return write(w, b.String())
}

func write(w io.Writer, s string) error {
	if _, err := io.WriteString(w, s); err != nil {
		return fmt.Errorf("report: write: %w", err)
	}
	return nil
}

// compare orders mutants by file, line, column, then operator.
func compare(a, b mutant.Mutant) int {
	if c := strings.Compare(a.Pos.File, b.Pos.File); c != 0 {
		return c
	}
	if c := a.Pos.Line - b.Pos.Line; c != 0 {
		return c
	}
	if c := a.Pos.Column - b.Pos.Column; c != 0 {
		return c
	}
	return strings.Compare(a.Operator, b.Operator)
}

// clean escapes every rune that is not printable, using \xNN, \uNNNN, or
// \UNNNNNNNN. Printable text, including non-ASCII letters, is kept as is.
func clean(s string) string {
	if !strings.ContainsFunc(s, func(r rune) bool { return !unicode.IsPrint(r) }) {
		return s
	}

	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsPrint(r):
			b.WriteRune(r)
		case r < 0x100:
			fmt.Fprintf(&b, `\x%02x`, r)
		case r < 0x10000:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			fmt.Fprintf(&b, `\U%08x`, r)
		}
	}
	return b.String()
}
