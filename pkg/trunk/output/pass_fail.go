package output

// ParsePassFail builds one Finding per file for a pass_fail command whose exit code was non-zero
// (and not in ErrorCodes) -- pass_fail carries no line/column/rule, only "this file didn't pass".
func ParsePassFail(linter string, files []string) []Finding {
	findings := make([]Finding, 0, len(files))
	for _, f := range files {
		findings = append(findings, Finding{Linter: linter, File: f, Severity: "error", Message: "file did not pass"})
	}
	return findings
}
