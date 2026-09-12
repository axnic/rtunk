package output

import (
	"encoding/json"
	"fmt"
)

type hamlLintDocument struct {
	Files []struct {
		Path     string `json:"path"`
		Offenses []struct {
			Severity   string `json:"severity"`
			Message    string `json:"message"`
			LinterName string `json:"linter_name"`
			Location   struct {
				Line int `json:"line"`
			} `json:"location"`
		} `json:"offenses"`
	} `json:"files"`
}

// ParseHamlLint decodes haml-lint's `--reporter=json` output (source-verified from
// sds/haml-lint's json_reporter.rb/hash_reporter.rb -- JsonReporter is literally
// HashReporter.to_json). haml-lint's schema has no column field at all, only offenses[].location.line.
func ParseHamlLint(data []byte, linter string) ([]Finding, error) {
	var doc hamlLintDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("check: parse haml_lint for %s: %w", linter, err)
	}
	var findings []Finding
	for _, f := range doc.Files {
		for _, o := range f.Offenses {
			findings = append(findings, Finding{
				Linter: linter, File: f.Path, Line: o.Location.Line,
				Severity: o.Severity, RuleID: o.LinterName, Message: o.Message,
			})
		}
	}
	return findings, nil
}
