package output

import (
	"encoding/json"
	"fmt"
)

type shellcheckFinding struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Column  int    `json:"column"`
	Level   string `json:"level"`
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ParseShellcheck decodes shellcheck's `-f json` output: a bare top-level array of finding
// objects (source-verified against a real `shellcheck ${target} -f json` run, shellcheck 0.11.0 --
// this is shellcheck's own native schema, not trunk's internal normalized "issues"/"comments"
// wrapper, a different, trunk-only shape). Severity is one of shellcheck's own
// "error"/"warning"/"info"/"style" levels, passed through as-is: severity() in internal/cli/render
// already maps anything but "error"/"warning" to low, which "info"/"style" both deserve. RuleID is
// "SC"+code (e.g. "SC2086"), matching this linter's own issue_url_format
// (https://github.com/koalaman/shellcheck/wiki/{}).
//
// shellcheck's own fix.replacements[] (when a finding has one) is line/column-addressed and can
// carry more than one replacement per finding -- output.InlineFix models a single byte-offset
// range instead (ESLint's shape), so it isn't translated here; `rtunk check --fix` won't autofix
// shellcheck findings yet.
func ParseShellcheck(data []byte, linter string) ([]Finding, error) {
	var raw []shellcheckFinding
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("check: parse shellcheck for %s: %w", linter, err)
	}
	findings := make([]Finding, 0, len(raw))
	for _, f := range raw {
		findings = append(findings, Finding{
			Linter: linter, File: f.File, Line: f.Line, Column: f.Column,
			Severity: f.Level, RuleID: fmt.Sprintf("SC%d", f.Code), Message: f.Message,
		})
	}
	return findings, nil
}
