// Package output normalizes every check-engine linter output format (SARIF, per-tool JSON
// schemas, and config-declared regex patterns) into a common Finding shape.
package output

import "strings"

// InlineFix is a computer-applicable replacement a tool attaches directly to one of its own
// findings (real example: ESLint's `--format json` messages[].fix), rather than through a
// separate fix command (see Command.InPlace+!Formatter for that case, ROADMAP.md v0.10 "Fix-only
// linters actually fix"). Range is a byte offset pair [start, end) into the file's content at the
// time the tool produced it; Text replaces that span verbatim.
type InlineFix struct {
	Range [2]int
	Text  string
}

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
	// Fix is this finding's own computer-applied replacement, when its tool reported one inline
	// (nil otherwise). Applied by engine.ApplyInlineFixes under check --fix.
	Fix *InlineFix
	// IsSecurity is true when the reporting command's own config.Command.IsSecurity is set (see
	// ApplyIsSecurity) -- every real parser leaves this at its zero value; it is filled in after
	// parsing, exactly like URL is by ApplyIssueURL.
	IsSecurity bool
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

// ApplyIsSecurity tags every finding with isSecurity (a command's own config.Command.IsSecurity),
// mirroring ApplyIssueURL's own one-pass-after-parsing shape -- see that function's doc comment
// for why this isn't done per-parser.
func ApplyIsSecurity(findings []Finding, isSecurity bool) {
	if !isSecurity {
		return
	}
	for i := range findings {
		findings[i].IsSecurity = true
	}
}
