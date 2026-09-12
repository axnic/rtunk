package output

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseBandit(t *testing.T) {
	const sample = `{"results":[
		{"filename":"app.py","line_number":42,"col_offset":4,"issue_severity":"HIGH","test_id":"B101","issue_text":"Use of assert detected. "},
		{"filename":"app.py","line_number":10,"col_offset":0,"issue_severity":"LOW","test_id":"B404","issue_text":"Consider possible security implications."}
	]}`
	got, err := ParseBandit([]byte(sample), "bandit")
	require.NoError(t, err)
	want := []Finding{
		{Linter: "bandit", File: "app.py", Line: 42, Column: 4, Severity: "error", RuleID: "B101", Message: "Use of assert detected."},
		{Linter: "bandit", File: "app.py", Line: 10, Column: 0, Severity: "info", RuleID: "B404", Message: "Consider possible security implications."},
	}
	assert.Equal(t, want, got)
}
