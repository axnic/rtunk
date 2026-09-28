package runtime

import (
	"os"
	"path/filepath"
	goruntime "runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInstallPythonPackage_ExtraPackages_AllLandInSamePkgInstallDir pins down the Task 4 fix: every
// entry in extra must land in the SAME pkgInstallDir as the main package, via one shared tmpDir and
// exactly one install.Finalize call. Before the fix, a naive "call installPythonPackage again per
// extra package" would silently no-op the second call (install.Finalize's fallback treats an
// already-existing pkgInstallDir as "a concurrent caller already finished"), so this test fails
// against that broken shape and passes against the loop-then-one-Finalize shape.
func TestInstallPythonPackage_ExtraPackages_AllLandInSamePkgInstallDir(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("pip stub is a POSIX shell script")
	}

	runtimeDir := t.TempDir()
	// $3 is --prefix's value (tmpDir); $4 is "name==version". Every invocation appends to the same
	// tmpDir-relative log, so the log's final contents prove both installs landed in one tree.
	pipScript := `#!/bin/sh
prefix=$3
mkdir -p "$prefix/bin"
echo "$4" >> "$prefix/bin/installed.log"
`
	require.NoError(t, os.MkdirAll(filepath.Join(runtimeDir, "bin"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(runtimeDir, "bin", "pip"), []byte(pipScript), 0o755))

	pkgInstallDir := filepath.Join(t.TempDir(), "install")
	err := installPythonPackage(runtimeDir, pkgInstallDir, "main", "1.0", []PackageSpec{{Name: "extra1", Version: "2.0"}})
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(pkgInstallDir, "bin", "installed.log"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "main==1.0")
	assert.Contains(t, string(data), "extra1==2.0")
}
