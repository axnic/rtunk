package output

import (
	"encoding/json"
	"fmt"

	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/internal/diagnostic"
)

type sarifLog struct {
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

// ParseSarif converts a SARIF 2.1.0 log (the format most real linters can
// emit natively — SPECS.md §8.3 P0, "toujours préférer sarif natif") into
// diagnostics. Unlike ParseRegex/ParseGitleaksJSON, each result carries its
// own severity (§8.1bis mapping: none/note->Note, warning->Warning,
// error->Error) — callers must not overwrite it with a per-command default.
func ParseSarif(raw, linterName, commandName string) ([]diagnostic.Diagnostic, error) {
	if raw == "" {
		return nil, nil
	}
	var log sarifLog
	if err := json.Unmarshal([]byte(raw), &log); err != nil {
		return nil, fmt.Errorf("parse sarif: %w", err)
	}

	var diags []diagnostic.Diagnostic
	for _, run := range log.Runs {
		for _, r := range run.Results {
			d := diagnostic.Diagnostic{
				Code:        r.RuleID,
				Message:     r.Message.Text,
				Severity:    sarifSeverity(r.Level),
				LinterName:  linterName,
				CommandName: commandName,
			}
			if len(r.Locations) > 0 {
				loc := r.Locations[0].PhysicalLocation
				d.Path = loc.ArtifactLocation.URI
				d.Line = loc.Region.StartLine
				d.Col = loc.Region.StartColumn
			}
			diags = append(diags, d)
		}
	}
	return diags, nil
}

func sarifSeverity(level string) diagnostic.Severity {
	switch level {
	case "error":
		return diagnostic.Error
	case "warning":
		return diagnostic.Warning
	default: // "note", "none", or unset
		return diagnostic.Note
	}
}
