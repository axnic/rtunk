package download

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// installRustPackage runs `cargo install --root <scratch> --version version pkg` using the cargo
// shipped by the already-downloaded rust runtime at runtimeInstallDir (never a system cargo, per
// AGENTS.md "Reproducibility"). cargo's own --root convention places binaries at
// <root>/bin/<name>, matching shimSearchPaths' existing bin/ check with no further changes.
//
// CARGO_TARGET_DIR points at its own sibling scratch dir, never at tmpDir (the dir finalizeInstall
// renames into pkgInstallDir, the PERMANENT per-tool cache entry): tmpDir/bin is the only real
// output; the build target dir is multi-hundred-MB-to-multi-GB build ephemera that must never be
// kept forever, and would otherwise make every later `rtunk cache clean`/`prune` on this tool drag
// that scratch along too.
func installRustPackage(runtimeInstallDir, pkgInstallDir, pkg, version string) error {
	cargo := filepath.Join(runtimeInstallDir, "bin", "cargo")
	if _, err := os.Stat(cargo); err != nil {
		return fmt.Errorf("download: cargo not found at %s: %w", cargo, err)
	}
	if err := os.MkdirAll(filepath.Dir(pkgInstallDir), 0o750); err != nil {
		return err
	}
	tmpDir, err := os.MkdirTemp(filepath.Dir(pkgInstallDir), ".tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	buildDir, err := os.MkdirTemp(filepath.Dir(pkgInstallDir), ".rustbuild-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(buildDir) }()

	cmd := exec.Command(cargo, "install", "--root", tmpDir, "--version", version, pkg)
	cmd.Env = append(os.Environ(),
		"PATH="+filepath.Join(runtimeInstallDir, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"),
		"CARGO_TARGET_DIR="+filepath.Join(buildDir, "target"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("download: cargo install %s --version %s: %w: %s", pkg, version, err, out)
	}
	return finalizeInstall(tmpDir, pkgInstallDir)
}
