package output

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseActionlint(t *testing.T) {
	const sample = `[{"message":"shellcheck reported issue","filepath":".github/workflows/ci.yml","line":12,"column":3,"kind":"shellcheck"}]`
	got, err := ParseActionlint([]byte(sample), "actionlint")
	require.NoError(t, err)
	want := []Finding{
		{Linter: "actionlint", File: ".github/workflows/ci.yml", Line: 12, Column: 3, Severity: "error", RuleID: "shellcheck", Message: "shellcheck reported issue"},
	}
	assert.Equal(t, want, got)
}

func TestParseActionlint_InvalidJSON(t *testing.T) {
	_, err := ParseActionlint([]byte("not json"), "actionlint")
	require.Error(t, err)
}
