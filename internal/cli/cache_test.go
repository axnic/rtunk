package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/trunk/config"
	"github.com/xunleii/rtunk/pkg/trunk/download"
)

func TestCacheClean(t *testing.T) {
	cacheDir := t.TempDir()
	root, err := download.Root(cacheDir)
	require.NoError(t, err)
	sharedRoot := filepath.Dir(root)

	for _, marker := range []string{
		filepath.Join(root, "installs", "tools", "whatever", "1.0.0", "marker"),
		filepath.Join(sharedRoot, "plugins", "checkouts", "abc", "marker"),
		filepath.Join(sharedRoot, "logs", "marker"),
	} {
		require.NoError(t, os.MkdirAll(filepath.Dir(marker), 0o755))
		require.NoError(t, os.WriteFile(marker, nil, 0o644))
	}
	unrelated := filepath.Join(cacheDir, "unrelated-file.txt")
	require.NoError(t, os.WriteFile(unrelated, nil, 0o644))

	_, stderr, err := run2(t, "--config", trunkYAML, "--cache-dir", cacheDir, "cache", "clean")
	require.NoError(t, err, "stderr: %s", stderr)
	assert.NoDirExists(t, root)
	assert.NoDirExists(t, filepath.Join(sharedRoot, "plugins"))
	assert.NoDirExists(t, filepath.Join(sharedRoot, "logs"))
	assert.FileExists(t, unrelated)
}

func TestCachePrune_DropsEntryForGoneRepo(t *testing.T) {
	cacheDir := t.TempDir()
	require.NoError(t, download.RecordUsage(cacheDir, filepath.Join(t.TempDir(), "gone"), config.Config{
		Tools: map[string]config.Tool{"eslint": {KnownGoodVersion: "1.0.0"}},
	}))
	root, err := download.Root(cacheDir)
	require.NoError(t, err)
	installDir := download.InstallDir(root, "tools", "eslint", "1.0.0")
	require.NoError(t, os.MkdirAll(installDir, 0o755))

	_, stderr, err := run2(t, "--config", trunkYAML, "--cache-dir", cacheDir, "cache", "prune")
	require.NoError(t, err, "stderr: %s", stderr)
	assert.NoDirExists(t, installDir)
}

func TestCachePrune_KeepsEntryForLiveRepo(t *testing.T) {
	cacheDir := t.TempDir()
	liveRepo := t.TempDir()
	require.NoError(t, download.RecordUsage(cacheDir, liveRepo, config.Config{
		Tools: map[string]config.Tool{"eslint": {KnownGoodVersion: "1.0.0"}},
	}))
	root, err := download.Root(cacheDir)
	require.NoError(t, err)
	installDir := download.InstallDir(root, "tools", "eslint", "1.0.0")
	require.NoError(t, os.MkdirAll(installDir, 0o755))

	_, stderr, err := run2(t, "--config", trunkYAML, "--cache-dir", cacheDir, "cache", "prune")
	require.NoError(t, err, "stderr: %s", stderr)
	assert.DirExists(t, installDir)
}
