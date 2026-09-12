package output

import (
	"encoding/json"
	"fmt"
	"strings"
)

type cfnLintFinding struct {
	Filename string `json:"Filename"`
	Level    string `json:"Level"`
	Location struct {
		Start struct {
			LineNumber   int `json:"LineNumber"`
			ColumnNumber int `json:"ColumnNumber"`
		} `json:"Start"`
	} `json:"Location"`
	Message string `json:"Message"`
	Rule    struct {
		ID string `json:"Id"`
	} `json:"Rule"`
}

// ParseCfnLint decodes cfn-lint's `-f=json` output (AWS cfn-lint's documented schema: a top-level
// array, PascalCase field names).
func ParseCfnLint(data []byte, linter string) ([]Finding, error) {
	var raw []cfnLintFinding
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("check: parse cfnlint for %s: %w", linter, err)
	}
	findings := make([]Finding, 0, len(raw))
	for _, r := range raw {
		findings = append(findings, Finding{
			Linter: linter, File: r.Filename, Line: r.Location.Start.LineNumber, Column: r.Location.Start.ColumnNumber,
			Severity: cfnLintSeverity(r.Level), RuleID: r.Rule.ID, Message: r.Message,
		})
	}
	return findings, nil
}

func cfnLintSeverity(level string) string {
	switch strings.ToLower(level) {
	case "error":
		return "error"
	case "warning":
		return "warning"
	default:
		return "info"
	}
}
