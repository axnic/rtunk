package cli

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/xunleii/rtunk/pkg/git"
	"github.com/xunleii/rtunk/pkg/run/githooks"
)

type gitHooksUninstallCmd struct{}

func (c *gitHooksUninstallCmd) Run(cli *CLI, stdout io.Writer) error {
	configPath := cli.Config
	if configPath == "" {
		found, err := findConfig()
		if err != nil {
			return err
		}
		configPath = found
	}
	repoRoot, err := git.RepoRoot(filepath.Dir(configPath))
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
