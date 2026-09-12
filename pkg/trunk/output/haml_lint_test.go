package output

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseHamlLint(t *testing.T) {
	const sample = `{"files":[{"path":"app.haml","offenses":[
		{"severity":"warning","message":"Line is too long.","linter_name":"LineLength","location":{"line":15}}
	]}]}`
	got, err := ParseHamlLint([]byte(sample), "haml-lint")
	require.NoError(t, err)
	want := []Finding{
		{Linter: "haml-lint", File: "app.haml", Line: 15, Severity: "warning", RuleID: "LineLength", Message: "Line is too long."},
	}
	assert.Equal(t, want, got)
}
