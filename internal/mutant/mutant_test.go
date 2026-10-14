package mutant_test

import (
	"testing"

	"github.com/pallat/gomu/internal/mutant"
)

func TestStatusString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status mutant.Status
		want   string
	}{
		{mutant.Pending, "pending"},
		{mutant.Killed, "killed"},
		{mutant.Survived, "survived"},
		{mutant.TimedOut, "timed out"},
		{mutant.NotCovered, "not covered"},
		{mutant.NotViable, "not viable"},
		{mutant.Status(99), "unknown"},
		{mutant.Status(-1), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()

			if got := tt.status.String(); got != tt.want {
				t.Errorf("Status(%d).String() = %q, want %q", int(tt.status), got, tt.want)
			}
		})
	}
}

func TestZeroValueMutantIsPending(t *testing.T) {
	t.Parallel()

	var m mutant.Mutant
	if m.Status != mutant.Pending {
		t.Errorf("zero Mutant status = %v, want %v", m.Status, mutant.Pending)
	}
}

func TestSummarize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		input  []mutant.Status
		want   mutant.Summary
		wantSc float64
	}{
		{
			name:   "nil input",
			input:  nil,
			want:   mutant.Summary{},
			wantSc: 0,
		},
		{
			name:   "only pending and not covered are not evaluated",
			input:  []mutant.Status{mutant.Pending, mutant.NotCovered},
			want:   mutant.Summary{Pending: 1, NotCovered: 1},
			wantSc: 0,
		},
		{
			name:   "all killed",
			input:  []mutant.Status{mutant.Killed, mutant.Killed},
			want:   mutant.Summary{Killed: 2},
			wantSc: 100,
		},
		{
			name:   "all survived",
			input:  []mutant.Status{mutant.Survived},
			want:   mutant.Summary{Survived: 1},
			wantSc: 0,
		},
		{
			name:   "timeout counts as killed",
			input:  []mutant.Status{mutant.TimedOut, mutant.Survived},
			want:   mutant.Summary{TimedOut: 1, Survived: 1},
			wantSc: 50,
		},
		{
			name: "mixed ignores not covered and pending in score",
			input: []mutant.Status{
				mutant.Killed, mutant.Killed, mutant.Killed,
				mutant.Survived,
				mutant.NotCovered, mutant.Pending,
			},
			want: mutant.Summary{
				Killed: 3, Survived: 1, NotCovered: 1, Pending: 1,
			},
			wantSc: 75,
		},
		{
			name:   "not viable is counted but not scored",
			input:  []mutant.Status{mutant.NotViable, mutant.Killed, mutant.Survived},
			want:   mutant.Summary{NotViable: 1, Killed: 1, Survived: 1},
			wantSc: 50,
		},
		{
			name:   "unknown status is ignored",
			input:  []mutant.Status{mutant.Status(42), mutant.Killed},
			want:   mutant.Summary{Killed: 1},
			wantSc: 100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mutants := make([]mutant.Mutant, len(tt.input))
			for i, s := range tt.input {
				mutants[i].Status = s
			}

			got := mutant.Summarize(mutants)
			if got != tt.want {
				t.Fatalf("Summarize() = %+v, want %+v", got, tt.want)
			}
			if sc := got.Score(); sc != tt.wantSc {
				t.Errorf("Score() = %v, want %v", sc, tt.wantSc)
			}
		})
	}
}

func TestPositionString(t *testing.T) {
	t.Parallel()

	p := mutant.Position{File: "a/b.go", Line: 12, Column: 3}
	if got, want := p.String(), "a/b.go:12:3"; got != want {
		t.Errorf("Position.String() = %q, want %q", got, want)
	}
}
