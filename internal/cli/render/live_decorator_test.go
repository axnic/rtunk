package render

import (
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/trunk/engine"
	"github.com/xunleii/rtunk/pkg/trunk/output"
)

// syncBuf is a mutex-guarded buffer: the redraw goroutine and the test both touch it.
type syncBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func fastTicks(t *testing.T) {
	t.Helper()
	old := liveTick
	liveTick = 5 * time.Millisecond
	t.Cleanup(func() { liveTick = old })
}

func runEvents() []engine.Event {
	return []engine.Event{
		{Linter: "lint", Phase: engine.Planned, Total: 2, Files: []string{"a.go"}},
		{Linter: "lint", Phase: engine.Running, File: "a.go"},
		{Linter: "lint", Phase: engine.JobDone, File: "a.go"},
		{Linter: "lint", Phase: engine.Done, Files: []string{"a.go"}, Findings: []output.Finding{{File: "a.go", Line: 1, Severity: "error", Message: "m"}}},
	}
}

func TestLive_CloseErasesTheAreaBeforeTheReportAndReportIsUnchanged(t *testing.T) {
	fastTicks(t)
	var shared syncBuf
	inner := New(&shared, io.Discard, Options{Command: Check, NoProgress: true})
	l := NewLive(inner, LiveOptions{Out: &shared, Size: func() (int, int) { return 80, 24 }, Command: Check, Signals: make(chan os.Signal)})

	l.Event(runEvents()[0])
	l.Event(runEvents()[1])
	time.Sleep(40 * time.Millisecond) // several ticks draw the area
	for _, ev := range runEvents()[2:] {
		l.Event(ev)
	}
	require.NoError(t, l.Close(Summary{Elapsed: time.Second}))
	out := shared.String()

	var alone strings.Builder
	ref := New(&alone, io.Discard, Options{Command: Check, NoProgress: true})
	for _, ev := range runEvents() {
		ref.Event(ev)
	}
	require.NoError(t, ref.Close(Summary{Elapsed: time.Second}))

	assert.Contains(t, out, "Checking", "the area was drawn while running")
	assert.True(t, strings.HasSuffix(out, alone.String()), "the report bytes equal the inner renderer alone")
	beforeReport := strings.TrimSuffix(out, alone.String())
	assert.Regexp(t, `\x1b\[\d+A$`, beforeReport, "the last thing before the report is the erase's cursor-up")
}

func TestLive_NothingIsWrittenAfterClose(t *testing.T) {
	fastTicks(t)
	var out syncBuf
	l := NewLive(New(io.Discard, io.Discard, Options{Command: Check, NoProgress: true}),
		LiveOptions{Out: &out, Size: func() (int, int) { return 80, 24 }, Command: Check, Signals: make(chan os.Signal)})
	l.Event(runEvents()[0])
	l.Event(runEvents()[1])
	time.Sleep(20 * time.Millisecond)
	require.NoError(t, l.Close(Summary{}))
	n := len(out.String())
	time.Sleep(40 * time.Millisecond)
	assert.Equal(t, n, len(out.String()), "no goroutine keeps drawing after Close")
}

func TestLive_ForwardsEventsToTheInnerRendererInOrder(t *testing.T) {
	fastTicks(t)
	var report strings.Builder
	l := NewLive(New(&report, io.Discard, Options{Command: Check, NoProgress: true}),
		LiveOptions{Out: io.Discard, Size: func() (int, int) { return 80, 24 }, Command: Check, Signals: make(chan os.Signal)})
	for _, ev := range runEvents() {
		l.Event(ev)
	}
	require.NoError(t, l.Close(Summary{}))
	assert.Contains(t, report.String(), "  (1)")
}

func TestLive_InterruptErasesTheAreaAndReRaises(t *testing.T) {
	fastTicks(t)
	var out syncBuf
	sigs := make(chan os.Signal, 1)
	var raised atomic.Int32
	l := NewLive(New(io.Discard, io.Discard, Options{Command: Check, NoProgress: true}), LiveOptions{
		Out: &out, Size: func() (int, int) { return 80, 24 }, Command: Check,
		Signals: sigs, Raise: func() { raised.Add(1) },
	})
	l.Event(runEvents()[0])
	l.Event(runEvents()[1])
	time.Sleep(30 * time.Millisecond)
	before := out.String()
	require.Contains(t, before, "Checking")

	sigs <- os.Interrupt
	require.Eventually(t, func() bool { return raised.Load() == 1 }, time.Second, 5*time.Millisecond)
	assert.Contains(t, strings.TrimPrefix(out.String(), before), "\x1b[2K", "the area is erased before the signal is re-raised")
	require.NoError(t, l.Close(Summary{}), "Close still works after the signal path exited")
	assert.Equal(t, int32(1), raised.Load())
}
