package output

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseBuildifier(t *testing.T) {
	const sample = `{"success":false,"files":[{"filename":"BUILD","warnings":[
		{"start":{"line":3,"column":1},"category":"module-docstring","message":"The file has no module docstring."}
	]}]}`
	got, err := ParseBuildifier([]byte(sample), "buildifier")
	require.NoError(t, err)
	want := []Finding{
		{Linter: "buildifier", File: "BUILD", Line: 3, Column: 1, Severity: "warning", RuleID: "module-docstring", Message: "The file has no module docstring."},
	}
	assert.Equal(t, want, got)
}
