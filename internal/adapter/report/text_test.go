package report_test

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/pallat/gomu/internal/adapter/report"
	"github.com/pallat/gomu/internal/mutant"
)

func m(file string, line, col int, op, from, to string, s mutant.Status) mutant.Mutant {
	return mutant.Mutant{
		Operator:    op,
		Pos:         mutant.Position{File: file, Line: line, Column: col},
		Original:    from,
		Replacement: to,
		Status:      s,
	}
}

func render(t *testing.T, r report.Text, mutants []mutant.Mutant) string {
	t.Helper()

	var buf bytes.Buffer
	if err := r.Write(&buf, mutants); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	return buf.String()
}

func TestTextDefaultListsOnlySurvivors(t *testing.T) {
	t.Parallel()

	got := render(t, report.Text{}, []mutant.Mutant{
		m("a.go", 3, 7, "conditional", "<", "<=", mutant.Killed),
		m("a.go", 4, 11, "arithmetic", "+", "-", mutant.Survived),
		m("a.go", 5, 2, "logical", "&&", "||", mutant.TimedOut),
		m("a.go", 6, 2, "logical", "||", "&&", mutant.NotCovered),
		m("a.go", 7, 2, "arithmetic", "*", "/", mutant.NotViable),
		m("a.go", 8, 2, "arithmetic", "*", "/", mutant.Killed),
	})

	want := `SURVIVED    a.go:4:11  arithmetic   + -> -

Mutation score: 75.0% (3 of 4 evaluated mutants detected)
Killed 2 | Survived 1 | Timed out 1 | Not covered 1 | Not viable 1
`
	if got != want {
		t.Errorf("output =\n%s\nwant\n%s", got, want)
	}
}

func TestTextVerboseListsEverything(t *testing.T) {
	t.Parallel()

	got := render(t, report.Text{Verbose: true}, []mutant.Mutant{
		m("a.go", 1, 1, "arithmetic", "+", "-", mutant.Killed),
		m("a.go", 2, 1, "arithmetic", "+", "-", mutant.Survived),
		m("a.go", 3, 1, "arithmetic", "+", "-", mutant.TimedOut),
		m("a.go", 4, 1, "arithmetic", "+", "-", mutant.NotCovered),
		m("a.go", 5, 1, "arithmetic", "+", "-", mutant.NotViable),
		m("a.go", 6, 1, "arithmetic", "+", "-", mutant.Pending),
	})

	want := `KILLED      a.go:1:1  arithmetic   + -> -
SURVIVED    a.go:2:1  arithmetic   + -> -
TIMED OUT   a.go:3:1  arithmetic   + -> -
NOT COVERED a.go:4:1  arithmetic   + -> -
NOT VIABLE  a.go:5:1  arithmetic   + -> -
PENDING     a.go:6:1  arithmetic   + -> -

Mutation score: 66.7% (2 of 3 evaluated mutants detected)
Killed 1 | Survived 1 | Timed out 1 | Not covered 1 | Not viable 1 | Pending 1
`
	if got != want {
		t.Errorf("output =\n%s\nwant\n%s", got, want)
	}
}

func TestTextEmptyAndUnevaluated(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutants []mutant.Mutant
		want    string
	}{
		{"nil", nil, "No mutants found.\n"},
		{"empty", []mutant.Mutant{}, "No mutants found.\n"},
		{
			"nothing evaluated",
			[]mutant.Mutant{m("a.go", 1, 1, "arithmetic", "+", "-", mutant.NotCovered)},
			"Mutation score: n/a (no mutants were evaluated)\n" +
				"Killed 0 | Survived 0 | Timed out 0 | Not covered 1 | Not viable 0\n",
		},
		{
			"all survived",
			[]mutant.Mutant{m("a.go", 1, 1, "arithmetic", "+", "-", mutant.Survived)},
			"SURVIVED    a.go:1:1  arithmetic   + -> -\n\n" +
				"Mutation score: 0.0% (0 of 1 evaluated mutants detected)\n" +
				"Killed 0 | Survived 1 | Timed out 0 | Not covered 0 | Not viable 0\n",
		},
		{
			"all killed",
			[]mutant.Mutant{m("a.go", 1, 1, "arithmetic", "+", "-", mutant.Killed)},
			"Mutation score: 100.0% (1 of 1 evaluated mutants detected)\n" +
				"Killed 1 | Survived 0 | Timed out 0 | Not covered 0 | Not viable 0\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := render(t, report.Text{}, tt.mutants); got != tt.want {
				t.Errorf("output =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

func TestTextScoreRounding(t *testing.T) {
	t.Parallel()

	got := render(t, report.Text{}, []mutant.Mutant{
		m("a.go", 1, 1, "arithmetic", "+", "-", mutant.Killed),
		m("a.go", 2, 1, "arithmetic", "+", "-", mutant.Killed),
		m("a.go", 3, 1, "arithmetic", "+", "-", mutant.Survived),
	})
	if !strings.Contains(got, "Mutation score: 66.7% (2 of 3") {
		t.Errorf("score not rounded to one decimal:\n%s", got)
	}
}

func TestTextOrdersByFileThenPosition(t *testing.T) {
	t.Parallel()

	in := []mutant.Mutant{
		m("b.go", 1, 1, "arithmetic", "+", "-", mutant.Survived),
		m("a.go", 10, 1, "arithmetic", "+", "-", mutant.Survived),
		m("a.go", 2, 9, "arithmetic", "+", "-", mutant.Survived),
		m("a.go", 2, 3, "logical", "&&", "||", mutant.Survived),
		m("a.go", 2, 3, "conditional", "<", "<=", mutant.Survived),
	}
	snapshot := slices.Clone(in)

	got := render(t, report.Text{}, in)

	var order []string
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "SURVIVED") {
			f := strings.Fields(line)
			order = append(order, f[1]+" "+f[2])
		}
	}
	want := []string{"a.go:2:3 conditional", "a.go:2:3 logical", "a.go:2:9 arithmetic", "a.go:10:1 arithmetic", "b.go:1:1 arithmetic"}
	if !slices.Equal(order, want) {
		t.Errorf("order = %q, want %q (numeric line order, then operator)", order, want)
	}
	if !slices.Equal(in, snapshot) {
		t.Error("Write reordered or changed the caller's slice")
	}
}

func TestTextEscapesControlCharacters(t *testing.T) {
	t.Parallel()

	got := render(t, report.Text{Verbose: true}, []mutant.Mutant{
		m("evil\x1b[31m.go", 1, 1, "x\x07", "a\nb", "c\r\x00d\u202e", mutant.Survived),
	})

	for _, r := range got {
		if r != '\n' && (r < ' ' || r == 0x7f || (r >= 0x80 && r < 0xa0) || r == '\u202e') {
			t.Fatalf("raw control character %U in output:\n%q", r, got)
		}
	}
	for _, want := range []string{`evil\x1b[31m.go`, `x\a`, `a\nb`, `c\r\x00d\u202e`} {
		if !strings.Contains(got, want) {
			t.Errorf("output does not contain escaped %q:\n%s", want, got)
		}
	}
}

func TestTextKeepsReadableUnicode(t *testing.T) {
	t.Parallel()

	got := render(t, report.Text{}, []mutant.Mutant{
		m("แพ็กเกจ/ไฟล์.go", 1, 1, "arithmetic", "+", "-", mutant.Survived),
	})
	if !strings.Contains(got, "แพ็กเกจ/ไฟล์.go:1:1") {
		t.Errorf("printable Unicode was altered:\n%s", got)
	}
}

type failingWriter struct{ writes int }

func (f *failingWriter) Write([]byte) (int, error) {
	f.writes++
	return 0, errors.New("pipe closed")
}

func TestTextWriteFailure(t *testing.T) {
	t.Parallel()

	fw := &failingWriter{}
	err := report.Text{}.Write(fw, []mutant.Mutant{m("a.go", 1, 1, "arithmetic", "+", "-", mutant.Survived)})
	if err == nil || !strings.Contains(err.Error(), "pipe closed") {
		t.Errorf("Write() error = %v, want the writer's error", err)
	}
	if fw.writes != 1 {
		t.Errorf("writer called %d times, want 1", fw.writes)
	}
}

func TestTextNilWriter(t *testing.T) {
	t.Parallel()

	if err := (report.Text{}).Write(nil, nil); err == nil {
		t.Error("Write(nil) returned nil error")
	}
}

func TestTextIsDeterministic(t *testing.T) {
	t.Parallel()

	in := []mutant.Mutant{
		m("b.go", 1, 1, "logical", "&&", "||", mutant.Survived),
		m("a.go", 1, 1, "arithmetic", "+", "-", mutant.Survived),
		m("a.go", 1, 1, "conditional", "<", "<=", mutant.Survived),
	}
	first := render(t, report.Text{Verbose: true}, in)
	for range 5 {
		if got := render(t, report.Text{Verbose: true}, in); got != first {
			t.Fatalf("output changed between calls:\n%s\n%s", first, got)
		}
	}
}
