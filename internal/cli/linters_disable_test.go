package cli

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLintersDisableCmd_RemovesEntry(t *testing.T) {
	path := writeScratchTrunkYAML(t, "version: \"0.1\"\nlint:\n  enabled: [shellcheck, prettier]\n")

	_, stderr, err := run2(t, "--config", path, "linters", "disable", "shellcheck")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.NotContains(t, string(got), "shellcheck")
	assert.Contains(t, string(got), "prettier")
}

func TestLintersDisableCmd_AbsentIsNoOp(t *testing.T) {
	path := writeScratchTrunkYAML(t, "version: \"0.1\"\nlint:\n  enabled: [prettier]\n")

	_, stderr, err := run2(t, "--config", path, "linters", "disable", "shellcheck")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(got), "prettier")
}

// TestLintersDisableCmd_VersionedIDRemoves guards against disable silently no-op'ing when the id
// passed on the command line still carries an @version pin (as copy-pasted straight out of
// enabled: or `linters list` output) -- removeEnabled must bare-compare its own ids too, not just
// the existing entries.
func TestLintersDisableCmd_VersionedIDRemoves(t *testing.T) {
	path := writeScratchTrunkYAML(t, "version: \"0.1\"\nlint:\n  enabled: [shellcheck@1.0.0, prettier]\n")

	_, stderr, err := run2(t, "--config", path, "linters", "disable", "shellcheck@1.0.0")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.NotContains(t, string(got), "shellcheck")
	assert.Contains(t, string(got), "prettier")
}
