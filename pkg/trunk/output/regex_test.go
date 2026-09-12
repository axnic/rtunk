package output

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseFromRegex_Perlcritic(t *testing.T) {
	pattern := `path=(?P<path>.*?),line=(?P<line>\d+),col=(?P<col>\d+),code=(?P<code>.*?),message=(?P<message>.*)`
	data := "path=lib/Foo.pm,line=12,col=3,code=Perl::Critic::Policy::Foo,message=some violation\n"
	got, err := ParseFromRegex(pattern, []byte(data), "perlcritic")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, Finding{
		Linter: "perlcritic", File: "lib/Foo.pm", Line: 12, Column: 3,
		Severity: "", RuleID: "Perl::Critic::Policy::Foo", Message: "some violation",
	}, got[0], "perlcritic's real parse_regex has no severity group at all -- Severity must stay empty, not default to anything")
}

func TestParseFromRegex_Yamllint(t *testing.T) {
	pattern := `((?P<path>.*):(?P<line>\d+):(?P<col>\d+): \[(?P<severity>.*)\] (?P<message>.*) \((?P<code>.*)\))`
	data := ".trunk/trunk.yaml:7:81: [error] line too long (82 > 80 characters) (line-length)\n"
	got, err := ParseFromRegex(pattern, []byte(data), "yamllint")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, Finding{
		Linter: "yamllint", File: ".trunk/trunk.yaml", Line: 7, Column: 81,
		Severity: "error", RuleID: "line-length", Message: "line too long (82 > 80 characters)",
	}, got[0], "this is trunk's own real documented example -- see the design spec")
}

func TestParseFromRegex_GitDiffCheck_NoColumnOrCodeGroup(t *testing.T) {
	pattern := `((?P<path>.*):(?P<line>-?\d+):(?P<message>.*))`
	data := "foo.go:12: trailing whitespace.\n"
	got, err := ParseFromRegex(pattern, []byte(data), "git-diff-check")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, Finding{
		Linter: "git-diff-check", File: "foo.go", Line: 12, Column: 0,
		Severity: "", RuleID: "", Message: " trailing whitespace.",
	}, got[0], "no col/severity/code group in this pattern at all -- those Finding fields must stay zero-valued")
}

func TestParseFromRegex_MultipleMatches(t *testing.T) {
	pattern := `(?P<path>.*):(?P<line>\d+): (?P<message>.*)`
	data := "a.txt:1: first\nb.txt:2: second\n"
	got, err := ParseFromRegex(pattern, []byte(data), "fake")
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "a.txt", got[0].File)
	assert.Equal(t, "b.txt", got[1].File)
}

func TestParseFromRegex_UnmappedGroupNameIsIgnoredNotAliased(t *testing.T) {
	// Mirrors ty's real parse_regex, which names its column group "column", not "col". "column"
	// has no entry in ParseFromRegex's mapping table -- must NOT be treated as an alias for "col".
	pattern := `(?P<path>.*):(?P<line>\d+):(?P<column>\d+): (?P<message>.*)`
	data := "a.py:1:5: bad thing\n"
	got, err := ParseFromRegex(pattern, []byte(data), "ty")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, 0, got[0].Column, `"column" is not "col" -- must stay unmapped, never guessed`)
}

func TestParseFromRegex_SeverityNormalization(t *testing.T) {
	pattern := `(?P<path>.*):(?P<severity>\w+):(?P<message>.*)`
	for raw, want := range map[string]string{
		"error": "error", "deny": "error",
		"warning": "warning", "allow": "warning",
		"info": "info", "note": "info", "notice": "info", "disabled": "info", "unknown": "info",
	} {
		got, err := ParseFromRegex(pattern, []byte("f.txt:"+raw+":msg\n"), "fake")
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, want, got[0].Severity, "raw severity %q", raw)
	}
}

func TestParseFromRegex_InvalidPattern(t *testing.T) {
	_, err := ParseFromRegex(`(unterminated`, []byte("x"), "fake")
	assert.Error(t, err)
}

func TestParseFromRegex_EmptyPatternIsAnErrorNotMatchEverything(t *testing.T) {
	// A stale cache (or a hand-authored trunk.yaml with output: regex and no parse_regex) would
	// otherwise compile "" successfully -- an empty regexp matches at every byte offset, turning
	// this into a flood of empty Findings (len(data)+1 of them) instead of a clear failure.
	got, err := ParseFromRegex("", []byte("some real linter output\nspanning two lines\n"), "fake")
	require.ErrorIs(t, err, ErrEmptyPattern)
	assert.Nil(t, got)
}

func TestParseFromRegex_NoMatchesReturnsEmpty(t *testing.T) {
	got, err := ParseFromRegex(`(?P<path>.*):(?P<line>\d+): (?P<message>.*)`, []byte("not matching at all\n"), "fake")
	require.NoError(t, err)
	assert.Empty(t, got)
}
