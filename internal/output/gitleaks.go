package output

import (
	"encoding/json"
	"fmt"

	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/internal/diagnostic"
)

type gitleaksFinding struct {
	RuleID      string `json:"RuleID"`
	Description string `json:"Description"`
	StartLine   int    `json:"StartLine"`
	StartColumn int    `json:"StartColumn"`
	File        string `json:"File"`
}

// ParseGitleaksJSON converts gitleaks' `--report-format json` output into
// diagnostics — a linter-specific hardcoded parser (SPECS.md §8.3, P2
// category: "types propres au linter... au cas par cas") because gitleaks'
// JSON doesn't fit the generic `regex`/`sarif` moulds.
func ParseGitleaksJSON(raw, linterName, commandName string) ([]diagnostic.Diagnostic, error) {
	if raw == "" {
		return nil, nil
	}
	var findings []gitleaksFinding
	if err := json.Unmarshal([]byte(raw), &findings); err != nil {
		return nil, fmt.Errorf("parse gitleaks json: %w", err)
	}
	diags := make([]diagnostic.Diagnostic, len(findings))
	for i, f := range findings {
		diags[i] = diagnostic.Diagnostic{
			Path:        f.File,
			Line:        f.StartLine,
			Col:         f.StartColumn,
			Code:        f.RuleID,
			Message:     f.Description,
			LinterName:  linterName,
			CommandName: commandName,
		}
	}
	return diags, nil
}
