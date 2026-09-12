package output

import (
	"encoding/json"
	"fmt"
)

type eslintFileResult struct {
	FilePath string `json:"filePath"`
	Messages []struct {
		RuleID   string `json:"ruleId"`
		Severity int    `json:"severity"`
		Message  string `json:"message"`
		Line     int    `json:"line"`
		Column   int    `json:"column"`
	} `json:"messages"`
}

// ParseESLint decodes eslint's `--format json` output (ESLint's documented formatter schema: a
// top-level array of per-file results, each with a nested messages[] array). Severity is 1
// (warning) or 2 (error), per ESLint's own docs.
func ParseESLint(data []byte, linter string) ([]Finding, error) {
	var raw []eslintFileResult
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("check: parse eslint for %s: %w", linter, err)
	}
	var findings []Finding
	for _, file := range raw {
		for _, m := range file.Messages {
			findings = append(findings, Finding{
				Linter: linter, File: file.FilePath, Line: m.Line, Column: m.Column,
				Severity: eslintSeverity(m.Severity), RuleID: m.RuleID, Message: m.Message,
			})
		}
	}
	return findings, nil
}

func eslintSeverity(s int) string {
	if s == 2 {
		return "error"
	}
	return "warning"
}
