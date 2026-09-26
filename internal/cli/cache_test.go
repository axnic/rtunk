package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/trunk/download"
)

func TestParseAge(t *testing.T) {
	d, err := parseAge("30d")
	require.NoError(t, err)
	assert.Equal(t, 30*24*time.Hour, d)
	d, err = parseAge("90m")
	require.NoError(t, err)
	assert.Equal(t, 90*time.Minute, d)
	for _, bad := range []string{"", "d", "-1d", "abc", "-5h"} {
		_, err := parseAge(bad)
		assert.Error(t, err, bad)
	}
}

func TestPruneOlderThan(t *testing.T) {
	root := t.TempDir()
	old := download.InstallDir(root, "tools", "eslint", "1.0.0")
	fresh := download.InstallDir(root, "tools", "actionlint", "1.0.0")
	oldShim := download.ShimPath(root, "tools", "eslint", "1.0.0", "eslint")
	freshShim := download.ShimPath(root, "tools", "actionlint", "1.0.0", "actionlint")
	for _, f := range []string{oldShim, freshShim} {
		require.NoError(t, os.MkdirAll(filepath.Dir(f), 0o755))
		require.NoError(t, os.WriteFile(f, nil, 0o644))
	}
	require.NoError(t, os.MkdirAll(old, 0o755))
	require.NoError(t, os.MkdirAll(fresh, 0o755))
	past := time.Now().Add(-40 * 24 * time.Hour)
	for _, d := range []string{filepath.Dir(old), filepath.Dir(oldShim)} {
		require.NoError(t, os.Chtimes(d, past, past))
	}

	require.NoError(t, pruneOlderThan(root, time.Now().Add(-30*24*time.Hour)))

	assert.NoDirExists(t, filepath.Dir(old))
	assert.NoFileExists(t, oldShim)
	assert.DirExists(t, fresh)
	assert.FileExists(t, freshShim)
}

func TestTouch_KeepsUsedEntryThroughPrune(t *testing.T) {
	root := t.TempDir()
	dir := download.InstallDir(root, "tools", "eslint", "1.0.0")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	past := time.Now().Add(-40 * 24 * time.Hour)
	require.NoError(t, os.Chtimes(filepath.Dir(dir), past, past))

	download.Touch(root, "tools", "eslint", "1.0.0")
	require.NoError(t, pruneOlderThan(root, time.Now().Add(-30*24*time.Hour)))
	assert.DirExists(t, dir)
}

func TestCacheDestroy(t *testing.T) {
	cacheDir := t.TempDir()
	root, err := download.Root(cacheDir)
	require.NoError(t, err)
	marker := filepath.Join(root, "installs", "tools", "whatever", "1.0.0", "marker")
	require.NoError(t, os.MkdirAll(filepath.Dir(marker), 0o755))
	require.NoError(t, os.WriteFile(marker, nil, 0o644))

	_, stderr, err := run2(t, "--config", trunkYAML, "--cache-dir", cacheDir, "cache", "destroy")
	require.NoError(t, err, "stderr: %s", stderr)
	assert.NoDirExists(t, root)
}
