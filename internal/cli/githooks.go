package cli

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/xunleii/rtunk/pkg/trunk/githooks"
)

// gitHooksCmd is `rtunk git-hooks`: ROADMAP.md v0.5.
type gitHooksCmd struct {
	Install   gitHooksInstallCmd   `cmd:"" help:"Install git hooks for enabled actions."`
	Uninstall gitHooksUninstallCmd `cmd:"" help:"Remove rtunk-installed git hooks."`
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
	repoRoot := filepath.Dir(filepath.Dir(configPath))

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
	repoRoot := filepath.Dir(filepath.Dir(configPath))

	removed, err := githooks.Uninstall(repoRoot)
	if err != nil {
		return err
	}
	for _, name := range removed {
		fmt.Fprintf(stdout, "removed: %s\n", name)
	}
	return nil
}
