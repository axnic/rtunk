package output

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseMarkdownlint(t *testing.T) {
	// A flat JSON array (real `npx markdownlint-cli bad.md --json` capture, trimmed of the
	// ruleInformation/errorContext/fixInfo fields this parser doesn't consume) -- NOT an object
	// keyed by filename, which was this function's incorrect original assumption.
	const sample = `[
		{"fileName":"bad.md","lineNumber":3,"ruleNames":["MD010","no-hard-tabs"],"ruleDescription":"Hard tabs","errorRange":[1,1],"severity":"error"}
	]`
	got, err := ParseMarkdownlint([]byte(sample), "markdownlint")
	require.NoError(t, err)
	want := []Finding{
		{Linter: "markdownlint", File: "bad.md", Line: 3, Column: 1, Severity: "error", RuleID: "MD010", Message: "Hard tabs"},
	}
	assert.Equal(t, want, got)
}

func TestParseMarkdownlint_MissingSeverityDefaultsToError(t *testing.T) {
	const sample = `[
		{"fileName":"bad.md","lineNumber":5,"ruleNames":["MD047"],"ruleDescription":"Files should end with a single newline","errorRange":null}
	]`
	got, err := ParseMarkdownlint([]byte(sample), "markdownlint")
	require.NoError(t, err)
	want := []Finding{
		{Linter: "markdownlint", File: "bad.md", Line: 5, Column: 0, Severity: "error", RuleID: "MD047", Message: "Files should end with a single newline"},
	}
	assert.Equal(t, want, got)
}
