// Package output normalizes every check-engine linter output format (SARIF, per-tool JSON
// schemas, and config-declared regex patterns) into a common Finding shape.
package output

import "strings"

// Finding is one normalized check result.
type Finding struct {
	Linter   string
	File     string
	Line     int    // 0 when the format doesn't report one (pass_fail)
	Column   int    // 0 when not reported
	Severity string // "error", "warning", or "info"
	RuleID   string
	Message  string
	URL      string // filled by ApplyIssueURL when both IssueURLFormat and RuleID are set
}

// ApplyIssueURL fills each finding's URL from format (a config.Linter.IssueURLFormat, using "{}"
// as RuleID's placeholder -- trunk-io's own plugin.yaml convention, e.g. shellcheck's
// "https://github.com/koalaman/shellcheck/wiki/{}"). A no-op per finding when format is empty or
// the finding has no RuleID.
func ApplyIssueURL(findings []Finding, format string) {
	if format == "" {
		return
	}
	for i := range findings {
		if findings[i].RuleID == "" {
			continue
		}
		findings[i].URL = strings.ReplaceAll(format, "{}", findings[i].RuleID)
	}
}
