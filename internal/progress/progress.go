// Package progress renders a live "Checking NN% [===>] done/total Ts" bar
// with the currently in-flight (linter/command: target) jobs listed below
// it, trunk-style — only when stdout is a real terminal (auto-detected, or
// forced off via --no-progress).
package progress

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
)

const barWidth = 40

// Tracker is safe for concurrent Start/Done calls from multiple goroutines
// (SPECS.md §7.4 — jobs run in parallel across a bounded pool).
type Tracker struct {
	mu        sync.Mutex
	w         io.Writer
	label     string
	total     int
	done      int
	active    []string
	start     time.Time
	lastLines int
	enabled   bool
}

// New creates a Tracker for total jobs. Rendering is a no-op (all methods
// safe, just inert) unless w is a real terminal and total > 0.
func New(w io.Writer, label string, total int) *Tracker {
	enabled := total > 0 && isTerminal(w)
	return &Tracker{w: w, label: label, total: total, start: time.Now(), enabled: enabled}
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(interface{ Fd() uintptr })
	return ok && term.IsTerminal(int(f.Fd()))
}

// Start marks name as in-flight and redraws.
func (t *Tracker) Start(name string) {
	if !t.enabled {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.active = append(t.active, name)
	t.render()
}

// Done marks name as finished and redraws.
func (t *Tracker) Done(name string) {
	if !t.enabled {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for i, n := range t.active {
		if n == name {
			t.active = append(t.active[:i], t.active[i+1:]...)
			break
		}
	}
	t.done++
	t.render()
}

// Finish clears the live block so the final report prints on a clean terminal.
func (t *Tracker) Finish() {
	if !t.enabled {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.clear()
}

func (t *Tracker) render() {
	t.clear()
	pct := 0
	if t.total > 0 {
		pct = t.done * 100 / t.total
	}
	filled := barWidth * t.done / maxInt(t.total, 1)
	bar := strings.Repeat("=", filled) + strings.Repeat(" ", barWidth-filled)
	elapsed := time.Since(t.start).Round(time.Second)
	fmt.Fprintf(t.w, "%s %3d%% [%s] %d/%d %s\n", t.label, pct, bar, t.done, t.total, elapsed)
	lines := 1
	for _, name := range t.active {
		fmt.Fprintf(t.w, "  %s\n", name)
		lines++
	}
	t.lastLines = lines
}

func (t *Tracker) clear() {
	for i := 0; i < t.lastLines; i++ {
		fmt.Fprint(t.w, "\x1b[1A\x1b[2K")
	}
	t.lastLines = 0
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
