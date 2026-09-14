package githooks_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/trunk/config"
	"github.com/xunleii/rtunk/pkg/trunk/githooks"
)

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, exec.Command("git", "-C", dir, "init", "-q").Run())
	return dir
}

func testConfig() config.Config {
	return config.Config{
		Actions: config.CategoryConfig[config.Action]{
			Enabled: []string{"commitlint", "trunk-fmt-pre-commit"},
			Definitions: map[string]config.Action{
				"commitlint":           {ID: "commitlint", Triggers: []config.Trigger{{GitHooks: []string{"commit-msg"}}}},
				"trunk-fmt-pre-commit": {ID: "trunk-fmt-pre-commit", Triggers: []config.Trigger{{GitHooks: []string{"pre-commit"}}}},
			},
		},
	}
}

func TestInstall_WritesOneShimPerReferencedHook(t *testing.T) {
	repo := initRepo(t)
	installed, skipped, err := githooks.Install(repo, testConfig(), false)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"commit-msg", "pre-commit"}, installed)
	assert.Empty(t, skipped)

	self, err := os.Executable()
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(repo, ".git", "hooks", "pre-commit"))
	require.NoError(t, err)
	assert.Contains(t, string(data), self+" actions run --hook pre-commit", "shim must exec the running binary's absolute path, not a bare `rtunk` that a minimal PATH (GUI git clients) can't resolve")
	assert.Contains(t, string(data), "Installed by rtunk git-hooks install")
}

func TestInstall_SkipsForeignHookWithoutForce(t *testing.T) {
	repo := initRepo(t)
	hooksDir := filepath.Join(repo, ".git", "hooks")
	require.NoError(t, os.WriteFile(filepath.Join(hooksDir, "pre-commit"), []byte("#!/bin/sh\necho husky\n"), 0o755))

	installed, skipped, err := githooks.Install(repo, testConfig(), false)
	require.NoError(t, err)
	assert.Equal(t, []string{"commit-msg"}, installed)
	assert.Equal(t, []string{"pre-commit"}, skipped)

	data, err := os.ReadFile(filepath.Join(hooksDir, "pre-commit"))
	require.NoError(t, err)
	assert.Equal(t, "#!/bin/sh\necho husky\n", string(data), "a foreign hook must be left byte-for-byte untouched")
}

func TestInstall_ForceOverwritesForeignHook(t *testing.T) {
	repo := initRepo(t)
	hooksDir := filepath.Join(repo, ".git", "hooks")
	require.NoError(t, os.WriteFile(filepath.Join(hooksDir, "pre-commit"), []byte("#!/bin/sh\necho husky\n"), 0o755))

	installed, skipped, err := githooks.Install(repo, testConfig(), true)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"commit-msg", "pre-commit"}, installed)
	assert.Empty(t, skipped)
}

func TestInstall_RerunIsIdempotent(t *testing.T) {
	repo := initRepo(t)
	_, _, err := githooks.Install(repo, testConfig(), false)
	require.NoError(t, err)
	installed, skipped, err := githooks.Install(repo, testConfig(), false)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"commit-msg", "pre-commit"}, installed, "rtunk's own hook is not foreign, so a re-run installs (overwrites) it again rather than skipping")
	assert.Empty(t, skipped)
}

func TestInstall_RespectsCoreHooksPath(t *testing.T) {
	repo := initRepo(t)
	require.NoError(t, exec.Command("git", "-C", repo, "config", "core.hooksPath", "custom-hooks").Run())

	installed, _, err := githooks.Install(repo, testConfig(), false)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"commit-msg", "pre-commit"}, installed)
	assert.FileExists(t, filepath.Join(repo, "custom-hooks", "pre-commit"))
}

func TestUninstall_RemovesOnlyRtunkHooks(t *testing.T) {
	repo := initRepo(t)
	_, _, err := githooks.Install(repo, testConfig(), false)
	require.NoError(t, err)
	hooksDir := filepath.Join(repo, ".git", "hooks")
	require.NoError(t, os.WriteFile(filepath.Join(hooksDir, "post-checkout"), []byte("#!/bin/sh\necho foreign\n"), 0o755))

	removed, err := githooks.Uninstall(repo)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"commit-msg", "pre-commit"}, removed)
	assert.NoFileExists(t, filepath.Join(hooksDir, "pre-commit"))
	assert.FileExists(t, filepath.Join(hooksDir, "post-checkout"), "a foreign hook must survive uninstall")
}
