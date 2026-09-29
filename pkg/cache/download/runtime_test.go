package download_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/cache/download"
	"github.com/xunleii/rtunk/pkg/trunk/config"
)

// TestExtraToolEnv_Python pins down Fix 1 Part B: a python tool's shim needs PYTHONPATH pointed
// at the site-packages dir pip install --prefix actually wrote into, since pip's --prefix scheme
// writes no venv/pyvenv.cfg for the shebang's own python interpreter to find it by any other means.
func TestExtraToolEnv_Python(t *testing.T) {
	installDir := t.TempDir()
	sitePkgs := filepath.Join(installDir, "lib", "python3.11", "site-packages")
	require.NoError(t, os.MkdirAll(sitePkgs, 0o755))

	env, err := download.ExtraToolEnv(config.Runtime{Type: "python"}, installDir)
	require.NoError(t, err)
	assert.Equal(t, []string{"PYTHONPATH=" + sitePkgs}, env)
}

// TestExtraToolEnv_Python_NoSitePackages surfaces a real, debuggable error instead of silently
// writing a shim with a PYTHONPATH pointed nowhere, if pip's own install layout ever changes.
func TestExtraToolEnv_Python_NoSitePackages(t *testing.T) {
	installDir := t.TempDir()
	_, err := download.ExtraToolEnv(config.Runtime{Type: "python"}, installDir)
	assert.Error(t, err)
}

// TestExtraToolEnv_OtherRuntimes pins down that every runtime besides python needs nothing extra
// beyond its plugin config's own runtime_environment/linter_environment -- their install layout
// already matches what that config assumes (confirmed by the final review for node/go/ruby/rust/
// php).
func TestExtraToolEnv_OtherRuntimes(t *testing.T) {
	for _, rtType := range []string{"node", "go", "ruby", "rust", "php"} {
		env, err := download.ExtraToolEnv(config.Runtime{Type: rtType}, t.TempDir())
		require.NoError(t, err, rtType)
		assert.Nil(t, env, rtType)
	}
}
