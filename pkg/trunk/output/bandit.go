package output

import (
	"encoding/json"
	"fmt"
	"strings"
)

type banditDocument struct {
	Results []struct {
		Filename      string `json:"filename"`
		LineNumber    int    `json:"line_number"`
		ColOffset     int    `json:"col_offset"`
		IssueSeverity string `json:"issue_severity"`
		TestID        string `json:"test_id"`
		IssueText     string `json:"issue_text"`
	} `json:"results"`
}

// ParseBandit decodes bandit's `--format json` output (bandit's own documented schema: a
// top-level "results" array). Bandit's own severities are HIGH/MEDIUM/LOW; mapped to this
// project's error/warning/info vocabulary via banditSeverity.
func ParseBandit(data []byte, linter string) ([]Finding, error) {
	var doc banditDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("check: parse bandit for %s: %w", linter, err)
	}
	findings := make([]Finding, 0, len(doc.Results))
	for _, r := range doc.Results {
		findings = append(findings, Finding{
			Linter: linter, File: r.Filename, Line: r.LineNumber, Column: r.ColOffset,
			Severity: banditSeverity(r.IssueSeverity), RuleID: r.TestID, Message: strings.TrimSpace(r.IssueText),
		})
	}
	return findings, nil
}

func banditSeverity(s string) string {
	switch strings.ToUpper(s) {
	case "HIGH":
		return "error"
	case "MEDIUM":
		return "warning"
	default:
		return "info"
	}
}
