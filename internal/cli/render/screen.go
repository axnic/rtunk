package render

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// screen redraws the live area in place with ANSI sequences. Every line of the area ends with a
// newline, so after a draw the cursor sits on the line below it. The cursor is never hidden:
// nothing has to be restored on exit or on a crash.
type screen struct {
	w      io.Writer
	lines  int   // lines currently drawn
	widths []int // rune width of each drawn line
	cols   int   // terminal width for the next draw; 0 when unknown
}

// rows is how many terminal rows the drawn area occupies now: a terminal that reflows on resize
// wraps a line wider than the new width over several rows, so a narrowed window makes the area
// taller than s.lines.
func (s *screen) rows() int {
	if s.cols <= 0 {
		return s.lines
	}
	n := 0
	for _, w := range s.widths {
		n += max(1, (w+s.cols-1)/s.cols)
	}
	return n
}

// draw replaces the area with frame in a single Write: cursor up over the old area, then each new
// line erased-and-rewritten, then any leftover old lines erased and the cursor moved back up.
func (s *screen) draw(frame []string) {
	if s.lines == 0 && len(frame) == 0 {
		return
	}
	var b strings.Builder
	up := s.rows()
	if up > 0 {
		fmt.Fprintf(&b, "\x1b[%dA", up)
	}
	b.WriteString("\r")
	if up > s.lines {
		b.WriteString("\x1b[J") // reflowed rows: erase the whole old area, not line by line
		s.lines = 0
	}
	for _, l := range frame {
		b.WriteString("\x1b[2K" + l + "\n")
	}
	if k := s.lines - len(frame); k > 0 {
		b.WriteString(strings.Repeat("\x1b[2K\n", k))
		fmt.Fprintf(&b, "\x1b[%dA", k)
	}
	s.lines = len(frame)
	s.widths = s.widths[:0]
	for _, l := range frame {
		s.widths = append(s.widths, utf8.RuneCountInString(l))
	}
	_, _ = io.WriteString(s.w, b.String())
}

// clear erases the area; the cursor ends where the area started, ready for the report.
func (s *screen) clear() { s.draw(nil) }
