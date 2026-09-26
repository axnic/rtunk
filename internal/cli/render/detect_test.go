package render

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsUTF8Locale(t *testing.T) {
	cases := []struct {
		name                 string
		lcAll, lcCtype, lang string
		want                 bool
	}{
		{"LANG utf-8", "", "", "en_US.UTF-8", true},
		{"utf8 spelling", "", "", "fr_FR.utf8", true},
		{"UTF8 upper", "", "", "C.UTF8", true},
		{"LC_ALL wins over LANG", "C", "", "en_US.UTF-8", false},
		{"LC_CTYPE wins over LANG", "", "C", "en_US.UTF-8", false},
		{"LC_ALL beats LC_CTYPE", "en_US.UTF-8", "C", "C", true},
		{"first non-empty only", "", "", "POSIX", false},
		{"latin1", "", "", "en_US.ISO-8859-1", false},
		{"all empty", "", "", "", false},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, IsUTF8Locale(c.lcAll, c.lcCtype, c.lang), c.name)
	}
}

func TestLiveEnabled(t *testing.T) {
	assert.True(t, LiveEnabled(true, false, "xterm-256color"))
	assert.True(t, LiveEnabled(true, false, ""), "an unset TERM is not dumb")
	assert.False(t, LiveEnabled(true, false, "dumb"))
	assert.False(t, LiveEnabled(true, true, "xterm"), "--no-progress disables it")
	assert.False(t, LiveEnabled(false, false, "xterm"), "stderr is not a terminal")
}
