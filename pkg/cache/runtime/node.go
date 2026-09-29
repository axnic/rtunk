package runtime

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/xunleii/rtunk/pkg/cache/install"
)

// installNodePackage runs `npm install --prefix <scratch dir> pkg@version` using the npm shipped
// by the already-downloaded node runtime at runtimeInstallDir (never a system npm, per AGENTS.md
// "Reproducibility" -- no silent fallback to whatever happens to be on PATH). Each tool gets its
// own pkgInstallDir, so its node_modules never shares or conflicts with another tool's.
//
// npm installs into a scratch temp directory (a sibling of pkgInstallDir, so the final os.Rename
// stays on one filesystem) and is only published to pkgInstallDir via install.Finalize once it has
// actually succeeded (see Fix 3) -- otherwise a failed/killed `npm install` left pkgInstallDir
// existing (MkdirAll used to be this function's first action), and dirNonEmpty(pkgInstallDir)
// would wrongly treat that as a completed, cached install forever.
func installNodePackage(runtimeInstallDir, pkgInstallDir, pkg, version string, extra []string) error {
	npm := filepath.Join(runtimeInstallDir, "bin", "npm")
	if _, err := os.Stat(npm); err != nil {
		return fmt.Errorf("runtime: npm not found at %s: %w", npm, err)
	}
	if err := os.MkdirAll(filepath.Dir(pkgInstallDir), 0o750); err != nil {
		return err
	}
	tmpDir, err := os.MkdirTemp(filepath.Dir(pkgInstallDir), ".tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmpDir) }() // no-op once install.Finalize renames it into pkgInstallDir

	env := append(os.Environ(), "PATH="+filepath.Join(runtimeInstallDir, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	install_ := func(name, ver string) error {
		spec := name
		if ver != "" {
			spec = name + "@" + ver
		}
		//nolint:gosec // npm is rtunk's own installed runtime; pkg/version come from the pinned plugin catalog
		cmd := exec.Command(npm, "install", "--prefix", tmpDir, spec)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("runtime: npm install %s: %w: %s", spec, err, out)
		}
		return nil
	}
	if err := install_(pkg, version); err != nil {
		return err
	}
	// Every real catalog entry is either a bare unpinned name or already carries npm's own
	// "@scope/pkg" syntax embedded in the string -- passed through as name with version always ""
	// so install_ uses it bare, exactly as-is.
	for _, e := range extra {
		if err := install_(e, ""); err != nil {
			return err
		}
	}
	return install.Finalize(tmpDir, pkgInstallDir)
}

// installNodePackagesFile runs a bare `npm install` (no package arg) with packagesFilePath copied
// into the scratch dir as ./package.json first, so npm reads it from its own cwd and installs into
// ./node_modules there -- unlike installNodePackage's `--prefix`, there is no single pkg@version to
// pass on the command line. Same scratch-dir-then-install.Finalize shape (see installNodePackage's
// own comment for why: a failed/killed npm install must never leave pkgInstallDir looking cached).
func installNodePackagesFile(runtimeInstallDir, pkgInstallDir, packagesFilePath string) error {
	npm := filepath.Join(runtimeInstallDir, "bin", "npm")
	if _, err := os.Stat(npm); err != nil {
		return fmt.Errorf("runtime: npm not found at %s: %w", npm, err)
	}
	if err := os.MkdirAll(filepath.Dir(pkgInstallDir), 0o750); err != nil {
		return err
	}
	tmpDir, err := os.MkdirTemp(filepath.Dir(pkgInstallDir), ".tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	data, err := os.ReadFile(packagesFilePath)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "package.json"), data, 0o600); err != nil {
		return err
	}

	//nolint:gosec // npm is rtunk's own installed runtime
	cmd := exec.Command(npm, "install")
	cmd.Dir = tmpDir
	cmd.Env = append(os.Environ(), "PATH="+filepath.Join(runtimeInstallDir, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("runtime: npm install (packages_file %s): %w: %s", packagesFilePath, err, out)
	}
	return install.Finalize(tmpDir, pkgInstallDir)
}

var nodeRuntime = Runtime{Install: installNodePackage, InstallFile: installNodePackagesFile}
