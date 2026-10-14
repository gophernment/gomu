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
