package cli

import (
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/xunleii/rtunk/pkg/trunk/githooks"
)

// gitHooksCmd is `rtunk git-hooks`: ROADMAP.md v0.5.
type gitHooksCmd struct {
	Install   gitHooksInstallCmd   `cmd:"" help:"Install git hooks for enabled actions."`
	Uninstall gitHooksUninstallCmd `cmd:"" help:"Remove rtunk-installed git hooks."`
}

// gitRepoRoot resolves the real git repository top-level directory containing dir, via
// `git rev-parse --show-toplevel`. dir doesn't need to be exactly the git root -- git itself
// resolves upward -- so this stays correct in nested layouts (e.g. .trunk/trunk.yaml not
// directly at the repo root), unlike a naive filepath.Dir(filepath.Dir(configPath)) guess.
func gitRepoRoot(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("rtunk: resolve git repo root: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

type gitHooksInstallCmd struct {
	Force bool `help:"Overwrite an existing, non-rtunk hook file."`
}

func (c *gitHooksInstallCmd) Run(cli *CLI, stdout io.Writer) error {
	configPath := cli.Config
	if configPath == "" {
		found, err := findTrunkYAML()
		if err != nil {
			return err
		}
		configPath = found
	}
	cfg, err := resolveConfig(configPath, cli.CacheDir, false)
	if err != nil {
		return err
	}
	repoRoot, err := gitRepoRoot(filepath.Dir(configPath))
	if err != nil {
		return err
	}

	installed, skipped, err := githooks.Install(repoRoot, cfg, c.Force)
	if err != nil {
		return err
	}
	for _, name := range installed {
		fmt.Fprintf(stdout, "installed: %s\n", name)
	}
	for _, name := range skipped {
		fmt.Fprintf(stdout, "skipped (foreign hook, use --force): %s\n", name)
	}
	return nil
}

type gitHooksUninstallCmd struct{}

func (c *gitHooksUninstallCmd) Run(cli *CLI, stdout io.Writer) error {
	configPath := cli.Config
	if configPath == "" {
		found, err := findTrunkYAML()
		if err != nil {
			return err
		}
		configPath = found
	}
	repoRoot, err := gitRepoRoot(filepath.Dir(configPath))
	if err != nil {
		return err
	}

	removed, err := githooks.Uninstall(repoRoot)
	if err != nil {
		return err
	}
	for _, name := range removed {
		fmt.Fprintf(stdout, "removed: %s\n", name)
	}
	return nil
}
