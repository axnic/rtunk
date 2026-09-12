package output

import (
	"encoding/json"
	"fmt"
)

type markdownlintViolation struct {
	FileName        string   `json:"fileName"`
	LineNumber      int      `json:"lineNumber"`
	RuleNames       []string `json:"ruleNames"`
	RuleDescription string   `json:"ruleDescription"`
	ErrorRange      []int    `json:"errorRange"`
	Severity        string   `json:"severity"`
}

// ParseMarkdownlint decodes markdownlint-cli's `--json` output: a flat JSON array (confirmed by
// running `npx markdownlint-cli <file> --json` locally against a real broken markdown fixture --
// NOT an object keyed by filename, which was this function's incorrect original assumption).
// RuleNames[0] is the short code (e.g. "MD010"); ErrorRange[0], when present, is the 1-based
// column. Severity is read directly from the real tool's own field (confirmed present in a real
// capture, "error" in the sample observed).
func ParseMarkdownlint(data []byte, linter string) ([]Finding, error) {
	var raw []markdownlintViolation
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("check: parse markdownlint for %s: %w", linter, err)
	}
	findings := make([]Finding, 0, len(raw))
	for _, v := range raw {
		col := 0
		if len(v.ErrorRange) > 0 {
			col = v.ErrorRange[0]
		}
		rule := ""
		if len(v.RuleNames) > 0 {
			rule = v.RuleNames[0]
		}
		severity := v.Severity
		if severity == "" {
			severity = "error"
		}
		findings = append(findings, Finding{
			Linter: linter, File: v.FileName, Line: v.LineNumber, Column: col,
			Severity: severity, RuleID: rule, Message: v.RuleDescription,
		})
	}
	return findings, nil
}
