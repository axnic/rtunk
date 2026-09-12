package output

import (
	"encoding/json"
	"fmt"
)

type stylelintFileResult struct {
	Source   string `json:"source"`
	Warnings []struct {
		Line     int    `json:"line"`
		Column   int    `json:"column"`
		Rule     string `json:"rule"`
		Severity string `json:"severity"`
		Text     string `json:"text"`
	} `json:"warnings"`
}

// ParseStylelint decodes stylelint's `-f json` output (stylelint's documented formatter schema: a
// top-level array of per-file results, each with a nested warnings[] array).
func ParseStylelint(data []byte, linter string) ([]Finding, error) {
	var raw []stylelintFileResult
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("check: parse stylelint for %s: %w", linter, err)
	}
	var findings []Finding
	for _, file := range raw {
		for _, w := range file.Warnings {
			findings = append(findings, Finding{
				Linter: linter, File: file.Source, Line: w.Line, Column: w.Column,
				Severity: w.Severity, RuleID: w.Rule, Message: w.Text,
			})
		}
	}
	return findings, nil
}
