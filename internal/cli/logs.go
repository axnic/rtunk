package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/xunleii/rtunk/pkg/trunk/runlog"
)

// logsCmd is `rtunk logs`: reads back, and cleans, the per-run JSONL logs that check, fmt and
// actions run write (pkg/trunk/runlog). Every subcommand but `clean --all` is scoped to the
// current repository.
type logsCmd struct {
	List  logsListCmd  `cmd:"" default:"withargs" help:"List this repository's recent runs."`
	Show  logsShowCmd  `cmd:"" help:"Show one run's log (default: the latest)."`
	Clean logsCleanCmd `cmd:"" help:"Delete this repository's run logs."`
}

// logsRepoRoot is the repository key the run-writing commands log under: the config file's
// grandparent directory (<repoRoot>/.trunk/trunk.yaml or <repoRoot>/.rtunk/rtunk.yaml), the same
// derivation check and fmt use for their repoRoot.
func logsRepoRoot(cli *CLI) (string, error) {
	configPath := cli.Config
	if configPath == "" {
		found, err := findTrunkYAML()
		if err != nil {
			return "", err
		}
		configPath = found
	}
	return filepath.Dir(filepath.Dir(configPath)), nil
}

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
