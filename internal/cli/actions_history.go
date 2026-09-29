package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/xunleii/rtunk/pkg/git"
	"github.com/xunleii/rtunk/pkg/run/actions"
)

type actionsHistoryCmd struct {
	ID    string `help:"Restrict to one action id."`
	Limit int    `aliases:"count" help:"Maximum entries to show. (alias: --count)" default:"20"`
}

func (c *actionsHistoryCmd) Run(cli *CLI, stdout io.Writer) error {
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

	entries, err := actions.History(cli.CacheDir, repoRoot, c.ID, c.Limit)
	if err != nil {
		return err
	}
	for _, e := range entries {
		status := fmt.Sprintf("exit %d", e.ExitCode)
		if e.Skipped {
			status = "skipped"
		}
		hook := e.Hook
		if hook == "" {
			hook = "manual"
		}
		_, _ = fmt.Fprintf(stdout, "%s  %s  %s  %s  %s\n",
			e.StartedAt.Format(time.RFC3339), e.ActionID, hook, status, e.Duration.Round(time.Millisecond))
	}
	return nil
}
