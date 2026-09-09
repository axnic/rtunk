package output

import (
	"encoding/json"
	"fmt"

	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/internal/diagnostic"
)

type markdownlintIssue struct {
	FileName        string   `json:"fileName"`
	LineNumber      int      `json:"lineNumber"`
	RuleNames       []string `json:"ruleNames"`
	RuleDescription string   `json:"ruleDescription"`
	ErrorDetail     *string  `json:"errorDetail"`
	ErrorRange      []int    `json:"errorRange"`
}

// ParseMarkdownlint converts markdownlint-cli's `--json` output — written to
// stderr, not stdout, confirmed empirically — into diagnostics. A
// linter-specific hardcoded parser (SPECS.md §8.3 P2), since trunk's own
// plugin.yaml names this a custom type with no published schema of its own.
func ParseMarkdownlint(raw, linterName, commandName string) ([]diagnostic.Diagnostic, error) {
	if raw == "" {
		return nil, nil
	}
	var issues []markdownlintIssue
	if err := json.Unmarshal([]byte(raw), &issues); err != nil {
		return nil, fmt.Errorf("parse markdownlint json: %w", err)
	}
	diags := make([]diagnostic.Diagnostic, len(issues))
	for i, iss := range issues {
		var code string
		if len(iss.RuleNames) > 0 {
			code = iss.RuleNames[0]
		}
		message := iss.RuleDescription
		if iss.ErrorDetail != nil {
			message += ": " + *iss.ErrorDetail
		}
		var col int
		if len(iss.ErrorRange) > 0 {
			col = iss.ErrorRange[0]
		}
		diags[i] = diagnostic.Diagnostic{
			Path:        iss.FileName,
			Line:        iss.LineNumber,
			Col:         col,
			Code:        code,
			Message:     message,
			LinterName:  linterName,
			CommandName: commandName,
		}
	}
	return diags, nil
}
