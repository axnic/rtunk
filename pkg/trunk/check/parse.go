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

type actionlintFinding struct {
	Message  string `json:"message"`
	Filepath string `json:"filepath"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Kind     string `json:"kind"`
}

// ParseActionlint decodes actionlint's `-format "{{json .}}"` output: a single JSON array
// covering every finding in one invocation (confirmed from actionlint's own error.go source --
// the template applies to the whole error slice at once, not one object per line). actionlint has
// no severity levels of its own, so every finding is reported as "error".
func ParseActionlint(data []byte, linter string) ([]Finding, error) {
	var raw []actionlintFinding
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("check: parse actionlint for %s: %w", linter, err)
	}
	findings := make([]Finding, 0, len(raw))
	for _, r := range raw {
		findings = append(findings, Finding{
			Linter: linter, File: r.Filepath, Line: r.Line, Column: r.Column,
			Severity: "error", RuleID: r.Kind, Message: r.Message,
		})
	}
	return findings, nil
}

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

type cfnLintFinding struct {
	Filename string `json:"Filename"`
	Level    string `json:"Level"`
	Location struct {
		Start struct {
			LineNumber   int `json:"LineNumber"`
			ColumnNumber int `json:"ColumnNumber"`
		} `json:"Start"`
	} `json:"Location"`
	Message string `json:"Message"`
	Rule    struct {
		ID string `json:"Id"`
	} `json:"Rule"`
}

// ParseCfnLint decodes cfn-lint's `-f=json` output (AWS cfn-lint's documented schema: a top-level
// array, PascalCase field names).
func ParseCfnLint(data []byte, linter string) ([]Finding, error) {
	var raw []cfnLintFinding
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("check: parse cfnlint for %s: %w", linter, err)
	}
	findings := make([]Finding, 0, len(raw))
	for _, r := range raw {
		findings = append(findings, Finding{
			Linter: linter, File: r.Filename, Line: r.Location.Start.LineNumber, Column: r.Location.Start.ColumnNumber,
			Severity: cfnLintSeverity(r.Level), RuleID: r.Rule.ID, Message: r.Message,
		})
	}
	return findings, nil
}

func cfnLintSeverity(level string) string {
	switch strings.ToLower(level) {
	case "error":
		return "error"
	case "warning":
		return "warning"
	default:
		return "info"
	}
}

type hadolintFinding struct {
	Line    int    `json:"line"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Column  int    `json:"column"`
	File    string `json:"file"`
	Level   string `json:"level"`
}

// ParseHadolint decodes hadolint's `-f json` output (hadolint's documented flat-array schema).
func ParseHadolint(data []byte, linter string) ([]Finding, error) {
	var raw []hadolintFinding
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("check: parse hadolint for %s: %w", linter, err)
	}
	findings := make([]Finding, 0, len(raw))
	for _, r := range raw {
		findings = append(findings, Finding{
			Linter: linter, File: r.File, Line: r.Line, Column: r.Column,
			Severity: hadolintSeverity(r.Level), RuleID: r.Code, Message: r.Message,
		})
	}
	return findings, nil
}

func hadolintSeverity(level string) string {
	switch strings.ToLower(level) {
	case "error":
		return "error"
	case "warning":
		return "warning"
	default:
		return "info"
	}
}

type pylintFinding struct {
	Type      string `json:"type"`
	Line      int    `json:"line"`
	Column    int    `json:"column"`
	Path      string `json:"path"`
	Message   string `json:"message"`
	MessageID string `json:"message-id"`
}

// ParsePylint decodes pylint's `--output-format json` output (pylint's legacy JSONReporter
// schema, source-verified field names -- note the hyphenated "message-id" JSON key).
func ParsePylint(data []byte, linter string) ([]Finding, error) {
	var raw []pylintFinding
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("check: parse pylint for %s: %w", linter, err)
	}
	findings := make([]Finding, 0, len(raw))
	for _, r := range raw {
		findings = append(findings, Finding{
			Linter: linter, File: r.Path, Line: r.Line, Column: r.Column,
			Severity: pylintSeverity(r.Type), RuleID: r.MessageID, Message: r.Message,
		})
	}
	return findings, nil
}

func pylintSeverity(t string) string {
	switch t {
	case "error", "fatal":
		return "error"
	case "warning":
		return "warning"
	default: // "convention", "refactor"
		return "info"
	}
}
