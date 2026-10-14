package gotest

import (
	"errors"
	"io/fs"
	"testing"
)

func TestCause(t *testing.T) {
	t.Parallel()

	inner := errors.New("permission denied")
	other := errors.New("something else")

	if got := cause(&fs.PathError{Op: "open", Path: "/secret/dir", Err: inner}); got != inner {
		t.Errorf("cause(PathError) = %v, want the inner error without the path", got)
	}
	if got := cause(other); got != other {
		t.Errorf("cause(other) = %v, want it unchanged", got)
	}
}

func TestEnvTempDir(t *testing.T) {
	t.Setenv("TMPDIR", "/parent/tmp")
	t.Setenv("TMP", "/parent/tmp")
	t.Setenv("TEMP", "/parent/tmp")
	t.Setenv("GOMU_SECRET", "x")

	base := &Runner{}
	has := func(env []string, want string) bool {
		for _, kv := range env {
			if kv == want {
				return true
			}
		}
		return false
	}

	if env := base.env(); !has(env, "TMPDIR=/parent/tmp") || has(env, "GOMU_SECRET=x") {
		t.Errorf("default env = %v, want the inherited TMPDIR and no secrets", env)
	}

	r, err := base.WithTempDir("/private/tmp-for-tests")
	if err != nil {
		t.Fatal(err)
	}
	env := r.env()
	for _, want := range []string{"TMPDIR=/private/tmp-for-tests", "TMP=/private/tmp-for-tests", "TEMP=/private/tmp-for-tests"} {
		if !has(env, want) {
			t.Errorf("env lacks %q: %v", want, env)
		}
	}
	if has(env, "TMPDIR=/parent/tmp") || has(env, "TMP=/parent/tmp") || has(env, "TEMP=/parent/tmp") || has(env, "GOMU_SECRET=x") {
		t.Errorf("env keeps the parent temp dir or a secret: %v", env)
	}
	if base.tmp != "" {
		t.Error("WithTempDir changed the receiver")
	}
}

func TestWithTempDirRejectsRelativePaths(t *testing.T) {
	t.Parallel()

	for _, dir := range []string{"", "tmp", "./tmp", "../tmp"} {
		if _, err := (&Runner{}).WithTempDir(dir); !errors.Is(err, ErrInvalidJob) {
			t.Errorf("WithTempDir(%q) error = %v, want %v", dir, err, ErrInvalidJob)
		}
	}
}
