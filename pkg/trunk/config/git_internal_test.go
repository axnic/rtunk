package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// gitFixtureInternal is gitFixture (git_test.go, package config_test) inlined here: this file
// needs unexported checkoutDirPath/fetchGitSource, which forces package config (white-box), and
// gitFixture itself isn't visible across that package boundary.
func gitFixtureInternal(t *testing.T) PluginSource {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte("version: \"0.1\"\n"), 0o644))

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=rtunk-test", "GIT_AUTHOR_EMAIL=rtunk-test@example.com",
			"GIT_COMMITTER_NAME=rtunk-test", "GIT_COMMITTER_EMAIL=rtunk-test@example.com")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}
	run("init", "-q")
	run("add", "-A")
	run("commit", "-q", "-m", "fixture")
	run("tag", "-m", "fixture", "--no-sign", "v1.0.0")

	return PluginSource{ID: "fixture", URI: dir, Ref: "v1.0.0"}
}

// TestFetchGitSource_DestinationAlreadyExists reproduces the race a final-review finding caught:
// two concurrent cold fetches of the same plugin source (same uri+ref, empty cache) both clone
// into their own tmpDir, then both try to persist to the same checkoutDir. The old code did
// os.RemoveAll(checkoutDir) followed by os.Rename(tmpDir, checkoutDir); interleaved across two
// processes, that pair deterministically failed with "directory not empty" (RemoveAll from one
// process racing the Rename from the other). Simulating real OS-level concurrency timing would be
// flaky, so instead this pre-creates checkoutDir as a real, non-empty directory before the very
// first fetch -- standing in for "someone else already has it", whether a concurrent process that
// won the race or a leftover checkout from before -- and confirms fetchGitSource now succeeds by
// falling back to what's already there instead of erroring.
func TestFetchGitSource_DestinationAlreadyExists(t *testing.T) {
	src := gitFixtureInternal(t)
	cacheDir := t.TempDir()

	cacheDir, err := filepath.Abs(cacheDir)
	require.NoError(t, err)
	checkoutDir := checkoutDirPath(cacheDir, src)
	require.NoError(t, os.MkdirAll(checkoutDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(checkoutDir, "already-here.txt"), []byte("winner\n"), 0o644))

	_, _, err = fetchGitSource(cacheDir, src)
	require.NoError(t, err, "a pre-existing (or concurrent-winner) checkoutDir must not abort the fetch")

	cacheFiles, err := filepath.Glob(filepath.Join(cacheDir, "*.json"))
	require.NoError(t, err)
	require.Len(t, cacheFiles, 1, "the parsed-definitions cache must still be written")

	// The pre-existing content must survive untouched -- proof the fetch used what was already
	// there rather than clearing and replacing it.
	require.FileExists(t, filepath.Join(checkoutDir, "already-here.txt"))
}
