package cli

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/xunleii/rtunk/pkg/git"
	"github.com/xunleii/rtunk/pkg/run/githooks"
)

type gitHooksInstallCmd struct {
	Force bool `help:"Overwrite an existing, non-rtunk hook file."`
}

func (c *gitHooksInstallCmd) Run(cli *CLI, stdout io.Writer) error {
	configPath := cli.Config
	if configPath == "" {
		found, err := findConfig()
		if err != nil {
			return err
		}
		configPath = found
	}
	cfg, err := resolveConfig(configPath, cli.CacheDir, false)
	if err != nil {
		return err
	}
	repoRoot, err := git.RepoRoot(filepath.Dir(configPath))
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
