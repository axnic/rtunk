package render

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/run/engine"
	"github.com/xunleii/rtunk/pkg/trunk/output"
)

func decodeSARIF(t *testing.T, s string) map[string]any {
	t.Helper()
	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(s), &doc))
	return doc
}

func TestSARIF_Structure(t *testing.T) {
	events := []engine.Event{
		{Linter: "lint", Phase: engine.Done, Files: []string{"a.go", "b.go"}, Findings: []output.Finding{
			{File: "a.go", Line: 1, Severity: "error", RuleID: "r0", Message: "m1", URL: "https://x/r0"},
			{File: "a.go", Line: 5, Column: 3, Severity: "warning", RuleID: "r0", Message: "a <b> & c"},
			{File: "b.go", Line: 0, Severity: "info", Message: "no position"},
		}},
		{Linter: "gitleaks", Phase: engine.Failed, Err: errors.New("boom\nsecond")},
		{Linter: "shfmt", Phase: engine.Skipped, Note: "unsupported"},
	}
	stdout, _ := run(t, Options{Format: SARIF, Command: Check, Version: "v9.9.9"}, events, Summary{})
	doc := decodeSARIF(t, stdout)

	assert.Equal(t, "2.1.0", doc["version"])
	assert.NotEmpty(t, doc["$schema"])
	runs := doc["runs"].([]any)
	require.Len(t, runs, 1)
	r := runs[0].(map[string]any)

	driver := r["tool"].(map[string]any)["driver"].(map[string]any)
	assert.Equal(t, "rtunk", driver["name"])
	assert.Equal(t, "v9.9.9", driver["version"])
	rules := driver["rules"].([]any)
	require.Len(t, rules, 2, "lint/r0 once (deduplicated), lint for the rule-less finding")
	assert.Equal(t, "lint/r0", rules[0].(map[string]any)["id"])
	assert.Equal(t, "https://x/r0", rules[0].(map[string]any)["helpUri"], "first URL seen for the rule")
	assert.Equal(t, "lint", rules[1].(map[string]any)["id"])

	results := r["results"].([]any)
	require.Len(t, results, 3)
	first := results[0].(map[string]any)
	assert.Equal(t, "lint/r0", first["ruleId"])
	assert.Equal(t, "error", first["level"])
	loc := first["locations"].([]any)[0].(map[string]any)["physicalLocation"].(map[string]any)
	assert.Equal(t, "a.go", loc["artifactLocation"].(map[string]any)["uri"])
	region := loc["region"].(map[string]any)
	assert.EqualValues(t, 1, region["startLine"])
	assert.NotContains(t, region, "startColumn", "omitted when 0")

	second := results[1].(map[string]any)
	assert.Equal(t, "warning", second["level"])
	assert.EqualValues(t, 3, second["locations"].([]any)[0].(map[string]any)["physicalLocation"].(map[string]any)["region"].(map[string]any)["startColumn"])
	assert.Contains(t, stdout, `"a <b> & c"`, "HTML characters are not escaped")

	third := results[2].(map[string]any)
	assert.Equal(t, "note", third["level"])
	assert.NotContains(t, third["locations"].([]any)[0].(map[string]any)["physicalLocation"], "region", "no region when line is 0")

	inv := r["invocations"].([]any)[0].(map[string]any)
	assert.Equal(t, false, inv["executionSuccessful"])
	notes := inv["toolExecutionNotifications"].([]any)
	require.Len(t, notes, 1, "the skipped linter is not represented")
	assert.Equal(t, "gitleaks: boom", notes[0].(map[string]any)["message"].(map[string]any)["text"])
}

func TestSARIF_CleanRun(t *testing.T) {
	events := []engine.Event{{Linter: "gofmt", Phase: engine.Done, Files: []string{"a.go"}}}
	stdout, _ := run(t, Options{Format: SARIF, Command: Check}, events, Summary{})
	r := decodeSARIF(t, stdout)["runs"].([]any)[0].(map[string]any)
	assert.Empty(t, r["results"])
	assert.NotNil(t, r["results"], "[] not null")
	assert.Empty(t, r["tool"].(map[string]any)["driver"].(map[string]any)["rules"])
	inv := r["invocations"].([]any)[0].(map[string]any)
	assert.Equal(t, true, inv["executionSuccessful"])
	assert.Empty(t, inv["toolExecutionNotifications"])
	assert.NotNil(t, inv["toolExecutionNotifications"])
}

func TestSARIF_SecurityFinding_RuleTaggedSecurityInProperties(t *testing.T) {
	events := []engine.Event{
		{Linter: "bandit", Phase: engine.Done, Files: []string{"a.py"}, Findings: []output.Finding{
			{File: "a.py", Line: 1, Severity: "error", RuleID: "B101", Message: "m1", IsSecurity: true},
		}},
		{Linter: "lint", Phase: engine.Done, Files: []string{"a.go"}, Findings: []output.Finding{
			{File: "a.go", Line: 1, Severity: "error", RuleID: "r0", Message: "m2"},
		}},
	}
	stdout, _ := run(t, Options{Format: SARIF, Command: Check}, events, Summary{})
	r := decodeSARIF(t, stdout)["runs"].([]any)[0].(map[string]any)
	rules := r["tool"].(map[string]any)["driver"].(map[string]any)["rules"].([]any)
	require.Len(t, rules, 2)

	byID := map[string]map[string]any{}
	for _, rule := range rules {
		m := rule.(map[string]any)
		byID[m["id"].(string)] = m
	}

	props := byID["bandit/B101"]["properties"].(map[string]any)
	assert.Equal(t, []any{"security"}, props["tags"])
	assert.NotContains(t, byID["lint/r0"], "properties")
}

func TestSARIF_SummaryFailuresMakeTheRunUnsuccessful(t *testing.T) {
	events := []engine.Event{{Linter: "gofmt", Phase: engine.Done, Files: []string{"a.go"}}}
	stdout, _ := run(t, Options{Format: SARIF, Command: Check}, events, Summary{Failures: []Failure{{Linter: "fmt", Err: "did not converge"}}})
	inv := decodeSARIF(t, stdout)["runs"].([]any)[0].(map[string]any)["invocations"].([]any)[0].(map[string]any)
	assert.Equal(t, false, inv["executionSuccessful"])
	assert.Equal(t, "fmt: did not converge", inv["toolExecutionNotifications"].([]any)[0].(map[string]any)["message"].(map[string]any)["text"])
}
