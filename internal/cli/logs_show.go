package cli

import (
	"io"
	"os"

	"github.com/xunleii/rtunk/pkg/run/runlog"
)

type logsShowCmd struct {
	Ref  string `arg:"" name:"run" optional:"" default:"latest" help:"Run name (or a unique prefix of it) as printed by 'logs list', or 'latest'."`
	JSON bool   `name:"json" help:"Print the raw JSONL instead of the text rendering."`
}

func (c *logsShowCmd) Run(cli *CLI, stdout io.Writer) error {
	repoRoot, err := logsRepoRoot(cli)
	if err != nil {
		return err
	}
	run, err := runlog.Find(cli.CacheDir, repoRoot, c.Ref)
	if err != nil {
		return err
	}
	if c.JSON {
		f, err := os.Open(run.Path)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		_, err = io.Copy(stdout, f)
		return err
	}
	events, err := runlog.Load(run.Path)
	if err != nil {
		return err
	}
	runlog.Render(stdout, events)
	return nil
}
