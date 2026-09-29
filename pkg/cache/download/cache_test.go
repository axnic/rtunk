package download_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/cache/download"
)

func TestRoot_ExplicitCacheDir(t *testing.T) {
	root, err := download.Root("/tmp/somewhere")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("/tmp/somewhere", "downloads"), root)
}

func TestRoot_DefaultCacheDir(t *testing.T) {
	root, err := download.Root("")
	require.NoError(t, err)
	userCache, err := os.UserCacheDir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(userCache, "rtunk", "downloads"), root)
}

func TestBlobPath(t *testing.T) {
	assert.Equal(t, filepath.Join("root", "blobs", "sha256", "abc123"), download.BlobPath("root", "abc123"))
}

func TestInstallDir(t *testing.T) {
	got := download.InstallDir("root", "tools", "shellcheck", "0.11.0")
	want := filepath.Join("root", "installs", "tools", "shellcheck", "0.11.0", download.Platform())
	assert.Equal(t, want, got)
}

func TestShimPath(t *testing.T) {
	got := download.ShimPath("root", "tools", "shellcheck", "0.11.0", "shellcheck")
	want := filepath.Join("root", "shims", "tools", "shellcheck", "0.11.0", "shellcheck")
	assert.Equal(t, want, got)
}

func TestPlatform(t *testing.T) {
	assert.Equal(t, runtime.GOOS+"-"+runtime.GOARCH, download.Platform())
}
