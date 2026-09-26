package render

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/trunk/engine"
	"github.com/xunleii/rtunk/pkg/trunk/output"
)

func TestJSON_CleanRunIsExactAndNeverNull(t *testing.T) {
	events := []engine.Event{{Linter: "gofmt", Phase: engine.Done, Files: []string{"a.go"}}}
	stdout, _ := run(t, Options{Format: JSON, Command: Check}, events, Summary{Elapsed: 200 * time.Millisecond})
	assert.Equal(t, `{
  "version": 1,
  "command": "check",
  "elapsed_ms": 200,
  "run_log": "",
  "files_checked": 1,
  "linters": 1,
  "issues": [],
  "failures": [],
  "skipped": [],
  "changed": []
}
`, stdout)
}

func TestJSON_IssuesFailuresSkipped(t *testing.T) {
	events := []engine.Event{
		{Linter: "lint", Phase: engine.Done, Files: []string{"a.go", "b.go"}, Findings: []output.Finding{
			{File: "b.go", Line: 0, Severity: "info", Message: "a <b> & c"},
			{File: "a.go", Line: 5, Column: 3, Severity: "warning", RuleID: "r1", Message: "m2", URL: "https://x/r1"},
			{File: "a.go", Line: 1, Severity: "error", RuleID: "r0", Message: "m1"},
		}},
		{Linter: "gitleaks", Phase: engine.Failed, Err: errors.New("boom\nsecond")},
	}
	stdout, _ := run(t, Options{Format: JSON, Command: Check}, events,
		Summary{RunLog: "3f9a2c", Skipped: []string{"shfmt [x]"}})

	var doc struct {
		Issues []struct {
			File     string `json:"file"`
			Line     int    `json:"line"`
			Column   int    `json:"column"`
			Severity string `json:"severity"`
			Message  string `json:"message"`
			Linter   string `json:"linter"`
			Rule     string `json:"rule"`
			URL      string `json:"url"`
		} `json:"issues"`
		Failures []struct{ Linter, Error string } `json:"failures"`
		Skipped  []string                         `json:"skipped"`
		RunLog   string                           `json:"run_log"`
		Files    int                              `json:"files_checked"`
		Linters  int                              `json:"linters"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &doc))

	require.Len(t, doc.Issues, 3)
	assert.Equal(t, "a.go", doc.Issues[0].File)
	assert.Equal(t, 1, doc.Issues[0].Line)
	assert.Equal(t, "high", doc.Issues[0].Severity)
	assert.Equal(t, "r0", doc.Issues[0].Rule)
	assert.Equal(t, "lint", doc.Issues[0].Linter, "linter is back-filled from the event")
	assert.Equal(t, "medium", doc.Issues[1].Severity)
	assert.Equal(t, "https://x/r1", doc.Issues[1].URL)
	assert.Equal(t, 0, doc.Issues[2].Line, "unknown position stays 0")
	assert.Equal(t, "low", doc.Issues[2].Severity)
	assert.Equal(t, "", doc.Issues[2].Rule)
	assert.Equal(t, "a <b> & c", doc.Issues[2].Message)
	assert.Contains(t, stdout, `"a <b> & c"`, "HTML characters are not escaped")

	require.Len(t, doc.Failures, 1)
	assert.Equal(t, "gitleaks", doc.Failures[0].Linter)
	assert.Equal(t, "boom", doc.Failures[0].Error, "first line of the error only")
	assert.Equal(t, []string{"shfmt [x]"}, doc.Skipped)
	assert.Equal(t, "3f9a2c", doc.RunLog)
	assert.Equal(t, 2, doc.Files)
	assert.Equal(t, 2, doc.Linters)
}

func TestJSON_FmtReportsChangedFiles(t *testing.T) {
	events := []engine.Event{{Linter: "gofmt", Phase: engine.Done, Files: []string{"a.go"}, ChangedFiles: []string{"b.go", "a.go"}}}
	for _, kind := range []Kind{Fmt, FmtCheck} {
		stdout, _ := run(t, Options{Format: JSON, Command: kind}, events, Summary{Changed: []string{"b.go", "a.go", "a.go"}})
		var doc struct {
			Command string   `json:"command"`
			Issues  []any    `json:"issues"`
			Changed []string `json:"changed"`
		}
		require.NoError(t, json.Unmarshal([]byte(stdout), &doc))
		assert.Equal(t, "fmt", doc.Command, "--check is not a separate command value")
		assert.Empty(t, doc.Issues)
		assert.Equal(t, []string{"a.go", "b.go"}, doc.Changed, "sorted and deduplicated")
	}
}

func TestJSON_StdoutIsOnlyTheDocumentAndProgressStaysOnStderr(t *testing.T) {
	events := []engine.Event{{Linter: "gofmt", Phase: engine.Done, Files: []string{"a.go"}}}
	stdout, stderr := run(t, Options{Format: JSON, Command: Check}, events, Summary{})
	dec := json.NewDecoder(strings.NewReader(stdout))
	var v any
	require.NoError(t, dec.Decode(&v))
	assert.ErrorIs(t, dec.Decode(&v), io.EOF, "exactly one JSON value")
	assert.Contains(t, stderr, "gofmt")
}

func TestJSON_SummaryFailuresJoinTheEventFailures(t *testing.T) {
	events := []engine.Event{{Linter: "gitleaks", Phase: engine.Failed, Err: errors.New("boom")}}
	stdout, _ := run(t, Options{Format: JSON, Command: Check}, events,
		Summary{Failures: []Failure{{Linter: "prettier", Err: "crashed"}, {Linter: "gitleaks", Err: "dup"}}})
	var doc struct {
		Failures []struct{ Linter, Error string } `json:"failures"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &doc))
	require.Len(t, doc.Failures, 2, "one entry per linter, the check pass's own failure wins")
	assert.Equal(t, "gitleaks", doc.Failures[0].Linter)
	assert.Equal(t, "boom", doc.Failures[0].Error)
	assert.Equal(t, "prettier", doc.Failures[1].Linter)
}
