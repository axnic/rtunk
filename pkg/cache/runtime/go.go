package runtime

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/xunleii/rtunk/pkg/cache/install"
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
func installGoPackage(runtimeInstallDir, pkgInstallDir, pkg, version string, extra []string) error {
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

	env := append(os.Environ(),
		"PATH="+filepath.Join(runtimeInstallDir, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GOBIN="+binDir,
		"GOPATH="+filepath.Join(buildDir, "gopath"),
		"GOCACHE="+filepath.Join(buildDir, "gocache"),
		"GOROOT="+runtimeInstallDir,
		"GOTOOLCHAIN=local",
		"GOFLAGS=-modcacherw",
	)
	installOne := func(name, ver string) error {
		// Go module versions are "v"-prefixed; trunk.yaml pins aren't (see pkg/renovate's
		// runtimeDatasources["go"].ExtractVersion, which strips it back off for Renovate).
		if ver != "" && ver[0] >= '0' && ver[0] <= '9' {
			ver = "v" + ver
		}
		//nolint:gosec // goBin is rtunk's own installed toolchain; pkg/version come from the pinned plugin catalog
		cmd := exec.Command(goBin, "install", name+"@"+ver)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("runtime: go install %s@%s: %w: %s", name, ver, err, out)
		}
		return nil
	}
	if err := installOne(pkg, version); err != nil {
		return err
	}
	// go modules require an explicit version always present ("go install pkg" with no "@version"
	// doesn't behave the same way): an entry already carrying "@version" (embedded, catalog-native
	// syntax) is split and used as-is; a bare entry defaults to go's own idiomatic "no pin" spelling.
	for _, e := range extra {
		name, ver, ok := strings.Cut(e, "@")
		if !ok {
			name, ver = e, "latest"
		}
		if err := installOne(name, ver); err != nil {
			return err
		}
	}
	return install.Finalize(tmpDir, pkgInstallDir)
}

var goRuntime = Runtime{Install: installGoPackage}
