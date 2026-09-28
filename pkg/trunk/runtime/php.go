package runtime

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/xunleii/rtunk/pkg/trunk/install"
)

// installPhpPackage runs `composer require --working-dir <scratch> --no-interaction pkg:version`
// using composer found on the system PATH. Unlike every other runtime in this file set, php's own
// runtime is never downloaded -- Runtime.SystemVersion == "required" (v0.2's Task 9,
// fetchRuntimeRef), so runtimeInstallDir never exists for php and there is no bundled composer to
// find there. Using the system's composer is a deliberate, documented exception (see
// docs/superpowers/specs/2026-09-12-package-runtimes-design.md's ruling): php already relies on
// the system for its own interpreter, so requiring a hermetically-downloaded Composer next to a
// system-provided PHP would be a stricter, inconsistent half-hermetic middle ground.
func installPhpPackage(_, pkgInstallDir, pkg, version string, extra []string) error {
	composer, err := exec.LookPath("composer")
	if err != nil {
		return fmt.Errorf("runtime: composer not found on PATH: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(pkgInstallDir), 0o750); err != nil {
		return err
	}
	tmpDir, err := os.MkdirTemp(filepath.Dir(pkgInstallDir), ".tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	install_ := func(name, ver string) error {
		spec := name
		if ver != "" {
			spec = name + ":" + ver
		}
		cmd := exec.Command(composer, "require", "--working-dir="+tmpDir, "--no-interaction", spec)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("runtime: composer require %s: %w: %s", spec, err, out)
		}
		return nil
	}
	if err := install_(pkg, version); err != nil {
		return err
	}
	for _, e := range extra {
		if err := install_(e, ""); err != nil {
			return err
		}
	}
	return install.Finalize(tmpDir, pkgInstallDir)
}

var phpRuntime = Runtime{Install: installPhpPackage, Datasource: "packagist"}
