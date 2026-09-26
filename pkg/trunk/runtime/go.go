package runtime

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/xunleii/rtunk/pkg/trunk/install"
)

// installGoPackage runs `go install pkg@version` using the go toolchain shipped by the
// already-downloaded go runtime at runtimeInstallDir (never a system go, per AGENTS.md
// "Reproducibility"). GOBIN is pointed at a scratch bin/ dir so the built binary lands where
// shimSearchPaths already looks. GOTOOLCHAIN=local and GOROOT pin go to the toolchain version
// we just downloaded, instead of silently using a different one from the caller's environment.
//
// GOPATH/GOCACHE point at their own sibling scratch dir, never at tmpDir (the dir install.Finalize
// renames into pkgInstallDir, the PERMANENT per-tool cache entry): tmpDir/bin is the only real
// output; the module cache and build cache are multi-hundred-MB-to-multi-GB build ephemera that
// must never be kept forever, and worse, go's module cache is written read-only by design, so a
// later `os.RemoveAll` on pkgInstallDir (rtunk cache clean/prune) would fail outright the moment
// any go tool had ever been installed. GOFLAGS=-modcacherw makes that module cache deletable too,
// so this scratch dir's own unconditional cleanup below doesn't hit the same failure.
func installGoPackage(runtimeInstallDir, pkgInstallDir, pkg, version string) error {
	goBin := filepath.Join(runtimeInstallDir, "bin", "go")
	if _, err := os.Stat(goBin); err != nil {
		return fmt.Errorf("runtime: go not found at %s: %w", goBin, err)
	}
	if err := os.MkdirAll(filepath.Dir(pkgInstallDir), 0o750); err != nil {
		return err
	}
	tmpDir, err := os.MkdirTemp(filepath.Dir(pkgInstallDir), ".tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	binDir := filepath.Join(tmpDir, "bin")
	if err := os.MkdirAll(binDir, 0o750); err != nil {
		return err
	}

	buildDir, err := os.MkdirTemp(filepath.Dir(pkgInstallDir), ".gobuild-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(buildDir) }()

	// Go module versions are "v"-prefixed; trunk.yaml pins aren't (see goRuntime.ExtractVersion).
	if version != "" && version[0] >= '0' && version[0] <= '9' {
		version = "v" + version
	}
	//nolint:gosec // goBin is rtunk's own installed toolchain; pkg/version come from the pinned plugin catalog
	cmd := exec.Command(goBin, "install", pkg+"@"+version)
	cmd.Env = append(os.Environ(),
		"PATH="+filepath.Join(runtimeInstallDir, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GOBIN="+binDir,
		"GOPATH="+filepath.Join(buildDir, "gopath"),
		"GOCACHE="+filepath.Join(buildDir, "gocache"),
		"GOROOT="+runtimeInstallDir,
		"GOTOOLCHAIN=local",
		"GOFLAGS=-modcacherw",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("runtime: go install %s@%s: %w: %s", pkg, version, err, out)
	}
	return install.Finalize(tmpDir, pkgInstallDir)
}

var goRuntime = Runtime{
	Install:    installGoPackage,
	Datasource: "go",
	// Go module versions are always "v"-prefixed, but a trunk.yaml pin isn't (installGoPackage
	// adds the "v" back right before `go install`).
	ExtractVersion: `^v(?<version>.+)$`,
}
