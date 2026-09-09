package shim

import (
	"fmt"
	"os"
	oexec "os/exec"
	"path/filepath"
)

// InstallNode installs pkgs via a single batched `pnpm add --global`
// (PNPM_HOME=dest), landing every requested package's shims under
// dest/node_modules/.bin in one pnpm invocation. pnpm itself is expected
// on rt's own PATH — pkg/runtime bootstraps it via corepack when the node
// runtime is installed (see pkg/runtime's doc), so there's no npm
// fallback here: a missing pnpm means the runtime wasn't set up right,
// not a machine-dependent choice to fall back on. A repeat call across an
// unchanged batch is a stat sweep (allPresent), not a re-install.
func InstallNode(rt Shim, dest string, pkgs []Package) ([]Shim, error) {
	binDir := filepath.Join(dest, "node_modules", ".bin")
	path := prependPath(rt, binDir)
	env := mergeEnv(rt, map[string]string{"NODE_PATH": filepath.Join(dest, "node_modules")})
	if allPresent(binDir, pkgs) {
		return buildShims(binDir, pkgs, dest, path, env)
	}

	if err := os.MkdirAll(dest, 0o755); err != nil {
		return nil, err
	}
	specs := make([]string, len(pkgs))
	for i, p := range pkgs {
		specs[i] = nodeSpec(p.Name, p.Version)
	}
	cmd := oexec.Command("pnpm", append([]string{"add", "--global"}, specs...)...)
	cmd.Env = append(os.Environ(), "PNPM_HOME="+dest, pathEnv(prependPath(rt, dest)))
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("pnpm add %v: %w: %s", specs, err, out)
	}
	return buildShims(binDir, pkgs, dest, path, env)
}

func nodeSpec(pkg, version string) string {
	if version == "" {
		return pkg
	}
	return pkg + "@" + version
}
