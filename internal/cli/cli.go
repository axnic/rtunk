// Package cli implements rtunk's command-line interface on top of
// github.com/alecthomas/kong. See ROADMAP.md for the staged command set; this currently
// implements v0.1 ("read and query"), v0.2 ("download"), and v0.3 ("check", read-only).
package cli

import (
	"io"

	"github.com/alecthomas/kong"
)

// Stderr is a defined type (not an alias) with the same method set as io.Writer, so that Kong's
// by-type dependency injection can bind it separately from stdout. Kong resolves Run() parameters
// by static type only: a command with two plain io.Writer parameters would have both resolve to
// whichever single io.Writer binding is registered (see execCmd.Run in exec.go, which needs both
// streams distinct so a shim's real stderr isn't merged into rtunk's stdout).
type Stderr io.Writer

// Argv is rtunk's own argument list (program name excluded), bound into every command's Run so
// the commands that write a run log can record how they were invoked.
type Argv []string

// Version is rtunk's own version string, set by cmd/rtunk/main.go before calling Run (see that
// file's own fallback chain: ldflags -X main.version=... -> debug.ReadBuildInfo() -> "dev").
// pkg/trunk/upgrade's Available treats "dev" as "cannot determine current version" and never
// reports an upgrade available against it.
var Version = "dev"

// CLI is Kong's grammar root: the two flags every subcommand needs to locate and resolve a
// trunk.yaml, plus the config subcommand tree.
type CLI struct {
	Config      string           `help:"Path to trunk.yaml (default: nearest .rtunk/rtunk.yaml or .trunk/trunk.yaml)."`
	CacheDir    string           `help:"Plugin cache directory (default: OS cache dir)." env:"RTUNK_CACHE_DIR"`
	VersionFlag kong.VersionFlag `name:"version" help:"Print rtunk's own version and exit."`
	// CI is accepted for trunk compatibility and has no effect: rtunk has no daemon, no
	// interactive prompts, and always produces deterministic output, so there is no
	// "CI mode" to switch into -- it is already the only mode.
	CI bool `help:"Accepted for trunk compatibility; rtunk is always CI-safe, this has no effect."`
	// Verbose is accepted for trunk compatibility and has no effect: rtunk already
	// unconditionally streams per-file running/done progress to stderr for every check/fmt run
	// (see internal/cli/check.go's printEvent) -- there is no quieter default this flag would
	// make louder.
	Verbose bool `short:"v" help:"Accepted for trunk compatibility; rtunk already prints this detail, this has no effect."`

	ConfigCmd   configCmd   `cmd:"" name:"config" help:"Query the resolved trunk configuration."`
	DownloadCmd downloadCmd `cmd:"" name:"download" help:"Download enabled tools/runtimes into the local cache."`
	WhereCmd    whereCmd    `cmd:"" name:"where" help:"Print a cached item's shim path."`
	ExecCmd     execCmd     `cmd:"" name:"exec" help:"Run a tool/runtime, downloading it first if missing."`
	XCmd        execCmd     `cmd:"" name:"x" hidden:"" help:"Alias for exec."`
	CacheCmd    cacheCmd    `cmd:"" name:"cache" help:"Manage the rtunk downloads cache."`
	CheckCmd    checkCmd    `cmd:"" name:"check" help:"Run enabled checks against source files (read-only)."`
	FmtCmd      fmtCmd      `cmd:"" name:"fmt" help:"Run configured formatters against source files."`
	ActionsCmd  actionsCmd  `cmd:"" name:"actions" help:"Manage and run trunk actions."`
	GitHooksCmd gitHooksCmd `cmd:"" name:"git-hooks" help:"Manage git hooks that trigger actions."`
	InitCmd     initCmd     `cmd:"" name:"init" help:"Initialize rtunk in this repository."`
	DeinitCmd   deinitCmd   `cmd:"" name:"deinit" help:"Remove rtunk's configuration and installed artifacts."`
	UpgradeCmd  upgradeCmd  `cmd:"" name:"upgrade" help:"Check for and install a newer rtunk release."`
	// RunCmd is `rtunk run <id>`: real trunk's own top-level shortcut for `trunk actions run
	// <id>` (see trunk --help's own subcommand list). Registered as the same actionsRunCmd type
	// used by CLI.ActionsCmd.Run -- both paths share one Run method, so there is nothing to keep
	// in sync between them.
	RunCmd actionsRunCmd `cmd:"" name:"run" help:"Run a specified action (shortcut for 'actions run')."`
	// RenovateCmd is `rtunk renovate`: generates Renovate annotations for trunk.yaml's version
	// pins (see docs/superpowers/specs/2026-09-17-renovate-annotations-design.md).
	RenovateCmd renovateCmd `cmd:"" name:"renovate" help:"Generate Renovate annotations for trunk.yaml's version pins."`
	// LogsCmd is `rtunk logs`: reads back the run logs check, fmt and actions run write (see
	// docs/superpowers/specs/2026-09-26-run-logs-design.md).
	LogsCmd logsCmd `cmd:"" name:"logs" help:"List, show and clean the logs of past runs."`
}

// exitPanic is a sentinel panic type used to signal that Kong called os.Exit without actually
// exiting the process (used for testing).
type exitPanic int

// Run parses args against CLI's grammar and executes the selected command's Run(), writing to
// stdout/stderr. It does not call os.Exit itself -- cmd/rtunk/main.go owns the process exit code.
// Both --help and --version route through Kong's BeforeReset(app *Kong, ...) -> app.Exit(0), which
// this function intercepts below (parser.Exit's panic/recover override) instead of letting either
// exit the process directly.
func Run(args []string, stdout, stderr io.Writer) error {
	var cli CLI
	parser, err := kong.New(&cli,
		kong.Name("rtunk"),
		kong.Description("Query and act on trunk-style repo configuration."),
		kong.Writers(stdout, stderr),
		kong.Vars{"version": Version},
		kong.BindFor[io.Writer](stdout),
		kong.BindFor[Stderr](stderr),
		kong.Bind(Argv(args)),
	)
	if err != nil {
		return err
	}

	// Replace Kong's default Exit handler with one that uses panic instead of os.Exit, so that
	// tests can verify version output without actually exiting the process.
	parser.Exit = func(code int) {
		panic(exitPanic(code))
	}

	defer func() {
		if r := recover(); r != nil {
			if code, ok := r.(exitPanic); ok && int(code) == 0 {
				// Recovered from a successful exit (e.g., --version, --help). These are normal
				// exits and we return without error.
				return
			}
			// Re-panic any other panic value.
			panic(r)
		}
	}()

	kctx, err := parser.Parse(args)
	if err != nil {
		return err
	}
	return kctx.Run()
}
