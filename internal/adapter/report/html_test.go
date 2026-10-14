package report_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/pallat/gomu/internal/adapter/report"
	"github.com/pallat/gomu/internal/mutant"
)

func renderHTML(t *testing.T, r report.HTML, mutants []mutant.Mutant) string {
	t.Helper()

	var sb strings.Builder
	if err := r.Write(&sb, mutants); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	return sb.String()
}

func TestHTMLIsOneSelfContainedPage(t *testing.T) {
	t.Parallel()

	got := renderHTML(t, report.HTML{}, []mutant.Mutant{
		m("a.go", 4, 11, "arithmetic", "+", "-", mutant.Survived),
	})

	if !strings.HasPrefix(got, "<!doctype html>") || !strings.HasSuffix(got, "</html>\n") {
		t.Errorf("not a complete document:\n%s", got)
	}
	if !strings.Contains(got, "Content-Security-Policy") || !strings.Contains(got, "default-src 'none'") {
		t.Errorf("missing restrictive Content-Security-Policy:\n%s", got)
	}
	for _, bad := range []string{"<script", "http://", "https://", "<link", "<img", "src="} {
		if strings.Contains(got, bad) {
			t.Errorf("page must not load anything external or run code, found %q", bad)
		}
	}
}

func TestHTMLDefaultListsOnlySurvivors(t *testing.T) {
	t.Parallel()

	got := renderHTML(t, report.HTML{}, []mutant.Mutant{
		m("a.go", 3, 7, "conditional", "<", "<=", mutant.Killed),
		m("a.go", 4, 11, "arithmetic", "+", "-", mutant.Survived),
		m("a.go", 5, 2, "logical", "&amp;&amp;", "||", mutant.TimedOut),
		m("a.go", 6, 2, "logical", "||", "&&", mutant.NotCovered),
	})

	if !strings.Contains(got, "a.go") || !strings.Contains(got, "4:11") {
		t.Errorf("survivor missing:\n%s", got)
	}
	if strings.Contains(got, "3:7") || strings.Contains(got, "5:2") || strings.Contains(got, "6:2") {
		t.Errorf("non-survivors listed without Verbose:\n%s", got)
	}
	// 2 of 3 evaluated mutants detected.
	if !strings.Contains(got, "66.7%") {
		t.Errorf("score missing:\n%s", got)
	}
}

func TestHTMLVerboseListsEverythingGroupedByFile(t *testing.T) {
	t.Parallel()

	got := renderHTML(t, report.HTML{Verbose: true}, []mutant.Mutant{
		m("b.go", 1, 1, "arithmetic", "+", "-", mutant.Killed),
		m("a.go", 9, 1, "arithmetic", "+", "-", mutant.Survived),
		m("a.go", 2, 5, "arithmetic", "+", "-", mutant.TimedOut),
		m("a.go", 3, 1, "arithmetic", "+", "-", mutant.NotCovered),
		m("a.go", 4, 1, "arithmetic", "+", "-", mutant.NotViable),
		m("a.go", 5, 1, "arithmetic", "+", "-", mutant.Pending),
	})

	for _, want := range []string{"killed", "survived", "timed out", "not covered", "not viable", "pending"} {
		if !strings.Contains(got, want) {
			t.Errorf("status %q missing:\n%s", want, got)
		}
	}
	for _, class := range []string{"tag killed", "tag survived", "tag timed-out", "tag not-covered", "tag not-viable", "tag pending"} {
		if !strings.Contains(got, `class="`+class+`"`) {
			t.Errorf("style class %q missing:\n%s", class, got)
		}
	}
	if n := strings.Count(got, "<h2"); n != 2 {
		t.Errorf("got %d file headings, want 2 (one per file)", n)
	}
	order := []string{"a.go", "2:5", "3:1", "4:1", "5:1", "9:1", "b.go"}
	last := -1
	for _, s := range order {
		i := strings.Index(got[max(last, 0):], s)
		if i < 0 {
			t.Fatalf("%q not found after previous item in:\n%s", s, got)
		}
		last += i + 1
	}
}

func TestHTMLScoreStates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutants []mutant.Mutant
		want    string
	}{
		{"nil", nil, "No mutants found."},
		{"nothing evaluated", []mutant.Mutant{m("a.go", 1, 1, "arithmetic", "+", "-", mutant.NotCovered)}, "n/a"},
		{"all survived", []mutant.Mutant{m("a.go", 1, 1, "arithmetic", "+", "-", mutant.Survived)}, "0.0%"},
		{"all killed", []mutant.Mutant{m("a.go", 1, 1, "arithmetic", "+", "-", mutant.Killed)}, "100.0%"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := renderHTML(t, report.HTML{}, tt.mutants); !strings.Contains(got, tt.want) {
				t.Errorf("want %q in:\n%s", tt.want, got)
			}
		})
	}
}

func TestHTMLNothingToListMessage(t *testing.T) {
	t.Parallel()

	got := renderHTML(t, report.HTML{}, []mutant.Mutant{m("a.go", 1, 1, "arithmetic", "+", "-", mutant.Killed)})
	if !strings.Contains(got, "No survived mutants") {
		t.Errorf("want a friendly message when nothing survived:\n%s", got)
	}
	if strings.Contains(got, "<table") {
		t.Errorf("no table expected when nothing is listed:\n%s", got)
	}
}

func TestHTMLEscapesHostileText(t *testing.T) {
	t.Parallel()

	got := renderHTML(t, report.HTML{Verbose: true}, []mutant.Mutant{
		m(`"><script>alert(1)</script>.go`, 1, 1, "op<b>", "<", "&\x1b[31m‮", mutant.Survived),
	})

	for _, bad := range []string{"<script", "<b>", "\x1b", "‮"} {
		if strings.Contains(got, bad) {
			t.Errorf("unescaped %q in output:\n%s", bad, got)
		}
	}
	if !strings.Contains(got, "&lt;script&gt;") {
		t.Errorf("file name should be HTML-escaped:\n%s", got)
	}
}

func TestHTMLDoesNotModifyInputAndIsDeterministic(t *testing.T) {
	t.Parallel()

	in := []mutant.Mutant{
		m("b.go", 1, 1, "logical", "&&", "||", mutant.Survived),
		m("a.go", 2, 1, "arithmetic", "+", "-", mutant.Survived),
		m("a.go", 1, 1, "conditional", "<", "<=", mutant.Survived),
	}
	snapshot := slices.Clone(in)

	first := renderHTML(t, report.HTML{Verbose: true}, in)
	if !slices.Equal(in, snapshot) {
		t.Error("Write reordered or changed the caller's slice")
	}
	for range 5 {
		if got := renderHTML(t, report.HTML{Verbose: true}, in); got != first {
			t.Fatal("output changed between calls")
		}
	}
}

func TestHTMLWriteFailure(t *testing.T) {
	t.Parallel()

	fw := &failingWriter{}
	err := report.HTML{}.Write(fw, []mutant.Mutant{m("a.go", 1, 1, "arithmetic", "+", "-", mutant.Survived)})
	if err == nil || !strings.Contains(err.Error(), "pipe closed") {
		t.Errorf("Write() error = %v, want the writer's error", err)
	}
	if fw.writes != 1 {
		t.Errorf("writer called %d times, want 1", fw.writes)
	}
}

func TestHTMLNilWriter(t *testing.T) {
	t.Parallel()

	if err := (report.HTML{}).Write(nil, nil); err == nil {
		t.Error("Write(nil) returned nil error")
	}
}

func TestHTMLDetectedOfEvaluated(t *testing.T) {
	t.Parallel()

	got := renderHTML(t, report.HTML{}, []mutant.Mutant{
		m("a.go", 1, 1, "arithmetic", "+", "-", mutant.Killed),
		m("a.go", 2, 1, "arithmetic", "+", "-", mutant.TimedOut),
		m("a.go", 3, 1, "arithmetic", "+", "-", mutant.Survived),
		m("a.go", 4, 1, "arithmetic", "+", "-", mutant.NotCovered),
		m("a.go", 5, 1, "arithmetic", "+", "-", mutant.NotViable),
	})
	if !strings.Contains(got, "2 of 3 evaluated mutants detected") {
		t.Errorf("want killed + timed out of evaluated:\n%s", got)
	}
}

func TestHTMLScoreLevelBoundaries(t *testing.T) {
	t.Parallel()

	// killed of 100 evaluated, so the score equals killed.
	tests := []struct {
		killed int
		want   string
	}{
		{100, "good"}, {80, "good"}, {79, "fair"}, {60, "fair"}, {59, "poor"}, {0, "poor"},
	}
	for _, tt := range tests {
		var in []mutant.Mutant
		for i := range 100 {
			s := mutant.Survived
			if i < tt.killed {
				s = mutant.Killed
			}
			in = append(in, m("a.go", i+1, 1, "arithmetic", "+", "-", s))
		}
		got := renderHTML(t, report.HTML{}, in)
		if want := `<div class="score ` + tt.want + `">`; !strings.Contains(got, want) {
			t.Errorf("score %d%%: want %q in page", tt.killed, want)
		}
	}
}
