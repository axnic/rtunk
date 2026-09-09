// Package output converts raw linter output into []diagnostic.Diagnostic.
package output

import (
	"strconv"

	"regexp"

	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/internal/diagnostic"
)

// ParseRegex applies a named-group regex (path required, line/col/message optional)
// to each line of raw output — see SPECS.md §8.3 (`output: regex`).
func ParseRegex(re *regexp.Regexp, raw, linterName, commandName string) []diagnostic.Diagnostic {
	names := re.SubexpNames()
	var diags []diagnostic.Diagnostic
	for _, line := range splitLines(raw) {
		m := re.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		d := diagnostic.Diagnostic{LinterName: linterName, CommandName: commandName}
		for i, name := range names {
			if i == 0 || m[i] == "" {
				continue
			}
			switch name {
			case "path":
				d.Path = m[i]
			case "line":
				d.Line, _ = strconv.Atoi(m[i])
			case "col":
				d.Col, _ = strconv.Atoi(m[i])
			case "message":
				d.Message = m[i]
			case "code":
				d.Code = m[i]
			}
		}
		if d.Path == "" {
			continue
		}
		diags = append(diags, d)
	}
	return diags
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
