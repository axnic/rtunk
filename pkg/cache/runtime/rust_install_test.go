package runtime_test

import (
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/cache/download"
	"github.com/xunleii/rtunk/pkg/cache/runtime"
)

// fakeCargo writes a stub `cargo` script into dir/bin that records its own argv to argvFile and
// creates a fake binary directly under the --root argument's bin/ subdirectory, the way real
// `cargo install --root` actually lays its output out. Optionally captures CARGO_TARGET_DIR to a
// file. It also drops a marker file into $CARGO_TARGET_DIR, the way a real `cargo install`
// actually populates it -- without this, a bugged installRustPackage that points
// CARGO_TARGET_DIR inside the renamed tmpDir would look identical, on disk, to the fixed version,
// so this is required to make TestInstallPackage_Rust's directory-listing assertion able to fail
// pre-fix.
func fakeCargo(t *testing.T, dir, argvFile string, cargoTargetDirFile ...string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "bin"), 0o755))
	script := `#!/bin/sh
echo "$@" > ` + argvFile + `
`
	if len(cargoTargetDirFile) > 0 {
		script += `echo "CARGO_TARGET_DIR=$CARGO_TARGET_DIR" >> ` + cargoTargetDirFile[0] + `
`
	}
	script += `root=""
prev=""
for arg in "$@"; do
  if [ "$prev" = "--root" ]; then root=$arg; fi
  prev=$arg
done
mkdir -p "$root/bin" "$CARGO_TARGET_DIR"
echo '#!/bin/sh' > "$root/bin/ripgrep"
chmod +x "$root/bin/ripgrep"
echo build-target-entry > "$CARGO_TARGET_DIR/marker"
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bin", "cargo"), []byte(script), 0o755))
}

func TestInstallPackage_Rust(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("fakeCargo is a POSIX shell script")
	}
	runtimeDir := t.TempDir()
	argvFile := filepath.Join(t.TempDir(), "argv")
	fakeCargo(t, runtimeDir, argvFile)

	pkgDir := filepath.Join(t.TempDir(), "install")
	rt, ok := runtime.Lookup("rust")
	require.True(t, ok)
	err := rt.Install(runtimeDir, pkgDir, "ripgrep", "14.1.0", nil)
	require.NoError(t, err)

	argv, err := os.ReadFile(argvFile)
	require.NoError(t, err)
	fields := strings.Fields(string(argv))
	assert.Contains(t, fields, "--root")
	assert.Contains(t, fields, "--version")
	assert.Contains(t, fields, "14.1.0")
	assert.Contains(t, fields, "ripgrep")

	assert.DirExists(t, pkgDir, "a successful cargo install must be renamed into pkgDir")
	target, err := download.FindShimTarget(pkgDir, "ripgrep")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(pkgDir, "bin", "ripgrep"), target)

	entries, err := os.ReadDir(pkgDir)
	require.NoError(t, err)
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	assert.Equal(t, []string{"bin"}, names,
		"pkgDir must contain only the real cargo install output (bin/); CARGO_TARGET_DIR build "+
			"scratch must never be renamed into the permanent per-tool cache entry")
}

func TestInstallPackage_Rust_CargoTargetDirHermeticity(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("fakeCargo is a POSIX shell script")
	}
	runtimeDir := t.TempDir()
	argvFile := filepath.Join(t.TempDir(), "argv")
	cargoTargetDirFile := filepath.Join(t.TempDir(), "cargo_target_dir")
	fakeCargo(t, runtimeDir, argvFile, cargoTargetDirFile)

	// Set a bogus CARGO_TARGET_DIR in the test process; installRustPackage must override it
	t.Setenv("CARGO_TARGET_DIR", "/nonexistent/bogus/target")

	pkgDir := filepath.Join(t.TempDir(), "install")
	rt, ok := runtime.Lookup("rust")
	require.True(t, ok)
	err := rt.Install(runtimeDir, pkgDir, "ripgrep", "14.1.0", nil)
	require.NoError(t, err, "install must succeed even with a bogus CARGO_TARGET_DIR in the calling environment")

	// Verify the fake cargo script received the correct CARGO_TARGET_DIR override, not the bogus one
	cargoTargetDirContent, err := os.ReadFile(cargoTargetDirFile)
	require.NoError(t, err)
	cargoTargetDirLine := strings.TrimSpace(string(cargoTargetDirContent))
	assert.True(t, strings.HasPrefix(cargoTargetDirLine, "CARGO_TARGET_DIR="), "cargo target dir should be set")
	cargoTargetDir := strings.TrimPrefix(cargoTargetDirLine, "CARGO_TARGET_DIR=")
	assert.NotEqual(t, "/nonexistent/bogus/target", cargoTargetDir, "installRustPackage must override inherited CARGO_TARGET_DIR")
	assert.True(t, strings.Contains(cargoTargetDir, "target"), "CARGO_TARGET_DIR must contain 'target' in its path")

	// Also verify the install completed successfully and the binary is in place
	assert.DirExists(t, pkgDir, "a successful cargo install must be renamed into pkgDir")
	target, err := download.FindShimTarget(pkgDir, "ripgrep")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(pkgDir, "bin", "ripgrep"), target)
}
