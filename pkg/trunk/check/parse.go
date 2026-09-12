package check

import (
	"encoding/json"
	"fmt"
	"strings"
)

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

type sarifDocument struct {
	Runs []struct {
		Results []struct {
			RuleID  string `json:"ruleId"`
			Level   string `json:"level"`
			Message struct {
				Text string `json:"text"`
			} `json:"message"`
			Locations []struct {
				PhysicalLocation struct {
					ArtifactLocation struct {
						URI string `json:"uri"`
					} `json:"artifactLocation"`
					Region struct {
						StartLine   int `json:"startLine"`
						StartColumn int `json:"startColumn"`
					} `json:"region"`
				} `json:"physicalLocation"`
			} `json:"locations"`
		} `json:"results"`
	} `json:"runs"`
}

// ParseSARIF decodes the subset of the SARIF JSON schema trunk-io plugins actually emit
// (runs[].results[]) into Findings tagged with linter. A result with no locations is skipped (a
// tool-level message, not a file-scoped finding) rather than erroring.
func ParseSARIF(data []byte, linter string) ([]Finding, error) {
	var doc sarifDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("check: parse sarif for %s: %w", linter, err)
	}

	var findings []Finding
	for _, run := range doc.Runs {
		for _, res := range run.Results {
			if len(res.Locations) == 0 {
				continue
			}
			loc := res.Locations[0].PhysicalLocation
			findings = append(findings, Finding{
				Linter:   linter,
				File:     strings.TrimPrefix(loc.ArtifactLocation.URI, "file://"),
				Line:     loc.Region.StartLine,
				Column:   loc.Region.StartColumn,
				Severity: sarifSeverity(res.Level),
				RuleID:   res.RuleID,
				Message:  strings.TrimSpace(res.Message.Text),
			})
		}
	}
	return findings, nil
}

func sarifSeverity(level string) string {
	switch level {
	case "error":
		return "error"
	case "warning":
		return "warning"
	default:
		return "info"
	}
}

// ParsePassFail builds one Finding per file for a pass_fail command whose exit code was non-zero
// (and not in ErrorCodes) -- pass_fail carries no line/column/rule, only "this file didn't pass".
func ParsePassFail(linter string, files []string) []Finding {
	findings := make([]Finding, 0, len(files))
	for _, f := range files {
		findings = append(findings, Finding{Linter: linter, File: f, Severity: "error", Message: "file did not pass"})
	}
	return findings
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
