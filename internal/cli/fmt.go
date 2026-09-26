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
// repository if none are given. Plain fmt does a single pass, then warns on stderr (without
// failing) if it re-touches a file the previous run for this repo also touched within the last 30s
// (see warnIfRecentOverlap) -- a cheap heuristic for the same instability --verify-stable checks
// rigorously. --check runs a single, standalone dry-run pass that never writes to disk, matching
// real trunk's own `trunk fmt --check`: a CI gate asking "would anything change," not "make it
// change.
type fmtCmd struct {
	Paths        []string `arg:"" optional:"" help:"Paths to format (default: whole repository)."`
	Jobs         int      `short:"j" help:"Number of parallel linter workers (default: number of CPUs)."`
	Check        bool     `aliases:"no-fix" short:"n" help:"Report files that would be reformatted, without writing them. (alias: --no-fix)"`
	VerifyStable bool     `help:"Verify the result is stable (write, dry-run check, write+check again if needed) instead of a single pass."`
	Filter       string   `help:"Comma-separated linter id allow-list, or --filter=-id,-id... deny-list (trunk compatibility)."`
	Exclude      string   `help:"Comma-separated linter id deny-list; shorthand for an inverse --filter (trunk compatibility)."`
	// PrintFailures is accepted for trunk compatibility and has no effect: fmt already always
	// prints Failed events to stderr unconditionally.
	PrintFailures bool `help:"Accepted for trunk compatibility; fmt already always prints failures, this has no effect."`
}

func (c *fmtCmd) Run(cli *CLI, stdout io.Writer, stderr Stderr, argv Argv) error {
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
	cfg, err = filterLinters(cfg, c.Filter, c.Exclude)
	if err != nil {
		return err
	}
	repoRoot := filepath.Dir(filepath.Dir(configPath))

	jobs := c.Jobs
	if jobs <= 0 {
		jobs = runtime.NumCPU()
	}
	env := engine.Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: cli.CacheDir, Concurrency: jobs}

	log := startLog(cli, "fmt", repoRoot, configPath, argv, jobs, c.Check, stderr)
	runFailed := true // cleared once the run reaches its normal end; an early error return keeps it
	defer func() { log.End(runFailed) }()
	env.Log = log

	if c.Check {
		env.DryRun = true
		events, err := engine.Run(context.Background(), env, c.Paths, func(cmd config.Command) bool { return cmd.Formatter })
		if err != nil {
			return err
		}
		_, wouldChange, skipped, failed := drainRunEvents(func(ev engine.Event) { printFmtEvent(stderr, ev) }, events)
		runFailed = failed != nil
		printFmtCheckReport(stdout, wouldChange, skipped)
		if failed != nil {
			return failed
		}
		if len(wouldChange) > 0 {
			return fmt.Errorf("rtunk: fmt --check found %d file(s) needing reformatting", len(wouldChange))
		}
		return nil
	}

	if c.VerifyStable {
		changed, skipped, err := runStableFormat(context.Background(), env, c.Paths, stderr)
		runFailed = runFailedBy(err)
		printFmtReport(stdout, changed, skipped)
		return err
	}

	changed, skipped, err := runFormatOnce(context.Background(), env, c.Paths, repoRoot, stderr)
	runFailed = runFailedBy(err)
	printFmtReport(stdout, changed, skipped)
	return err
}
