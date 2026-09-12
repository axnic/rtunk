package output

import (
	"encoding/json"
	"fmt"
)

type buildifierDocument struct {
	Files []struct {
		Filename string `json:"filename"`
		Warnings []struct {
			Start struct {
				Line   int `json:"line"`
				Column int `json:"column"`
			} `json:"start"`
			Category string `json:"category"`
			Message  string `json:"message"`
		} `json:"warnings"`
	} `json:"files"`
}

// ParseBuildifier decodes buildifier's `--format=json --mode=check` output (source-verified from
// bazelbuild/buildtools's diagnostics.go). Buildifier warnings carry no severity level of their
// own, so every finding is reported as "warning".
func ParseBuildifier(data []byte, linter string) ([]Finding, error) {
	var doc buildifierDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("check: parse buildifier for %s: %w", linter, err)
	}
	var findings []Finding
	for _, f := range doc.Files {
		for _, w := range f.Warnings {
			findings = append(findings, Finding{
				Linter: linter, File: f.Filename, Line: w.Start.Line, Column: w.Start.Column,
				Severity: "warning", RuleID: w.Category, Message: w.Message,
			})
		}
	}
	return findings, nil
}
