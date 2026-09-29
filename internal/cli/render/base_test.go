package render

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/xunleii/rtunk/pkg/run/engine"
)

func TestBase_IgnoresLiveOnlyPhases(t *testing.T) {
	var out, errOut strings.Builder
	r := New(&out, &errOut, Options{Command: Check})
	for _, ph := range []engine.Phase{engine.Planned, engine.JobDone, engine.InstallStart, engine.InstallProgress, engine.InstallDone} {
		r.Event(engine.Event{Linter: "x", Phase: ph, Files: []string{"a.go"}, Item: "tools/x"})
	}
	assert.Empty(t, errOut.String(), "no progress line for a live-only phase")
	assert.NoError(t, r.Close(Summary{}))
	assert.Contains(t, out.String(), "Checked 0 files with 0 linters", "they never count as a linter or a file")
}
