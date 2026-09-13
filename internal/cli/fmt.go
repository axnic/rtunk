package cli

import (
	"context"
	"io"
	"path/filepath"
	"runtime"

	"github.com/xunleii/rtunk/pkg/trunk/config"
	"github.com/xunleii/rtunk/pkg/trunk/engine"
)

// fmtCmd is `rtunk fmt [paths...]`: ROADMAP.md v0.4, running every enabled linter's Formatter
// command(s) -- gofmt, prettier, black, and the rest -- against the given paths, or the whole
// repository if none are given.
type fmtCmd struct {
	Paths []string `arg:"" optional:"" help:"Paths to format (default: whole repository)."`
	Jobs  int      `short:"j" help:"Number of parallel linter workers (default: number of CPUs)."`
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

	events, err := engine.Run(context.Background(), engine.Env{
		Cfg: cfg, RepoRoot: repoRoot, CacheDir: cli.CacheDir, Concurrency: jobs,
	}, c.Paths, func(cmd config.Command) bool { return cmd.Formatter })
	if err != nil {
		return err
	}

	_, changed, skipped, failed := drainRunEvents(func(ev engine.Event) { printFmtEvent(stderr, ev) }, events)
	printFmtReport(stdout, changed, skipped)
	return failed
}
