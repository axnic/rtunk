package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"runtime"

	"github.com/xunleii/rtunk/pkg/trunk/config"
	"github.com/xunleii/rtunk/pkg/trunk/engine"
)

// fmtCmd is `rtunk fmt [paths...]`: ROADMAP.md v0.4, running every enabled linter's Formatter
// command(s) -- gofmt, prettier, black, and the rest -- against the given paths, or the whole
// repository if none are given. Plain fmt automatically verifies its own result is stable (see
// runStableFormat) -- real trunk's own fmt safety net, no flag needed. --check instead runs a
// single, standalone dry-run pass that never writes to disk, matching real trunk's own `trunk fmt
// --check`: a CI gate asking "would anything change," not "make it change."
type fmtCmd struct {
	Paths []string `arg:"" optional:"" help:"Paths to format (default: whole repository)."`
	Jobs  int      `short:"j" help:"Number of parallel linter workers (default: number of CPUs)."`
	Check bool     `help:"Report files that would be reformatted, without writing them."`
}

func (c *fmtCmd) Run(cli *CLI, stdout io.Writer, stderr Stderr) error {
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

	jobs := c.Jobs
	if jobs <= 0 {
		jobs = runtime.NumCPU()
	}
	env := engine.Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: cli.CacheDir, Concurrency: jobs}

	if c.Check {
		env.DryRun = true
		events, err := engine.Run(context.Background(), env, c.Paths, func(cmd config.Command) bool { return cmd.Formatter })
		if err != nil {
			return err
		}
		_, wouldChange, skipped, failed := drainRunEvents(func(ev engine.Event) { printFmtEvent(stderr, ev) }, events)
		printFmtCheckReport(stdout, wouldChange, skipped)
		if failed != nil {
			return failed
		}
		if len(wouldChange) > 0 {
			return fmt.Errorf("rtunk: fmt --check found %d file(s) needing reformatting", len(wouldChange))
		}
		return nil
	}

	changed, skipped, err := runStableFormat(context.Background(), env, c.Paths, stderr)
	printFmtReport(stdout, changed, skipped)
	return err
}
