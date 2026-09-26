package render

import (
	"bytes"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScreenDrawSequences(t *testing.T) {
	var buf bytes.Buffer
	s := &screen{w: &buf}

	s.draw([]string{"a", "b"})
	assert.Equal(t, "\r\x1b[2Ka\n\x1b[2Kb\n", buf.String(), "first draw: no cursor-up")
	assert.Equal(t, 2, s.lines)

	buf.Reset()
	s.draw([]string{"c", "d"})
	assert.Equal(t, "\x1b[2A\r\x1b[2Kc\n\x1b[2Kd\n", buf.String(), "redraw in place")

	buf.Reset()
	s.draw([]string{"x"})
	assert.Equal(t, "\x1b[2A\r\x1b[2Kx\n\x1b[2K\n\x1b[1A", buf.String(), "the leftover line is erased and the cursor goes back up by exactly that many")
	assert.Equal(t, 1, s.lines)

	buf.Reset()
	s.clear()
	assert.Equal(t, "\x1b[1A\r\x1b[2K\n\x1b[1A", buf.String())
	assert.Equal(t, 0, s.lines)

	buf.Reset()
	s.clear()
	assert.Empty(t, buf.String(), "clearing an empty area writes nothing")
}

func TestScreenNeverHidesTheCursor(t *testing.T) {
	var buf bytes.Buffer
	s := &screen{w: &buf}
	s.draw([]string{"a"})
	s.draw([]string{"b", "c"})
	s.clear()
	assert.NotContains(t, buf.String(), "\x1b[?25")
}

func TestScreenWritesEachDrawInOneWrite(t *testing.T) {
	w := &countingWriter{}
	s := &screen{w: w}
	s.draw([]string{"a", "b", "c"})
	s.draw([]string{"a"})
	assert.Equal(t, 2, w.writes, "one Write per redraw: no tearing")
}

type countingWriter struct{ writes int }

func (c *countingWriter) Write(p []byte) (int, error) { c.writes++; return len(p), nil }

func TestTermSizeFallsBackOnANonTerminal(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "x")
	require.NoError(t, err)
	cols, rows := TermSize(f)
	assert.Equal(t, 80, cols)
	assert.Equal(t, 24, rows)
}
