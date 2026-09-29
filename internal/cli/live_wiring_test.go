package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/internal/cli/render"
	"github.com/xunleii/rtunk/pkg/run/engine"
)

func liveEvents() []engine.Event {
	return []engine.Event{
		{Linter: "lint", Phase: engine.Planned, Total: 1, Files: []string{"a.go"}},
		{Linter: "lint", Phase: engine.Running, File: "a.go"},
	}
}

func TestBuildRenderer_LiveOnATerminalStderr(t *testing.T) {
	var stdout, stderr strings.Builder
	r := buildRenderer("human", &stdout, &stderr, render.Check, progressOpts{}, true, "xterm-256color", true)
	for _, ev := range liveEvents() {
		r.Event(ev)
	}
	time.Sleep(250 * time.Millisecond) // a couple of 100 ms ticks draw the area
	r.Event(engine.Event{Linter: "lint", Phase: engine.JobDone, File: "a.go"})
	r.Event(engine.Event{Linter: "lint", Phase: engine.Done, Files: []string{"a.go"}})
	require.NoError(t, r.Close(render.Summary{}))

	assert.Contains(t, stderr.String(), "Checking", "the live area is on stderr")
	assert.NotContains(t, stderr.String(), "done ", "the plain progress lines are replaced, not duplicated")
	assert.Contains(t, stdout.String(), "Checked 1 file with 1 linter", "the report is unchanged")
	assert.NotContains(t, stdout.String(), "\x1b")
}

func TestBuildRenderer_FallsBackToPlainLines(t *testing.T) {
	events := append(liveEvents(),
		engine.Event{Linter: "lint", Phase: engine.JobDone, File: "a.go"},
		engine.Event{Linter: "lint", Phase: engine.Done, Files: []string{"a.go"}})

	for name, c := range map[string]struct {
		tty        bool
		term       string
		noProgress bool
		wantLines  bool
	}{
		"stderr is not a terminal": {tty: false, term: "xterm", wantLines: true},
		"TERM=dumb":                {tty: true, term: "dumb", wantLines: true},
		"--no-progress":            {tty: true, term: "xterm", noProgress: true, wantLines: false},
	} {
		var stdout, stderr strings.Builder
		r := buildRenderer("human", &stdout, &stderr, render.Check, progressOpts{NoProgress: c.noProgress}, c.tty, c.term, true)
		for _, ev := range events {
			r.Event(ev)
		}
		require.NoError(t, r.Close(render.Summary{}))
		assert.NotContains(t, stderr.String(), "\x1b", name)
		assert.Equal(t, c.wantLines, strings.Contains(stderr.String(), "done"), name)
	}
}

func TestLiveFlagsParse(t *testing.T) {
	cfgPath, work := twoLinterFixture(t)
	for _, cmd := range []string{"check", "fmt"} {
		_, _, err := run2(t, "--config", cfgPath, "--cache-dir", t.TempDir(), cmd, "--ascii", "--live-height", "5", "--no-progress", work)
		if cmd == "check" {
			assert.Error(t, err, "check exits non-zero on the fixture's findings, but the flags parsed")
			assert.NotContains(t, err.Error(), "unknown flag")
		} else {
			assert.NoError(t, err)
		}
		_, _, err = run2(t, "--config", cfgPath, cmd, "--live-height", "abc", work)
		assert.Error(t, err, "a non-numeric --live-height is a usage error")
	}
}

func TestLiveHeightEnvIsRead(t *testing.T) {
	cfgPath, work := twoLinterFixture(t)
	t.Setenv("RTUNK_LIVE_HEIGHT", "abc")
	_, _, err := run2(t, "--config", cfgPath, "check", work)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "RTUNK_LIVE_HEIGHT")
}

func TestNonTerminalRunHasNoEscapesOnStderr(t *testing.T) {
	cfgPath, work := twoLinterFixture(t)
	_, stderr, _ := run2(t, "--config", cfgPath, "--cache-dir", t.TempDir(), "check", work)
	assert.NotContains(t, stderr, "\x1b")
	assert.Contains(t, stderr, "alpha")
}
