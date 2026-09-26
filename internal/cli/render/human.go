package render

import (
	"fmt"
	"io"
	"strings"
)

// human is the text renderer: the v0.9.1 report, with ANSI color when Options.Color is set.
type human struct{ base }

func (h *human) Close(s Summary) error {
	var b strings.Builder
	var sev [3]int // high, medium, low
	issues, changed := 0, 0
	if h.opts.Command == Check {
		issues = h.writeIssues(&b, &sev)
	} else {
		changed = h.writeChanged(&b, s.Changed)
	}
	h.writeFailures(&b, s.RunLog)
	if len(s.Skipped) > 0 {
		_, _ = fmt.Fprintf(&b, "Skipped  %s: %s\n", plural(len(s.Skipped), "linter"), strings.Join(s.Skipped, ", "))
	}
	_, _ = fmt.Fprintf(&b, "Checked %s with %s in %.1fs\n",
		plural(len(h.files), "file"), plural(len(h.linters), "linter"), s.Elapsed.Seconds())

	ok := true
	var verdict string
	switch h.opts.Command {
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
	if s.Unstable {
		ok = false
		verdict += " · did not converge"
	}
	if n := len(h.failures); n > 0 {
		ok = false
		verdict += " · " + plural(n, "failure")
	}
	glyph, code := "✔", sgrGreen
	if !ok {
		glyph, code = "✖", sgrRed
	}
	_, _ = fmt.Fprintf(&b, "%s\n", paint(h.opts.Color, code, glyph+" "+verdict))

	_, err := io.WriteString(h.stdout, b.String())
	return err
}

// writeIssues writes the ISSUES block (nothing when there is none) and returns the issue count,
// filling sev with the high/medium/low breakdown.
func (h *human) writeIssues(b *strings.Builder, sev *[3]int) int {
	findings := h.sortedFindings()
	if len(findings) == 0 {
		return 0
	}
	byFile := map[string]int{}
	for _, f := range findings {
		byFile[f.File]++
	}
	_, _ = fmt.Fprintf(b, "ISSUES   %d in %s\n", len(findings), plural(len(byFile), "file"))

	last := ""
	for i, f := range findings {
		if i == 0 || f.File != last {
			_, _ = fmt.Fprintf(b, "\n%s  (%d)\n", paint(h.opts.Color, sgrBold, f.File), byFile[f.File])
			last = f.File
		}
		word := severity(f.Severity)
		glyph, code := "·", sgrDim
		switch word {
		case "high":
			sev[0]++
			glyph, code = "✖", sgrRed
		case "medium":
			sev[1]++
			glyph, code = "▲", sgrYellow
		default:
			sev[2]++
		}
		cell := fmt.Sprintf("%-6s", word) // padded before painting so escapes never shift columns
		if h.opts.Color {
			cell = paint(true, code, glyph+" "+cell)
		}
		_, _ = fmt.Fprintf(b, "  %d:%d  %s  %s  %s\n", f.Line, f.Column, cell, f.Message, paint(h.opts.Color, sgrDim, linterRule(f)))
	}
	b.WriteString("\n")
	return len(findings)
}

// writeChanged writes the fmt file list (nothing when empty) and returns its deduplicated size.
func (h *human) writeChanged(b *strings.Builder, changed []string) int {
	files := sortedUnique(changed)
	if len(files) == 0 {
		return 0
	}
	header := "REFORMATTED"
	if h.opts.Command == FmtCheck {
		header = "WOULD REFORMAT"
	}
	_, _ = fmt.Fprintf(b, "%s   %s\n\n", header, plural(len(files), "file"))
	for _, f := range files {
		_, _ = fmt.Fprintf(b, "  %s\n", f)
	}
	b.WriteString("\n")
	return len(files)
}

func (h *human) writeFailures(b *strings.Builder, runLog string) {
	failures := h.sortedFailures()
	if len(failures) == 0 {
		return
	}
	b.WriteString(paint(h.opts.Color, sgrRed, "FAILURES") + "\n")
	for _, f := range failures {
		line := fmt.Sprintf("  %s %s  failed to run", paint(h.opts.Color, sgrRed, "✖"), f.Linter)
		if runLog != "" {
			line += "  rtunk logs show " + runLog
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n")
}
