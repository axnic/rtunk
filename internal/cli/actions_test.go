package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/run/actions"
)

// seedHistoryForTest writes one history entry directly (bypassing actions.Run), so
// TestActionsHistoryCmd_ReflectsPastRuns only exercises the CLI's own rendering, not Run() itself.
func seedHistoryForTest(cacheDir, repoRoot string) error {
	return actions.AppendHistory(cacheDir, repoRoot, actions.Result{ActionID: "seeded-action", StartedAt: time.Now()})
}

func TestActionsEnableCmd_AddsToEnabledAndRemovesFromDisabled(t *testing.T) {
	path := writeScratchTrunkYAML(t, "version: \"0.1\"\nactions:\n  enabled: []\n  disabled: [commitlint]\n")
	_, stderr, err := run2(t, "--config", path, "actions", "enable", "commitlint")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(got), "enabled:\n    - commitlint\n")
	assert.NotContains(t, string(got), "disabled:\n    - commitlint\n")
}

func TestActionsDisableCmd_AddsToDisabledAndRemovesFromEnabled(t *testing.T) {
	path := writeScratchTrunkYAML(t, "version: \"0.1\"\nactions:\n  enabled: [commitlint]\n  disabled: []\n")
	_, stderr, err := run2(t, "--config", path, "actions", "disable", "commitlint")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(got), "disabled:\n    - commitlint\n")
	assert.NotContains(t, string(got), "enabled:\n    - commitlint\n")
}

func TestActionsRunCmd_ByID(t *testing.T) {
	path := writeScratchTrunkYAML(t, "version: \"0.1\"\nactions:\n  enabled: [greet]\nplugins:\n  sources: []\n")
	// A trunk.yaml with no plugin source defining "greet" can't resolve it -- this test instead
	// exercises the "unknown action" error path, proving ID-mode dispatch reaches Definitions
	// lookup at all (a real end-to-end run against a defined action is covered by
	// pkg/run/actions' own TestRun_* suite; this CLI layer only needs to prove wiring).
	_, stderr, err := run2(t, "--config", path, "actions", "run", "greet")
	require.Error(t, err)
	assert.Contains(t, stderr+err.Error(), "unknown action")
}

// TestRunCmd_TopLevelAlias_MatchesActionsRun: real trunk's own top-level `trunk run <id>` is a
// documented shortcut for `trunk actions run <id>` (see trunk --help's own subcommand list,
// which lists "run" alongside "actions"). rtunk's CLI.RunCmd reuses the exact same actionsRunCmd
// struct/Run method, registered a second time under the name "run" -- this verifies both
// invocation paths produce identical stdout, stderr, and error.
func TestRunCmd_TopLevelAlias_MatchesActionsRun(t *testing.T) {
	path := writeScratchTrunkYAML(t, "version: \"0.1\"\nactions:\n  enabled: [greet]\nplugins:\n  sources: []\n")

	actionsOut, actionsStderr, actionsErr := run2(t, "--config", path, "actions", "run", "greet")
	topLevelOut, topLevelStderr, topLevelErr := run2(t, "--config", path, "run", "greet")

	assert.Equal(t, actionsOut, topLevelOut)
	assert.Equal(t, actionsStderr, topLevelStderr)
	assert.Equal(t, actionsErr, topLevelErr)
}

func TestActionsRunCmd_RequiresIDOrHook(t *testing.T) {
	path := writeScratchTrunkYAML(t, "version: \"0.1\"\n")
	_, _, err := run2(t, "--config", path, "actions", "run")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "specify an action id or --hook")
}

func TestActionsHistoryCmd_ReflectsPastRuns(t *testing.T) {
	path := writeScratchTrunkYAML(t, "version: \"0.1\"\n")
	cacheDir := t.TempDir()
	// Seed history directly (this test's own concern is the CLI's rendering, not Run() itself).
	// repoRoot must match actionsHistoryCmd.Run's own computation (gitRepoRoot, the real git
	// toplevel) or the seeded entry's key and the CLI's lookup key diverge and history reads back
	// empty.
	repoRoot, err := gitRepoRoot(filepath.Dir(path))
	require.NoError(t, err)
	require.NoError(t, seedHistoryForTest(cacheDir, repoRoot))

	stdout, stderr, err := run2(t, "--config", path, "--cache-dir", cacheDir, "actions", "history")
	require.NoError(t, err, "stderr: %s", stderr)
	assert.Contains(t, stdout, "seeded-action")
}

// TestActionsHistoryCmd_CountAlias_MatchesLimitFlag: --count is real trunk's own flag name for
// the same "how many entries" concept as rtunk's existing --limit.
func TestActionsHistoryCmd_CountAlias_MatchesLimitFlag(t *testing.T) {
	path := writeScratchTrunkYAML(t, "version: \"0.1\"\n")
	cacheDir := t.TempDir()
	repoRoot, err := gitRepoRoot(filepath.Dir(path))
	require.NoError(t, err)
	require.NoError(t, actions.AppendHistory(cacheDir, repoRoot, actions.Result{ActionID: "first-action", StartedAt: time.Now()}))
	require.NoError(t, actions.AppendHistory(cacheDir, repoRoot, actions.Result{ActionID: "second-action", StartedAt: time.Now()}))

	limitOut, limitStderr, limitErr := run2(t, "--config", path, "--cache-dir", cacheDir, "actions", "history", "--limit", "1")
	countOut, countStderr, countErr := run2(t, "--config", path, "--cache-dir", cacheDir, "actions", "history", "--count", "1")
	require.NoError(t, limitErr, "stderr: %s", limitStderr)
	require.NoError(t, countErr, "stderr: %s", countStderr)
	assert.Equal(t, limitOut, countOut)
	assert.Contains(t, limitOut, "second-action")
	assert.NotContains(t, limitOut, "first-action")
}
