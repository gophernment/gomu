package cover_test

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pallat/gomu/internal/adapter/cover"
)

func parse(t *testing.T, s string) *cover.Profile {
	t.Helper()

	p, err := cover.Parse(strings.NewReader(s))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	return p
}

func TestCoveredSingleLineBlock(t *testing.T) {
	t.Parallel()

	p := parse(t, "mode: set\nx/a.go:10.5,10.20 1 1\n")

	tests := []struct {
		line, col int
		want      bool
	}{
		{10, 5, true},   // start is inclusive
		{10, 19, true},  // last column inside
		{10, 20, false}, // end is exclusive
		{10, 4, false},
		{9, 10, false},
		{11, 10, false},
	}
	for _, tt := range tests {
		if got := p.Covered("x/a.go", tt.line, tt.col); got != tt.want {
			t.Errorf("Covered(%d,%d) = %v, want %v", tt.line, tt.col, got, tt.want)
		}
	}
}

func TestCoveredMultiLineBlock(t *testing.T) {
	t.Parallel()

	p := parse(t, "mode: set\nx/a.go:10.5,12.3 2 1\n")

	tests := []struct {
		line, col int
		want      bool
	}{
		{10, 4, false},
		{10, 5, true},
		{10, 200, true},
		{11, 1, true}, // any column on a middle line
		{11, 200, true},
		{12, 1, true},
		{12, 2, true},
		{12, 3, false},
		{13, 1, false},
	}
	for _, tt := range tests {
		if got := p.Covered("x/a.go", tt.line, tt.col); got != tt.want {
			t.Errorf("Covered(%d,%d) = %v, want %v", tt.line, tt.col, got, tt.want)
		}
	}
}

func TestCoveredCountsAndMerging(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		profile string
		want    bool
	}{
		{"set mode hit", "mode: set\nx/a.go:1.1,3.2 1 1\n", true},
		{"set mode miss", "mode: set\nx/a.go:1.1,3.2 1 0\n", false},
		{"count mode", "mode: count\nx/a.go:1.1,3.2 1 42\n", true},
		{"atomic mode", "mode: atomic\nx/a.go:1.1,3.2 1 7\n", true},
		{"merged profiles, one hit", "mode: set\nx/a.go:1.1,3.2 1 0\nx/a.go:1.1,3.2 1 1\n", true},
		{"merged profiles, no hit", "mode: set\nx/a.go:1.1,3.2 1 0\nx/a.go:1.1,3.2 1 0\n", false},
		{"blank lines are ignored", "mode: set\n\nx/a.go:1.1,3.2 1 1\n\n", true},
		{"windows line endings", "mode: set\r\nx/a.go:1.1,3.2 1 1\r\n", true},
		{"no trailing newline", "mode: set\nx/a.go:1.1,3.2 1 1", true},
		{"mode line only", "mode: set\n", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := parse(t, tt.profile).Covered("x/a.go", 2, 1); got != tt.want {
				t.Errorf("Covered() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCoveredUnknownFileAndNilProfile(t *testing.T) {
	t.Parallel()

	p := parse(t, "mode: set\nx/a.go:1.1,3.2 1 1\n")
	if p.Covered("x/b.go", 2, 1) {
		t.Error("Covered() for a file not in the profile = true")
	}
	if p.Covered("", 2, 1) {
		t.Error("Covered() for an empty name = true")
	}

	var nilProfile *cover.Profile
	if nilProfile.Covered("x/a.go", 2, 1) {
		t.Error("Covered() on a nil Profile = true")
	}
}

func TestCoveredNameWithColon(t *testing.T) {
	t.Parallel()

	p := parse(t, "mode: set\nhost:8080/pkg/a.go:1.1,3.2 1 1\n")
	if !p.Covered("host:8080/pkg/a.go", 2, 1) {
		t.Error("name containing a colon was split at the wrong place")
	}
}

func TestParseRejectsMalformedInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		profile string
	}{
		{"empty input", ""},
		{"only blank lines", "\n\n"},
		{"missing mode line", "x/a.go:1.1,3.2 1 1\n"},
		{"unknown mode", "mode: bogus\nx/a.go:1.1,3.2 1 1\n"},
		{"mode without value", "mode:\n"},
		{"no colon", "mode: set\nx/a.go 1 1\n"},
		{"empty name", "mode: set\n:1.1,3.2 1 1\n"},
		{"missing fields", "mode: set\nx/a.go:1.1,3.2 1\n"},
		{"extra fields", "mode: set\nx/a.go:1.1,3.2 1 1 1\n"},
		{"no comma in range", "mode: set\nx/a.go:1.1 1 1\n"},
		{"no dot in start", "mode: set\nx/a.go:1,3.2 1 1\n"},
		{"no dot in end", "mode: set\nx/a.go:1.1,3 1 1\n"},
		{"non-numeric line", "mode: set\nx/a.go:a.1,3.2 1 1\n"},
		{"non-numeric statements", "mode: set\nx/a.go:1.1,3.2 x 1\n"},
		{"non-numeric count", "mode: set\nx/a.go:1.1,3.2 1 x\n"},
		{"zero line", "mode: set\nx/a.go:0.1,3.2 1 1\n"},
		{"zero column", "mode: set\nx/a.go:1.0,3.2 1 1\n"},
		{"negative line", "mode: set\nx/a.go:-1.1,3.2 1 1\n"},
		{"negative count", "mode: set\nx/a.go:1.1,3.2 1 -1\n"},
		{"negative statements", "mode: set\nx/a.go:1.1,3.2 -1 1\n"},
		{"end line before start line", "mode: set\nx/a.go:5.1,3.2 1 1\n"},
		{"end column before start column", "mode: set\nx/a.go:3.5,3.2 1 1\n"},
		{"number overflow", "mode: set\nx/a.go:99999999999999999999.1,3.2 1 1\n"},
		{"second mode line", "mode: set\nmode: set\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p, err := cover.Parse(strings.NewReader(tt.profile))
			if !errors.Is(err, cover.ErrFormat) {
				t.Fatalf("Parse() error = %v, want %v", err, cover.ErrFormat)
			}
			if p != nil {
				t.Error("Parse() returned a Profile together with an error")
			}
		})
	}
}

func TestParseErrorMentionsLineNumberNotContent(t *testing.T) {
	t.Parallel()

	_, err := cover.Parse(strings.NewReader("mode: set\nx/a.go:1.1,3.2 1 1\nSECRET-TOKEN\n"))
	if err == nil {
		t.Fatal("Parse() returned nil error")
	}
	if !strings.Contains(err.Error(), "line 3") {
		t.Errorf("error %q does not name the bad line", err)
	}
	if strings.Contains(err.Error(), "SECRET-TOKEN") {
		t.Errorf("error %q echoes input content", err)
	}
}

func TestParseRejectsOversizedLine(t *testing.T) {
	t.Parallel()

	in := "mode: set\n" + strings.Repeat("a", 2<<20) + "\n"
	if _, err := cover.Parse(strings.NewReader(in)); !errors.Is(err, bufio.ErrTooLong) {
		t.Errorf("Parse() error = %v, want %v", err, bufio.ErrTooLong)
	}
}

func TestParseRejectsOversizedInput(t *testing.T) {
	t.Parallel()

	// Valid lines with count 0, so nothing is stored and the test stays cheap.
	line := strings.Repeat("a", 900_000) + ".go:1.1,2.2 1 0\n"
	var sb strings.Builder
	sb.WriteString("mode: set\n")
	for sb.Len() <= cover.MaxSize {
		sb.WriteString(line)
	}

	if _, err := cover.Parse(strings.NewReader(sb.String())); !errors.Is(err, cover.ErrTooLarge) {
		t.Errorf("Parse() error = %v, want %v", err, cover.ErrTooLarge)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("disk on fire") }

func TestParseReadError(t *testing.T) {
	t.Parallel()

	if _, err := cover.Parse(failingReader{}); err == nil {
		t.Error("Parse() returned nil error for a failing reader")
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{
		"mode: set\nx/a.go:1.1,3.2 1 1\n",
		"mode: count\nx/a.go:10.5,12.3 2 7\nx/b.go:1.1,1.9 1 0\n",
		"mode: atomic\n",
		"",
		"mode: set\n:::.,, 1 1\n",
		"mode: set\nx.go:1.1,1.1 1 1\n",
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, in string) {
		p, err := cover.Parse(strings.NewReader(in))
		if err != nil {
			if p != nil {
				t.Fatal("Profile and error both returned")
			}
			return
		}
		// Must never panic, whatever the query.
		p.Covered("x/a.go", 2, 1)
		p.Covered("", 0, 0)
		p.Covered("x.go", -1, -1)
	})
}

// TestParseRealProfile checks the parser against what the go tool writes.
func TestParseRealProfile(t *testing.T) {
	t.Parallel()

	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go tool not found")
	}

	mod := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/p\n\ngo 1.21\n",
		"add.go": "package p\n\nfunc Add(a, b int) int {\n\treturn a + b\n}\n\nfunc Unused() int {\n\treturn 1\n}\n",
		"add_test.go": "package p\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n" +
			"\tif Add(2, 3) != 5 {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(mod, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	out := filepath.Join(t.TempDir(), "cover.out")
	cmd := exec.Command(goBin, "test", "-count=1", "-coverprofile="+out, "./...")
	cmd.Dir = mod
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go test failed: %v\n%s", err, b)
	}

	fh, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()

	p, err := cover.Parse(fh)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	const name = "example.com/p/add.go"
	if !p.Covered(name, 4, 11) {
		t.Error("the + inside Add (4:11) should be covered")
	}
	if p.Covered(name, 8, 2) {
		t.Error("the body of Unused (8:2) should not be covered")
	}
}
