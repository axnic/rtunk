package ignore_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/ignore"
	"github.com/xunleii/rtunk/pkg/trunk/output"
)

func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	return name
}

func TestFilter_SameLine(t *testing.T) {
	dir := t.TempDir()
	file := write(t, dir, "a.go", "line1\nfoo() // rtunk-ignore(golangci-lint2/errcheck)\nline3\n")

	kept, suppressed := ignore.Filter(dir, []output.Finding{
		{File: file, Line: 2, Linter: "golangci-lint2", RuleID: "errcheck"},
		{File: file, Line: 3, Linter: "golangci-lint2", RuleID: "errcheck"},
	})
	assert.Equal(t, 1, suppressed)
	require.Len(t, kept, 1)
	assert.Equal(t, 3, kept[0].Line)
}

func TestFilter_NextLine(t *testing.T) {
	dir := t.TempDir()
	file := write(t, dir, "a.go", "line1\n  // rtunk-ignore(eslint/no-console)\nconsole.log(1)\n")

	kept, suppressed := ignore.Filter(dir, []output.Finding{
		{File: file, Line: 3, Linter: "eslint", RuleID: "no-console"},
	})
	assert.Equal(t, 1, suppressed)
	assert.Empty(t, kept)
}

func TestFilter_LinterOnlyMatchesEveryRule(t *testing.T) {
	dir := t.TempDir()
	file := write(t, dir, "a.go", "foo() // rtunk-ignore(golangci-lint2)\n")

	kept, suppressed := ignore.Filter(dir, []output.Finding{
		{File: file, Line: 1, Linter: "golangci-lint2", RuleID: "errcheck"},
		{File: file, Line: 1, Linter: "golangci-lint2", RuleID: "revive"},
	})
	assert.Equal(t, 2, suppressed)
	assert.Empty(t, kept)
}

func TestFilter_CommaList(t *testing.T) {
	dir := t.TempDir()
	file := write(t, dir, "a.go", "x // rtunk-ignore(eslint/no-console,no-unused-vars)\n")

	kept, suppressed := ignore.Filter(dir, []output.Finding{
		{File: file, Line: 1, Linter: "eslint", RuleID: "no-console"},
		{File: file, Line: 1, Linter: "eslint", RuleID: "no-unused-vars"},
		{File: file, Line: 1, Linter: "eslint", RuleID: "eqeqeq"},
	})
	assert.Equal(t, 2, suppressed)
	require.Len(t, kept, 1)
	assert.Equal(t, "eqeqeq", kept[0].RuleID)
}

func TestFilter_CommaListOfWholeLinters(t *testing.T) {
	dir := t.TempDir()
	file := write(t, dir, "a.go", "x // rtunk-ignore(eslint,prettier)\n")

	kept, suppressed := ignore.Filter(dir, []output.Finding{
		{File: file, Line: 1, Linter: "eslint", RuleID: "no-console"},
		{File: file, Line: 1, Linter: "prettier", RuleID: ""},
		{File: file, Line: 1, Linter: "golangci-lint2", RuleID: "errcheck"},
	})
	assert.Equal(t, 2, suppressed)
	require.Len(t, kept, 1)
	assert.Equal(t, "golangci-lint2", kept[0].Linter)
}

func TestFilter_RuleContainsSlash(t *testing.T) {
	dir := t.TempDir()
	file := write(t, dir, "a.md", "x <!-- rtunk-ignore(markdownlint/MD013/line-length) -->\n")

	kept, suppressed := ignore.Filter(dir, []output.Finding{
		{File: file, Line: 1, Linter: "markdownlint", RuleID: "MD013/line-length"},
	})
	assert.Equal(t, 1, suppressed)
	assert.Empty(t, kept)
}

func TestFilter_TrunkIgnoreAlias(t *testing.T) {
	dir := t.TempDir()
	file := write(t, dir, "a.go", "foo() // trunk-ignore(golangci-lint2/errcheck)\n")

	_, suppressed := ignore.Filter(dir, []output.Finding{
		{File: file, Line: 1, Linter: "golangci-lint2", RuleID: "errcheck"},
	})
	assert.Equal(t, 1, suppressed)
}

func TestFilter_All(t *testing.T) {
	dir := t.TempDir()
	file := write(t, dir, "a.go", "// rtunk-ignore-all(gitleaks)\nline2\nline3\n")

	kept, suppressed := ignore.Filter(dir, []output.Finding{
		{File: file, Line: 2, Linter: "gitleaks", RuleID: "generic-secret"},
		{File: file, Line: 300, Linter: "gitleaks", RuleID: "aws-key"},
		{File: file, Line: 2, Linter: "eslint", RuleID: "no-console"},
	})
	assert.Equal(t, 2, suppressed)
	require.Len(t, kept, 1)
	assert.Equal(t, "eslint", kept[0].Linter)
}

func TestFilter_BeginEndRange(t *testing.T) {
	dir := t.TempDir()
	file := write(t, dir, "a.go", joinLines(
		"// rtunk-ignore-begin(golangci-lint2/errcheck)",
		"line2",
		"line3",
		"// rtunk-ignore-end(golangci-lint2/errcheck)",
		"line5",
	))

	kept, suppressed := ignore.Filter(dir, []output.Finding{
		{File: file, Line: 2, Linter: "golangci-lint2", RuleID: "errcheck"},
		{File: file, Line: 3, Linter: "golangci-lint2", RuleID: "errcheck"},
		{File: file, Line: 5, Linter: "golangci-lint2", RuleID: "errcheck"},
	})
	assert.Equal(t, 2, suppressed)
	require.Len(t, kept, 1)
	assert.Equal(t, 5, kept[0].Line)
}

func TestFilter_UnmatchedBeginSuppressesNothing(t *testing.T) {
	dir := t.TempDir()
	file := write(t, dir, "a.go", joinLines(
		"// rtunk-ignore-begin(golangci-lint2/errcheck)",
		"line2",
		"line3",
	))

	kept, suppressed := ignore.Filter(dir, []output.Finding{
		{File: file, Line: 2, Linter: "golangci-lint2", RuleID: "errcheck"},
	})
	assert.Equal(t, 0, suppressed)
	assert.Len(t, kept, 1)
}

func TestFilter_LineZeroOnlySuppressedByAll(t *testing.T) {
	dir := t.TempDir()
	file := write(t, dir, "a.go", "foo() // rtunk-ignore(passfail)\n// rtunk-ignore-all(passfail)\n")

	kept, suppressed := ignore.Filter(dir, []output.Finding{
		{File: file, Line: 0, Linter: "passfail", RuleID: ""},
	})
	assert.Equal(t, 1, suppressed)
	assert.Empty(t, kept)
}

func TestFilter_EmptyFileNoOp(t *testing.T) {
	kept, suppressed := ignore.Filter(t.TempDir(), []output.Finding{{File: "", Line: 1, Linter: "x"}})
	assert.Equal(t, 0, suppressed)
	assert.Len(t, kept, 1)
}

func TestFilter_UnreadableFilePassesThrough(t *testing.T) {
	kept, suppressed := ignore.Filter(t.TempDir(), []output.Finding{{File: "does-not-exist.go", Line: 1, Linter: "x"}})
	assert.Equal(t, 0, suppressed)
	assert.Len(t, kept, 1)
}

func joinLines(lines ...string) string {
	out := ""
	for _, l := range lines {
		out += l + "\n"
	}
	return out
}
