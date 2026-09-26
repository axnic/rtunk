package render

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/trunk/engine"
)

var t0 = time.UnixMilli(999_999_996_800) // Dots and Sand both sit on frame index 0 at this instant

func liveOf(kind Kind, events ...engine.Event) *liveState {
	s := newLiveState(kind, t0.Add(-3200*time.Millisecond))
	for _, ev := range events {
		s.apply(ev)
	}
	return s
}

func plan(linter string, total int, batch bool) engine.Event {
	return engine.Event{Linter: linter, Phase: engine.Planned, Total: total, Batch: batch}
}
func running(linter, file string) engine.Event {
	return engine.Event{Linter: linter, Phase: engine.Running, File: file}
}
func jobDone(linter, file string) engine.Event {
	return engine.Event{Linter: linter, Phase: engine.JobDone, File: file}
}

func TestBrailleBarSubSteps(t *testing.T) {
	// one cell, 6 sub-steps: each step fills one more dot pair of the glyph table
	want := []string{"⣀", "⣄", "⣤", "⣦", "⣶", "⣷", "⣿"}
	for step, glyph := range want {
		assert.Equal(t, glyph, brailleBar(step, 6, 1), "step %d/6", step)
	}
	assert.Equal(t, "⣀⣀⣀", brailleBar(0, 0, 3), "total 0 renders an empty bar")
	assert.Equal(t, "⣿⣿⣿⣦⣀", brailleBar(7, 10, 5), "7/10 of 5 cells = 21 of 30 steps: 3 full cells, then state 3")
	assert.Equal(t, "⣿⣿", brailleBar(9, 3, 2), "clamped at full")
	assert.Equal(t, "[==  ]", asciiBar(1, 2, 6))
}

func TestHeader(t *testing.T) {
	s := liveOf(Check, plan("a", 31, false))
	for i := 0; i < 12; i++ {
		s.apply(jobDone("a", "f"))
	}
	f := s.frame(60, 10, t0, false)
	require.NotEmpty(t, f)
	assert.Equal(t, "Checking   39% "+strings.Repeat("⣿", 12)+strings.Repeat("⣀", 19)+" 12/31 · 3.2s", f[0])

	fmtBar := strings.Repeat("⣀", 59-14-2-utf8.RuneCountInString("0/4 · 3.2s"))
	assert.Equal(t, "Formatting  0% "+fmtBar+" 0/4 · 3.2s",
		liveOf(Fmt, plan("a", 4, false)).frame(60, 10, t0, false)[0], "Formatting verb, 0%")
	assert.Equal(t, "Formatting  0% "+fmtBar+" 0/4 · 3.2s",
		liveOf(FmtCheck, plan("a", 4, false)).frame(60, 10, t0, false)[0])

	done := liveOf(Check, plan("a", 2, false), running("a", "f"), jobDone("a", "f"), running("a", "g"))
	done.apply(jobDone("a", "g"))
	done.apply(engine.Event{Linter: "a", Phase: engine.Done})
	assert.Nil(t, done.frame(60, 10, t0, false), "everything finished and nothing in flight: quiescent")
}

func TestHeaderNarrowDropsTheBar(t *testing.T) {
	s := liveOf(Check, plan("a", 31, false))
	for i := 0; i < 12; i++ {
		s.apply(jobDone("a", "f"))
	}
	assert.Equal(t, "Checking   39% 12/31 · 3.2s", s.frame(39, 10, t0, false)[0])
	long := s.frame(10, 10, t0, false)[0]
	assert.LessOrEqual(t, utf8.RuneCountInString(long), 9, "truncated to width-1")
}

func TestPerFileLinterTree(t *testing.T) {
	s := liveOf(Check,
		plan("markdownlint", 47, false),
		running("markdownlint", "docs/a.md"), running("markdownlint", "docs/b.md"),
	)
	for i := 0; i < 7; i++ {
		s.apply(jobDone("markdownlint", "x"))
	}
	f := s.frame(80, 10, t0, false)
	assert.Equal(t, []string{
		"Checking   15% " + brailleBar(7, 47, 80-1-14-2-utf8.RuneCountInString("7/47 · 3.2s")) + " 7/47 · 3.2s",
		"  markdownlint  ⣶⣀⣀⣀⣀ 7/47",
		"    ⠋ docs/a.md",
		"    ⠋ docs/b.md",
	}, f)

	s.apply(jobDone("markdownlint", "docs/a.md"))
	f = s.frame(80, 10, t0, false)
	assert.NotContains(t, strings.Join(f, "\n"), "docs/a.md", "a file leaves on JobDone")
	assert.Contains(t, strings.Join(f, "\n"), "docs/b.md")

	for _, ph := range []engine.Phase{engine.Done, engine.Skipped, engine.Failed} {
		x := liveOf(Check, plan("l", 2, false), running("l", "f"))
		x.apply(engine.Event{Linter: "l", Phase: ph})
		assert.NotContains(t, strings.Join(x.frame(80, 10, t0, false), "\n"), "  l ", "row removed on %v", ph)
	}
}

func TestBatchLinterLine(t *testing.T) {
	s := liveOf(Check, plan("golangci-lint", 1, true), running("golangci-lint", "a.go, b.go, c.go, d.go"))
	f := s.frame(80, 10, t0, false)
	require.Len(t, f, 3)
	assert.Equal(t, "  golangci-lint  ⠋", f[1], "spinner only, no mini-bar or counter")
	assert.Equal(t, "    a.go, b.go (+2)", f[2])

	two := liveOf(Check, plan("g", 1, true), running("g", "a.go, b.go"))
	assert.Equal(t, "    a.go, b.go", two.frame(80, 10, t0, false)[2], "no (+N) when N is 0")
}

func TestInstallRows(t *testing.T) {
	s := liveOf(Check,
		plan("l", 1, false), running("l", "f"),
		engine.Event{Phase: engine.InstallStart, Item: "tools/shfmt", BytesTotal: -1},
	)
	f := s.frame(80, 10, t0, false)
	assert.Equal(t, "↓ shfmt  ⠁", f[len(f)-1], "spinner alone without a total")
	assert.Contains(t, f[0], "0/2", "the header total grows on InstallStart")

	s.apply(engine.Event{Phase: engine.InstallProgress, Item: "tools/shfmt", Bytes: 4_300_000, BytesTotal: 12_582_912})
	f = s.frame(80, 10, t0, false)
	assert.Equal(t, "↓ shfmt  ⠁ 4.1/12 MB", f[len(f)-1])

	s.apply(engine.Event{Phase: engine.InstallDone, Item: "tools/shfmt"})
	assert.NotContains(t, strings.Join(s.frame(80, 10, t0, false), "\n"), "shfmt")
	assert.Contains(t, s.frame(80, 10, t0, false)[0], "1/2")

	s.apply(engine.Event{Phase: engine.InstallDone, Item: "tools/ghost"})
	assert.Contains(t, s.frame(80, 10, t0, false)[0], "1/2", "an unknown InstallDone never counts")
}

func TestNamesAlignAcrossLintersAndInstalls(t *testing.T) {
	s := liveOf(Check, plan("go", 2, false), running("go", "a.go"),
		engine.Event{Phase: engine.InstallStart, Item: "tools/shfmt", BytesTotal: -1})
	f := s.frame(80, 10, t0, false)
	assert.Equal(t, "  go     ⣀⣀⣀⣀⣀ 0/2", f[1])
	assert.Equal(t, "↓ shfmt  ⠁", f[3])
}

func TestFailureCreditReaches100Percent(t *testing.T) {
	s := liveOf(Check, plan("l", 3, false), running("l", "a"), jobDone("l", "a"))
	s.apply(engine.Event{Linter: "l", Phase: engine.Failed})
	assert.Nil(t, s.frame(80, 10, t0, false), "the 2 unrun jobs are credited: 3/3, quiescent")
	assert.Equal(t, s.total, s.done)
}

func TestReplanResetsTheRowAndGrowsTheTotal(t *testing.T) {
	s := liveOf(Fmt, plan("l", 2, false), running("l", "a"), jobDone("l", "a"), engine.Event{Linter: "l", Phase: engine.Done})
	s.apply(plan("l", 2, false))
	s.apply(running("l", "a"))
	f := s.frame(80, 10, t0, false)
	assert.Contains(t, f[1], "0/2", "the per-linter bar restarts")
	assert.Contains(t, f[0], "2/4", "round one is complete (2 of 4); the header total keeps growing")
}

func TestOverflowFoldsIntoWorkers(t *testing.T) {
	s := liveOf(Check,
		plan("a", 3, false), running("a", "1"), running("a", "2"),
		plan("b", 3, false), running("b", "1"),
		plan("c", 3, false), running("c", "1"),
		plan("d", 3, false), running("d", "1"),
	)
	f := s.frame(80, 5, t0, false)
	assert.LessOrEqual(t, len(f), 5)
	assert.Equal(t, "  +3 workers", f[len(f)-1], "b, c and d are folded (a fits)")
	assert.Contains(t, strings.Join(f, "\n"), "  a ")

	tiny := s.frame(80, 1, t0, false)
	assert.LessOrEqual(t, len(tiny), 3, "height is clamped to 3, never below")
	assert.GreaterOrEqual(t, len(tiny), 1)
}

func TestFirstOversizedBlockIsTrimmed(t *testing.T) {
	s := liveOf(Check, plan("a", 9, false),
		running("a", "1"), running("a", "2"), running("a", "3"), running("a", "4"))
	f := s.frame(80, 3, t0, false)
	require.Len(t, f, 3)
	assert.Equal(t, "  a  ⣀⣀⣀⣀⣀ 0/9", f[1])
	assert.Equal(t, "    ⠋ 1", f[2], "as many file lines as fit, trimmed lines are not counted")
}

func TestShortenPath(t *testing.T) {
	p := "docs/superpowers/plans/2026-09-26-run-logs.md"
	assert.Equal(t, p, shortenPath(p, 100, false))
	assert.Equal(t, "docs/superpowers…/2026-09-26-run-logs.md", shortenPath(p, 40, false))
	assert.Equal(t, "docs/superpowers.../2026-09-26-run-logs.md", shortenPath(p, 42, true))
	assert.LessOrEqual(t, utf8.RuneCountInString(shortenPath(p, 10, false)), 10)
}

func TestLinesNeverExceedTheWidth(t *testing.T) {
	long := strings.Repeat("dir/", 60) + "file.go"
	s := liveOf(Check, plan("l", 2, false), running("l", long))
	for _, w := range []int{20, 40, 80} {
		for _, line := range s.frame(w, 10, t0, false) {
			assert.LessOrEqual(t, utf8.RuneCountInString(line), w-1, "width %d: %q", w, line)
		}
	}
}

func TestASCIIFrame(t *testing.T) {
	s := liveOf(Check, plan("l", 2, false), running("l", "a.go"),
		engine.Event{Phase: engine.InstallStart, Item: "tools/x", BytesTotal: -1})
	f := s.frame(60, 10, t0, true)
	assert.Regexp(t, `^Checking {4}0% \[ +\] 0/3 - 3\.2s$`, f[0])
	assert.Equal(t, "  l  [     ] 0/2", f[1])
	assert.Equal(t, "    | a.go", f[2])
	assert.Equal(t, "v x  |", f[3])
}

func TestSpinnerFramesFollowTheClock(t *testing.T) {
	assert.Equal(t, "⠋", dotsFrame(t0))
	assert.Equal(t, "⠋", dotsFrame(t0.Add(79*time.Millisecond)))
	assert.Equal(t, "⠙", dotsFrame(t0.Add(80*time.Millisecond)), "one Dots step per 80 ms")
	assert.Equal(t, "⠁", sandFrame(t0))
	assert.Equal(t, "⠂", sandFrame(t0.Add(70*time.Millisecond)), "one Sand step per 70 ms")
	assert.Equal(t, "⠁", sandFrame(t0.Add(35*70*time.Millisecond)), "Sand has 35 frames")
}

func TestFormatters(t *testing.T) {
	assert.Equal(t, "3.2s", formatElapsed(3200*time.Millisecond))
	assert.Equal(t, "1m04s", formatElapsed(64*time.Second))
	assert.Equal(t, "4.1/12 MB", formatBytes(4_300_000, 12_582_912))
	assert.Equal(t, "512.0/900.0 B", formatBytes(512, 900))
}
