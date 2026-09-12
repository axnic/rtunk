package output

import (
	"encoding/json"
	"fmt"
)

type pylintFinding struct {
	Type      string `json:"type"`
	Line      int    `json:"line"`
	Column    int    `json:"column"`
	Path      string `json:"path"`
	Message   string `json:"message"`
	MessageID string `json:"message-id"`
}

// ParsePylint decodes pylint's `--output-format json` output (pylint's legacy JSONReporter
// schema, source-verified field names -- note the hyphenated "message-id" JSON key).
func ParsePylint(data []byte, linter string) ([]Finding, error) {
	var raw []pylintFinding
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("check: parse pylint for %s: %w", linter, err)
	}
	findings := make([]Finding, 0, len(raw))
	for _, r := range raw {
		findings = append(findings, Finding{
			Linter: linter, File: r.Path, Line: r.Line, Column: r.Column,
			Severity: pylintSeverity(r.Type), RuleID: r.MessageID, Message: r.Message,
		})
	}
	return findings, nil
}

func pylintSeverity(t string) string {
	switch t {
	case "error", "fatal":
		return "error"
	case "warning":
		return "warning"
	default: // "convention", "refactor"
		return "info"
	}
}
