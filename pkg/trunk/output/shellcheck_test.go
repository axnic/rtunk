package output

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseShellcheck(t *testing.T) {
	// Captured verbatim from a real `shellcheck sample.sh -f json --external-sources` run
	// (shellcheck 0.11.0), against a script iterating `$(ls)` unquoted.
	const sample = `[{"file":"sample.sh","line":4,"endLine":4,"column":10,"endColumn":15,"level":"error","code":2045,"message":"Iterating over ls output is fragile. Use globs.","fix":null},{"file":"sample.sh","line":4,"endLine":4,"column":25,"endColumn":27,"level":"info","code":2086,"message":"Double quote to prevent globbing and word splitting.","fix":{"replacements":[{"column":25,"endColumn":25,"endLine":4,"insertionPoint":"afterEnd","line":4,"precedence":10,"replacement":"\""},{"column":27,"endColumn":27,"endLine":4,"insertionPoint":"beforeStart","line":4,"precedence":10,"replacement":"\""}]}}]`
	got, err := ParseShellcheck([]byte(sample), "shellcheck")
	require.NoError(t, err)
	want := []Finding{
		{Linter: "shellcheck", File: "sample.sh", Line: 4, Column: 10, Severity: "error", RuleID: "SC2045", Message: "Iterating over ls output is fragile. Use globs."},
		{Linter: "shellcheck", File: "sample.sh", Line: 4, Column: 25, Severity: "info", RuleID: "SC2086", Message: "Double quote to prevent globbing and word splitting."},
	}
	assert.Equal(t, want, got)
}

func TestParseShellcheck_Empty(t *testing.T) {
	got, err := ParseShellcheck([]byte(`[]`), "shellcheck")
	require.NoError(t, err)
	assert.Empty(t, got)
}
