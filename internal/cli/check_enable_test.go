package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeScratchTrunkYAML writes content into a fresh git repo's root (actions/git-hooks commands
// resolve repoRoot via `git rev-parse --show-toplevel`, so callers exercising those code paths
// need a real repo here, not a bare tempdir).
func writeScratchTrunkYAML(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, exec.Command("git", "-C", dir, "init", "-q").Run())
	path := filepath.Join(dir, "trunk.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func TestCheckEnableCmd_AddsAndPreservesComments(t *testing.T) {
	path := writeScratchTrunkYAML(t, "version: \"0.1\"\n# a leading comment, must survive\nlint:\n  enabled: []\n")

	_, stderr, err := run2(t, "--config", path, "check", "enable", "shellcheck")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(got), "# a leading comment, must survive")
	assert.Contains(t, string(got), "shellcheck")
}

func TestCheckEnableCmd_Idempotent(t *testing.T) {
	path := writeScratchTrunkYAML(t, "version: \"0.1\"\nlint:\n  enabled: [shellcheck]\n")

	_, stderr, err := run2(t, "--config", path, "check", "enable", "shellcheck")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(got), "shellcheck"))
}

func TestCheckEnableCmd_VersionPinReplacesOldPin(t *testing.T) {
	path := writeScratchTrunkYAML(t, "version: \"0.1\"\nlint:\n  enabled: [shellcheck@1.0.0]\n")

	_, stderr, err := run2(t, "--config", path, "check", "enable", "shellcheck@2.0.0")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(got), "shellcheck@2.0.0")
	assert.NotContains(t, string(got), "shellcheck@1.0.0")
}

func TestCheckEnableCmd_NoLintKeyAtAll(t *testing.T) {
	path := writeScratchTrunkYAML(t, "version: \"0.1\"\n")

	_, stderr, err := run2(t, "--config", path, "check", "enable", "shellcheck")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(got), "shellcheck")
}

func TestCheckDisableCmd_RemovesEntry(t *testing.T) {
	path := writeScratchTrunkYAML(t, "version: \"0.1\"\nlint:\n  enabled: [shellcheck, prettier]\n")

	_, stderr, err := run2(t, "--config", path, "check", "disable", "shellcheck")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.NotContains(t, string(got), "shellcheck")
	assert.Contains(t, string(got), "prettier")
}

func TestCheckDisableCmd_AbsentIsNoOp(t *testing.T) {
	path := writeScratchTrunkYAML(t, "version: \"0.1\"\nlint:\n  enabled: [prettier]\n")

	_, stderr, err := run2(t, "--config", path, "check", "disable", "shellcheck")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(got), "prettier")
}

// TestCheckEnableCmd_LintKeyWithNoValue guards against a real silent data-loss bug: a
// hand-edited/partial trunk.yaml with a bare "lint:" key (no value at all) parses that key's
// value as a null scalar node, not a mapping. Appending onto a non-mapping node's Content is
// silently ignored by the yaml.v3 encoder, so without coercing the node back to a MappingNode
// first, the whole edit vanished on write and the command still reported success.
func TestCheckEnableCmd_LintKeyWithNoValue(t *testing.T) {
	path := writeScratchTrunkYAML(t, "version: \"0.1\"\nlint:\n")

	_, stderr, err := run2(t, "--config", path, "check", "enable", "shellcheck")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(got), "shellcheck")
	assert.Contains(t, string(got), "enabled")
}

// TestCheckDisableCmd_VersionedIDRemoves guards against disable silently no-op'ing when the id
// passed on the command line still carries an @version pin (as copy-pasted straight out of
// enabled: or `check list` output) -- removeEnabled must bare-compare its own ids too, not just
// the existing entries.
func TestCheckDisableCmd_VersionedIDRemoves(t *testing.T) {
	path := writeScratchTrunkYAML(t, "version: \"0.1\"\nlint:\n  enabled: [shellcheck@1.0.0, prettier]\n")

	_, stderr, err := run2(t, "--config", path, "check", "disable", "shellcheck@1.0.0")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.NotContains(t, string(got), "shellcheck")
	assert.Contains(t, string(got), "prettier")
}

// TestCheckEnableCmd_PreservesSourceIndentWidth guards against editEnabled reformatting the
// whole file to yaml.v3's default 4-space indent (yaml.Marshal's default) instead of keeping
// the 2-space indent trunk.yaml's real-world convention (and this repo's own .trunk/trunk.yaml)
// actually uses -- untouched keys must keep their original indentation exactly.
func TestCheckEnableCmd_PreservesSourceIndentWidth(t *testing.T) {
	path := writeScratchTrunkYAML(t, "version: \"0.1\"\nruntimes:\n  enabled:\n    - node@22.18.0\nlint:\n  enabled:\n    - prettier\n")

	_, stderr, err := run2(t, "--config", path, "check", "enable", "shellcheck")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(got), "runtimes:\n  enabled:\n    - node@22.18.0\n")
	assert.Contains(t, string(got), "shellcheck")
}
