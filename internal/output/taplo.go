package output

import (
	"regexp"
	"strconv"
	"strings"

	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/internal/diagnostic"
)

var (
	taploErrorRe    = regexp.MustCompile(`^error: (.*)$`)
	taploLocationRe = regexp.MustCompile(`┌─ (?P<path>.*):(?P<line>\d+):(?P<col>\d+)$`)
)

// ParseTaplo converts taplo's `lint` output into diagnostics. There is no
// machine-readable format (confirmed via `taplo lint --help`: no --format/
// --json flag exists) — a linter-specific hardcoded parser (SPECS.md §8.3
// P2) for its pretty-printed error blocks:
//
//	error: conflicting keys
//	  ┌─ /abs/path/to/file.toml:4:2
//	  │
//	...
//
// taplo prints the absolute path; the caller re-relativizes it to the repo root.
func ParseTaplo(raw, linterName, commandName string) ([]diagnostic.Diagnostic, error) {
	lines := strings.Split(raw, "\n")
	var diags []diagnostic.Diagnostic
	for i, line := range lines {
		m := taploErrorRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil || i+1 >= len(lines) {
			continue
		}
		loc := taploLocationRe.FindStringSubmatch(lines[i+1])
		if loc == nil {
			continue
		}
		d := diagnostic.Diagnostic{Message: m[1], LinterName: linterName, CommandName: commandName}
		for j, name := range taploLocationRe.SubexpNames() {
			if j == 0 || loc[j] == "" {
				continue
			}
			switch name {
			case "path":
				d.Path = loc[j]
			case "line":
				d.Line, _ = strconv.Atoi(loc[j])
			case "col":
				d.Col, _ = strconv.Atoi(loc[j])
			}
		}
		diags = append(diags, d)
	}
	return diags, nil
}
