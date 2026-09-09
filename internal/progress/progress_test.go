package progress

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

// forceEnabled builds a Tracker writing to a bytes.Buffer (never a real
// terminal) with rendering forced on, so the render logic itself is testable
// without a pty.
func forceEnabled(w io.Writer, label string, total int) *Tracker {
	t := New(w, label, total)
	t.enabled = true
	return t
}

func TestTracker_NotATerminal_Disabled(t *testing.T) {
	var buf bytes.Buffer
	tr := New(&buf, "Checking", 3)
	tr.Start("a")
	tr.Done("a")
	tr.Finish()
	if buf.Len() != 0 {
		t.Errorf("expected no output when w isn't a terminal, got %q", buf.String())
	}
}

func TestTracker_RendersBarAndActiveJobs(t *testing.T) {
	var buf bytes.Buffer
	tr := forceEnabled(&buf, "Checking", 2)
	tr.Start("govet: internal/plugin")
	out := buf.String()
	if !strings.Contains(out, "Checking") || !strings.Contains(out, "0/2") {
		t.Errorf("expected bar with 0/2, got %q", out)
	}
	if !strings.Contains(out, "govet: internal/plugin") {
		t.Errorf("expected active job listed, got %q", out)
	}

	buf.Reset()
	tr.Done("govet: internal/plugin")
	out = buf.String()
	if !strings.Contains(out, "1/2") {
		t.Errorf("expected 1/2 after Done, got %q", out)
	}
	if strings.Contains(out, "govet: internal/plugin") {
		t.Errorf("expected the finished job to no longer be listed, got %q", out)
	}
}

func TestTracker_Finish_ClearsWithoutError(t *testing.T) {
	var buf bytes.Buffer
	tr := forceEnabled(&buf, "Checking", 1)
	tr.Start("x")
	tr.Finish() // must not panic even with an active job still listed
}
