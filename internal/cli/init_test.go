package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// initGitRepo creates a real, empty git repo in a fresh temp dir -- gitRepoRoot shells out to a
// real `git rev-parse --show-toplevel`, so init/deinit's own tests need a real repo, not a fixture
// trunk.yaml alone (mirrors pkg/run/githooks' own established real-git-repo test convention).
func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, exec.Command("git", "-C", dir, "init", "-q").Run())
	return dir
}

// chdir switches the test process's cwd to dir for the duration of the test, restoring the
// original cwd via t.Cleanup -- init/deinit resolve their own repo root from os.Getwd(), same as
// findConfig already does (see cli_test.go's own TestFindConfig for the identical pattern).
func chdir(t *testing.T, dir string) {
	t.Helper()
	cwd, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.Chdir(cwd)) })
	require.NoError(t, os.Chdir(dir))
}

func TestInitCmd_WritesScaffold(t *testing.T) {
	repo := initGitRepo(t)
	chdir(t, repo)

	stdout, stderr, err := run2(t, "init")
	require.NoError(t, err, "stderr: %s", stderr)
	assert.Contains(t, stdout, "initialized rtunk")

	data, err := os.ReadFile(filepath.Join(repo, ".rtunk", "rtunk.yaml"))
	require.NoError(t, err)
	assert.Equal(t, initScaffold, string(data))
}

func TestInitCmd_RefusesWithoutForceWhenAlreadyExists(t *testing.T) {
	repo := initGitRepo(t)
	chdir(t, repo)

	_, stderr, err := run2(t, "init")
	require.NoError(t, err, "stderr: %s", stderr)

	_, _, err = run2(t, "init")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestInitCmd_ForceOverwrites(t *testing.T) {
	repo := initGitRepo(t)
	chdir(t, repo)

	_, stderr, err := run2(t, "init")
	require.NoError(t, err, "stderr: %s", stderr)
	require.NoError(t, os.WriteFile(filepath.Join(repo, ".rtunk", "rtunk.yaml"), []byte("version: \"0.1\"\nhand-edited: true\n"), 0o644))

	_, stderr, err = run2(t, "init", "--force")
	require.NoError(t, err, "stderr: %s", stderr)

	data, err := os.ReadFile(filepath.Join(repo, ".rtunk", "rtunk.yaml"))
	require.NoError(t, err)
	assert.Equal(t, initScaffold, string(data))
}

// TestInitCmd_ScaffoldIsActuallyFoundByFindConfig is the direct proof Task 1's fix closes the
// gap this whole feature exists for: NOT a config-resolution test (which would need to fetch the
// scaffold's own real https://github.com/trunk-io/plugins source over the network -- never done in
// a test), just confirming the freshly-scaffolded file is actually locatable afterward.
func TestInitCmd_ScaffoldIsActuallyFoundByFindConfig(t *testing.T) {
	repo := initGitRepo(t)
	// EvalSymlinks: on macOS, t.TempDir() lives under /var, a symlink to /private/var, and
	// os.Getwd() (which findConfig calls) returns the resolved physical path -- normalize here
	// so the two sides of the comparison below agree.
	repo, err := filepath.EvalSymlinks(repo)
	require.NoError(t, err)
	chdir(t, repo)

	_, stderr, err := run2(t, "init")
	require.NoError(t, err, "stderr: %s", stderr)

	found, err := findConfig()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(repo, ".rtunk", "rtunk.yaml"), found)
}

func TestInitCmd_WarnsWhenShadowingExistingTrunkYAML(t *testing.T) {
	repo := initGitRepo(t)
	require.NoError(t, os.MkdirAll(filepath.Join(repo, ".trunk"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repo, ".trunk", "trunk.yaml"), []byte("version: \"0.1\"\n"), 0o644))
	chdir(t, repo)

	_, stderr, err := run2(t, "init")
	require.NoError(t, err, "stderr: %s", stderr)
	assert.Contains(t, stderr, filepath.Join(repo, ".trunk", "trunk.yaml"))
	assert.Contains(t, stderr, "already exists")
}

func TestInitCmd_NoWarningWhenNoExistingTrunkYAML(t *testing.T) {
	repo := initGitRepo(t)
	chdir(t, repo)

	_, stderr, err := run2(t, "init")
	require.NoError(t, err, "stderr: %s", stderr)
	assert.NotContains(t, stderr, "already exists")
}

func TestDeinitCmd_RemovesRtunkDir(t *testing.T) {
	repo := initGitRepo(t)
	chdir(t, repo)
	_, stderr, err := run2(t, "init")
	require.NoError(t, err, "stderr: %s", stderr)

	stdout, stderr, err := run2(t, "deinit")
	require.NoError(t, err, "stderr: %s", stderr)
	assert.Contains(t, stdout, "removed")
	assert.NoDirExists(t, filepath.Join(repo, ".rtunk"))
}

// TestDeinitCmd_RemovesInstalledGitHooks writes its own minimal local-plugin-source trunk.yaml
// directly (rather than via `rtunk init`, whose own scaffold points at the real
// https://github.com/trunk-io/plugins -- never resolved in a test) so `git-hooks sync` has a
// real, local, enabled action with a git_hooks trigger to work from, matching
// pkg/run/githooks' own established real-git-repo test pattern.
func TestDeinitCmd_RemovesInstalledGitHooks(t *testing.T) {
	repo := initGitRepo(t)
	require.NoError(t, os.MkdirAll(filepath.Join(repo, ".rtunk"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "pluginrepo", "actions", "greet"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "pluginrepo", "actions", "greet", "plugin.yaml"), []byte(`actions:
  definitions:
    - id: greet
      run: echo hello
      triggers:
        - git_hooks: [pre-commit]
`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repo, ".rtunk", "rtunk.yaml"), []byte(`version: "0.1"
plugins:
  sources:
    - id: local
      local: ../pluginrepo
actions:
  enabled:
    - greet
`), 0o644))
	chdir(t, repo)

	_, stderr, err := run2(t, "git-hooks", "sync")
	require.NoError(t, err, "stderr: %s", stderr)
	require.FileExists(t, filepath.Join(repo, ".git", "hooks", "pre-commit"))

	stdout, stderr, err := run2(t, "deinit")
	require.NoError(t, err, "stderr: %s", stderr)
	assert.Contains(t, stdout, "removed hook: pre-commit")
	assert.NoFileExists(t, filepath.Join(repo, ".git", "hooks", "pre-commit"))
	assert.NoDirExists(t, filepath.Join(repo, ".rtunk"))
}

// TestDeinitCmd_RemovesHookWithoutRtunkDir is the direct proof of the bug the final reviewer
// reproduced live: a repo that only ever used .trunk/trunk.yaml (rtunk's own primary
// drop-in-to-an-existing-trunk-repo use case, never running `rtunk init` at all) and ran
// `rtunk git-hooks sync` must still have its hook removed by `rtunk deinit`, and must NOT be
// told "nothing to deinit" -- deinit must not gate hook removal on .rtunk/ existing.
func TestDeinitCmd_RemovesHookWithoutRtunkDir(t *testing.T) {
	repo := initGitRepo(t)
	require.NoError(t, os.MkdirAll(filepath.Join(repo, ".trunk"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "pluginrepo", "actions", "greet"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "pluginrepo", "actions", "greet", "plugin.yaml"), []byte(`actions:
  definitions:
    - id: greet
      run: echo hello
      triggers:
        - git_hooks: [pre-commit]
`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repo, ".trunk", "trunk.yaml"), []byte(`version: "0.1"
plugins:
  sources:
    - id: local
      local: ../pluginrepo
actions:
  enabled:
    - greet
`), 0o644))
	chdir(t, repo)

	_, stderr, err := run2(t, "git-hooks", "sync")
	require.NoError(t, err, "stderr: %s", stderr)
	require.FileExists(t, filepath.Join(repo, ".git", "hooks", "pre-commit"))

	stdout, stderr, err := run2(t, "deinit")
	require.NoError(t, err, "stderr: %s", stderr)
	assert.Contains(t, stdout, "removed hook: pre-commit")
	assert.NotContains(t, stdout, "nothing to deinit")
	assert.NoFileExists(t, filepath.Join(repo, ".git", "hooks", "pre-commit"))
}

func TestDeinitCmd_NothingToDeinit_IsNoOp(t *testing.T) {
	repo := initGitRepo(t)
	chdir(t, repo)

	stdout, stderr, err := run2(t, "deinit")
	require.NoError(t, err, "stderr: %s", stderr)
	assert.Contains(t, stdout, "nothing to deinit")
}

// TestDeinitCmd_YesFlag_Accepted: deinit never prompts (an established non-goal since v0.7), so
// -y/--yes asks for behavior deinit already has -- a documented no-op, accepted so scripts that
// defensively pass it (the common case for any automated deinit) don't fail.
func TestDeinitCmd_YesFlag_Accepted(t *testing.T) {
	repo := initGitRepo(t)
	require.NoError(t, os.MkdirAll(filepath.Join(repo, ".rtunk"), 0o755))
	chdir(t, repo)

	_, _, err := run2(t, "deinit", "-y")
	require.NoError(t, err)

	require.NoError(t, os.MkdirAll(filepath.Join(repo, ".rtunk"), 0o755))
	_, _, err = run2(t, "deinit", "--yes")
	require.NoError(t, err)
}
