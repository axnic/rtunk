package render

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUseColor(t *testing.T) {
	assert.True(t, UseColor(true, ""))
	assert.False(t, UseColor(true, "1"), "any non-empty NO_COLOR disables color")
	assert.False(t, UseColor(false, ""), "not a terminal")
	assert.False(t, UseColor(false, "1"))
}

func TestPaint(t *testing.T) {
	assert.Equal(t, "x", paint(false, "31", "x"))
	assert.Equal(t, "\x1b[31mx\x1b[0m", paint(true, "31", "x"))
}
