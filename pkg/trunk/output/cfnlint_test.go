package output

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseCfnLint(t *testing.T) {
	const sample = `[{"Filename":"template.yml","Level":"Warning","Location":{"Start":{"LineNumber":115,"ColumnNumber":3}},"Message":"S3 bucket should have logging","Rule":{"Id":"W3011"}}]`
	got, err := ParseCfnLint([]byte(sample), "cfn-lint")
	require.NoError(t, err)
	want := []Finding{
		{Linter: "cfn-lint", File: "template.yml", Line: 115, Column: 3, Severity: "warning", RuleID: "W3011", Message: "S3 bucket should have logging"},
	}
	assert.Equal(t, want, got)
}
