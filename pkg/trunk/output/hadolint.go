package output

import (
	"encoding/json"
	"fmt"
	"strings"
)

type hadolintFinding struct {
	Line    int    `json:"line"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Column  int    `json:"column"`
	File    string `json:"file"`
	Level   string `json:"level"`
}

// ParseHadolint decodes hadolint's `-f json` output (hadolint's documented flat-array schema).
func ParseHadolint(data []byte, linter string) ([]Finding, error) {
	var raw []hadolintFinding
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("check: parse hadolint for %s: %w", linter, err)
	}
	findings := make([]Finding, 0, len(raw))
	for _, r := range raw {
		findings = append(findings, Finding{
			Linter: linter, File: r.File, Line: r.Line, Column: r.Column,
			Severity: hadolintSeverity(r.Level), RuleID: r.Code, Message: r.Message,
		})
	}
	return findings, nil
}

func hadolintSeverity(level string) string {
	switch strings.ToLower(level) {
	case "error":
		return "error"
	case "warning":
		return "warning"
	default:
		return "info"
	}
}
