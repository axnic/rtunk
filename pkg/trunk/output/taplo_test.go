package output

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTaplo(t *testing.T) {
	const sample = "INFO taplo:lint_files:collect_files: found files total=1 excluded=0 cwd=\"/tmp/x\"\n" +
		"error: invalid TOML\n" +
		"  ┌─ /tmp/x/bad.toml:2:8\n" +
		"  │\n" +
		"2 │ name = \"test\n" +
		"  │        ^ unexpected token\n" +
		"\n" +
		"warning: trailing comma\n" +
		"  ┌─ /tmp/x/bad.toml:5:12\n" +
		"  │\n" +
		"5 │ list = [1, 2,]\n" +
		"  │             ^ trailing comma\n" +
		"\n" +
		"ERROR taplo:lint_files: invalid file error=syntax errors found path=\"/tmp/x/bad.toml\"\n"

	got, err := ParseTaplo([]byte(sample), "taplo")
	require.NoError(t, err)
	want := []Finding{
		{Linter: "taplo", File: "/tmp/x/bad.toml", Line: 2, Column: 8, Severity: "error", Message: "invalid TOML"},
		{Linter: "taplo", File: "/tmp/x/bad.toml", Line: 5, Column: 12, Severity: "warning", Message: "trailing comma"},
	}
	assert.Equal(t, want, got, "the INFO/ERROR tracing-crate log lines must be ignored, not mistaken for findings")
}
