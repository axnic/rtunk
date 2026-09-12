package output

import (
	"encoding/json"
	"fmt"
)

type rubocopDocument struct {
	Files []struct {
		Path     string `json:"path"`
		Offenses []struct {
			Severity string `json:"severity"`
			Message  string `json:"message"`
			CopName  string `json:"cop_name"`
			Location struct {
				Line   int `json:"line"`
				Column int `json:"column"`
			} `json:"location"`
		} `json:"offenses"`
	} `json:"files"`
}

// ParseRubocop decodes RuboCop's `--format json` output (source-verified from rubocop's own
// json_formatter.rb -- standardrb wraps rubocop and shares this exact formatter). RuboCop's own
// severities (refactor/convention/warning/error/fatal) are mapped via rubocopSeverity.
func ParseRubocop(data []byte, linter string) ([]Finding, error) {
	var doc rubocopDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("check: parse rubocop for %s: %w", linter, err)
	}
	var findings []Finding
	for _, f := range doc.Files {
		for _, o := range f.Offenses {
			findings = append(findings, Finding{
				Linter: linter, File: f.Path, Line: o.Location.Line, Column: o.Location.Column,
				Severity: rubocopSeverity(o.Severity), RuleID: o.CopName, Message: o.Message,
			})
		}
	}
	return findings, nil
}

func rubocopSeverity(s string) string {
	switch s {
	case "error", "fatal":
		return "error"
	case "warning":
		return "warning"
	default: // "convention", "refactor"
		return "info"
	}
}
