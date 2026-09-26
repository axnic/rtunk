package render

import (
	"fmt"
	"io"
	"strings"
)

// screen redraws the live area in place with ANSI sequences. Every line of the area ends with a
// newline, so after a draw the cursor sits on the line below it. The cursor is never hidden:
// nothing has to be restored on exit or on a crash.
type screen struct {
	w     io.Writer
	lines int // lines currently drawn
}

// draw replaces the area with frame in a single Write: cursor up over the old area, then each new
// line erased-and-rewritten, then any leftover old lines erased and the cursor moved back up.
func (s *screen) draw(frame []string) {
	if s.lines == 0 && len(frame) == 0 {
		return
	}
	var b strings.Builder
	if s.lines > 0 {
		fmt.Fprintf(&b, "\x1b[%dA", s.lines)
	}
	b.WriteString("\r")
	for _, l := range frame {
		b.WriteString("\x1b[2K" + l + "\n")
	}
	if k := s.lines - len(frame); k > 0 {
		b.WriteString(strings.Repeat("\x1b[2K\n", k))
		fmt.Fprintf(&b, "\x1b[%dA", k)
	}
	s.lines = len(frame)
	_, _ = io.WriteString(s.w, b.String())
}

// clear erases the area; the cursor ends where the area started, ready for the report.
func (s *screen) clear() { s.draw(nil) }
