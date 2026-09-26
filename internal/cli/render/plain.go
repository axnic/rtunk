package render

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/xunleii/rtunk/pkg/trunk/engine"
	"github.com/xunleii/rtunk/pkg/trunk/output"
)

// plain is the non-TTY renderer: streamed progress lines on stderr, one report on stdout.
type plain struct {
	stdout, stderr io.Writer
	opts           Options

	findings   []output.Finding
	failed     []string        // linters with a Failed event, in first-failure order
	failedSeen map[string]bool //
	files      map[string]bool // unique files across terminal events
	linters    map[string]bool // linters that emitted a terminal event
}

// NewPlain returns the plain renderer: no color, no redraw, safe for pipes and CI.
func NewPlain(stdout, stderr io.Writer, opts Options) Renderer {
	return &plain{
		stdout: stdout, stderr: stderr, opts: opts,
		failedSeen: map[string]bool{}, files: map[string]bool{}, linters: map[string]bool{},
	}
}

func (p *plain) Event(ev engine.Event) {
	if ev.Phase == engine.Running {
		return
	}
	p.linters[ev.Linter] = true
	for _, f := range ev.Files {
		p.files[f] = true
	}

	var glyph, word, detail string
	switch ev.Phase {
	case engine.Done:
		for _, f := range ev.Findings {
			if f.Linter == "" {
				f.Linter = ev.Linter // only some parsers fill it; the report needs it for linter/rule
			}
			p.findings = append(p.findings, f)
		}
		glyph, word, detail = "✔", "done", p.doneDetail(ev)
		if detail != "clean" {
			glyph = "▲"
		}
	case engine.Skipped:
		glyph, word, detail = "-", "skipped", ev.Note
	case engine.Failed:
		glyph, word, detail = "✖", "failed", failureText(ev)
		if !p.failedSeen[ev.Linter] {
			p.failedSeen[ev.Linter] = true
			p.failed = append(p.failed, ev.Linter)
		}
	}
	if !p.opts.NoProgress {
		_, _ = fmt.Fprintf(p.stderr, "%s %-16s %-8s %s\n", glyph, ev.Linter, word, detail)
	}
}

func (p *plain) doneDetail(ev engine.Event) string {
	switch p.opts.Command {
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

// failureText is the first line of the failure's error (or its note when there is no error).
func failureText(ev engine.Event) string {
	s := ev.Note
	if ev.Err != nil {
		s = ev.Err.Error()
	}
	first, _, _ := strings.Cut(s, "\n")
	return first
}

func (p *plain) Close(s Summary) error {
	var b strings.Builder
	var sev [3]int // high, medium, low
	issues, changed := 0, 0
	if p.opts.Command == Check {
		issues = p.writeIssues(&b, &sev)
	} else {
		changed = p.writeChanged(&b, s.Changed)
	}
	p.writeFailures(&b, s.RunLog)
	if len(s.Skipped) > 0 {
		_, _ = fmt.Fprintf(&b, "Skipped  %s: %s\n", plural(len(s.Skipped), "linter"), strings.Join(s.Skipped, ", "))
	}
	_, _ = fmt.Fprintf(&b, "Checked %s with %s in %.1fs\n",
		plural(len(p.files), "file"), plural(len(p.linters), "linter"), s.Elapsed.Seconds())

	ok := true
	var verdict string
	switch p.opts.Command {
	case Check:
		verdict = "no issues"
		if issues > 0 {
			ok = false
			verdict = fmt.Sprintf("%s (%d high · %d medium · %d low)", plural(issues, "issue"), sev[0], sev[1], sev[2])
		}
	case Fmt:
		verdict = "no files reformatted"
		if changed > 0 {
			verdict = plural(changed, "file") + " reformatted"
		}
	case FmtCheck:
		verdict = "no files would be reformatted"
		if changed > 0 {
			ok = false
			verdict = plural(changed, "file") + " would be reformatted"
		}
	}
	if n := len(p.failed); n > 0 {
		ok = false
		verdict += " · " + plural(n, "failure")
	}
	glyph := "✔"
	if !ok {
		glyph = "✖"
	}
	_, _ = fmt.Fprintf(&b, "%s %s\n", glyph, verdict)

	_, err := io.WriteString(p.stdout, b.String())
	return err
}

// writeIssues writes the ISSUES block (nothing when there is none) and returns the issue count,
// filling sev with the high/medium/low breakdown.
func (p *plain) writeIssues(b *strings.Builder, sev *[3]int) int {
	if len(p.findings) == 0 {
		return 0
	}
	sort.SliceStable(p.findings, func(i, j int) bool {
		a, c := p.findings[i], p.findings[j]
		if a.File != c.File {
			return a.File < c.File
		}
		if a.Line != c.Line {
			return a.Line < c.Line
		}
		return a.Column < c.Column
	})
	byFile := map[string]int{}
	for _, f := range p.findings {
		byFile[f.File]++
	}
	_, _ = fmt.Fprintf(b, "ISSUES   %d in %s\n", len(p.findings), plural(len(byFile), "file"))

	last := ""
	for i, f := range p.findings {
		if i == 0 || f.File != last {
			_, _ = fmt.Fprintf(b, "\n%s  (%d)\n", f.File, byFile[f.File])
			last = f.File
		}
		s := severity(f.Severity)
		switch s {
		case "high":
			sev[0]++
		case "medium":
			sev[1]++
		default:
			sev[2]++
		}
		src := f.Linter
		if f.RuleID != "" {
			src += "/" + f.RuleID
		}
		_, _ = fmt.Fprintf(b, "  %d:%d  %-6s  %s  %s\n", f.Line, f.Column, s, f.Message, src)
	}
	b.WriteString("\n")
	return len(p.findings)
}

// writeChanged writes the fmt file list (nothing when empty) and returns its deduplicated size.
func (p *plain) writeChanged(b *strings.Builder, changed []string) int {
	seen := map[string]bool{}
	var files []string
	for _, f := range changed {
		if !seen[f] {
			seen[f] = true
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		return 0
	}
	sort.Strings(files)
	header := "REFORMATTED"
	if p.opts.Command == FmtCheck {
		header = "WOULD REFORMAT"
	}
	_, _ = fmt.Fprintf(b, "%s   %s\n\n", header, plural(len(files), "file"))
	for _, f := range files {
		_, _ = fmt.Fprintf(b, "  %s\n", f)
	}
	b.WriteString("\n")
	return len(files)
}

func (p *plain) writeFailures(b *strings.Builder, runLog string) {
	if len(p.failed) == 0 {
		return
	}
	names := append([]string(nil), p.failed...)
	sort.Strings(names)
	b.WriteString("FAILURES\n")
	for _, name := range names {
		line := fmt.Sprintf("  ✖ %s  failed to run", name)
		if runLog != "" {
			line += "  rtunk logs show " + runLog
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n")
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
