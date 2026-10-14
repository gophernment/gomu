package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestModulePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"plain", "module example.com/p\n\ngo 1.21\n", "example.com/p", false},
		{"tab separator", "module\texample.com/p\n", "example.com/p", false},
		{"quoted", "module \"example.com/p\"\n", "example.com/p", false},
		{"backquoted", "module `example.com/p`\n", "example.com/p", false},
		{"trailing comment", "module example.com/p // the thing\n", "example.com/p", false},
		{"leading comment and blank lines", "// header\n\nmodule example.com/p\n", "example.com/p", false},
		{"windows line endings", "module example.com/p\r\ngo 1.21\r\n", "example.com/p", false},
		{"indented", "  module example.com/p\n", "example.com/p", false},
		{"first directive wins", "module a.com/one\nmodule a.com/two\n", "a.com/one", false},
		{"no trailing newline", "module example.com/p", "example.com/p", false},
		{"module-like word is not the directive", "modules example.com/p\n", "", true},
		{"missing directive", "go 1.21\n", "", true},
		{"empty file", "", "", true},
		{"directive without path", "module\n", "", true},
		{"directive with blank path", "module   \n", "", true},
		{"two paths", "module a b\n", "", true},
		{"unterminated quote", "module \"example.com/p\n", "", true},
		{"empty quotes", "module \"\"\n", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := modulePath([]byte(tt.in))
			if (err != nil) != tt.wantErr {
				t.Fatalf("modulePath() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("modulePath() = %q, want %q", got, tt.want)
			}
		})
	}
}

// gomu runs the command in-process and returns its exit code and output.
func gomu(ctx context.Context, args ...string) (code int, stdout, stderr string) {
	var out, errb bytes.Buffer
	code = run(ctx, args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestUsageErrors(t *testing.T) {
	t.Parallel()

	empty := t.TempDir()

	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStderr string
	}{
		{"help", []string{"-h"}, exitOK, "usage: gomu"},
		{"unknown flag", []string{"-nope"}, exitError, "flag provided but not defined"},
		{"negative threshold", []string{"-threshold", "-1"}, exitError, "-threshold"},
		{"threshold over 100", []string{"-threshold", "101"}, exitError, "-threshold"},
		{"NaN threshold", []string{"-threshold", "NaN"}, exitError, "-threshold"},
		{"negative workers", []string{"-workers", "-2"}, exitError, "-workers"},
		{"missing directory", []string{"-dir", filepath.Join(empty, "nope")}, exitError, "module directory"},
		{"directory without go.mod", []string{"-dir", empty}, exitError, "go.mod"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			code, stdout, stderr := gomu(context.Background(), tt.args...)
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d\nstderr: %s", code, tt.wantCode, stderr)
			}
			if !strings.Contains(stderr, tt.wantStderr) {
				t.Errorf("stderr does not contain %q:\n%s", tt.wantStderr, stderr)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want nothing on a usage error", stdout)
			}
			if strings.Contains(stderr, empty) {
				t.Errorf("stderr leaks a absolute path:\n%s", stderr)
			}
		})
	}
}

func TestModuleDirIsNotAGoModDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "go.mod"), 0o700); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := gomu(context.Background(), "-dir", dir); code != exitError || !strings.Contains(stderr, "go.mod") {
		t.Errorf("a go.mod directory was accepted: code=%d stderr=%s", code, stderr)
	}
}

const (
	fxAdd = `package fx

// Add is covered and well tested: its mutant is killed.
func Add(a, b int) int { return a + b }

// Max is covered but never called with equal values: > to >= survives.
func Max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Unused has no test at all.
func Unused(a, b int) int { return a * b }
`
	fxAddTest = `package fx

import "testing"

func TestAdd(t *testing.T) {
	if Add(2, 3) != 5 {
		t.Fatal("bad")
	}
}

func TestMax(t *testing.T) {
	if Max(5, 3) != 5 {
		t.Fatal("bad")
	}
}
`
	fxSub = `package sub

func Neg(a int) int { return a - 1 }
`
	fxSubTest = `package sub

import "testing"

func TestNeg(t *testing.T) {
	if Neg(1) != 0 {
		t.Fatal("bad")
	}
}
`
)

// fixture writes a small module and returns its directory.
func fixture(t *testing.T, files map[string]string) string {
	t.Helper()

	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func needGo(t *testing.T) {
	t.Helper()

	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not found")
	}
}

// TestEndToEnd runs the real pipeline on a tiny module: discovery, baseline
// with coverage, overlays, go test, and the report.
func TestEndToEnd(t *testing.T) {
	t.Parallel()
	needGo(t)

	files := map[string]string{
		"go.mod":          "module example.com/fx\n\ngo 1.21\n",
		"add.go":          fxAdd,
		"add_test.go":     fxAddTest,
		"sub/s.go":        fxSub,
		"sub/s_test.go":   fxSubTest,
		"testdata/bad.go": "this is not Go",
	}
	dir := fixture(t, files)

	before := func() map[string]string {
		got := map[string]string{}
		for name := range files {
			b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
			if err != nil {
				t.Fatal(err)
			}
			got[name] = string(b)
		}
		return got
	}()

	ctx := context.Background()

	t.Run("whole module", func(t *testing.T) {
		code, stdout, stderr := gomu(ctx, "-dir", dir)
		if code != exitOK {
			t.Fatalf("exit code = %d\nstderr: %s\nstdout: %s", code, stderr, stdout)
		}
		for _, want := range []string{
			"SURVIVED",
			"add.go:",
			"conditional",
			"> -> >=",
			"Mutation score: 66.7% (2 of 3 evaluated mutants detected)",
			"Killed 2 | Survived 1 | Timed out 0 | Not covered 1 | Not viable 0",
		} {
			if !strings.Contains(stdout, want) {
				t.Errorf("stdout does not contain %q:\n%s", want, stdout)
			}
		}
		if strings.Contains(stdout, "KILLED") {
			t.Error("non-verbose output lists killed mutants")
		}
		if strings.Contains(stdout, dir) || strings.Contains(stderr, dir) {
			t.Errorf("output leaks the absolute module path:\n%s\n%s", stdout, stderr)
		}
	})

	t.Run("verbose lists every mutant", func(t *testing.T) {
		code, stdout, stderr := gomu(ctx, "-dir", dir, "-v")
		if code != exitOK {
			t.Fatalf("exit code = %d\nstderr: %s", code, stderr)
		}
		for _, want := range []string{"KILLED", "SURVIVED", "NOT COVERED", "sub/s.go:"} {
			if !strings.Contains(stdout, want) {
				t.Errorf("verbose stdout does not contain %q:\n%s", want, stdout)
			}
		}
	})

	t.Run("output is deterministic", func(t *testing.T) {
		_, first, _ := gomu(ctx, "-dir", dir, "-v", "-workers", "1")
		_, second, _ := gomu(ctx, "-dir", dir, "-v", "-workers", "4")
		if first != second {
			t.Errorf("output depends on -workers:\n%s\nvs\n%s", first, second)
		}
	})

	t.Run("threshold", func(t *testing.T) {
		if code, _, stderr := gomu(ctx, "-dir", dir, "-threshold", "66"); code != exitOK {
			t.Errorf("score 66.7 against 66: exit code = %d, want %d\n%s", code, exitOK, stderr)
		}
		if code, stdout, _ := gomu(ctx, "-dir", dir, "-threshold", "67"); code != exitBelowThreshold {
			t.Errorf("score 66.7 against 67: exit code = %d, want %d", code, exitBelowThreshold)
		} else if !strings.Contains(stdout, "Mutation score: 66.7%") {
			t.Errorf("the report must still be printed when the threshold fails:\n%s", stdout)
		}
	})

	t.Run("patterns narrow the files", func(t *testing.T) {
		code, stdout, stderr := gomu(ctx, "-dir", dir, "-v", "sub")
		if code != exitOK {
			t.Fatalf("exit code = %d\n%s", code, stderr)
		}
		if strings.Contains(stdout, "add.go") {
			t.Errorf("pattern sub also mutated add.go:\n%s", stdout)
		}
		if !strings.Contains(stdout, "Mutation score: 100.0% (1 of 1") {
			t.Errorf("unexpected score for sub only:\n%s", stdout)
		}
	})

	t.Run("a single file", func(t *testing.T) {
		code, stdout, _ := gomu(ctx, "-dir", dir, "add.go")
		if code != exitOK || !strings.Contains(stdout, "Killed 1 | Survived 1 | Timed out 0 | Not covered 1") {
			t.Errorf("code=%d stdout:\n%s", code, stdout)
		}
	})

	t.Run("nothing to mutate", func(t *testing.T) {
		empty := fixture(t, map[string]string{
			"go.mod": "module example.com/e\n\ngo 1.21\n",
			"doc.go": "package e\n",
		})
		code, stdout, _ := gomu(ctx, "-dir", empty)
		if code != exitOK || !strings.Contains(stdout, "No mutants found.") {
			t.Errorf("code=%d stdout:\n%s", code, stdout)
		}
		if code, _, _ := gomu(ctx, "-dir", empty, "-threshold", "50"); code != exitBelowThreshold {
			t.Errorf("a threshold with no evidence: exit code = %d, want %d", code, exitBelowThreshold)
		}
	})

	t.Run("bad pattern", func(t *testing.T) {
		code, _, stderr := gomu(ctx, "-dir", dir, "../outside")
		if code != exitError || !strings.Contains(stderr, "invalid pattern") {
			t.Errorf("code=%d stderr:\n%s", code, stderr)
		}
	})

	t.Run("canceled", func(t *testing.T) {
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if code, _, stderr := gomu(canceled, "-dir", dir); code != exitError || !strings.Contains(stderr, "canceled") {
			t.Errorf("code=%d stderr:\n%s", code, stderr)
		}
	})

	// Run last: nothing above may have touched the user's files.
	t.Run("source files are untouched", func(t *testing.T) {
		for name, want := range before {
			got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != want {
				t.Errorf("%s was modified", name)
			}
		}
	})
}

func TestFailingTestsStopBeforeMutating(t *testing.T) {
	t.Parallel()
	needGo(t)

	dir := fixture(t, map[string]string{
		"go.mod":      "module example.com/fx\n\ngo 1.21\n",
		"add.go":      "package fx\n\nfunc Add(a, b int) int { return a - b }\n",
		"add_test.go": fxAddTest[:strings.Index(fxAddTest, "func TestMax")],
	})

	code, stdout, stderr := gomu(context.Background(), "-dir", dir)
	if code != exitError {
		t.Errorf("exit code = %d, want %d", code, exitError)
	}
	if !strings.Contains(stderr, "fail before any change") {
		t.Errorf("stderr should explain the baseline failed:\n%s", stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing", stdout)
	}
}

func TestSyntaxErrorIsReportedNotSkipped(t *testing.T) {
	t.Parallel()
	needGo(t)

	dir := fixture(t, map[string]string{
		"go.mod": "module example.com/fx\n\ngo 1.21\n",
		"a.go":   "package fx\n\nfunc Add(a, b int) int { return a + }\n",
	})

	code, _, stderr := gomu(context.Background(), "-dir", dir)
	if code != exitError {
		t.Errorf("exit code = %d, want %d\n%s", code, exitError, stderr)
	}
}
