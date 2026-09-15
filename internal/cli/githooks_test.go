package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pluginRepoLocalFor returns the `local:` value the git-hooks tests below must write into a
// trunk.yaml living in trunkDir so its plugin source resolves to the fixture plugin repo checked
// into this project. resolve.go joins `local:` onto filepath.Dir(<trunk.yaml path>) with plain
// filepath.Join, which does NOT special-case an absolute second argument (it's simply
// concatenated, then cleaned) -- so an absolute path here would silently produce a bogus, always
// nonexistent path. The tests below write their trunk.yaml into a throwaway t.TempDir() repo
// unrelated to this project's own directory tree, so a literal "../../" (as trunkYAML's own
// checked-in fixture uses, relative to *this* package's directory) can't reach it either; a
// genuine relative path from trunkDir is required instead.
func pluginRepoLocalFor(t *testing.T, trunkDir string) string {
	t.Helper()
	abs, err := filepath.Abs("../../pkg/trunk/config/testdata/pluginrepo")
	require.NoError(t, err)
	rel, err := filepath.Rel(trunkDir, abs)
	require.NoError(t, err)
	return rel
}

// TestGitRepoRoot_OutsideGitRepo_SurfacesGitError proves gitRepoRoot surfaces git's own real
// stderr message (e.g. "fatal: not a git repository...") instead of a generic "exit status 128",
// and doesn't prefix its own "rtunk:" on top of the one cmd/rtunk/main.go already adds -- this
// path is newly reachable as a brand new user's very first command (`rtunk init` outside a git
// repo) since this branch added init/deinit.
func TestGitRepoRoot_OutsideGitRepo_SurfacesGitError(t *testing.T) {
	dir := t.TempDir()

	_, err := gitRepoRoot(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a git repository")
	assert.NotContains(t, err.Error(), "rtunk: rtunk:")
}

func TestGitHooksInstallCmd_WritesHooksForEnabledActions(t *testing.T) {
	repo := t.TempDir()
	require.NoError(t, exec.Command("git", "-C", repo, "init", "-q").Run())
	require.NoError(t, os.MkdirAll(filepath.Join(repo, ".trunk"), 0o755))
	trunkYAMLPath := filepath.Join(repo, ".trunk", "trunk.yaml")
	content := fmt.Sprintf("version: \"0.1\"\nactions:\n  enabled: [commitlint]\nplugins:\n  sources:\n    - id: trunk\n      local: %s\n", pluginRepoLocalFor(t, filepath.Dir(trunkYAMLPath)))
	require.NoError(t, os.WriteFile(trunkYAMLPath, []byte(content), 0o644))

	stdout, stderr, err := run2(t, "--config", trunkYAMLPath, "git-hooks", "install")
	require.NoError(t, err, "stderr: %s", stderr)
	assert.Contains(t, stdout, "installed: commit-msg")
	assert.FileExists(t, filepath.Join(repo, ".git", "hooks", "commit-msg"))
}

// TestGitHooksCmd_SyncAlias_MatchesInstall: real trunk's subcommand is `git-hooks sync`; rtunk's
// is `git-hooks install` (same idempotent operation for rtunk's simpler model). aliases:"sync" on
// the Install field's cmd tag makes both names resolve to the same gitHooksInstallCmd.
func TestGitHooksCmd_SyncAlias_MatchesInstall(t *testing.T) {
	repo := t.TempDir()
	require.NoError(t, exec.Command("git", "-C", repo, "init", "-q").Run())
	require.NoError(t, os.MkdirAll(filepath.Join(repo, ".trunk"), 0o755))
	trunkYAMLPath := filepath.Join(repo, ".trunk", "trunk.yaml")
	content := fmt.Sprintf("version: \"0.1\"\nactions:\n  enabled: [commitlint]\nplugins:\n  sources:\n    - id: trunk\n      local: %s\n", pluginRepoLocalFor(t, filepath.Dir(trunkYAMLPath)))
	require.NoError(t, os.WriteFile(trunkYAMLPath, []byte(content), 0o644))

	stdout, stderr, err := run2(t, "--config", trunkYAMLPath, "git-hooks", "sync")
	require.NoError(t, err, "stderr: %s", stderr)
	assert.Contains(t, stdout, "installed: commit-msg")
	assert.FileExists(t, filepath.Join(repo, ".git", "hooks", "commit-msg"))
}

func TestGitHooksUninstallCmd_RemovesInstalledHooks(t *testing.T) {
	repo := t.TempDir()
	require.NoError(t, exec.Command("git", "-C", repo, "init", "-q").Run())
	require.NoError(t, os.MkdirAll(filepath.Join(repo, ".trunk"), 0o755))
	trunkYAMLPath := filepath.Join(repo, ".trunk", "trunk.yaml")
	content := fmt.Sprintf("version: \"0.1\"\nactions:\n  enabled: [commitlint]\nplugins:\n  sources:\n    - id: trunk\n      local: %s\n", pluginRepoLocalFor(t, filepath.Dir(trunkYAMLPath)))
	require.NoError(t, os.WriteFile(trunkYAMLPath, []byte(content), 0o644))

	_, stderr, err := run2(t, "--config", trunkYAMLPath, "git-hooks", "install")
	require.NoError(t, err, "stderr: %s", stderr)

	stdout, stderr, err := run2(t, "--config", trunkYAMLPath, "git-hooks", "uninstall")
	require.NoError(t, err, "stderr: %s", stderr)
	assert.Contains(t, stdout, "removed: commit-msg")
	assert.NoFileExists(t, filepath.Join(repo, ".git", "hooks", "commit-msg"))
}
