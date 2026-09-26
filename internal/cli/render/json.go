package render

import (
	"encoding/json"
	"io"
)

// jsonRenderer writes the run as one JSON document (docs: spec 2026-09-26-v0.9.2, section 4).
type jsonRenderer struct{ base }

type jsonIssue struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Linter   string `json:"linter"`
	Rule     string `json:"rule"`
	URL      string `json:"url"`
}

type jsonFailure struct {
	Linter string `json:"linter"`
	Error  string `json:"error"`
}

type jsonDoc struct {
	Version      int           `json:"version"`
	Command      string        `json:"command"`
	ElapsedMs    int64         `json:"elapsed_ms"`
	RunLog       string        `json:"run_log"`
	FilesChecked int           `json:"files_checked"`
	Linters      int           `json:"linters"`
	Issues       []jsonIssue   `json:"issues"`
	Failures     []jsonFailure `json:"failures"`
	Skipped      []string      `json:"skipped"`
	Changed      []string      `json:"changed"`
}

func (j *jsonRenderer) Close(s Summary) error {
	doc := jsonDoc{
		Version: 1, Command: "check", ElapsedMs: s.Elapsed.Milliseconds(), RunLog: s.RunLog,
		FilesChecked: len(j.files), Linters: len(j.linters),
		Issues: []jsonIssue{}, Failures: []jsonFailure{},
		Skipped: append([]string{}, s.Skipped...), Changed: sortedUnique(s.Changed),
	}
	if j.opts.Command != Check {
		doc.Command = "fmt"
	}
	for _, f := range j.sortedFindings() {
		doc.Issues = append(doc.Issues, jsonIssue{
			File: f.File, Line: f.Line, Column: f.Column, Severity: severity(f.Severity),
			Message: f.Message, Linter: f.Linter, Rule: f.RuleID, URL: f.URL,
		})
	}
	for _, f := range j.sortedFailures() {
		doc.Failures = append(doc.Failures, jsonFailure{Linter: f.Linter, Error: f.Err})
	}
	return writeJSON(j.stdout, doc)
}

// writeJSON encodes v with two-space indentation, a trailing newline and no HTML escaping.
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
