package cli

import "github.com/xunleii/rtunk/pkg/run/runlog"

type logsCleanCmd struct {
	All bool `help:"Delete every repository's logs, not just this one's."`
}

func (c *logsCleanCmd) Run(cli *CLI) error {
	if c.All {
		return runlog.Clean(cli.CacheDir, "")
	}
	repoRoot, err := logsRepoRoot(cli)
	if err != nil {
		return err
	}
	return runlog.Clean(cli.CacheDir, repoRoot)
}
