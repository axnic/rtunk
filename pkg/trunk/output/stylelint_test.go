package output

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseStylelint(t *testing.T) {
	const sample = `[{"source":"a.css","warnings":[
		{"line":1,"column":2,"rule":"block-no-empty","severity":"warning","text":"Unexpected empty block (block-no-empty)"}
	]}]`
	got, err := ParseStylelint([]byte(sample), "stylelint")
	require.NoError(t, err)
	want := []Finding{
		{Linter: "stylelint", File: "a.css", Line: 1, Column: 2, Severity: "warning", RuleID: "block-no-empty", Message: "Unexpected empty block (block-no-empty)"},
	}
	assert.Equal(t, want, got)
}
