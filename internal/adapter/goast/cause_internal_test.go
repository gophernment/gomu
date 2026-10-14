package goast

import (
	"errors"
	"io/fs"
	"testing"
)

func TestCause(t *testing.T) {
	t.Parallel()

	plain := errors.New("plain")
	inner := errors.New("denied")

	if got := cause(plain); got != plain {
		t.Errorf("cause(plain error) = %v, want it unchanged", got)
	}
	if got := cause(&fs.PathError{Op: "open", Path: "/secret", Err: inner}); got != inner {
		t.Errorf("cause(PathError) = %v, want the inner error", got)
	}
}
