package check

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
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

type markdownlintViolation struct {
	LineNumber      int      `json:"lineNumber"`
	RuleNames       []string `json:"ruleNames"`
	RuleDescription string   `json:"ruleDescription"`
	ErrorRange      []int    `json:"errorRange"`
}

// ParseMarkdownlint decodes markdownlint's `--json` output: an object keyed by filename, each
// value an array of violations (DavidAnson/markdownlint's documented schema). RuleNames[0] is the
// short code (e.g. "MD010"); ErrorRange[0], when present, is the 1-based column. Every violation
// is reported as "error" -- markdownlint's base --json output does not reliably include its own
// severity field (only cli2-formatter output was confirmed to have one).
func ParseMarkdownlint(data []byte, linter string) ([]Finding, error) {
	var doc map[string][]markdownlintViolation
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("check: parse markdownlint for %s: %w", linter, err)
	}
	var findings []Finding
	for file, violations := range doc {
		for _, v := range violations {
			col := 0
			if len(v.ErrorRange) > 0 {
				col = v.ErrorRange[0]
			}
			rule := ""
			if len(v.RuleNames) > 0 {
				rule = v.RuleNames[0]
			}
			findings = append(findings, Finding{
				Linter: linter, File: file, Line: v.LineNumber, Column: col,
				Severity: "error", RuleID: rule, Message: v.RuleDescription,
			})
		}
	}
	return findings, nil
}

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

type stylelintFileResult struct {
	Source   string `json:"source"`
	Warnings []struct {
		Line     int    `json:"line"`
		Column   int    `json:"column"`
		Rule     string `json:"rule"`
		Severity string `json:"severity"`
		Text     string `json:"text"`
	} `json:"warnings"`
}

// ParseStylelint decodes stylelint's `-f json` output (stylelint's documented formatter schema: a
// top-level array of per-file results, each with a nested warnings[] array).
func ParseStylelint(data []byte, linter string) ([]Finding, error) {
	var raw []stylelintFileResult
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("check: parse stylelint for %s: %w", linter, err)
	}
	var findings []Finding
	for _, file := range raw {
		for _, w := range file.Warnings {
			findings = append(findings, Finding{
				Linter: linter, File: file.Source, Line: w.Line, Column: w.Column,
				Severity: w.Severity, RuleID: w.Rule, Message: w.Text,
			})
		}
	}
	return findings, nil
}

// perlCriticLineRE matches perlcritic's real invocation in this project's plugin catalog:
// `perlcritic --verbose 'path=%f,line=%l,col=%c,code=%p,message=%m\n' ${target}` -- a
// comma-separated key=value format, NOT the shared colon-separated convention most other
// "regex"-output linters use. path/code use a non-greedy match up to the next known ",key="
// delimiter; message is greedy to the end of the line since it may itself contain commas.
var perlCriticLineRE = regexp.MustCompile(`^path=(.*?),line=(\d+),col=(\d+),code=(.*?),message=(.*)$`)

// ParsePerlCritic decodes perlcritic's custom --verbose format-string output, one finding per
// line. A line that doesn't match the expected shape (e.g. blank lines) is silently skipped, not
// an error -- perlcritic's own severity levels (1-5) aren't included in this format string, so
// every finding is reported as "warning".
func ParsePerlCritic(data []byte, linter string) ([]Finding, error) {
	var findings []Finding
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		m := perlCriticLineRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		lineNum, err := strconv.Atoi(m[2])
		if err != nil {
			continue
		}
		col, _ := strconv.Atoi(m[3])
		findings = append(findings, Finding{
			Linter: linter, File: m[1], Line: lineNum, Column: col,
			Severity: "warning", RuleID: m[4], Message: strings.TrimSpace(m[5]),
		})
	}
	return findings, nil
}

// taploMessageRE matches taplo's codespan_reporting message line, e.g. "error: invalid TOML" or
// "warning: some lint message".
var taploMessageRE = regexp.MustCompile(`^(error|warning): (.*)$`)

// taploLocationRE matches taplo's codespan_reporting location line, e.g.
// "┌─ /path/to/file.toml:2:8" (the box-drawing characters are U+250C U+2500, real bytes from a
// direct local capture of `taplo lint`, not typed manually as ASCII lookalikes).
var taploLocationRE = regexp.MustCompile(`^┌─ (.+):(\d+):(\d+)$`)

// ParseTaplo decodes taplo's default `taplo lint` output: a multi-line codespan_reporting-style
// diagnostic block per finding (real shape captured by running `npx @taplo/cli lint` locally
// against a broken TOML file -- see the design spec). Each block's first line ("error: ..."/
// "warning: ...") carries the message; a following "┌─ file:line:col" line carries the location.
// Lines that match neither pattern (context/caret lines, or taplo's own tracing-crate INFO/ERROR
// log lines interleaved in the same stream) are silently ignored, not treated as findings or
// errors.
func ParseTaplo(data []byte, linter string) ([]Finding, error) {
	var findings []Finding
	var pendingSeverity, pendingMessage string
	havePending := false

	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)

		if m := taploMessageRE.FindStringSubmatch(trimmed); m != nil {
			pendingSeverity, pendingMessage = m[1], m[2]
			havePending = true
			continue
		}
		if m := taploLocationRE.FindStringSubmatch(trimmed); m != nil && havePending {
			lineNum, err := strconv.Atoi(m[2])
			if err != nil {
				havePending = false
				continue
			}
			col, _ := strconv.Atoi(m[3])
			findings = append(findings, Finding{
				Linter: linter, File: m[1], Line: lineNum, Column: col,
				Severity: pendingSeverity, Message: pendingMessage,
			})
			havePending = false
		}
	}
	return findings, nil
}
