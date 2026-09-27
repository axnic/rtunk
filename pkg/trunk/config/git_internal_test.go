package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

	// fetchGitSource now nests plugin sources under plugins/, so we need to calculate
	// the checkout path using the plugins-nested cacheDir
	pluginsCacheDir := filepath.Join(cacheDir, "plugins")
	checkoutDir := checkoutDirPath(pluginsCacheDir, src)

	require.NoError(t, os.MkdirAll(checkoutDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(checkoutDir, "already-here.txt"), []byte("winner\n"), 0o644))

	_, _, err = fetchGitSource(cacheDir, src)
	require.NoError(t, err, "a pre-existing (or concurrent-winner) checkoutDir must not abort the fetch")

	cacheFiles, err := filepath.Glob(filepath.Join(pluginsCacheDir, "*.json"))
	require.NoError(t, err)
	require.Len(t, cacheFiles, 1, "the parsed-definitions cache must still be written")

	// The pre-existing content must survive untouched -- proof the fetch used what was already
	// there rather than clearing and replacing it.
	require.FileExists(t, filepath.Join(checkoutDir, "already-here.txt"))
}

// TestFetchGitSource_CustomCacheDir_NestsUnderPlugins verifies that cacheFilePath returns
// cache files nested under the plugins directory.
func TestFetchGitSource_CustomCacheDir_NestsUnderPlugins(t *testing.T) {
	cacheDir := t.TempDir()
	src := PluginSource{ID: "local-test", URI: cacheDir, Ref: "HEAD"}

	got := cacheFilePath(filepath.Join(cacheDir, "plugins"), src)
	require.True(t, strings.HasPrefix(got, filepath.Join(cacheDir, "plugins")), "cache file must live under <cacheDir>/plugins, got %s", got)
}

// TestFetchGitSource_CustomCacheDir_ChecksOutUnderPluginsSubdir verifies that when fetchGitSource
// is called with a custom cache directory, it nests plugin sources under a plugins/ subdirectory
// rather than polluting the cache root.
func TestFetchGitSource_CustomCacheDir_ChecksOutUnderPluginsSubdir(t *testing.T) {
	src := gitFixtureInternal(t)
	cacheDir := t.TempDir()

	_, _, err := fetchGitSource(cacheDir, src)
	require.NoError(t, err)

	require.DirExists(t, filepath.Join(cacheDir, "plugins", "checkouts"), "checkout must be nested under plugins/")
	require.NoDirExists(t, filepath.Join(cacheDir, "checkouts"), "checkout must not leak into the raw cacheDir root")
}
