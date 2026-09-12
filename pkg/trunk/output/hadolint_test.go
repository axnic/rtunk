package output

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseHadolint(t *testing.T) {
	const sample = `[{"line":1,"code":"DL3006","message":"Always tag the version of an image explicitly","column":1,"file":"Dockerfile","level":"warning"}]`
	got, err := ParseHadolint([]byte(sample), "hadolint")
	require.NoError(t, err)
	want := []Finding{
		{Linter: "hadolint", File: "Dockerfile", Line: 1, Column: 1, Severity: "warning", RuleID: "DL3006", Message: "Always tag the version of an image explicitly"},
	}
	assert.Equal(t, want, got)
}
