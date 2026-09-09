package shim

import (
	"fmt"
	"os"
	oexec "os/exec"
	"path/filepath"
)

// InstallPHP installs pkgs via a single batched `composer require
// --working-dir dest`, one "vendor/name:version" spec per package —
// composer bootstraps dest/composer.json on first use. Executables land
// under dest/vendor/bin. rt supplies the PHP runtime this runs on top of
// (trunk's own catalog often leaves the php runtime's download/shims
// empty, assuming a system PHP — rt.Path/rt.Env may then be empty too,
// which is fine: composer just runs against whatever `php` is already on
// PATH). A repeat call across an unchanged batch is a stat sweep
// (allPresent), not a re-install.
//
// ponytail: no real catalog tool exercises the php runtime yet (the
// explored trunk cache has only empty stub dirs for it) — verify against
// a real composer install before shipping a php-based linter.
func InstallPHP(rt Shim, dest string, pkgs []Package) ([]Shim, error) {
	binDir := filepath.Join(dest, "vendor", "bin")
	env := mergeEnv(rt, nil)
	path := prependPath(rt, binDir)
	if allPresent(binDir, pkgs) {
		return buildShims(binDir, pkgs, dest, path, env)
	}

	if err := os.MkdirAll(dest, 0o755); err != nil {
		return nil, err
	}
	specs := make([]string, len(pkgs))
	for i, p := range pkgs {
		specs[i] = phpSpec(p.Name, p.Version)
	}
	args := append([]string{"require", "--working-dir", dest, "--no-interaction"}, specs...)
	cmd := oexec.Command("composer", args...)
	cmd.Env = append(os.Environ(), pathEnv(prependPath(rt, dest)))
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("composer require %v: %w: %s", specs, err, out)
	}
	return buildShims(binDir, pkgs, dest, path, env)
}

func phpSpec(pkg, version string) string {
	if version == "" {
		return pkg
	}
	return pkg + ":" + version
}
