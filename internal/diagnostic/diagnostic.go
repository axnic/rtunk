// Package diagnostic defines the normalized issue structure every linter output is converted to.
package diagnostic

import "fmt"

type Severity int

const (
	Note Severity = iota
	Warning
	Error
)

func (s Severity) String() string {
	switch s {
	case Warning:
		return "warning"
	case Error:
		return "error"
	default:
		return "note"
	}
}

// ParseSeverity parses a --fail-on flag value (note|warning|error).
func ParseSeverity(s string) (Severity, error) {
	switch s {
	case "note":
		return Note, nil
	case "warning":
		return Warning, nil
	case "error":
		return Error, nil
	default:
		return 0, fmt.Errorf("invalid severity %q (want note|warning|error)", s)
	}
}

type Diagnostic struct {
	Path        string
	Line        int
	Col         int
	Severity    Severity
	Code        string
	Message     string
	LinterName  string
	CommandName string
}

// Fix is a proposed autofix from an `output: rewrite` command (SPECS.md
// §8.3): the tool printed the whole reformatted file to stdout instead of
// writing it directly (that's `in_place: true`, applied without preview) —
// rtunk decides whether/when to apply it, so the caller can show a diff and
// prompt first.
type Fix struct {
	Path          string
	LinterName    string
	CommandName   string
	Before, After []byte
}
