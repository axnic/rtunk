package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLinkDirectConfigs(t *testing.T) {
	repo := t.TempDir()
	cfgDir := filepath.Join(repo, ".trunk", "configs")
	require.NoError(t, os.MkdirAll(cfgDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, ".shared.yaml"), []byte("trunk\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, ".own.yaml"), []byte("trunk\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repo, ".own.yaml"), []byte("project\n"), 0o644))
	names := []string{".shared.yaml", ".own.yaml", ".absent.yaml"}

	first := linkDirectConfigs(repo, repo, names)
	second := linkDirectConfigs(repo, repo, names)

	got, err := os.ReadFile(filepath.Join(repo, ".shared.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "trunk\n", string(got), "config from .trunk/configs must be visible in dir")
	got, err = os.ReadFile(filepath.Join(repo, ".own.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "project\n", string(got), "the project's own config must win")
	assert.NoFileExists(t, filepath.Join(repo, ".absent.yaml"))

	first()
	assert.FileExists(t, filepath.Join(repo, ".shared.yaml"), "still used by the second job")
	second()
	assert.NoFileExists(t, filepath.Join(repo, ".shared.yaml"))
	assert.FileExists(t, filepath.Join(repo, ".own.yaml"), "a file we did not create is never removed")
}
