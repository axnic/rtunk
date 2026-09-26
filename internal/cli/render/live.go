package render

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/xunleii/rtunk/pkg/trunk/engine"
)

// Spinner frame sets (docs/ux.md "Spinners"); the frame is picked from the clock so every row of a
// kind stays in sync and frame() stays pure.
var (
	dotsFrames  = strings.Fields("⠋ ⠙ ⠹ ⠸ ⠼ ⠴ ⠦ ⠧ ⠇ ⠏")
	sandFrames  = strings.Fields("⠁ ⠂ ⠄ ⡀ ⡈ ⡐ ⡠ ⣀ ⣁ ⣂ ⣄ ⣌ ⣔ ⣤ ⣥ ⣦ ⣮ ⣶ ⣷ ⣿ ⡿ ⠿ ⢟ ⠟ ⡛ ⠛ ⠫ ⢋ ⠋ ⠍ ⡉ ⠉ ⠑ ⠡ ⢁")
	asciiFrames = strings.Split(`|/-\`, "")
)

const (
	dotsInterval = 80 * time.Millisecond
	sandInterval = 70 * time.Millisecond
	miniCells    = 5
	maxNameWidth = 20
)

func spinFrame(now time.Time, interval time.Duration, frames []string) string {
	return frames[int(now.UnixMilli()/interval.Milliseconds())%len(frames)]
}

func dotsFrame(now time.Time) string { return spinFrame(now, dotsInterval, dotsFrames) }
func sandFrame(now time.Time) string { return spinFrame(now, sandInterval, sandFrames) }

// brailleStates are the cell states, empty to full: the dot pairs filled bottom-up, keeping the
// empty cell visible as ⣀. Six sub-steps per cell.
var brailleStates = []string{"⣀", "⣄", "⣤", "⣦", "⣶", "⣷", "⣿"}

// brailleBar renders done/total over cells cells: the first steps/6 cells are full, the next one
// holds the remainder, the rest are empty. total <= 0 is an empty bar; done is clamped.
func brailleBar(done, total, cells int) string {
	if cells <= 0 {
		return ""
	}
	steps := 0
	if total > 0 {
		steps = min(done*cells*6/total, cells*6)
	}
	var b strings.Builder
	for i := 0; i < cells; i++ {
		switch {
		case i < steps/6:
			b.WriteString(brailleStates[6])
		case i == steps/6:
			b.WriteString(brailleStates[steps%6])
		default:
			b.WriteString(brailleStates[0])
		}
	}
	return b.String()
}

// asciiBar is "[==  ]": cells includes the brackets, one step per inner cell.
func asciiBar(done, total, cells int) string {
	inner := cells - 2
	if inner < 0 {
		inner = 0
	}
	filled := 0
	if total > 0 {
		filled = min(done*inner/total, inner)
	}
	return "[" + strings.Repeat("=", filled) + strings.Repeat(" ", inner-filled) + "]"
}

func formatElapsed(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	s := int(d.Seconds())
	return fmt.Sprintf("%dm%02ds", s/60, s%60)
}

// formatBytes is "4.1/12 MB": units scale by 1024 from the total's magnitude, the received value
// has one decimal, the total none from 10 up.
func formatBytes(got, total int64) string {
	units := []string{"B", "KB", "MB", "GB"}
	div, u := 1.0, 0
	for float64(total) >= div*1024 && u < len(units)-1 {
		div *= 1024
		u++
	}
	t := float64(total) / div
	tf := "%.1f"
	if t >= 10 && u > 0 {
		tf = "%.0f"
	}
	return fmt.Sprintf("%.1f/"+tf+" %s", float64(got)/div, t, units[u])
}

// shortenPath cuts p to at most max runes in the middle, keeping the base name whole when possible.
func shortenPath(p string, max int, ascii bool) string {
	if utf8.RuneCountInString(p) <= max {
		return p
	}
	ell := "…"
	if ascii {
		ell = "..."
	}
	base := p[strings.LastIndex(p, "/")+1:]
	dir := strings.TrimSuffix(p[:len(p)-len(base)], "/")
	avail := max - utf8.RuneCountInString(ell) - 1 - utf8.RuneCountInString(base)
	if avail < 0 || dir == "" {
		return truncate(p, max-utf8.RuneCountInString(ell)) + ell
	}
	return truncate(dir, avail) + ell + "/" + base
}

// truncate keeps the first n runes of s.
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

type lintRow struct {
	total, jobDone int
	batch          bool
	seen           bool     // a Running event arrived: a worker is on it, so it is drawn
	inflight       []string // Running.File strings (batches or files) in arrival order
}

type installRow struct {
	item         string
	bytes, total int64
}

// liveState is everything the live area draws, mutated by apply and read by frame.
type liveState struct {
	kind        Kind
	start       time.Time
	total, done int
	linters     map[string]*lintRow
	installs    []*installRow
}

func newLiveState(kind Kind, start time.Time) *liveState {
	return &liveState{kind: kind, start: start, linters: map[string]*lintRow{}}
}

func (s *liveState) apply(ev engine.Event) {
	switch ev.Phase {
	case engine.Planned:
		s.total += ev.Total
		s.linters[ev.Linter] = &lintRow{total: ev.Total, batch: ev.Batch} // a re-plan replaces the row
	case engine.Running:
		r := s.linters[ev.Linter]
		if r == nil {
			r = &lintRow{}
			s.linters[ev.Linter] = r
		}
		r.seen = true
		r.inflight = append(r.inflight, ev.File)
	case engine.JobDone:
		s.done++
		if r := s.linters[ev.Linter]; r != nil {
			r.jobDone++
			for i, f := range r.inflight {
				if f == ev.File {
					r.inflight = append(r.inflight[:i], r.inflight[i+1:]...)
					break
				}
			}
		}
	case engine.InstallStart:
		s.total++
		s.installs = append(s.installs, &installRow{item: ev.Item, total: -1})
	case engine.InstallProgress:
		for _, r := range s.installs {
			if r.item == ev.Item {
				r.bytes, r.total = ev.Bytes, ev.BytesTotal
			}
		}
	case engine.InstallDone:
		for i, r := range s.installs {
			if r.item == ev.Item {
				s.done++
				s.installs = append(s.installs[:i], s.installs[i+1:]...)
				break
			}
		}
	case engine.Done, engine.Failed:
		if r := s.linters[ev.Linter]; r != nil {
			s.done += max(r.total-r.jobDone, 0) // the jobs a failed linter never ran still count
			delete(s.linters, ev.Linter)
		}
	case engine.Skipped:
		delete(s.linters, ev.Linter)
	}
}

// frame is the live area for a terminal of width x height, at time now. At most max(height, 3)
// lines, each at most width-1 runes (the last column stays free so nothing auto-wraps). It returns
// nil when there is nothing in flight and everything planned is finished.
func (s *liveState) frame(width, height int, now time.Time, ascii bool) []string {
	height = max(height, 3)
	limit := max(width-1, 1)

	names := make([]string, 0, len(s.linters))
	for name, r := range s.linters {
		if r.seen {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) == 0 && len(s.installs) == 0 && s.done >= s.total {
		return nil
	}

	nameW := 0
	for _, n := range names {
		nameW = max(nameW, utf8.RuneCountInString(n))
	}
	for _, r := range s.installs {
		nameW = max(nameW, utf8.RuneCountInString(installName(r.item)))
	}
	nameW = min(nameW, maxNameWidth)

	spin := func() string {
		if ascii {
			return spinFrame(now, dotsInterval, asciiFrames)
		}
		return dotsFrame(now)
	}
	sandSpin := func() string {
		if ascii {
			return spinFrame(now, dotsInterval, asciiFrames)
		}
		return sandFrame(now)
	}

	var blocks [][]string
	for _, name := range names {
		r := s.linters[name]
		head := "  " + pad(name, nameW) + "  "
		var blk []string
		if r.batch {
			blk = append(blk, head+spin())
			blk = append(blk, "    "+batchLine(r.inflight))
		} else {
			line := head
			if r.total > 0 {
				bar := brailleBar(r.jobDone, r.total, miniCells)
				if ascii {
					bar = asciiBar(r.jobDone, r.total, miniCells+2)
				}
				line += fmt.Sprintf("%s %d/%d", bar, r.jobDone, r.total)
			} else {
				line += spin()
			}
			blk = append(blk, line)
			for _, f := range r.inflight {
				blk = append(blk, "    "+spin()+" "+shortenPath(f, limit-6, ascii))
			}
		}
		blocks = append(blocks, blk)
	}
	for _, r := range s.installs {
		glyph := "↓"
		if ascii {
			glyph = "v"
		}
		line := glyph + " " + pad(installName(r.item), nameW) + "  " + sandSpin()
		if r.total >= 0 && r.bytes > 0 {
			line += " " + formatBytes(r.bytes, r.total)
		}
		blocks = append(blocks, []string{line})
	}

	out := []string{s.header(limit, now, ascii)}
	remaining := height - 1
	for i, blk := range blocks {
		more := len(blocks) - i - 1
		reserve := 0
		if more > 0 {
			reserve = 1
		}
		if len(blk)+reserve <= remaining {
			out = append(out, blk...)
			remaining -= len(blk)
			continue
		}
		show := remaining - reserve
		hidden := more
		if show < 1 {
			show = 0
			hidden = more + 1
		}
		out = append(out, blk[:min(show, len(blk))]...)
		if hidden > 0 {
			out = append(out, fmt.Sprintf("  +%d workers", hidden))
		}
		break
	}
	for i := range out {
		out[i] = truncate(out[i], limit)
	}
	return out
}

func installName(item string) string {
	return item[strings.LastIndex(item, "/")+1:]
}

func pad(s string, w int) string {
	s = truncate(s, w)
	return s + strings.Repeat(" ", w-utf8.RuneCountInString(s))
}

// batchLine is "a, b (+N)": the first two in-flight files, N the rest (omitted at 0).
func batchLine(inflight []string) string {
	var files []string
	for _, b := range inflight {
		files = append(files, strings.Split(b, ", ")...)
	}
	if len(files) <= 2 {
		return strings.Join(files, ", ")
	}
	return fmt.Sprintf("%s (+%d)", strings.Join(files[:2], ", "), len(files)-2)
}

// header is "<verb> <pct>% <bar> <done>/<total> · <elapsed>"; the bar takes the free columns and
// is dropped below 40 columns.
func (s *liveState) header(limit int, now time.Time, ascii bool) string {
	verb := "Checking"
	if s.kind != Check {
		verb = "Formatting"
	}
	pct := 0
	if s.total > 0 {
		pct = (100*s.done + s.total/2) / s.total
	}
	sep := "·"
	if ascii {
		sep = "-"
	}
	prefix := fmt.Sprintf("%-10s%3d%%", verb, pct)
	suffix := fmt.Sprintf("%d/%d %s %s", s.done, s.total, sep, formatElapsed(now.Sub(s.start)))
	cells := limit - utf8.RuneCountInString(prefix) - 1 - 1 - utf8.RuneCountInString(suffix)
	if limit+1 < 40 || cells < 1 {
		return prefix + " " + suffix
	}
	bar := brailleBar(s.done, s.total, cells)
	if ascii {
		bar = asciiBar(s.done, s.total, cells)
	}
	return prefix + " " + bar + " " + suffix
}

// liveTick is the redraw period (about 10 Hz); a variable so tests can speed it up.
var liveTick = 100 * time.Millisecond

// LiveOptions configures NewLive.
type LiveOptions struct {
	Out     io.Writer               // the live area is drawn here (stderr)
	Size    func() (cols, rows int) // terminal size, queried every tick (so a resize needs no SIGWINCH handler)
	Height  int                     // --live-height; <= 0 means half the terminal height (at least 3)
	ASCII   bool
	Command Kind
	Now     func() time.Time // clock, time.Now by default
	Signals <-chan os.Signal // nil: register for os.Interrupt; tests inject one
	Raise   func()           // re-sends SIGINT to the process after erasing the area; default os.Interrupt to self
}

// live is the decorator: it feeds every event to the live state and to the inner renderer, redraws
// the area on a ticker, and erases it before the inner renderer writes its report.
type live struct {
	inner Renderer
	opts  LiveOptions

	mu    sync.Mutex
	state *liveState
	scr   *screen

	stop     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
	sigCh    chan os.Signal // the channel this decorator registered, nil when injected
}

// NewLive wraps inner with the live view. inner must be built with NoProgress so its stderr lines
// do not collide with the area.
func NewLive(inner Renderer, opts LiveOptions) Renderer {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Size == nil {
		opts.Size = func() (int, int) { return 80, 24 }
	}
	if opts.Raise == nil {
		opts.Raise = func() {
			if p, err := os.FindProcess(os.Getpid()); err == nil {
				_ = p.Signal(os.Interrupt)
			}
		}
	}
	l := &live{
		inner: inner, opts: opts,
		state: newLiveState(opts.Command, opts.Now()), scr: &screen{w: opts.Out},
		stop: make(chan struct{}),
	}
	sigs := opts.Signals
	if sigs == nil {
		l.sigCh = make(chan os.Signal, 1)
		signal.Notify(l.sigCh, os.Interrupt)
		sigs = l.sigCh
	}
	l.wg.Add(1)
	go l.loop(sigs)
	return l
}

func (l *live) loop(sigs <-chan os.Signal) {
	defer l.wg.Done()
	t := time.NewTicker(liveTick)
	defer t.Stop()
	for {
		select {
		case <-l.stop:
			return
		case <-sigs:
			l.mu.Lock()
			l.scr.clear()
			l.mu.Unlock()
			if l.sigCh != nil {
				signal.Stop(l.sigCh) // default SIGINT handling is back once the last handler is gone
			}
			l.opts.Raise()
			return
		case <-t.C:
			cols, rows := l.opts.Size()
			height := l.opts.Height
			if height <= 0 {
				height = rows / 2
			}
			l.mu.Lock()
			l.scr.draw(l.state.frame(cols, max(height, 3), l.opts.Now(), l.opts.ASCII))
			l.mu.Unlock()
		}
	}
}

func (l *live) Event(ev engine.Event) {
	l.mu.Lock()
	l.state.apply(ev)
	l.mu.Unlock()
	l.inner.Event(ev)
}

func (l *live) Close(s Summary) error {
	l.stopOnce.Do(func() { close(l.stop) })
	l.wg.Wait()
	if l.sigCh != nil {
		signal.Stop(l.sigCh)
	}
	l.mu.Lock()
	l.scr.clear()
	l.mu.Unlock()
	return l.inner.Close(s)
}
