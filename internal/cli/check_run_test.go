package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/trunk/config"
)

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
	stdout, stderr, err := run2(t, "--config", cfgPath, "--cache-dir", cacheDir, "check", filepath.Dir(filepath.Dir(cfgPath)))
	require.NoError(t, err, "stderr: %s", stderr)
	assert.Contains(t, stdout, "Skipped  1 linter: unsupported-fmt [unsupported output format \"xml\"]\n")
	assert.Contains(t, stdout, "✔ no issues\n")
	assert.Contains(t, stderr, "unsupported-fmt")
	assert.Contains(t, stderr, "skipped")
	assert.NotContains(t, stderr, "formatter-only", "formatter-only produces no event at all")
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

// TestCheckListCmd_ShowsDisabledLinters covers item 6: ROADMAP.md promises `rtunk linters list`
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
	stdout, stderr, err := run2(t, "--config", cfgPath, "linters", "list")
	require.NoError(t, err, "stderr: %s", stderr)
	want := "* alpha  Alpha linter\n  beta  Beta linter\n"
	assert.Equal(t, want, stdout)
}

// TestCheckRunCmd_FailedLinterKeepsOtherFindings covers item 4: a Failed event used to return
// immediately from inside the events loop, discarding every Finding already collected from other
// linters in the same run and skipping the report entirely. "alpha" always reports a finding
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
	require.Error(t, err)
	assert.EqualError(t, err, "engine: beta: check exited 1: ")

	assert.Contains(t, stdout, "ISSUES   1 in 1 file\n", "alpha's finding must still be printed despite beta's Failed event")
	assert.Contains(t, stdout, "work/file.txt  (1)\n  0:0  high    file did not pass  alpha\n")
	assert.Contains(t, stdout, "FAILURES\n  ✖ beta  failed to run  rtunk logs show ")

	// cfg.Lint.Definitions is a Go map, so alpha/beta's lines can interleave in either order;
	// one progress line per finished linter (no running lines).
	lines := strings.Split(strings.TrimRight(stderr, "\n"), "\n")
	require.Len(t, lines, 2, "stderr: %s", stderr)
	assert.Contains(t, stderr, "alpha")
	assert.Contains(t, stderr, "done     1 issue")
	assert.Contains(t, stderr, "failed   engine: beta: check exited 1: ")
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
          run: "faketool sarif ${target} ${workspace}"
          output: sarif
        - name: c2
          run: "faketool sarif ${target}"
          output: xml
        - name: c3
          run: "faketool sarif ${target}"
          output: sarif
          parser:
            runtime: python
            run: convert.py
`)
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "work"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "work", "file.txt"), []byte("hi\n"), 0o644))

	cacheDir := t.TempDir()
	stdout, stderr, err := run2(t, "--config", cfgPath, "--cache-dir", cacheDir, "check", filepath.Join(repoRoot, "work"))
	require.NoError(t, err, "stderr: %s", stderr)

	assert.Contains(t, stdout, "Skipped  1 linter: multiskip [unsupported template var \"${workspace}\"]\n")
}

// TestCheckRunCmd_DoesNotRunInPlaceCommands proves the read-only contract: check must never
// execute a command with InPlace: true, even one that (unusually, but reachable via a real
// catalog example -- sourcery's own real "fix" command) sets InPlace without Formatter. Before
// this fix, check's own predicate (!cmd.Formatter) let such a command straight through, since
// only Formatter was excluded -- confirmed via real reproduction that check would silently
// rewrite the target file and report a clean "0 issue(s)".
func TestCheckRunCmd_DoesNotRunInPlaceCommands(t *testing.T) {
	cfgPath, repoRoot := writeLinterFixture(t, []string{"sneaky"}, `    - name: sneaky
      description: An in_place command with no formatter marker (real catalog shape -- sourcery's own "fix" command)
      files: [ALL]
      commands:
        - name: fix
          run: printf 'REWRITTEN\n' > ${target}
          output: rewrite
          success_codes: [0]
          in_place: true
`)
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "work"), 0o755))
	target := filepath.Join(repoRoot, "work", "file.txt")
	require.NoError(t, os.WriteFile(target, []byte("original\n"), 0o644))

	cacheDir := t.TempDir()
	stdout, stderr, err := run2(t, "--config", cfgPath, "--cache-dir", cacheDir, "check", filepath.Join(repoRoot, "work"))
	require.NoError(t, err, "stderr: %s", stderr)
	assert.Regexp(t, `^Checked 0 files with 0 linters in \d+\.\ds\n✔ no issues\n$`, stdout)
	assert.Empty(t, stderr, "an in_place command excluded from both predicates must produce no event at all")

	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "original\n", string(data), "check must never execute an in_place command, even one without formatter: true")
}

// TestCheckRunCmd_Fix_AppliesFixesBeforeReporting proves the two-pass ordering for real: a linter
// with both a checking command (fails while the file's content isn't "formatted\n") and a
// formatter command (rewrites it to exactly that). Plain `check` must report the finding; `check
// --fix` must not, since the formatter pass fixes the file before the checking pass ever reads it.
func TestCheckRunCmd_Fix_AppliesFixesBeforeReporting(t *testing.T) {
	cfgPath, repoRoot := writeLinterFixture(t, []string{"fakefix"}, `    - name: fakefix
      description: A fake linter with both a checker and a formatter command
      files: [ALL]
      commands:
        - name: lint
          run: grep -qxF formatted ${target}
          output: pass_fail
        - name: format
          run: printf 'formatted\n' > ${target}
          output: rewrite
          success_codes: [0]
          in_place: true
          formatter: true
`)
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "work"), 0o755))
	target := filepath.Join(repoRoot, "work", "file.txt")
	require.NoError(t, os.WriteFile(target, []byte("messy\n"), 0o644))

	cacheDir := t.TempDir()

	// Without --fix: the checking command reports the file as failing.
	stdout, stderr, err := run2(t, "--config", cfgPath, "--cache-dir", cacheDir, "check", filepath.Join(repoRoot, "work"))
	require.Error(t, err, "stderr: %s", stderr)
	assert.Contains(t, stdout, "ISSUES   1 in 1 file\n")

	require.NoError(t, os.WriteFile(target, []byte("messy\n"), 0o644)) // reset for the --fix run

	// With --fix: the formatter pass rewrites the file before the checking pass ever reads it, so
	// the finding that showed up above must be absent here.
	stdout, stderr, err = run2(t, "--config", cfgPath, "--cache-dir", cacheDir, "check", "--fix", filepath.Join(repoRoot, "work"))
	require.NoError(t, err, "stderr: %s", stderr)
	assert.Contains(t, stdout, "REFORMATTED   1 file\n")
	assert.Contains(t, stdout, "✔ no issues\n")

	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "formatted\n", string(data))
}

// TestCheckRunCmd_Filter_OnlyRunsAllowedLinter reuses writeLinterFixture's own zero-network-call
// pattern (echo-based commands, no Tools reference) -- --filter keep-me must mean drop-me's
// commands never run at all, not merely that drop-me's findings are suppressed after the fact.
func TestCheckRunCmd_Filter_OnlyRunsAllowedLinter(t *testing.T) {
	cfgPath, _ := writeLinterFixture(t, []string{"keep-me", "drop-me"}, `    - name: keep-me
      description: Should run
      files: [ALL]
      commands:
        - name: lint
          run: echo unused
          output: xml
    - name: drop-me
      description: Should not run
      files: [ALL]
      commands:
        - name: lint
          run: echo unused
          output: xml
`)
	_, stderr, _ := run2(t, "--config", cfgPath, "check", "--filter", "keep-me", filepath.Dir(filepath.Dir(cfgPath)))
	assert.Contains(t, stderr, "keep-me")
	assert.NotContains(t, stderr, "drop-me")
}

// TestCheckRunCmd_FilterDenyList_SkipsDeniedLinter proves the deny-list form of --filter is
// reachable through the real CLI as --filter=-id (Kong parses a bare "-id" as an unknown short
// flag, so the "=" is required, not merely idiomatic -- see the reworded help text on
// checkRunCmd.Filter).
func TestCheckRunCmd_FilterDenyList_SkipsDeniedLinter(t *testing.T) {
	cfgPath, _ := writeLinterFixture(t, []string{"keep-me", "drop-me"}, `    - name: keep-me
      description: Should run
      files: [ALL]
      commands:
        - name: lint
          run: echo unused
          output: xml
    - name: drop-me
      description: Should not run
      files: [ALL]
      commands:
        - name: lint
          run: echo unused
          output: xml
`)
	_, stderr, _ := run2(t, "--config", cfgPath, "check", "--filter=-drop-me", filepath.Dir(filepath.Dir(cfgPath)))
	assert.Contains(t, stderr, "keep-me")
	assert.NotContains(t, stderr, "drop-me")
}

func TestCheckRunCmd_Exclude_SkipsExcludedLinter(t *testing.T) {
	cfgPath, _ := writeLinterFixture(t, []string{"keep-me", "drop-me"}, `    - name: keep-me
      description: Should run
      files: [ALL]
      commands:
        - name: lint
          run: echo unused
          output: xml
    - name: drop-me
      description: Should not run
      files: [ALL]
      commands:
        - name: lint
          run: echo unused
          output: xml
`)
	_, stderr, _ := run2(t, "--config", cfgPath, "check", "--exclude", "drop-me", filepath.Dir(filepath.Dir(cfgPath)))
	assert.Contains(t, stderr, "keep-me")
	assert.NotContains(t, stderr, "drop-me")
}

func TestCheckRunCmd_ShortFixFlag_MatchesLongForm(t *testing.T) {
	cfgPath, _ := writeLinterFixture(t, nil, "")
	longOut, longStderr, longErr := run2(t, "--config", cfgPath, "check", "--fix", filepath.Dir(filepath.Dir(cfgPath)))
	shortOut, shortStderr, shortErr := run2(t, "--config", cfgPath, "check", "-y", filepath.Dir(filepath.Dir(cfgPath)))
	assert.Equal(t, longErr, shortErr)
	assert.Equal(t, longOut, shortOut)
	assert.Equal(t, longStderr, shortStderr)
}

// TestCheckRunCmd_NoFix_Accepted: check already never auto-fixes unless --fix is given, so
// --no-fix asks for check's existing default -- this is a documented no-op accepted for scripts
// that pass it defensively, not a behavior change.
func TestCheckRunCmd_NoFix_Accepted(t *testing.T) {
	cfgPath, _ := writeLinterFixture(t, nil, "")
	_, _, err := run2(t, "--config", cfgPath, "check", "-n", filepath.Dir(filepath.Dir(cfgPath)))
	require.NoError(t, err)
	_, _, err = run2(t, "--config", cfgPath, "check", "--no-fix")
	require.NoError(t, err)
}

// TestCheckRunCmd_PrintFailures_Accepted: check already always prints Failed events to stderr
// unconditionally -- this is a documented no-op.
func TestCheckRunCmd_PrintFailures_Accepted(t *testing.T) {
	cfgPath, _ := writeLinterFixture(t, nil, "")
	_, _, err := run2(t, "--config", cfgPath, "check", "--print-failures")
	require.NoError(t, err)
}

func TestCheckRunCmd_Filter_UnknownLinter_ReturnsUsageError(t *testing.T) {
	cfgPath, _ := writeLinterFixture(t, []string{"keep-me"}, `    - name: keep-me
      description: Should run
      files: [ALL]
      commands:
        - name: lint
          run: echo unused
          output: xml
`)
	_, _, err := run2(t, "--config", cfgPath, "check", "--filter", "nope")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"nope"`)
}

func TestCheckRunCmd_NoProgressSilencesStderr(t *testing.T) {
	cfgPath, repoRoot := writeLinterFixture(t, []string{"alpha"}, `    - name: alpha
      description: Alpha linter
      files: [ALL]
      commands:
        - name: check
          run: "false"
          output: pass_fail
          error_codes: [1]
`)
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "f.txt"), []byte("x\n"), 0o644))
	cache := t.TempDir()

	_, stderr, err := run2(t, "--config", cfgPath, "--cache-dir", cache, "check", repoRoot)
	assert.Error(t, err)
	assert.Contains(t, stderr, "alpha")

	stdout, quiet, err := run2(t, "--config", cfgPath, "--cache-dir", cache, "check", "--no-progress", repoRoot)
	assert.Error(t, err, "exit code is unchanged")
	assert.Empty(t, quiet)
	assert.Contains(t, stdout, "FAILURES\n  ✖ alpha  failed to run  rtunk logs show ")
}
