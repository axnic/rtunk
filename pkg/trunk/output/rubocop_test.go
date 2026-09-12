package output

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRubocop(t *testing.T) {
	const sample = `{"files":[{"path":"app.rb","offenses":[
		{"severity":"warning","message":"Line is too long.","cop_name":"Layout/LineLength","location":{"line":15,"column":9}}
	]}]}`
	got, err := ParseRubocop([]byte(sample), "standardrb")
	require.NoError(t, err)
	want := []Finding{
		{Linter: "standardrb", File: "app.rb", Line: 15, Column: 9, Severity: "warning", RuleID: "Layout/LineLength", Message: "Line is too long."},
	}
	assert.Equal(t, want, got)
}
