package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLintersListCmd_ShowsDisabledLinters covers item 6: ROADMAP.md promises `rtunk linters list`
// shows every linter available for the configuration, not only enabled ones. Before the fix,
// lintersListCmd.Run resolved enabled+used only (config.Resolve), so a defined-but-disabled linter
// (here "beta") could never appear, and the "*" enabled marker was always "*" -- dead code.
func TestLintersListCmd_ShowsDisabledLinters(t *testing.T) {
	cfgPath, _ := writeLinterFixture(t, []string{"alpha"}, `    - name: alpha
      description: Alpha linter
      files: [ALL]
    - name: beta
      description: Beta linter
      files: [ALL]
`)
	stdout, stderr, err := run2(t, "--config", cfgPath, "linters", "list")
	require.NoError(t, err, "stderr: %s", stderr)
	assert.Contains(t, stdout, "Enabled\n  ✔ alpha")
	assert.Contains(t, stdout, "Available for this repo (not enabled)\n  ◯ beta")
}
