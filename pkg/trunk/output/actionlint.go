package output

import (
	"encoding/json"
	"fmt"
)

type actionlintFinding struct {
	Message  string `json:"message"`
	Filepath string `json:"filepath"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Kind     string `json:"kind"`
}

// ParseActionlint decodes actionlint's `-format "{{json .}}"` output: a single JSON array
// covering every finding in one invocation (confirmed from actionlint's own error.go source --
// the template applies to the whole error slice at once, not one object per line). actionlint has
// no severity levels of its own, so every finding is reported as "error".
func ParseActionlint(data []byte, linter string) ([]Finding, error) {
	var raw []actionlintFinding
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("check: parse actionlint for %s: %w", linter, err)
	}
	findings := make([]Finding, 0, len(raw))
	for _, r := range raw {
		findings = append(findings, Finding{
			Linter: linter, File: r.Filepath, Line: r.Line, Column: r.Column,
			Severity: "error", RuleID: r.Kind, Message: r.Message,
		})
	}
	return findings, nil
}
