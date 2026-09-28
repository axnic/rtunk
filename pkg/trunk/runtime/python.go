package runtime

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/xunleii/rtunk/pkg/trunk/install"
)

// installPythonPackage runs `pip install --prefix <scratch dir> pkg==version` using the pip
// shipped by the already-downloaded python runtime at runtimeInstallDir (never a system pip, per
// AGENTS.md "Reproducibility" -- no silent fallback to whatever happens to be on PATH). pip's
// --prefix scheme places console-script entry points at <prefix>/bin/<name>, matching
// shimSearchPaths' existing bin/ check with no further changes needed there.
func installPythonPackage(runtimeInstallDir, pkgInstallDir, pkg, version string, extra []string) error {
	pip := filepath.Join(runtimeInstallDir, "bin", "pip")
	if _, err := os.Stat(pip); err != nil {
		return fmt.Errorf("runtime: pip not found at %s: %w", pip, err)
	}
	if err := os.MkdirAll(filepath.Dir(pkgInstallDir), 0o750); err != nil {
		return err
	}
	tmpDir, err := os.MkdirTemp(filepath.Dir(pkgInstallDir), ".tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmpDir) }() // no-op once install.Finalize renames it into pkgInstallDir

	env := append(os.Environ(),
		"PATH="+filepath.Join(runtimeInstallDir, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"),
		// Empirically confirmed against real pip 26.1: an inherited PIP_TARGET/PIP_USER makes
		// `pip install --prefix` fail outright ("Cannot set --home and --prefix together" /
		// "Can not combine '--user' and '--prefix'"); an inherited PYTHONHOME/PYTHONPATH could
		// make the runtime's own python load a foreign stdlib or pip/setuptools. Empty-string
		// overrides here beat an inherited value the same way GOROOT's override does in
		// runtime_go.go.
		"PIP_CONFIG_FILE="+os.DevNull,
		"PYTHONHOME=",
		"PYTHONPATH=",
		"PIP_TARGET=",
		"PIP_USER=",
	)
	install_ := func(name, ver string) error {
		spec := name
		if ver != "" {
			spec = name + "==" + ver
		}
		cmd := exec.Command(pip, "install", "--prefix", tmpDir, spec)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("runtime: pip install %s: %w: %s", spec, err, out)
		}
		return nil
	}
	if err := install_(pkg, version); err != nil {
		return err
	}
	// Every real catalog entry is either a bare unpinned name or already carries pip's own
	// "pkg==version"/"pkg[extra]" syntax embedded in the string -- passed through as name with
	// version always "" so install_ uses it bare, exactly as-is.
	for _, e := range extra {
		if err := install_(e, ""); err != nil {
			return err
		}
	}
	return install.Finalize(tmpDir, pkgInstallDir)
}

// pythonSitePackages returns the site-packages directory pip install --prefix wrote pkg into,
// under installDir. pip's --prefix scheme writes no venv/pyvenv.cfg, so nothing else lets an
// installed console-script's shebang (the runtime's own python, not a venv python) find its own
// package at run time -- without this, every python-based tool's shim fails at exec time with
// ModuleNotFoundError despite pip install having reported success and the shim existing at the
// expected path.
func pythonSitePackages(installDir string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(installDir, "lib", "python*", "site-packages"))
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("runtime: no site-packages found under %s", installDir)
	}
	return matches[0], nil
}

var pythonRuntime = Runtime{
	Install:    installPythonPackage,
	ShimEnv:    pythonShimEnv,
	Datasource: "pypi",
}

func pythonShimEnv(installDir string) ([]string, error) {
	sitePackages, err := pythonSitePackages(installDir)
	if err != nil {
		return nil, err
	}
	return []string{"PYTHONPATH=" + sitePackages}, nil
}
