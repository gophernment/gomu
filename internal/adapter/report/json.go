package report

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/pallat/gomu/internal/mutant"
)

// JSON writes a machine-readable report, for CI and other tools.
//
// Like Text, it lists the survived mutants by default and every mutant when
// Verbose is set, in the same order. The document looks like this:
//
//	{
//	  "score": 91.1,          // percent, or null when no mutant was evaluated
//	  "evaluated": 269,       // killed + timed out + survived
//	  "summary": {"killed": 0, "survived": 0, "timedOut": 0,
//	              "notCovered": 0, "notViable": 0, "pending": 0},
//	  "mutants": [
//	    {"file": "a.go", "line": 4, "column": 11, "operator": "arithmetic",
//	     "original": "+", "replacement": "-", "status": "survived"}
//	  ]
//	}
//
// "mutants" is always an array. File names are relative to the module root.
// The "status" values are the names of mutant.Status: killed, survived,
// timed out, not covered, not viable, pending.
type JSON struct {
	Verbose bool
}

type jsonDoc struct {
	Score     *float64     `json:"score"`
	Evaluated int          `json:"evaluated"`
	Summary   jsonSummary  `json:"summary"`
	Mutants   []jsonMutant `json:"mutants"`
}

type jsonSummary struct {
	Killed     int `json:"killed"`
	Survived   int `json:"survived"`
	TimedOut   int `json:"timedOut"`
	NotCovered int `json:"notCovered"`
	NotViable  int `json:"notViable"`
	Pending    int `json:"pending"`
}

type jsonMutant struct {
	File        string `json:"file"`
	Line        int    `json:"line"`
	Column      int    `json:"column"`
	Operator    string `json:"operator"`
	Original    string `json:"original"`
	Replacement string `json:"replacement"`
	Status      string `json:"status"`
}

// Write renders mutants to w in a single Write call. It does not modify mutants.
func (j JSON) Write(w io.Writer, mutants []mutant.Mutant) error {
	if w == nil {
		return errors.New("report: nil writer")
	}

	sorted := slices.Clone(mutants)
	slices.SortStableFunc(sorted, compare)

	s := mutant.Summarize(mutants)
	doc := jsonDoc{
		Evaluated: s.Killed + s.TimedOut + s.Survived,
		Summary: jsonSummary{
			Killed: s.Killed, Survived: s.Survived, TimedOut: s.TimedOut,
			NotCovered: s.NotCovered, NotViable: s.NotViable, Pending: s.Pending,
		},
		Mutants: []jsonMutant{},
	}
	if doc.Evaluated > 0 {
		score := s.Score()
		doc.Score = &score
	}
	for _, mu := range sorted {
		if !j.Verbose && mu.Status != mutant.Survived {
			continue
		}
		doc.Mutants = append(doc.Mutants, jsonMutant{
			File: mu.Pos.File, Line: mu.Pos.Line, Column: mu.Pos.Column,
			Operator: mu.Operator, Original: mu.Original, Replacement: mu.Replacement,
			Status: mu.Status.String(),
		})
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // a terminal or CI log, not a web page: keep "<=" readable
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return fmt.Errorf("report: encode: %w", err)
	}
	return write(w, buf.String())
}
