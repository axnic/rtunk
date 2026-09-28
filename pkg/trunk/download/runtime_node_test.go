package download_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/trunk/config"
	"github.com/xunleii/rtunk/pkg/trunk/download"
)

// fakeNpm writes a stub `npm` script into dir/bin that records its own argv to argvFile instead
// of touching the real network -- runtime_node.go only needs to know it invoked npm correctly,
// not that npm itself works.
func fakeNpm(t *testing.T, dir, argvFile string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "bin"), 0o755))
	script := "#!/bin/sh\necho \"$@\" > " + argvFile + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bin", "npm"), []byte(script), 0o755))
}

func TestInstallPackage_Node(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fakeNpm is a POSIX shell script")
	}
	runtimeDir := t.TempDir()
	argvFile := filepath.Join(t.TempDir(), "argv")
	fakeNpm(t, runtimeDir, argvFile)

	// pkgDir must not exist yet -- InstallPackage now only creates it via an atomic rename once
	// npm has actually succeeded (Fix 3), matching how InstallDir() hands it a not-yet-existing
	// path in real use.
	pkgDir := filepath.Join(t.TempDir(), "install")
	err := download.InstallPackage(config.Runtime{Type: "node"}, runtimeDir, pkgDir, "eslint", "8.10.0", nil)
	require.NoError(t, err)

	argv, err := os.ReadFile(argvFile)
	require.NoError(t, err)
	fields := strings.Fields(string(argv))
	require.Len(t, fields, 4)
	assert.Equal(t, "install", fields[0])
	assert.Equal(t, "--prefix", fields[1])
	// npm is invoked against a scratch temp dir, not pkgDir directly -- only a successful
	// install gets renamed into pkgDir.
	assert.NotEqual(t, pkgDir, fields[2], "npm must run against a scratch temp dir, not pkgDir directly")
	assert.Equal(t, filepath.Dir(pkgDir), filepath.Dir(fields[2]), "the scratch dir must be a sibling of pkgDir (same filesystem for the final rename)")
	assert.Equal(t, "eslint@8.10.0", fields[3])

	assert.DirExists(t, pkgDir, "a successful npm install must be renamed into pkgDir")
}

func TestInstallPackage_UnsupportedRuntime(t *testing.T) {
	err := download.InstallPackage(config.Runtime{Type: "java"}, t.TempDir(), t.TempDir(), "checkstyle", "10.0.0", nil)
	assert.ErrorContains(t, err, "java")
}
