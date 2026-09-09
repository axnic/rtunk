package runtime

import (
	"fmt"
	"os"
	oexec "os/exec"
	"path/filepath"
	"strings"
)

// bootstrapNode activates pnpm via corepack — bundled with every node
// distribution already (node's own shims include "corepack", see the real
// catalog), so no separate download is needed. `corepack prepare
// pnpm@latest --activate` (rather than a bare `corepack enable`) pins a
// concrete pnpm version into dir, since corepack's normal per-project
// resolution (reading a package.json's own packageManager field) doesn't
// apply to this global, hermetic install context.
//
// ponytail: pins "latest" rather than a specific pnpm version — fine while
// nothing depends on a specific pnpm release's own behavior; pin one once
// something does.
func bootstrapNode(dir string, path []string) error {
	corepack := filepath.Join(dir, "bin", "corepack")
	if _, err := os.Stat(corepack); err != nil {
		corepack = filepath.Join(dir, "corepack") // windows layout
	}
	if _, err := os.Stat(filepath.Join(dir, "bin", "pnpm")); err == nil {
		return nil // already activated
	}
	cmd := oexec.Command(corepack, "prepare", "pnpm@latest", "--activate")
	cmd.Env = append(os.Environ(), "PATH="+strings.Join(append([]string{dir}, path...), string(os.PathListSeparator)))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("corepack prepare pnpm: %w: %s", err, out)
	}
	return nil
}
