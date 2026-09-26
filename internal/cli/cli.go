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
	// streams one progress line per finished linter to stderr for every check/fmt run (see
	// internal/cli/render; --no-progress silences it) -- there is no louder mode this flag would
	// switch on.
	Verbose bool `short:"v" help:"Accepted for trunk compatibility; rtunk already prints this detail, this has no effect."`

	LintersCmd  lintersCmd  `cmd:"" name:"linters" help:"List, enable and disable linters." group:"commands"`
	PluginsCmd  pluginsCmd  `cmd:"" name:"plugins" help:"Inspect plugins." group:"commands"`
	ConfigCmd   configCmd   `cmd:"" name:"config" help:"Query the resolved trunk configuration." group:"extended"`
	CacheCmd    cacheCmd    `cmd:"" name:"cache" help:"Manage the rtunk downloads cache." group:"extended"`
	CheckCmd    checkCmd    `cmd:"" name:"check" help:"Run enabled checks against source files (read-only)." group:"commands"`
	FmtCmd      fmtCmd      `cmd:"" name:"fmt" help:"Run configured formatters against source files." group:"commands"`
	ActionsCmd  actionsCmd  `cmd:"" name:"actions" help:"Manage and run trunk actions." group:"commands"`
	GitHooksCmd gitHooksCmd `cmd:"" name:"git-hooks" help:"Manage git hooks that trigger actions." group:"commands"`
	InitCmd     initCmd     `cmd:"" name:"init" help:"Initialize rtunk in this repository." group:"commands"`
	DeinitCmd   deinitCmd   `cmd:"" name:"deinit" help:"Remove rtunk's configuration and installed artifacts." group:"commands"`
	UpgradeCmd  upgradeCmd  `cmd:"" name:"upgrade" help:"Check for and install a newer rtunk release." group:"commands"`
	// RunCmd is `rtunk run <id>`: real trunk's own top-level shortcut for `trunk actions run
	// <id>` (see trunk --help's own subcommand list). Registered as the same actionsRunCmd type
	// used by CLI.ActionsCmd.Run -- both paths share one Run method, so there is nothing to keep
	// in sync between them.
	RunCmd actionsRunCmd `cmd:"" name:"run" help:"Run a specified action (shortcut for 'actions run')." group:"commands"`
	// ToolboxCmd is `rtunk toolbox`: internal commands (download, exec, where, renovate), hidden from
	// the default help.
	ToolboxCmd toolboxCmd `cmd:"" name:"toolbox" hidden:"" help:"Internal commands: download, exec, where, renovate." group:"commands"`
	// LogsCmd is `rtunk logs`: reads back the run logs check, fmt and actions run write (see
	// docs/superpowers/specs/2026-09-26-run-logs-design.md).
	LogsCmd logsCmd `cmd:"" name:"logs" help:"List, show and clean the logs of past runs." group:"commands"`
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
		kong.ConfigureHelp(kong.HelpOptions{Compact: true, FlagsLast: true, NoExpandSubcommands: true}),
		kong.ExplicitGroups([]kong.Group{
			{Key: "commands", Title: "commands"},
			{Key: "extended", Title: "extended commands"},
		}),
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

	// `rtunk help [--all]` is `--help`; --all also lists the hidden commands (toolbox, ...).
	// Bare `rtunk` is the same as `--help` -- like trunk, showing usage beats Kong's default
	// "expected one of ..." parse error.
	if len(args) == 0 || args[0] == "help" {
		if len(args) == 2 && args[1] == "--all" {
			showHidden(parser.Model.Node)
		}
		args = []string{"--help"}
	}

	kctx, err := parser.Parse(args)
	if err != nil {
		return err
	}
	return kctx.Run()
}

// showHidden un-hides n and every command below it, for `rtunk help --all`.
func showHidden(n *kong.Node) {
	n.Hidden = false
	for _, c := range n.Children {
		showHidden(c)
	}
}
