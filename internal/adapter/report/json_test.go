package report_test

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/pallat/gomu/internal/adapter/report"
	"github.com/pallat/gomu/internal/mutant"
)

type jsonReport struct {
	Score     *float64 `json:"score"`
	Evaluated int      `json:"evaluated"`
	Summary   struct {
		Killed     int `json:"killed"`
		Survived   int `json:"survived"`
		TimedOut   int `json:"timedOut"`
		NotCovered int `json:"notCovered"`
		NotViable  int `json:"notViable"`
		Pending    int `json:"pending"`
	} `json:"summary"`
	Mutants []jsonMutant `json:"mutants"`
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

func renderJSON(t *testing.T, r report.JSON, mutants []mutant.Mutant) (string, jsonReport) {
	t.Helper()

	var buf bytes.Buffer
	if err := r.Write(&buf, mutants); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	var got jsonReport
	dec := json.NewDecoder(bytes.NewReader(buf.Bytes()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("output is not the documented JSON: %v\n%s", err, buf.String())
	}
	return buf.String(), got
}

func TestJSONDefaultListsOnlySurvivors(t *testing.T) {
	t.Parallel()

	_, got := renderJSON(t, report.JSON{}, []mutant.Mutant{
		m("a.go", 3, 7, "conditional", "<", "<=", mutant.Killed),
		m("a.go", 4, 11, "arithmetic", "+", "-", mutant.Survived),
		m("a.go", 5, 2, "logical", "&&", "||", mutant.TimedOut),
		m("a.go", 6, 2, "logical", "||", "&&", mutant.NotCovered),
		m("a.go", 7, 2, "arithmetic", "*", "/", mutant.NotViable),
		m("a.go", 8, 2, "arithmetic", "*", "/", mutant.Killed),
	})

	want := []jsonMutant{{File: "a.go", Line: 4, Column: 11, Operator: "arithmetic", Original: "+", Replacement: "-", Status: "survived"}}
	if !slices.Equal(got.Mutants, want) {
		t.Errorf("mutants = %+v, want %+v", got.Mutants, want)
	}
	if got.Score == nil || *got.Score != 75 {
		t.Errorf("score = %v, want 75", got.Score)
	}
	if got.Evaluated != 4 {
		t.Errorf("evaluated = %d, want 4", got.Evaluated)
	}
	s := got.Summary
	if s.Killed != 2 || s.Survived != 1 || s.TimedOut != 1 || s.NotCovered != 1 || s.NotViable != 1 || s.Pending != 0 {
		t.Errorf("summary = %+v", s)
	}
}

func TestJSONVerboseListsEverythingWithStatusNames(t *testing.T) {
	t.Parallel()

	_, got := renderJSON(t, report.JSON{Verbose: true}, []mutant.Mutant{
		m("a.go", 1, 1, "arithmetic", "+", "-", mutant.Killed),
		m("a.go", 2, 1, "arithmetic", "+", "-", mutant.Survived),
		m("a.go", 3, 1, "arithmetic", "+", "-", mutant.TimedOut),
		m("a.go", 4, 1, "arithmetic", "+", "-", mutant.NotCovered),
		m("a.go", 5, 1, "arithmetic", "+", "-", mutant.NotViable),
		m("a.go", 6, 1, "arithmetic", "+", "-", mutant.Pending),
	})

	var statuses []string
	for _, mu := range got.Mutants {
		statuses = append(statuses, mu.Status)
	}
	want := []string{"killed", "survived", "timed out", "not covered", "not viable", "pending"}
	if !slices.Equal(statuses, want) {
		t.Errorf("statuses = %q, want %q", statuses, want)
	}
	if got.Summary.Pending != 1 {
		t.Errorf("summary.pending = %d, want 1", got.Summary.Pending)
	}
}

func TestJSONEmptyAndUnevaluated(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutants []mutant.Mutant
	}{
		{"nil", nil},
		{"empty", []mutant.Mutant{}},
		{"nothing evaluated", []mutant.Mutant{m("a.go", 1, 1, "arithmetic", "+", "-", mutant.NotCovered)}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			out, got := renderJSON(t, report.JSON{}, tt.mutants)
			if got.Score != nil {
				t.Errorf("score = %v, want null when nothing was evaluated", *got.Score)
			}
			if !strings.Contains(out, `"score": null`) {
				t.Errorf("score must be an explicit null:\n%s", out)
			}
			if !strings.Contains(out, `"mutants": []`) {
				t.Errorf("mutants must be an empty array, never null:\n%s", out)
			}
		})
	}
}

func TestJSONScoreIsAnExactNumber(t *testing.T) {
	t.Parallel()

	out, got := renderJSON(t, report.JSON{}, []mutant.Mutant{
		m("a.go", 1, 1, "arithmetic", "+", "-", mutant.Killed),
		m("a.go", 2, 1, "arithmetic", "+", "-", mutant.Killed),
		m("a.go", 3, 1, "arithmetic", "+", "-", mutant.Survived),
	})
	if got.Score == nil || *got.Score < 66.66 || *got.Score > 66.67 {
		t.Errorf("score = %v, want about 66.67 (not rounded to text precision)\n%s", got.Score, out)
	}
}

func TestJSONOrdersByFileThenPosition(t *testing.T) {
	t.Parallel()

	in := []mutant.Mutant{
		m("b.go", 1, 1, "arithmetic", "+", "-", mutant.Survived),
		m("a.go", 10, 1, "arithmetic", "+", "-", mutant.Survived),
		m("a.go", 2, 9, "arithmetic", "+", "-", mutant.Survived),
		m("a.go", 2, 3, "logical", "&&", "||", mutant.Survived),
		m("a.go", 2, 3, "conditional", "<", "<=", mutant.Survived),
	}
	snapshot := slices.Clone(in)

	_, got := renderJSON(t, report.JSON{}, in)

	if len(got.Mutants) != 5 {
		t.Fatalf("got %d mutants, want 5", len(got.Mutants))
	}
	want := []jsonMutant{
		{File: "a.go", Line: 2, Column: 3, Operator: "conditional"},
		{File: "a.go", Line: 2, Column: 3, Operator: "logical"},
		{File: "a.go", Line: 2, Column: 9, Operator: "arithmetic"},
		{File: "a.go", Line: 10, Column: 1, Operator: "arithmetic"},
		{File: "b.go", Line: 1, Column: 1, Operator: "arithmetic"},
	}
	for i := range want {
		g := got.Mutants[i]
		if g.File != want[i].File || g.Line != want[i].Line || g.Column != want[i].Column || g.Operator != want[i].Operator {
			t.Errorf("mutant %d = %s:%d:%d %s, want %s:%d:%d %s", i,
				g.File, g.Line, g.Column, g.Operator,
				want[i].File, want[i].Line, want[i].Column, want[i].Operator)
		}
	}
	if !slices.Equal(in, snapshot) {
		t.Error("Write reordered or changed the caller's slice")
	}
}

func TestJSONRoundTripsAnyText(t *testing.T) {
	t.Parallel()

	file := "evil\x1b[31m.go"
	orig, repl := "a\nb <>&", "c\r\x00d‮"
	out, got := renderJSON(t, report.JSON{Verbose: true}, []mutant.Mutant{
		m(file, 1, 1, "op\x07", orig, repl, mutant.Survived),
	})

	g := got.Mutants[0]
	if g.File != file || g.Original != orig || g.Replacement != repl || g.Operator != "op\x07" {
		t.Errorf("values changed on the way: %+v", g)
	}
	for _, r := range out {
		if r != '\n' && r < ' ' {
			t.Fatalf("raw control character %U in JSON output:\n%q", r, out)
		}
	}
	if !strings.Contains(out, "<>&") {
		t.Errorf("HTML-sensitive characters should not be escaped in a CLI report:\n%s", out)
	}
}

func TestJSONIsDeterministicAndEndsWithNewline(t *testing.T) {
	t.Parallel()

	in := []mutant.Mutant{
		m("b.go", 1, 1, "logical", "&&", "||", mutant.Survived),
		m("a.go", 1, 1, "arithmetic", "+", "-", mutant.Survived),
		m("a.go", 1, 1, "conditional", "<", "<=", mutant.Survived),
	}
	first, _ := renderJSON(t, report.JSON{Verbose: true}, in)
	if !strings.HasSuffix(first, "}\n") {
		t.Errorf("output should end with a closing brace and a newline: %q", first[max(0, len(first)-10):])
	}
	for range 5 {
		if got, _ := renderJSON(t, report.JSON{Verbose: true}, in); got != first {
			t.Fatalf("output changed between calls:\n%s\n%s", first, got)
		}
	}
}

func TestJSONWriteFailure(t *testing.T) {
	t.Parallel()

	fw := &failingWriter{}
	err := report.JSON{}.Write(fw, []mutant.Mutant{m("a.go", 1, 1, "arithmetic", "+", "-", mutant.Survived)})
	if err == nil || !strings.Contains(err.Error(), "pipe closed") {
		t.Errorf("Write() error = %v, want the writer's error", err)
	}
	if fw.writes != 1 {
		t.Errorf("writer called %d times, want 1", fw.writes)
	}
}

func TestJSONNilWriter(t *testing.T) {
	t.Parallel()

	if err := (report.JSON{}).Write(nil, nil); err == nil {
		t.Error("Write(nil) returned nil error")
	}
}
