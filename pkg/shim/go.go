package shim

import (
	"fmt"
	"os"
	oexec "os/exec"
	"strings"
)

// InstallGo installs pkgs via `go install <pkg>@<version>`, one invocation
// per distinct (package, version) pair — go install only accepts one
// pinned version per invocation, unlike npm/pip's multi-package installs —
// all landing in the same dest (GOBIN=dest), so repeated calls across a
// batch still share one directory/cache-key. rt supplies the go toolchain
// itself (GOROOT/PATH) this runs on top of. A repeat call across an
// unchanged batch is a stat sweep (allPresent), not a re-install.
func InstallGo(rt Shim, dest string, pkgs []Package) ([]Shim, error) {
	env := mergeEnv(rt, map[string]string{
		"GOPATH": dest, "GOROOT": rt.Dir, "GO111MODULE": "on", "CGO_ENABLED": "0",
	})
	path := prependPath(rt, dest)
	if allPresent(dest, pkgs) {
		return buildShims(dest, pkgs, dest, path, env)
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return nil, err
	}
	cmdEnv := append(os.Environ(), "GOBIN="+dest, pathEnv(path))
	for k, v := range env {
		cmdEnv = append(cmdEnv, k+"="+v)
	}

	for _, p := range pkgs {
		spec := goSpec(p.Name, p.Version)
		cmd := oexec.Command("go", "install", spec)
		cmd.Env = cmdEnv
		if out, err := cmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("go install %s: %w: %s", spec, err, out)
		}
	}
	return buildShims(dest, pkgs, dest, path, env)
}

// goSpec builds the "<package>@v<version>" spec go install expects — Go
// module version tags require a "v" prefix (confirmed empirically: "go
// install pkg@2.12.2" fails with "unknown revision", "go install
// pkg@v2.12.2" works).
func goSpec(pkg, version string) string {
	if version == "" {
		return pkg
	}
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	return pkg + "@" + version
}
