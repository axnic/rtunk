package download_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/cache/download"
	"github.com/xunleii/rtunk/pkg/trunk/config"
)

// fakePip writes a stub `pip` script into dir/bin that records its own argv to argvFile and,
// mimicking real `pip install --prefix <dir>`'s actual on-disk effect, creates a console-script
// executable at <dir>/bin/black -- runtime_python.go only needs to know it invoked pip correctly
// and that the result lands where FindShimTarget looks, not that pip itself works. If envFile is
// given, it also records the hermeticity-relevant env vars pip actually received, one per line, so
// a test can assert installPythonPackage's overrides reached the subprocess rather than merely
// not crashing.
func fakePip(t *testing.T, dir, argvFile string, envFile ...string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "bin"), 0o755))
	script := `#!/bin/sh
echo "$@" > ` + argvFile + `
`
	if len(envFile) > 0 {
		script += `{
  echo "PIP_TARGET=$PIP_TARGET"
  echo "PIP_USER=$PIP_USER"
  echo "PYTHONHOME=$PYTHONHOME"
  echo "PYTHONPATH=$PYTHONPATH"
  echo "PIP_CONFIG_FILE=$PIP_CONFIG_FILE"
} > ` + envFile[0] + `
`
	}
	script += `prefix=$3
mkdir -p "$prefix/bin"
echo '#!/bin/sh' > "$prefix/bin/black"
chmod +x "$prefix/bin/black"
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bin", "pip"), []byte(script), 0o755))
}

func TestInstallPackage_Python(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fakePip is a POSIX shell script")
	}
	runtimeDir := t.TempDir()
	argvFile := filepath.Join(t.TempDir(), "argv")
	fakePip(t, runtimeDir, argvFile)

	pkgDir := filepath.Join(t.TempDir(), "install")
	err := download.InstallPackage(config.Runtime{Type: "python"}, runtimeDir, pkgDir, "black", "24.0.0", nil)
	require.NoError(t, err)

	argv, err := os.ReadFile(argvFile)
	require.NoError(t, err)
	fields := strings.Fields(string(argv))
	require.Len(t, fields, 4)
	assert.Equal(t, "install", fields[0])
	assert.Equal(t, "--prefix", fields[1])
	assert.NotEqual(t, pkgDir, fields[2], "pip must run against a scratch temp dir, not pkgDir directly")
	assert.Equal(t, filepath.Dir(pkgDir), filepath.Dir(fields[2]), "the scratch dir must be a sibling of pkgDir (same filesystem for the final rename)")
	assert.Equal(t, "black==24.0.0", fields[3])

	assert.DirExists(t, pkgDir, "a successful pip install must be renamed into pkgDir")
	target, err := download.FindShimTarget(pkgDir, "black")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(pkgDir, "bin", "black"), target)
}

// TestInstallPackage_Python_PipHermeticity pins down Fix 3: real pip 26.1 fails outright with a
// confusing error if it inherits PIP_TARGET or PIP_USER alongside --prefix ("Cannot set --home
// and --prefix together" / "Can not combine '--user' and '--prefix'"), and an inherited
// PYTHONHOME/PYTHONPATH could make the runtime's own python load a foreign stdlib or
// pip/setuptools. installPythonPackage must clear all of these (and set PIP_CONFIG_FILE to
// os.DevNull) regardless of what the calling process's environment holds.
func TestInstallPackage_Python_PipHermeticity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fakePip is a POSIX shell script")
	}
	runtimeDir := t.TempDir()
	argvFile := filepath.Join(t.TempDir(), "argv")
	envFile := filepath.Join(t.TempDir(), "env")
	fakePip(t, runtimeDir, argvFile, envFile)

	// Set bogus values in the test process; installPythonPackage must override every one of them.
	t.Setenv("PIP_TARGET", "/bogus/target")
	t.Setenv("PIP_USER", "1")
	t.Setenv("PYTHONHOME", "/bogus/pythonhome")
	t.Setenv("PYTHONPATH", "/bogus/pythonpath")

	pkgDir := filepath.Join(t.TempDir(), "install")
	err := download.InstallPackage(config.Runtime{Type: "python"}, runtimeDir, pkgDir, "black", "24.0.0", nil)
	require.NoError(t, err, "install must succeed even with hostile pip/python env vars inherited")

	envContent, err := os.ReadFile(envFile)
	require.NoError(t, err)
	env := string(envContent)
	assert.Contains(t, env, "PIP_TARGET=\n", "PIP_TARGET must be cleared")
	assert.Contains(t, env, "PIP_USER=\n", "PIP_USER must be cleared")
	assert.Contains(t, env, "PYTHONHOME=\n", "PYTHONHOME must be cleared")
	assert.Contains(t, env, "PYTHONPATH=\n", "PYTHONPATH must be cleared")
	assert.Contains(t, env, "PIP_CONFIG_FILE="+os.DevNull, "PIP_CONFIG_FILE must be pinned to the null device")
}
