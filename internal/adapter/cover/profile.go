// Package cover reads Go coverage profiles written by "go test -coverprofile",
// so Gomu can skip mutants on code no test executes.
package cover

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const (
	// MaxSize is the most bytes of profile Parse reads.
	MaxSize = 64 << 20
	// maxLine is the longest single line Parse accepts.
	maxLine = 1 << 20
)

// Errors returned by Parse.
var (
	// ErrFormat means the input is not a valid coverage profile.
	ErrFormat = errors.New("cover: invalid profile")
	// ErrTooLarge means the input is bigger than MaxSize.
	ErrTooLarge = errors.New("cover: profile is too large")
)

// block is a source range that some test executed. Start is inclusive and end
// is exclusive, both as 1-based line and column.
type block struct {
	startLine, startCol int
	endLine, endCol     int
}

func (b block) contains(line, col int) bool {
	if line < b.startLine || line > b.endLine {
		return false
	}
	if line == b.startLine && col < b.startCol {
		return false
	}
	if line == b.endLine && col >= b.endCol {
		return false
	}
	return true
}

// Profile records which source ranges were executed. Only blocks with a
// non-zero count are kept, so merged profiles work: a range counts as covered
// when any entry for it was hit.
type Profile struct {
	covered map[string][]block
}

// Covered reports whether the 1-based position line:col in the named file was
// executed. name is the file name exactly as written in the profile, which is
// the import path plus file name, such as "example.com/p/add.go".
//
// Code the compiler does not instrument, such as package-level variable
// initializers, is in no block and reports false. A nil Profile covers nothing.
func (p *Profile) Covered(name string, line, col int) bool {
	if p == nil {
		return false
	}
	for _, b := range p.covered[name] {
		if b.contains(line, col) {
			return true
		}
	}
	return false
}

// Parse reads a coverage profile. It reads at most MaxSize bytes and accepts
// lines up to 1 MiB. Errors name the offending line number, never its content.
func Parse(r io.Reader) (*Profile, error) {
	cr := &capReader{r: r, left: MaxSize}
	sc := bufio.NewScanner(cr)
	sc.Buffer(make([]byte, 0, 64<<10), maxLine)

	p := &Profile{covered: make(map[string][]block)}
	lineNo := 0
	sawMode := false

	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}

		if !sawMode {
			if err := parseMode(line); err != nil {
				return nil, fmt.Errorf("line %d: %w", lineNo, err)
			}
			sawMode = true
			continue
		}

		name, b, count, err := parseBlock(line)
		if err != nil {
			// The cap can cut the last line short; report the real cause.
			if cr.exceeded {
				return nil, ErrTooLarge
			}
			return nil, fmt.Errorf("line %d: %w", lineNo, err)
		}
		if count > 0 {
			p.covered[name] = append(p.covered[name], b)
		}
	}

	if err := sc.Err(); err != nil {
		if cr.exceeded {
			return nil, ErrTooLarge
		}
		return nil, fmt.Errorf("cover: read: %w", err)
	}
	if cr.exceeded {
		return nil, ErrTooLarge
	}
	if !sawMode {
		return nil, fmt.Errorf("%w: missing mode line", ErrFormat)
	}
	return p, nil
}

func parseMode(line string) error {
	mode, ok := strings.CutPrefix(line, "mode:")
	if !ok {
		return fmt.Errorf("%w: first line must be the mode", ErrFormat)
	}
	switch strings.TrimSpace(mode) {
	case "set", "count", "atomic":
		return nil
	default:
		return fmt.Errorf("%w: unknown mode", ErrFormat)
	}
}

// parseBlock parses "name:startLine.startCol,endLine.endCol numStmts count".
func parseBlock(line string) (name string, b block, count int64, err error) {
	bad := func(what string) (string, block, int64, error) {
		return "", block{}, 0, fmt.Errorf("%w: %s", ErrFormat, what)
	}

	fields := strings.Fields(line)
	if len(fields) != 3 {
		return bad("want 3 fields")
	}

	i := strings.LastIndexByte(fields[0], ':')
	if i <= 0 {
		return bad("missing file name")
	}
	name = fields[0][:i]

	start, end, ok := strings.Cut(fields[0][i+1:], ",")
	if !ok {
		return bad("missing range end")
	}
	if b.startLine, b.startCol, ok = parsePos(start); !ok {
		return bad("bad range start")
	}
	if b.endLine, b.endCol, ok = parsePos(end); !ok {
		return bad("bad range end")
	}
	if b.endLine < b.startLine || (b.endLine == b.startLine && b.endCol < b.startCol) {
		return bad("range ends before it starts")
	}

	stmts, perr := strconv.ParseInt(fields[1], 10, 64)
	if perr != nil || stmts < 0 {
		return bad("bad statement count")
	}
	count, perr = strconv.ParseInt(fields[2], 10, 64)
	if perr != nil || count < 0 {
		return bad("bad hit count")
	}
	return name, b, count, nil
}

// parsePos parses "line.col" where both are positive integers.
func parsePos(s string) (line, col int, ok bool) {
	l, c, found := strings.Cut(s, ".")
	if !found {
		return 0, 0, false
	}
	line, err1 := strconv.Atoi(l)
	col, err2 := strconv.Atoi(c)
	if err1 != nil || err2 != nil || line < 1 || col < 1 {
		return 0, 0, false
	}
	return line, col, true
}

// capReader passes through at most left bytes and then reports EOF, recording
// that the input was longer so Parse can tell "too large" from "complete".
type capReader struct {
	r        io.Reader
	left     int
	exceeded bool
}

func (c *capReader) Read(p []byte) (int, error) {
	if c.exceeded {
		return 0, io.EOF
	}
	if c.left == 0 {
		// Probe one byte: more data means the input exceeds the cap.
		var one [1]byte
		n, err := c.r.Read(one[:])
		if n > 0 {
			c.exceeded = true
			return 0, io.EOF
		}
		return 0, err
	}
	if len(p) > c.left {
		p = p[:c.left]
	}
	n, err := c.r.Read(p)
	c.left -= n
	return n, err
}
