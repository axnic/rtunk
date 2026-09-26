package cli

import (
	"io"
	"path/filepath"

	"github.com/xunleii/rtunk/pkg/trunk/actions"
	"github.com/xunleii/rtunk/pkg/trunk/runlog"
)

// stdinIsTerminal is a variable so tests can pin it: `go test` may hand the test binary the
// caller's own terminal as stdin, which would silently turn the actions-run log off.
var stdinIsTerminal = actions.IsInteractive

// startLog opens the run log for one command invocation (returning nil, a valid no-op Writer, if
// the log cannot be opened -- Start prints the one warning to stderr). The caller ends it with
// log.End once it knows whether the run itself failed.
func startLog(cli *CLI, cmd, repoRoot, configPath string, argv Argv, concurrency int, dryRun bool, stderr io.Writer) *runlog.Writer {
	// run_start.repo_root must be usable from any cwd, so never record a relative path.
	if abs, err := filepath.Abs(repoRoot); err == nil {
		repoRoot = abs
	}
	return runlog.Start(runlog.StartOpts{
		CacheDir: cli.CacheDir, RepoRoot: repoRoot, Cmd: cmd, Version: Version,
		Argv: append([]string{"rtunk"}, argv...), Config: configPath,
		Concurrency: concurrency, DryRun: dryRun, Warn: stderr,
	})
}
