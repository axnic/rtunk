package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/trunk/check"
	"github.com/xunleii/rtunk/pkg/trunk/config"
)

func TestPrintReport_FindingsSortedAndFormatted(t *testing.T) {
	var buf strings.Builder
	findings := []check.Finding{
		{File: "b.go", Line: 2, Severity: "error", RuleID: "r2", Message: "msg2"},
		{File: "a.go", Line: 5, Column: 3, Severity: "warning", RuleID: "r1", Message: "msg1", URL: "https://example.com/r1"},
		{File: "a.go", Line: 1, Severity: "error", Message: "no rule"},
	}
	printReport(&buf, findings, nil)

	want := "a.go:1 error no rule\n" +
		"a.go:5:3 warning [r1] msg1 (https://example.com/r1)\n" +
		"b.go:2 error [r2] msg2\n" +
		"\n" +
		"3 issue(s) in 2 file(s)\n"
	assert.Equal(t, want, buf.String())
}

func TestPrintReport_Clean(t *testing.T) {
	var buf strings.Builder
	printReport(&buf, nil, nil)
	assert.Equal(t, "\n0 issue(s) in 0 file(s)\n", buf.String())
}

func TestPrintReport_Skipped(t *testing.T) {
	var buf strings.Builder
	printReport(&buf, nil, []string{`circleci [unsupported run_from ""]`, `cspell [unsupported output format "regex"]`})
	want := "\n0 issue(s) in 0 file(s) (2 linter(s) skipped: circleci [unsupported run_from \"\"], cspell [unsupported output format \"regex\"])\n"
	assert.Equal(t, want, buf.String())
}

func TestFormatLintList(t *testing.T) {
	cfg := config.Config{
		Lint: config.LintConfig{
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Enabled: []string{"gofmt@1.2.3"},
				Definitions: map[string]config.Linter{
					"gofmt":         {Description: "Formats go"},
					"golangci-lint": {Description: "Lints go"},
				},
			},
		},
	}
	got := formatLintList(cfg)
	want := "* gofmt  Formats go\n  golangci-lint  Lints go\n"
	assert.Equal(t, want, got)
}

// TestCheckRunCmd_SkipsUnsupportedFormats drives the real CLI end to end against the existing
// trunk-with-plugins.yaml fixture (already used by exec_test.go/cli_test.go): actionlint's
// Output is "actionlint" (a bespoke format this plan doesn't implement) and prettier's only
// command is Formatter: true -- both must be handled without ever attempting a network fetch.
func TestCheckRunCmd_SkipsUnsupportedFormats(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".github", "workflows"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".github", "workflows", "ci.yml"), []byte("on: push\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.js"), []byte("console.log(1)\n"), 0o644))

	cacheDir := t.TempDir()
	stdout, stderr, err := run2(t, "--config", trunkYAML, "--cache-dir", cacheDir, "check", dir)
	require.NoError(t, err, "stderr: %s", stderr)
	want := "\n0 issue(s) in 0 file(s) (1 linter(s) skipped: actionlint [unsupported output format \"actionlint\"])\n"
	assert.Equal(t, want, stdout)
}
