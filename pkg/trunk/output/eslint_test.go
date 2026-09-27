package output

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseESLint(t *testing.T) {
	const sample = `[{"filePath":"src/app.js","messages":[
		{"ruleId":"no-eval","severity":2,"message":"eval is evil","line":12,"column":4},
		{"ruleId":"prefer-const","severity":1,"message":"Use const","line":3,"column":1,"fix":{"range":[10,15],"text":"const"}}
	]}]`
	got, err := ParseESLint([]byte(sample), "eslint")
	require.NoError(t, err)
	want := []Finding{
		{Linter: "eslint", File: "src/app.js", Line: 12, Column: 4, Severity: "error", RuleID: "no-eval", Message: "eval is evil"},
		{Linter: "eslint", File: "src/app.js", Line: 3, Column: 1, Severity: "warning", RuleID: "prefer-const", Message: "Use const",
			Fix: &InlineFix{Range: [2]int{10, 15}, Text: "const"}},
	}
	assert.Equal(t, want, got)
}
