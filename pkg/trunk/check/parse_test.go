package check

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sampleSARIF = `{
  "runs": [
    {
      "results": [
        {
          "ruleId": "no-eval",
          "level": "error",
          "message": {"text": "eval is evil"},
          "locations": [
            {"physicalLocation": {"artifactLocation": {"uri": "src/app.js"}, "region": {"startLine": 12, "startColumn": 4}}}
          ]
        },
        {
          "ruleId": "style",
          "level": "warning",
          "message": {"text": "prefer const"},
          "locations": [
            {"physicalLocation": {"artifactLocation": {"uri": "src/other.js"}, "region": {"startLine": 3}}}
          ]
        },
        {
          "ruleId": "tool-note",
          "level": "note",
          "message": {"text": "no location info"},
          "locations": []
        }
      ]
    }
  ]
}`

func TestParseSARIF(t *testing.T) {
	got, err := ParseSARIF([]byte(sampleSARIF), "eslint")
	require.NoError(t, err)
	want := []Finding{
		{Linter: "eslint", File: "src/app.js", Line: 12, Column: 4, Severity: "error", RuleID: "no-eval", Message: "eval is evil"},
		{Linter: "eslint", File: "src/other.js", Line: 3, Column: 0, Severity: "warning", RuleID: "style", Message: "prefer const"},
	}
	assert.Equal(t, want, got, "the no-location result must be skipped, not errored")
}

func TestParseSARIF_InvalidJSON(t *testing.T) {
	_, err := ParseSARIF([]byte("not json"), "eslint")
	require.Error(t, err)
}

func TestParsePassFail(t *testing.T) {
	got := ParsePassFail("check-added-large-files", []string{"a.bin", "b.bin"})
	want := []Finding{
		{Linter: "check-added-large-files", File: "a.bin", Severity: "error", Message: "file did not pass"},
		{Linter: "check-added-large-files", File: "b.bin", Severity: "error", Message: "file did not pass"},
	}
	assert.Equal(t, want, got)
}

func TestApplyIssueURL(t *testing.T) {
	findings := []Finding{{RuleID: "no-eval"}, {RuleID: ""}}
	ApplyIssueURL(findings, "https://example.com/rules/{}")
	assert.Equal(t, "https://example.com/rules/no-eval", findings[0].URL)
	assert.Equal(t, "", findings[1].URL, "no RuleID means no URL even with a format set")
}

func TestApplyIssueURL_EmptyFormat(t *testing.T) {
	findings := []Finding{{RuleID: "no-eval"}}
	ApplyIssueURL(findings, "")
	assert.Equal(t, "", findings[0].URL)
}

func TestParseActionlint(t *testing.T) {
	const sample = `[{"message":"shellcheck reported issue","filepath":".github/workflows/ci.yml","line":12,"column":3,"kind":"shellcheck"}]`
	got, err := ParseActionlint([]byte(sample), "actionlint")
	require.NoError(t, err)
	want := []Finding{
		{Linter: "actionlint", File: ".github/workflows/ci.yml", Line: 12, Column: 3, Severity: "error", RuleID: "shellcheck", Message: "shellcheck reported issue"},
	}
	assert.Equal(t, want, got)
}

func TestParseBandit(t *testing.T) {
	const sample = `{"results":[
		{"filename":"app.py","line_number":42,"col_offset":4,"issue_severity":"HIGH","test_id":"B101","issue_text":"Use of assert detected. "},
		{"filename":"app.py","line_number":10,"col_offset":0,"issue_severity":"LOW","test_id":"B404","issue_text":"Consider possible security implications."}
	]}`
	got, err := ParseBandit([]byte(sample), "bandit")
	require.NoError(t, err)
	want := []Finding{
		{Linter: "bandit", File: "app.py", Line: 42, Column: 4, Severity: "error", RuleID: "B101", Message: "Use of assert detected."},
		{Linter: "bandit", File: "app.py", Line: 10, Column: 0, Severity: "info", RuleID: "B404", Message: "Consider possible security implications."},
	}
	assert.Equal(t, want, got)
}

func TestParseCfnLint(t *testing.T) {
	const sample = `[{"Filename":"template.yml","Level":"Warning","Location":{"Start":{"LineNumber":115,"ColumnNumber":3}},"Message":"S3 bucket should have logging","Rule":{"Id":"W3011"}}]`
	got, err := ParseCfnLint([]byte(sample), "cfn-lint")
	require.NoError(t, err)
	want := []Finding{
		{Linter: "cfn-lint", File: "template.yml", Line: 115, Column: 3, Severity: "warning", RuleID: "W3011", Message: "S3 bucket should have logging"},
	}
	assert.Equal(t, want, got)
}

func TestParseHadolint(t *testing.T) {
	const sample = `[{"line":1,"code":"DL3006","message":"Always tag the version of an image explicitly","column":1,"file":"Dockerfile","level":"warning"}]`
	got, err := ParseHadolint([]byte(sample), "hadolint")
	require.NoError(t, err)
	want := []Finding{
		{Linter: "hadolint", File: "Dockerfile", Line: 1, Column: 1, Severity: "warning", RuleID: "DL3006", Message: "Always tag the version of an image explicitly"},
	}
	assert.Equal(t, want, got)
}

func TestParsePylint(t *testing.T) {
	const sample = `[{"type":"convention","line":3,"column":0,"path":"app.py","message":"Missing module docstring","message-id":"C0111"}]`
	got, err := ParsePylint([]byte(sample), "pylint")
	require.NoError(t, err)
	want := []Finding{
		{Linter: "pylint", File: "app.py", Line: 3, Column: 0, Severity: "info", RuleID: "C0111", Message: "Missing module docstring"},
	}
	assert.Equal(t, want, got)
}

func TestParseActionlint_InvalidJSON(t *testing.T) {
	_, err := ParseActionlint([]byte("not json"), "actionlint")
	require.Error(t, err)
}

func TestParseESLint(t *testing.T) {
	const sample = `[{"filePath":"src/app.js","messages":[
		{"ruleId":"no-eval","severity":2,"message":"eval is evil","line":12,"column":4},
		{"ruleId":"prefer-const","severity":1,"message":"Use const","line":3,"column":1}
	]}]`
	got, err := ParseESLint([]byte(sample), "eslint")
	require.NoError(t, err)
	want := []Finding{
		{Linter: "eslint", File: "src/app.js", Line: 12, Column: 4, Severity: "error", RuleID: "no-eval", Message: "eval is evil"},
		{Linter: "eslint", File: "src/app.js", Line: 3, Column: 1, Severity: "warning", RuleID: "prefer-const", Message: "Use const"},
	}
	assert.Equal(t, want, got)
}

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

func TestParseHamlLint(t *testing.T) {
	const sample = `{"files":[{"path":"app.haml","offenses":[
		{"severity":"warning","message":"Line is too long.","linter_name":"LineLength","location":{"line":15}}
	]}]}`
	got, err := ParseHamlLint([]byte(sample), "haml-lint")
	require.NoError(t, err)
	want := []Finding{
		{Linter: "haml-lint", File: "app.haml", Line: 15, Severity: "warning", RuleID: "LineLength", Message: "Line is too long."},
	}
	assert.Equal(t, want, got)
}

func TestParseMarkdownlint(t *testing.T) {
	const sample = `{"README.md":[
		{"lineNumber":3,"ruleNames":["MD010","no-hard-tabs"],"ruleDescription":"Hard tabs","errorRange":[17,1]}
	]}`
	got, err := ParseMarkdownlint([]byte(sample), "markdownlint")
	require.NoError(t, err)
	want := []Finding{
		{Linter: "markdownlint", File: "README.md", Line: 3, Column: 17, Severity: "error", RuleID: "MD010", Message: "Hard tabs"},
	}
	assert.Equal(t, want, got)
}

func TestParseRubocop(t *testing.T) {
	const sample = `{"files":[{"path":"app.rb","offenses":[
		{"severity":"warning","message":"Line is too long.","cop_name":"Layout/LineLength","location":{"line":15,"column":9}}
	]}]}`
	got, err := ParseRubocop([]byte(sample), "standardrb")
	require.NoError(t, err)
	want := []Finding{
		{Linter: "standardrb", File: "app.rb", Line: 15, Column: 9, Severity: "warning", RuleID: "Layout/LineLength", Message: "Line is too long."},
	}
	assert.Equal(t, want, got)
}

func TestParseStylelint(t *testing.T) {
	const sample = `[{"source":"a.css","warnings":[
		{"line":1,"column":2,"rule":"block-no-empty","severity":"warning","text":"Unexpected empty block (block-no-empty)"}
	]}]`
	got, err := ParseStylelint([]byte(sample), "stylelint")
	require.NoError(t, err)
	want := []Finding{
		{Linter: "stylelint", File: "a.css", Line: 1, Column: 2, Severity: "warning", RuleID: "block-no-empty", Message: "Unexpected empty block (block-no-empty)"},
	}
	assert.Equal(t, want, got)
}

func TestParsePerlCritic(t *testing.T) {
	const sample = "path=lib/Foo.pm,line=12,col=5,code=Subroutines::ProhibitExcessComplexity,message=Subroutine \"bar\" with high complexity score\npath=lib/Foo.pm,line=20,col=1,code=Modules::RequireExplicitPackage,message=Code not contained in explicit package\n\n"
	got, err := ParsePerlCritic([]byte(sample), "perlcritic")
	require.NoError(t, err)
	want := []Finding{
		{Linter: "perlcritic", File: "lib/Foo.pm", Line: 12, Column: 5, Severity: "warning", RuleID: "Subroutines::ProhibitExcessComplexity", Message: `Subroutine "bar" with high complexity score`},
		{Linter: "perlcritic", File: "lib/Foo.pm", Line: 20, Column: 1, Severity: "warning", RuleID: "Modules::RequireExplicitPackage", Message: "Code not contained in explicit package"},
	}
	assert.Equal(t, want, got)
}

func TestParsePerlCritic_SkipsNonMatchingLines(t *testing.T) {
	const sample = "some unrelated stderr noise\npath=lib/Foo.pm,line=1,col=1,code=TestingAndDebugging::RequireUseStrict,message=Code before strictures are enabled\n"
	got, err := ParsePerlCritic([]byte(sample), "perlcritic")
	require.NoError(t, err)
	require.Len(t, got, 1, "the non-matching noise line must be skipped, not produce a finding or an error")
	assert.Equal(t, "lib/Foo.pm", got[0].File)
}

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
