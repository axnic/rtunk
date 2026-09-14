package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/trunk/actions"
)

// seedHistoryForTest writes one history entry directly (bypassing actions.Run), so
// TestActionsHistoryCmd_ReflectsPastRuns only exercises the CLI's own rendering, not Run() itself.
func seedHistoryForTest(cacheDir, repoRoot string) error {
	return actions.AppendHistory(cacheDir, repoRoot, actions.Result{ActionID: "seeded-action", StartedAt: time.Now()})
}

func TestActionsListCmd_MarksEnabled(t *testing.T) {
	stdout, stderr, err := run2(t, "--config", trunkYAML, "actions", "list")
	require.NoError(t, err, "stderr: %s", stderr)
	assert.Contains(t, stdout, "* commitlint")
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
	// pkg/trunk/actions' own TestRun_* suite; this CLI layer only needs to prove wiring).
	_, stderr, err := run2(t, "--config", path, "actions", "run", "greet")
	require.Error(t, err)
	assert.Contains(t, stderr+err.Error(), "unknown action")
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
