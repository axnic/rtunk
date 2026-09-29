package cli

import (
	"fmt"
	"io"
	"time"

	"github.com/xunleii/rtunk/pkg/run/runlog"
)

type logsListCmd struct{}

func (c *logsListCmd) Run(cli *CLI, stdout io.Writer) error {
	repoRoot, err := logsRepoRoot(cli)
	if err != nil {
		return err
	}
	runs, err := runlog.List(cli.CacheDir, repoRoot)
	if err != nil {
		return err
	}
	for _, r := range runs {
		dur := "-" // an interrupted run never recorded its duration
		if r.Status != "interrupted" {
			dur = (time.Duration(r.Ms) * time.Millisecond).String()
		}
		_, _ = fmt.Fprintf(stdout, "%s  %-12s  %-11s  %8s  %s\n", r.Start.Local().Format(time.RFC3339), r.Cmd, r.Status, dur, r.Name)
	}
	return nil
}
