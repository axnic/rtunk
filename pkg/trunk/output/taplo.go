package output

import (
	"regexp"
	"strconv"
	"strings"
)

// taploMessageRE matches taplo's codespan_reporting message line, e.g. "error: invalid TOML" or
// "warning: some lint message".
var taploMessageRE = regexp.MustCompile(`^(error|warning): (.*)$`)

// taploLocationRE matches taplo's codespan_reporting location line, e.g.
// "┌─ /path/to/file.toml:2:8" (the box-drawing characters are U+250C U+2500, real bytes from a
// direct local capture of `taplo lint`, not typed manually as ASCII lookalikes).
var taploLocationRE = regexp.MustCompile(`^┌─ (.+):(\d+):(\d+)$`)

// ParseTaplo decodes taplo's default `taplo lint` output: a multi-line codespan_reporting-style
// diagnostic block per finding (real shape captured by running `npx @taplo/cli lint` locally
// against a broken TOML file -- see the design spec). Each block's first line ("error: ..."/
// "warning: ...") carries the message; a following "┌─ file:line:col" line carries the location.
// Lines that match neither pattern (context/caret lines, or taplo's own tracing-crate INFO/ERROR
// log lines interleaved in the same stream) are silently ignored, not treated as findings or
// errors.
func ParseTaplo(data []byte, linter string) ([]Finding, error) {
	var findings []Finding
	var pendingSeverity, pendingMessage string
	havePending := false

	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)

		if m := taploMessageRE.FindStringSubmatch(trimmed); m != nil {
			pendingSeverity, pendingMessage = m[1], m[2]
			havePending = true
			continue
		}
		if m := taploLocationRE.FindStringSubmatch(trimmed); m != nil && havePending {
			lineNum, err := strconv.Atoi(m[2])
			if err != nil {
				havePending = false
				continue
			}
			col, _ := strconv.Atoi(m[3])
			findings = append(findings, Finding{
				Linter: linter, File: m[1], Line: lineNum, Column: col,
				Severity: pendingSeverity, Message: pendingMessage,
			})
			havePending = false
		}
	}
	return findings, nil
}
