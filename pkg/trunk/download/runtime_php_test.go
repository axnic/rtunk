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

// fakeComposer writes a stub `composer` script into dir that records its own argv to argvFile
// and creates a fake executable under vendor/bin inside the --working-dir argument, the way real
// `composer require`'s default bin-dir actually lays its output out.
func fakeComposer(t *testing.T, dir, argvFile string) {
	t.Helper()
	script := `#!/bin/sh
echo "$@" > ` + argvFile + `
wd=""
prev=""
for arg in "$@"; do
  case $arg in
    --working-dir=*) wd=${arg#--working-dir=} ;;
  esac
  prev=$arg
done
mkdir -p "$wd/vendor/bin"
echo '#!/bin/sh' > "$wd/vendor/bin/php-cs-fixer"
chmod +x "$wd/vendor/bin/php-cs-fixer"
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "composer"), []byte(script), 0o755))
}

func TestInstallPackage_Php(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fakeComposer is a POSIX shell script")
	}
	fakeComposerDir := t.TempDir()
	argvFile := filepath.Join(t.TempDir(), "argv")
	fakeComposer(t, fakeComposerDir, argvFile)
	t.Setenv("PATH", fakeComposerDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	pkgDir := filepath.Join(t.TempDir(), "install")
	// runtimeInstallDir is irrelevant for php -- pass a path that doesn't exist to prove it's
	// never touched.
	err := download.InstallPackage(config.Runtime{Type: "php"}, "/does/not/exist", pkgDir, "friendsofphp/php-cs-fixer", "3.40.0", nil)
	require.NoError(t, err)

	argv, err := os.ReadFile(argvFile)
	require.NoError(t, err)
	fields := strings.Fields(string(argv))
	assert.Contains(t, fields, "require")
	assert.Contains(t, fields, "--no-interaction")
	assert.Contains(t, fields, "friendsofphp/php-cs-fixer:3.40.0")

	assert.DirExists(t, pkgDir, "a successful composer require must be renamed into pkgDir")
	target, err := download.FindShimTarget(pkgDir, "php-cs-fixer")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(pkgDir, "vendor", "bin", "php-cs-fixer"), target)
}

func TestInstallPackage_Php_ComposerNotOnPath(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // empty dir, guaranteed no composer
	err := download.InstallPackage(config.Runtime{Type: "php"}, "/does/not/exist", filepath.Join(t.TempDir(), "install"), "pkg", "1.0.0", nil)
	assert.ErrorContains(t, err, "composer")
}
