package render

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/xunleii/rtunk/pkg/run/engine"
	"github.com/xunleii/rtunk/pkg/trunk/output"
)

// base collects what every renderer needs from the event stream and writes the stderr progress
// lines; the format renderers embed it and only implement Close.
type base struct {
	stdout, stderr io.Writer
	opts           Options

	findings   []output.Finding // Linter back-filled from the event
	failures   []Failure        // first Failed event per linter, arrival order
	failedSeen map[string]bool
	files      map[string]bool // unique files across terminal events
	linters    map[string]bool // linters that emitted a terminal event
}

func newBase(stdout, stderr io.Writer, opts Options) base {
	return base{
		stdout: stdout, stderr: stderr, opts: opts,
		failedSeen: map[string]bool{}, files: map[string]bool{}, linters: map[string]bool{},
	}
}

func (b *base) Event(ev engine.Event) {
	if ev.Phase != engine.Done && ev.Phase != engine.Skipped && ev.Phase != engine.Failed {
		return // Running and the live-view phases (Planned, JobDone, Install*) are not outcomes
	}
	b.linters[ev.Linter] = true
	for _, f := range ev.Files {
		b.files[f] = true
	}

	var glyph, word, detail string
	switch ev.Phase {
	case engine.Done:
		for _, f := range ev.Findings {
			if f.Linter == "" {
				f.Linter = ev.Linter // only some parsers fill it; the report needs it for linter/rule
			}
			b.findings = append(b.findings, f)
		}
		glyph, word, detail = "✔", "done", b.doneDetail(ev)
		if detail != "clean" {
			glyph = "▲"
		}
	case engine.Skipped:
		glyph, word, detail = "-", "skipped", ev.Note
	case engine.Failed:
		glyph, word, detail = "✖", "failed", failureText(ev)
		if !b.failedSeen[ev.Linter] {
			b.failedSeen[ev.Linter] = true
			b.failures = append(b.failures, Failure{Linter: ev.Linter, Err: detail})
		}
	}
	if !b.opts.NoProgress {
		_, _ = fmt.Fprintf(b.stderr, "%s %-16s %-8s %s\n", glyph, ev.Linter, word, detail)
	}
}

func (b *base) doneDetail(ev engine.Event) string {
	switch b.opts.Command {
	case Fmt:
		if n := len(ev.ChangedFiles); n > 0 {
			return plural(n, "file") + " changed"
		}
	case FmtCheck:
		if n := len(ev.ChangedFiles); n > 0 {
			return plural(n, "file") + " would change"
		}
	default:
		if n := len(ev.Findings); n > 0 {
			return plural(n, "issue")
		}
	}
	return "clean"
}

// FailureFrom is the Failure a Failed event describes, for callers that feed a renderer's
// Summary.Failures (a pass whose events never reach that renderer).
func FailureFrom(ev engine.Event) Failure { return Failure{Linter: ev.Linter, Err: failureText(ev)} }

// failureText is the first line of the failure's error (or its note when there is no error).
func failureText(ev engine.Event) string {
	s := ev.Note
	if ev.Err != nil {
		s = ev.Err.Error()
	}
	first, _, _ := strings.Cut(s, "\n")
	return first
}

// sortedFindings is the findings ordered by file, line, column (stable for ties).
func (b *base) sortedFindings() []output.Finding {
	out := append([]output.Finding(nil), b.findings...)
	sort.SliceStable(out, func(i, j int) bool {
		a, c := out[i], out[j]
		if a.File != c.File {
			return a.File < c.File
		}
		if a.Line != c.Line {
			return a.Line < c.Line
		}
		return a.Column < c.Column
	})
	return out
}

// sortedFailures is the failures ordered by linter name, extra (Summary.Failures) joined in: one
// entry per linter, the event stream's own entry winning over an extra one.
func (b *base) sortedFailures(extra ...Failure) []Failure {
	out := append([]Failure(nil), b.failures...)
	for _, f := range extra {
		if !b.failedSeen[f.Linter] {
			b.failedSeen[f.Linter] = true
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Linter < out[j].Linter })
	return out
}

// linterRule is "linter/rule", or "linter" alone when the finding has no rule id.
func linterRule(f output.Finding) string {
	if f.RuleID == "" {
		return f.Linter
	}
	return f.Linter + "/" + f.RuleID
}

// sortedUnique is ss deduplicated and sorted, never nil.
func sortedUnique(ss []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// severity maps output.Finding's error/warning/info onto trunk's high/medium/low.
func severity(s string) string {
	switch s {
	case "error":
		return "high"
	case "warning":
		return "medium"
	}
	return "low"
}

// plural is "1 issue" / "3 issues".
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
