package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

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

// TestMain points every test that does not pass --cache-dir at a throwaway directory: check and
// fmt now always write a run log, which would otherwise land in the developer's real OS cache dir.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "rtunk-cli-test-cache-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_ = os.Setenv("RTUNK_CACHE_DIR", dir)
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
