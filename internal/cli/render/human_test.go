package render

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/trunk/engine"
	"github.com/xunleii/rtunk/pkg/trunk/output"
)

// run feeds events to a fresh plain renderer and returns what it wrote to stderr and stdout.
func run(t *testing.T, opts Options, events []engine.Event, s Summary) (stdout, stderr string) {
	t.Helper()
	var out, errOut strings.Builder
	r := New(&out, &errOut, opts)
	for _, ev := range events {
		r.Event(ev)
	}
	require.NoError(t, r.Close(s))
	return out.String(), errOut.String()
}

func TestPlain_CheckIssuesAndFailures(t *testing.T) {
	events := []engine.Event{
		{Linter: "markdownlint", Phase: engine.Running, File: "a.go"},
		{Linter: "markdownlint", Phase: engine.Done, Files: []string{"a.go", "b.go"}, Findings: []output.Finding{
			{File: "b.go", Line: 2, Severity: "info", Message: "m3"},
			{File: "a.go", Line: 5, Column: 3, Severity: "warning", RuleID: "r1", Message: "m2"},
			{File: "a.go", Line: 1, Severity: "error", RuleID: "r0", Message: "m1"},
		}},
		{Linter: "gitleaks", Phase: engine.Failed, Files: []string{"c.go"}, Err: errors.New("boom\nsecond line")},
	}
	stdout, stderr := run(t, Options{Command: Check}, events, Summary{Elapsed: 1500 * time.Millisecond, RunLog: "3f9a2c"})

	assert.Equal(t, ""+
		"▲ markdownlint     done     3 issues\n"+
		"✖ gitleaks         failed   boom\n", stderr)
	assert.Equal(t, ""+
		"ISSUES   3 in 2 files\n"+
		"\n"+
		"a.go  (2)\n"+
		"  1:0  high    m1  markdownlint/r0\n"+
		"  5:3  medium  m2  markdownlint/r1\n"+
		"\n"+
		"b.go  (1)\n"+
		"  2:0  low     m3  markdownlint\n"+
		"\n"+
		"FAILURES\n"+
		"  ✖ gitleaks  failed to run  rtunk logs show 3f9a2c\n"+
		"\n"+
		"Checked 3 files with 2 linters in 1.5s\n"+
		"✖ 3 issues (1 high · 1 medium · 1 low) · 1 failure\n", stdout)
}

func TestPlain_CheckClean(t *testing.T) {
	events := []engine.Event{{Linter: "gofmt", Phase: engine.Done, Files: []string{"a.go"}}}
	stdout, stderr := run(t, Options{Command: Check}, events, Summary{Elapsed: 200 * time.Millisecond})
	assert.Equal(t, "✔ gofmt            done     clean\n", stderr)
	assert.Equal(t, "Checked 1 file with 1 linter in 0.2s\n✔ no issues\n", stdout)
}

func TestPlain_FailureWithoutRunLogOmitsTheLogTail(t *testing.T) {
	events := []engine.Event{{Linter: "gitleaks", Phase: engine.Failed, Err: errors.New("boom")}}
	stdout, _ := run(t, Options{Command: Check}, events, Summary{})
	assert.Contains(t, stdout, "FAILURES\n  ✖ gitleaks  failed to run\n")
	assert.NotContains(t, stdout, "rtunk logs show")
	assert.Contains(t, stdout, "✖ no issues · 1 failure\n")
}

func TestPlain_SkippedLintersCountOnce(t *testing.T) {
	events := []engine.Event{
		{Linter: "cspell", Phase: engine.Skipped, Note: "unsupported a", Files: []string{"a.go"}},
		{Linter: "cspell", Phase: engine.Skipped, Note: "unsupported b", Files: []string{"a.go"}},
	}
	stdout, stderr := run(t, Options{Command: Check}, events, Summary{Skipped: []string{"cspell [unsupported a]"}})
	assert.Equal(t, 2, strings.Count(stderr, "skipped"), "one progress line per Skipped event")
	assert.Contains(t, stdout, "Skipped  1 linter: cspell [unsupported a]\nChecked 1 file with 1 linter in 0.0s\n")
}

func TestPlain_FindingLinterIsStampedFromTheEvent(t *testing.T) {
	events := []engine.Event{{Linter: "shellcheck", Phase: engine.Done, Files: []string{"a.sh"},
		Findings: []output.Finding{{File: "a.sh", Line: 1, Severity: "error", RuleID: "SC1", Message: "m"}}}}
	stdout, _ := run(t, Options{Command: Check}, events, Summary{})
	assert.Contains(t, stdout, "  1:0  high    m  shellcheck/SC1\n")
}

func TestPlain_NoProgressSilencesStderrOnly(t *testing.T) {
	events := []engine.Event{
		{Linter: "gofmt", Phase: engine.Running, File: "a.go"},
		{Linter: "gofmt", Phase: engine.Done, Files: []string{"a.go"}},
	}
	withProgress, stderr := run(t, Options{Command: Check}, events, Summary{})
	quiet, quietStderr := run(t, Options{Command: Check, NoProgress: true}, events, Summary{})
	assert.NotEmpty(t, stderr)
	assert.Empty(t, quietStderr)
	assert.Equal(t, withProgress, quiet, "the stdout report is identical, and never carries ANSI escapes")
	assert.NotContains(t, quiet, "\x1b")
}

func TestPlain_Fmt(t *testing.T) {
	events := []engine.Event{{Linter: "gofmt", Phase: engine.Done, Files: []string{"a.go", "b.go", "c.go"}, ChangedFiles: []string{"b.go", "a.go"}}}
	s := Summary{Elapsed: 6100 * time.Millisecond, Changed: []string{"b.go", "a.go"}}

	stdout, stderr := run(t, Options{Command: Fmt}, events, s)
	assert.Equal(t, "▲ gofmt            done     2 files changed\n", stderr)
	assert.Equal(t, ""+
		"REFORMATTED   2 files\n"+
		"\n"+
		"  a.go\n"+
		"  b.go\n"+
		"\n"+
		"Checked 3 files with 1 linter in 6.1s\n"+
		"✔ 2 files reformatted\n", stdout)

	stdout, stderr = run(t, Options{Command: FmtCheck}, events, s)
	assert.Equal(t, "▲ gofmt            done     2 files would change\n", stderr)
	assert.Equal(t, ""+
		"WOULD REFORMAT   2 files\n"+
		"\n"+
		"  a.go\n"+
		"  b.go\n"+
		"\n"+
		"Checked 3 files with 1 linter in 6.1s\n"+
		"✖ 2 files would be reformatted\n", stdout)
}

func TestPlain_FmtNothingToDo(t *testing.T) {
	events := []engine.Event{{Linter: "gofmt", Phase: engine.Done, Files: []string{"a.go"}}}
	stdout, _ := run(t, Options{Command: Fmt}, events, Summary{})
	assert.Equal(t, "Checked 1 file with 1 linter in 0.0s\n✔ no files reformatted\n", stdout)
	stdout, _ = run(t, Options{Command: FmtCheck}, events, Summary{})
	assert.Equal(t, "Checked 1 file with 1 linter in 0.0s\n✔ no files would be reformatted\n", stdout)
}

func TestHuman_ColorKeepsTheWordsAndAddsGlyphs(t *testing.T) {
	events := []engine.Event{{Linter: "lint", Phase: engine.Done, Files: []string{"a.go"},
		Findings: []output.Finding{{File: "a.go", Line: 1, Severity: "error", RuleID: "r0", Message: "m1"}}}}
	stdout, _ := run(t, Options{Command: Check, Color: true}, events, Summary{})

	assert.Equal(t, ""+
		"ISSUES   1 in 1 file\n"+
		"\n"+
		"\x1b[1ma.go\x1b[0m  (1)\n"+
		"  1:0  \x1b[31m✖ high  \x1b[0m  m1  \x1b[2mlint/r0\x1b[0m\n"+
		"\n"+
		"Checked 1 file with 1 linter in 0.0s\n"+
		"\x1b[31m✖ 1 issue (1 high · 0 medium · 0 low)\x1b[0m\n", stdout)
}

func TestHuman_NoColorIsByteIdenticalToV091(t *testing.T) {
	events := []engine.Event{{Linter: "lint", Phase: engine.Done, Files: []string{"a.go"},
		Findings: []output.Finding{{File: "a.go", Line: 1, Severity: "error", RuleID: "r0", Message: "m1"}}}}
	stdout, _ := run(t, Options{Command: Check}, events, Summary{})
	assert.NotContains(t, stdout, "\x1b")
	assert.Contains(t, stdout, "  1:0  high    m1  lint/r0\n")
}

func TestPlain_UnstableFlipsTheVerdict(t *testing.T) {
	events := []engine.Event{{Linter: "gofmt", Phase: engine.Done, Files: []string{"a.go"}, ChangedFiles: []string{"a.go"}}}
	stdout, _ := run(t, Options{Command: Fmt}, events, Summary{Changed: []string{"a.go"}, Unstable: true})
	assert.Contains(t, stdout, "✖ 1 file reformatted · did not converge\n")
}
