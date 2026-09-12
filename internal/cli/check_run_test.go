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

// TestCheckRunCmd_SkipsUnsupportedFormats: an unsupported Output format and a formatter-only
// command must both be handled without ever attempting a network fetch or a real tool
// invocation -- neither linter here references any Tools, so there is nothing to download.
func TestCheckRunCmd_SkipsUnsupportedFormats(t *testing.T) {
	cfgPath, repoRoot := writeLinterFixture(t, []string{"unsupported-fmt", "formatter-only"}, `    - name: unsupported-fmt
      description: Uses a made-up Output value this project will never implement
      files: [ALL]
      commands:
        - name: lint
          run: echo unused
          output: xml
    - name: formatter-only
      description: Only has a Formatter command, never run by check at all
      files: [ALL]
      commands:
        - name: fmt
          run: echo unused
          output: rewrite
          formatter: true
          in_place: true
`)
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "app.txt"), []byte("content\n"), 0o644))

	cacheDir := t.TempDir()
	stdout, stderr, err := run2(t, "--config", cfgPath, "--cache-dir", cacheDir, "check")
	require.NoError(t, err, "stderr: %s", stderr)
	want := "\n0 issue(s) in 0 file(s) (1 linter(s) skipped: unsupported-fmt [unsupported output format \"xml\"])\n"
	assert.Equal(t, want, stdout)
}

// writeLinterFixture builds a trunk.yaml + local plugin source under t.TempDir(), laid out the
// way findTrunkYAML/checkRunCmd expect a real repo (<repoRoot>/.trunk/trunk.yaml, repoRoot two
// directories up): enabled lists the linter ids to turn on, and lintYAML is the raw `lint:
// definitions:` block content (everything checkRunCmd/checkListCmd need -- names, descriptions,
// files, commands). Returns the trunk.yaml path and repoRoot.
func writeLinterFixture(t *testing.T, enabled []string, lintYAML string) (cfgPath, repoRoot string) {
	t.Helper()
	repoRoot = t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, ".trunk"), 0o755))
	// parseSourceDir only treats linters/<name>/plugin.yaml (a subdirectory) as a resource file
	// contributing lint.definitions[] -- linters/plugin.yaml itself is the category-root file
	// (comment_formats/files only), same layout the real pluginrepo fixture uses.
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "pluginrepo", "linters", "fixture"), 0o755))

	enabledYAML := ""
	for _, e := range enabled {
		enabledYAML += "    - " + e + "\n"
	}
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, ".trunk", "trunk.yaml"), []byte(`version: "0.1"
plugins:
  sources:
    - id: local
      local: ../pluginrepo
lint:
  enabled:
`+enabledYAML), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "pluginrepo", "linters", "fixture", "plugin.yaml"),
		[]byte("lint:\n  definitions:\n"+lintYAML), 0o644))
	return filepath.Join(repoRoot, ".trunk", "trunk.yaml"), repoRoot
}

// TestCheckListCmd_ShowsDisabledLinters covers item 6: ROADMAP.md promises `rtunk check list`
// shows every linter available for the configuration, not only enabled ones. Before the fix,
// checkListCmd.Run resolved enabled+used only (config.Resolve), so a defined-but-disabled linter
// (here "beta") could never appear, and the "*" enabled marker was always "*" -- dead code.
func TestCheckListCmd_ShowsDisabledLinters(t *testing.T) {
	cfgPath, _ := writeLinterFixture(t, []string{"alpha"}, `    - name: alpha
      description: Alpha linter
      files: [ALL]
    - name: beta
      description: Beta linter
      files: [ALL]
`)
	stdout, stderr, err := run2(t, "--config", cfgPath, "check", "list")
	require.NoError(t, err, "stderr: %s", stderr)
	want := "* alpha  Alpha linter\n  beta  Beta linter\n"
	assert.Equal(t, want, stdout)
}

// TestCheckRunCmd_FailedLinterKeepsOtherFindings covers item 4: a Failed event used to return
// immediately from inside the events loop, discarding every Finding already collected from other
// linters in the same run and skipping printReport entirely. "alpha" always reports a finding
// (pass_fail, exit 1, no error_codes); "beta" always crashes (exit 1, error_codes: [1]) --
// cfg.Lint.Definitions is a Go map, so whichever event Run() happens to send first, alpha's
// finding must still reach the printed report, and the command must still return a non-nil error.
func TestCheckRunCmd_FailedLinterKeepsOtherFindings(t *testing.T) {
	cfgPath, repoRoot := writeLinterFixture(t, []string{"alpha", "beta"}, `    - name: alpha
      description: Alpha linter
      files: [ALL]
      commands:
        - name: check
          run: "false"
          output: pass_fail
    - name: beta
      description: Beta linter
      files: [ALL]
      commands:
        - name: check
          run: "false"
          output: pass_fail
          error_codes: [1]
`)
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "work"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "work", "file.txt"), []byte("hi\n"), 0o644))

	cacheDir := t.TempDir()
	stdout, stderr, err := run2(t, "--config", cfgPath, "--cache-dir", cacheDir, "check", filepath.Join(repoRoot, "work"))
	require.Empty(t, stderr)
	require.Error(t, err)
	assert.EqualError(t, err, "check: beta: check exited 1: ")

	want := "work/file.txt error file did not pass\n\n1 issue(s) in 1 file(s)\n"
	assert.Equal(t, want, stdout, "alpha's finding must still be printed despite beta's Failed event")
}

// TestCheckRunCmd_DedupesSkippedByLinter covers item 8: a linter with several unsupported
// commands used to emit one Skipped event per command, and checkRunCmd listed every one --
// reporting "N linter(s) skipped" where N counted commands, not distinct linters. "multiskip"
// here has 3 commands, each unsupported for a different reason; the report must still say
// "1 linter(s) skipped", naming multiskip once (with its first command's Skipped note).
func TestCheckRunCmd_DedupesSkippedByLinter(t *testing.T) {
	cfgPath, repoRoot := writeLinterFixture(t, []string{"multiskip"}, `    - name: multiskip
      description: Multi skip linter
      files: [ALL]
      commands:
        - name: c1
          run: "faketool sarif ${target}"
          output: sarif
          run_from: "${parent}"
        - name: c2
          run: "faketool sarif ${target}"
          output: sarif
          sandbox_type: copy_targets
        - name: c3
          run: "faketool sarif ${target}"
          output: regex
`)
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "work"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "work", "file.txt"), []byte("hi\n"), 0o644))

	cacheDir := t.TempDir()
	stdout, stderr, err := run2(t, "--config", cfgPath, "--cache-dir", cacheDir, "check", filepath.Join(repoRoot, "work"))
	require.NoError(t, err, "stderr: %s", stderr)

	want := "\n0 issue(s) in 0 file(s) (1 linter(s) skipped: multiskip [unsupported run_from \"${parent}\"])\n"
	assert.Equal(t, want, stdout)
}
