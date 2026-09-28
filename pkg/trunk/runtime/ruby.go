package runtime

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/xunleii/rtunk/pkg/trunk/install"
)

// installRubyPackage runs `gem install --install-dir <scratch> --bindir <scratch>/bin pkg -v
// version` using the gem shipped by the already-downloaded ruby runtime at runtimeInstallDir
// (never a system gem, per AGENTS.md "Reproducibility"). --bindir explicitly controls where the
// executable lands, matching shimSearchPaths' existing bin/ check with no further changes.
func installRubyPackage(runtimeInstallDir, pkgInstallDir, pkg, version string, extra []PackageSpec) error {
	gem := filepath.Join(runtimeInstallDir, "bin", "gem")
	if _, err := os.Stat(gem); err != nil {
		return fmt.Errorf("runtime: gem not found at %s: %w", gem, err)
	}
	if err := os.MkdirAll(filepath.Dir(pkgInstallDir), 0o750); err != nil {
		return err
	}
	tmpDir, err := os.MkdirTemp(filepath.Dir(pkgInstallDir), ".tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	env := append(os.Environ(), "PATH="+filepath.Join(runtimeInstallDir, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	install_ := func(name, ver string) error {
		cmd := exec.Command(gem, "install", "--no-document",
			"--install-dir", tmpDir, "--bindir", filepath.Join(tmpDir, "bin"),
			name, "-v", ver)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("runtime: gem install %s -v %s: %w: %s", name, ver, err, out)
		}
		return nil
	}
	if err := install_(pkg, version); err != nil {
		return err
	}
	for _, e := range extra {
		if err := install_(e.Name, e.Version); err != nil {
			return err
		}
	}
	return install.Finalize(tmpDir, pkgInstallDir)
}

var rubyRuntime = Runtime{Install: installRubyPackage}
