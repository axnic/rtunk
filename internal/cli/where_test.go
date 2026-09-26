package cli

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/trunk/download"
)

func TestWhereCmd_NotDownloaded(t *testing.T) {
	_, stderr, err := run2(t, "--config", trunkYAML, "--cache-dir", t.TempDir(), "toolbox", "where", "runtime", "node")
	assert.Error(t, err, "stderr: %s", stderr)
}

func TestWhereCmd_PrintsInstallDir(t *testing.T) {
	cacheDir := t.TempDir()
	root, err := download.Root(cacheDir)
	require.NoError(t, err)
	dir := download.InstallDir(root, "tools", "shellcheck", "1.2.3")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	stdout, stderr, err := run2(t, "--config", trunkYAML, "--cache-dir", cacheDir, "toolbox", "where", "tools", "shellcheck@1.2.3")
	require.NoError(t, err, "stderr: %s", stderr)
	assert.Equal(t, dir+"\n", stdout)
}
