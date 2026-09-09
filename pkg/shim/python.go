package shim

import (
	"fmt"
	"os"
	oexec "os/exec"
	"path/filepath"
)

// InstallPython installs pkgs via a single batched `uv pip install
// --prefix dest`. uv itself is expected on rt's own PATH — pkg/runtime
// bootstraps it when the python runtime is installed (see pkg/runtime's
// doc), so there's no pip fallback here. uv's pip-compatible interface
// writes scripts to dest/bin but leaves the package itself under
// dest/lib/pythonX.Y/site-packages, same as pip's own --prefix scheme —
// the script's shebang doesn't search there on its own, so a PYTHONPATH
// pointing at it is still needed at run time (reused from the pre-split
// pkg/plugin/tool.go packageEnv logic). A repeat call across an unchanged
// batch is a stat sweep (allPresent), not a re-install.
func InstallPython(rt Shim, dest string, pkgs []Package) ([]Shim, error) {
	binDir := filepath.Join(dest, "bin")
	extra := map[string]string{"VIRTUAL_ENV": dest, "PYTHONUTF8": "1"}
	if p := sitePackages(dest); p != "" {
		extra["PYTHONPATH"] = p
	}
	env := mergeEnv(rt, extra)
	path := prependPath(rt, binDir)
	if allPresent(binDir, pkgs) {
		return buildShims(binDir, pkgs, dest, path, env)
	}

	if err := os.MkdirAll(dest, 0o755); err != nil {
		return nil, err
	}
	specs := make([]string, len(pkgs))
	for i, p := range pkgs {
		specs[i] = pythonSpec(p.Name, p.Version)
	}
	cmd := oexec.Command("uv", append([]string{"pip", "install", "--prefix", dest}, specs...)...)
	cmd.Env = append(os.Environ(), pathEnv(prependPath(rt, dest)))
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("uv pip install %v: %w: %s", specs, err, out)
	}

	// Recompute: the install may have just populated site-packages for the
	// first time.
	env = mergeEnv(rt, map[string]string{"VIRTUAL_ENV": dest, "PYTHONUTF8": "1"})
	if p := sitePackages(dest); p != "" {
		env["PYTHONPATH"] = p
	}
	return buildShims(binDir, pkgs, dest, path, env)
}

func pythonSpec(pkg, version string) string {
	if version == "" {
		return pkg
	}
	return pkg + "==" + version
}

// sitePackages returns prefixDir's own site-packages dir if the install
// populated one ("" if not found).
func sitePackages(prefixDir string) string {
	matches, err := filepath.Glob(filepath.Join(prefixDir, "lib", "python*", "site-packages"))
	if err != nil || len(matches) == 0 {
		return ""
	}
	return matches[0]
}
