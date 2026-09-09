package shim

import (
	"fmt"
	"os"
	oexec "os/exec"
	"path/filepath"
)

// InstallRuby installs pkgs via a single batched `gem install
// --install-dir dest --bindir dest/bin`, one "name:version" spec per
// package (gem's own colon-version syntax). rt supplies the ruby
// toolchain (gem/bundler) this runs on top of. A repeat call across an
// unchanged batch is a stat sweep (allPresent), not a re-install.
//
// ponytail: no real catalog tool exercises the ruby runtime yet (the
// explored trunk cache has only empty stub dirs for it) — the colon-spec
// batching and --bindir layout follow gem's documented behavior but
// aren't empirically confirmed the way node/python's were; verify against
// a real gem install before shipping a ruby-based linter.
func InstallRuby(rt Shim, dest string, pkgs []Package) ([]Shim, error) {
	binDir := filepath.Join(dest, "bin")
	env := mergeEnv(rt, map[string]string{"GEM_HOME": dest})
	path := prependPath(rt, binDir)
	if allPresent(binDir, pkgs) {
		return buildShims(binDir, pkgs, dest, path, env)
	}

	if err := os.MkdirAll(dest, 0o755); err != nil {
		return nil, err
	}
	specs := make([]string, len(pkgs))
	for i, p := range pkgs {
		specs[i] = rubySpec(p.Name, p.Version)
	}
	args := append([]string{"install", "--install-dir", dest, "--bindir", binDir, "--no-document"}, specs...)
	cmd := oexec.Command("gem", args...)
	cmd.Env = append(os.Environ(), pathEnv(prependPath(rt, binDir)))
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("gem install %v: %w: %s", specs, err, out)
	}
	return buildShims(binDir, pkgs, dest, path, env)
}

func rubySpec(pkg, version string) string {
	if version == "" {
		return pkg
	}
	return pkg + ":" + version
}
