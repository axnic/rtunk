package output

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
