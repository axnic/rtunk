package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"time"

	"github.com/xunleii/rtunk/internal/cli/render"
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
	Paths        []string `arg:"" optional:"" help:"Paths to format (default: changed files, see --from)."`
	NoProgress   bool     `help:"Do not print the per-linter progress lines on stderr."`
	ASCII        bool     `name:"ascii" help:"Use ASCII glyphs in the live view."`
	LiveHeight   int      `help:"Maximum height of the live view in lines (default: half the terminal, minimum 3)." env:"RTUNK_LIVE_HEIGHT"`
	Format       string   `enum:"human,sarif,json" default:"human" help:"Output format: human or json (sarif is only supported by check)."`
	From         string   `help:"Diff base for the default file selection (e.g. origin/main, for CI)."`
	Force        bool     `help:"Also format files with both staged and unstaged changes (skipped with a warning by default)."`
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
	if c.Format == "sarif" {
		return errors.New("--format sarif is only supported by check")
	}
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
	if err := checkDeprecations(cfg, stderr); err != nil {
		return err
	}
	cfg, err = filterLinters(cfg, c.Filter, c.Exclude)
	if err != nil {
		return err
	}
	repoRoot := filepath.Dir(filepath.Dir(configPath))

	files, err := resolvePaths(repoRoot, c.Paths, c.From)
	if err == nil && !c.Check && !c.Force {
		files = skipPartiallyStaged(stderr, repoRoot, files)
		if len(files) == 0 {
			err = errNoFiles
		}
	}
	if errors.Is(err, errNoFiles) {
		_, _ = fmt.Fprintln(stderr, "rtunk: no files to format")
		return nil
	}
	if err != nil {
		return err
	}

	jobs := c.Jobs
	if jobs <= 0 {
		jobs = runtime.NumCPU()
	}
	env := engine.Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: cli.CacheDir, Concurrency: jobs}

	log := startLog(cli, "fmt", repoRoot, configPath, argv, jobs, c.Check, stderr)
	runFailed := true // cleared once the run reaches its normal end; an early error return keeps it
	defer func() { log.End(runFailed) }()
	env.Log = log

	kind := render.Fmt
	if c.Check {
		kind = render.FmtCheck
	}
	started := time.Now()
	r := newRenderer(c.Format, stdout, stderr, kind, progressOpts{c.NoProgress, c.ASCII, c.LiveHeight})
	summary := func(changed, skipped []string, err error) render.Summary {
		return render.Summary{Elapsed: time.Since(started), RunLog: log.Name(), Skipped: skipped, Changed: changed, Unstable: isUnstable(err)}
	}

	if c.Check {
		env.DryRun = true
		events, err := engine.Run(context.Background(), env, files, func(cmd config.Command) bool { return cmd.Formatter })
		if err != nil {
			return err
		}
		_, wouldChange, skipped, failed := drainRunEvents(r.Event, events)
		runFailed = failed != nil
		_ = r.Close(summary(wouldChange, skipped, nil))
		if failed != nil {
			return errors.New("fmt: a linter failed to run")
		}
		if len(wouldChange) > 0 {
			return fmt.Errorf("rtunk: fmt --check found %d file(s) needing reformatting", len(wouldChange))
		}
		return nil
	}

	if c.VerifyStable {
		changed, skipped, err := runStableFormat(context.Background(), env, files, r.Event)
		runFailed = runFailedBy(err)
		_ = r.Close(summary(changed, skipped, err))
		return err
	}

	changed, skipped, err := runFormatOnce(context.Background(), env, files, repoRoot, stderr, r.Event)
	runFailed = runFailedBy(err)
	_ = r.Close(summary(changed, skipped, err))
	return err
}

// skipPartiallyStaged drops, with a warning, the files that have both staged and unstaged
// changes: fmt writes to the working tree only and never touches the index, so formatting one
// would silently mix formatter output into what the user staged.
func skipPartiallyStaged(stderr io.Writer, repoRoot string, files []string) []string {
	partial := partiallyStaged(repoRoot)
	if len(partial) == 0 {
		return files
	}
	kept := files[:0:0]
	for _, f := range files {
		if partial[f] {
			_, _ = fmt.Fprintf(stderr, "rtunk: skipping partially staged file %s (use --force to format it)\n", f)
			continue
		}
		kept = append(kept, f)
	}
	return kept
}
