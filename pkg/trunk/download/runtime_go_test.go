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

// fakeGo writes a stub `go` script into dir/bin that records its own argv, GOBIN, and optionally
// GOROOT env var values to files, then creates a fake binary inside $GOBIN -- mimicking real
// `go install`'s actual on-disk effect (a binary directly inside GOBIN, no further subdirectory).
// It also drops a marker file into $GOPATH and $GOCACHE, the way a real `go install` actually
// populates those directories -- without this, a bugged installGoPackage that points GOPATH/
// GOCACHE inside the renamed tmpDir would look identical, on disk, to the fixed version (neither
// fake script writes there otherwise), so this is required to make TestInstallPackage_Go's
// directory-listing assertion able to fail pre-fix.
func fakeGo(t *testing.T, dir, argvFile string, gorootFile ...string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "bin"), 0o755))
	script := `#!/bin/sh
echo "$@ GOBIN=$GOBIN" > ` + argvFile + `
`
	if len(gorootFile) > 0 {
		script += `echo "GOROOT=$GOROOT" >> ` + gorootFile[0] + `
`
	}
	script += `mkdir -p "$GOBIN"
echo '#!/bin/sh' > "$GOBIN/gofumpt"
chmod +x "$GOBIN/gofumpt"
mkdir -p "$GOPATH/pkg/mod" "$GOCACHE"
echo module-cache-entry > "$GOPATH/pkg/mod/marker"
echo build-cache-entry > "$GOCACHE/marker"
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bin", "go"), []byte(script), 0o755))
}

func TestInstallPackage_Go(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fakeGo is a POSIX shell script")
	}
	runtimeDir := t.TempDir()
	argvFile := filepath.Join(t.TempDir(), "argv")
	fakeGo(t, runtimeDir, argvFile)

	pkgDir := filepath.Join(t.TempDir(), "install")
	err := download.InstallPackage(config.Runtime{Type: "go"}, runtimeDir, pkgDir, "mvdan.cc/gofumpt", "0.6.0", nil)
	require.NoError(t, err)

	argv, err := os.ReadFile(argvFile)
	require.NoError(t, err)
	line := strings.TrimSpace(string(argv))
	fields := strings.Fields(line)
	require.GreaterOrEqual(t, len(fields), 3)
	assert.Equal(t, "install", fields[0])
	assert.Equal(t, "mvdan.cc/gofumpt@v0.6.0", fields[1])
	assert.True(t, strings.HasPrefix(fields[2], "GOBIN="))
	gobin := strings.TrimPrefix(fields[2], "GOBIN=")
	assert.NotEqual(t, pkgDir, gobin, "go install must run against a scratch GOBIN, not pkgDir directly")
	assert.Equal(t, filepath.Dir(pkgDir), filepath.Dir(filepath.Dir(gobin)), "the scratch dir must be a sibling of pkgDir (same filesystem for the final rename)")

	assert.DirExists(t, pkgDir, "a successful go install must be renamed into pkgDir")
	target, err := download.FindShimTarget(pkgDir, "gofumpt")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(pkgDir, "bin", "gofumpt"), target)

	entries, err := os.ReadDir(pkgDir)
	require.NoError(t, err)
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	assert.Equal(t, []string{"bin"}, names,
		"pkgDir must contain only the real go install output (bin/); GOPATH/GOCACHE build "+
			"scratch must never be renamed into the permanent per-tool cache entry")
}

func TestInstallPackage_Go_GoRootHermeticity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fakeGo is a POSIX shell script")
	}
	runtimeDir := t.TempDir()
	argvFile := filepath.Join(t.TempDir(), "argv")
	gorootFile := filepath.Join(t.TempDir(), "goroot")
	fakeGo(t, runtimeDir, argvFile, gorootFile)

	// Set a bogus GOROOT in the test process; installGoPackage must override it
	t.Setenv("GOROOT", "/nonexistent/bogus/goroot")

	pkgDir := filepath.Join(t.TempDir(), "install")
	err := download.InstallPackage(config.Runtime{Type: "go"}, runtimeDir, pkgDir, "mvdan.cc/gofumpt", "0.6.0", nil)
	require.NoError(t, err, "install must succeed even with a bogus GOROOT in the calling environment")

	// Verify the fake go script received the correct GOROOT override, not the bogus one
	gorootContent, err := os.ReadFile(gorootFile)
	require.NoError(t, err)
	gorootLine := strings.TrimSpace(string(gorootContent))
	assert.Equal(t, "GOROOT="+runtimeDir, gorootLine, "installGoPackage must override inherited GOROOT")

	// Also verify the install completed successfully and the binary is in place
	assert.DirExists(t, pkgDir, "a successful go install must be renamed into pkgDir")
	target, err := download.FindShimTarget(pkgDir, "gofumpt")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(pkgDir, "bin", "gofumpt"), target)
}

// TestInstallPackage_Go_ExtraPackages_DefaultsAndExplicitVersion covers go's own exception to the
// otherwise-uniform "pass extra_packages through raw" rule: go modules require an explicit
// version always present, so a bare entry with no "@" must default to "@latest" (go's own
// idiomatic "no pin" spelling), while an entry that already carries "@version" is split and used
// as-is.
func TestInstallPackage_Go_ExtraPackages_DefaultsAndExplicitVersion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fakeGo is a POSIX shell script")
	}
	runtimeDir := t.TempDir()
	logFile := filepath.Join(t.TempDir(), "argv.log")
	require.NoError(t, os.MkdirAll(filepath.Join(runtimeDir, "bin"), 0o755))
	script := `#!/bin/sh
echo "$@" >> ` + logFile + `
mkdir -p "$GOBIN"
echo '#!/bin/sh' > "$GOBIN/gofumpt"
chmod +x "$GOBIN/gofumpt"
`
	require.NoError(t, os.WriteFile(filepath.Join(runtimeDir, "bin", "go"), []byte(script), 0o755))

	pkgDir := filepath.Join(t.TempDir(), "install")
	err := download.InstallPackage(config.Runtime{Type: "go"}, runtimeDir, pkgDir, "mvdan.cc/gofumpt", "0.6.0",
		[]string{"example.com/nopin", "example.com/pinned@1.2.3"})
	require.NoError(t, err)

	data, err := os.ReadFile(logFile)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	require.Len(t, lines, 3)
	assert.Contains(t, lines[0], "mvdan.cc/gofumpt@v0.6.0")
	assert.Contains(t, lines[1], "example.com/nopin@latest", "a bare, \"@\"-less extra must default to go's own @latest")
	assert.Contains(t, lines[2], "example.com/pinned@v1.2.3", "an extra already carrying \"@version\" must be used as-is (with the \"v\" prefix installGoPackage always adds back)")
}
