package cli

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/xunleii/rtunk/pkg/run/githooks"
)

// gitHooksCmd is `rtunk git-hooks`: ROADMAP.md v0.5. "sync" is real trunk's own subcommand name
// for the equivalent operation ("sync git hooks with trunk.yaml"); it is idempotent (re-running it
// just rewrites the same hook files). "unsync" removes what sync installed.
type gitHooksCmd struct {
	Sync   gitHooksInstallCmd   `cmd:"" help:"Install git hooks for enabled actions."`
	Unsync gitHooksUninstallCmd `cmd:"" help:"Remove rtunk-installed git hooks."`
}

// gitRepoRoot resolves the real git repository top-level directory containing dir, via
// `git rev-parse --show-toplevel`. dir doesn't need to be exactly the git root -- git itself
// resolves upward -- so this stays correct in nested layouts (e.g. .trunk/trunk.yaml not
// directly at the repo root), unlike a naive filepath.Dir(filepath.Dir(configPath)) guess.
func gitRepoRoot(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return "", errors.New(strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", fmt.Errorf("resolve git repo root: %w", err)
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
		_, _ = fmt.Fprintf(stdout, "installed: %s\n", name)
	}
	for _, name := range skipped {
		_, _ = fmt.Fprintf(stdout, "skipped (foreign hook, use --force): %s\n", name)
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
		_, _ = fmt.Fprintf(stdout, "removed: %s\n", name)
	}
	return nil
}
