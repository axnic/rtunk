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

// fakeGem writes a stub `gem` script into dir/bin that records its own argv to argvFile and
// creates a fake executable at the --bindir argument the way real `gem install --bindir` would.
func fakeGem(t *testing.T, dir, argvFile string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "bin"), 0o755))
	script := `#!/bin/sh
echo "$@" > ` + argvFile + `
bindir=""
prev=""
for arg in "$@"; do
  if [ "$prev" = "--bindir" ]; then bindir=$arg; fi
  prev=$arg
done
mkdir -p "$bindir"
echo '#!/bin/sh' > "$bindir/rufo"
chmod +x "$bindir/rufo"
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bin", "gem"), []byte(script), 0o755))
}

func TestInstallPackage_Ruby(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("fakeGem is a POSIX shell script")
	}
	runtimeDir := t.TempDir()
	argvFile := filepath.Join(t.TempDir(), "argv")
	fakeGem(t, runtimeDir, argvFile)

	pkgDir := filepath.Join(t.TempDir(), "install")
	rt, ok := runtime.Lookup("ruby")
	require.True(t, ok)
	err := rt.Install(runtimeDir, pkgDir, "rufo", "0.15.0", nil)
	require.NoError(t, err)

	argv, err := os.ReadFile(argvFile)
	require.NoError(t, err)
	fields := strings.Fields(string(argv))
	assert.Contains(t, fields, "--install-dir")
	assert.Contains(t, fields, "--bindir")
	assert.Contains(t, fields, "rufo")
	assert.Contains(t, fields, "-v")
	assert.Contains(t, fields, "0.15.0")

	assert.DirExists(t, pkgDir, "a successful gem install must be renamed into pkgDir")
	target, err := download.FindShimTarget(pkgDir, "rufo")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(pkgDir, "bin", "rufo"), target)
}
