package output

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePylint(t *testing.T) {
	const sample = `[{"type":"convention","line":3,"column":0,"path":"app.py","message":"Missing module docstring","message-id":"C0111"}]`
	got, err := ParsePylint([]byte(sample), "pylint")
	require.NoError(t, err)
	want := []Finding{
		{Linter: "pylint", File: "app.py", Line: 3, Column: 0, Severity: "info", RuleID: "C0111", Message: "Missing module docstring"},
	}
	assert.Equal(t, want, got)
}
