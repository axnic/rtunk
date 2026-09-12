package check

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sampleSARIF = `{
  "runs": [
    {
      "results": [
        {
          "ruleId": "no-eval",
          "level": "error",
          "message": {"text": "eval is evil"},
          "locations": [
            {"physicalLocation": {"artifactLocation": {"uri": "src/app.js"}, "region": {"startLine": 12, "startColumn": 4}}}
          ]
        },
        {
          "ruleId": "style",
          "level": "warning",
          "message": {"text": "prefer const"},
          "locations": [
            {"physicalLocation": {"artifactLocation": {"uri": "src/other.js"}, "region": {"startLine": 3}}}
          ]
        },
        {
          "ruleId": "tool-note",
          "level": "note",
          "message": {"text": "no location info"},
          "locations": []
        }
      ]
    }
  ]
}`

func TestParseSARIF(t *testing.T) {
	got, err := ParseSARIF([]byte(sampleSARIF), "eslint")
	require.NoError(t, err)
	want := []Finding{
		{Linter: "eslint", File: "src/app.js", Line: 12, Column: 4, Severity: "error", RuleID: "no-eval", Message: "eval is evil"},
		{Linter: "eslint", File: "src/other.js", Line: 3, Column: 0, Severity: "warning", RuleID: "style", Message: "prefer const"},
	}
	assert.Equal(t, want, got, "the no-location result must be skipped, not errored")
}

func TestParseSARIF_InvalidJSON(t *testing.T) {
	_, err := ParseSARIF([]byte("not json"), "eslint")
	require.Error(t, err)
}

func TestParsePassFail(t *testing.T) {
	got := ParsePassFail("check-added-large-files", []string{"a.bin", "b.bin"})
	want := []Finding{
		{Linter: "check-added-large-files", File: "a.bin", Severity: "error", Message: "file did not pass"},
		{Linter: "check-added-large-files", File: "b.bin", Severity: "error", Message: "file did not pass"},
	}
	assert.Equal(t, want, got)
}

func TestApplyIssueURL(t *testing.T) {
	findings := []Finding{{RuleID: "no-eval"}, {RuleID: ""}}
	ApplyIssueURL(findings, "https://example.com/rules/{}")
	assert.Equal(t, "https://example.com/rules/no-eval", findings[0].URL)
	assert.Equal(t, "", findings[1].URL, "no RuleID means no URL even with a format set")
}

func TestApplyIssueURL_EmptyFormat(t *testing.T) {
	findings := []Finding{{RuleID: "no-eval"}}
	ApplyIssueURL(findings, "")
	assert.Equal(t, "", findings[0].URL)
}
