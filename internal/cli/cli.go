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

// CLI is Kong's grammar root: the two flags every subcommand needs to locate and resolve a
// trunk.yaml, plus the config subcommand tree.
type CLI struct {
	Config   string `help:"Path to trunk.yaml (default: nearest .trunk/trunk.yaml)."`
	CacheDir string `help:"Plugin cache directory (default: OS cache dir)." env:"RTUNK_CACHE_DIR"`

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
}

// Run parses args against CLI's grammar and executes the selected command's Run(), writing to
// stdout/stderr. It does not call os.Exit itself -- cmd/rtunk/main.go owns the process exit code
// -- except that Kong's own --help handling exits the process directly (kong.Exit's default).
func Run(args []string, stdout, stderr io.Writer) error {
	var cli CLI
	parser, err := kong.New(&cli,
		kong.Name("rtunk"),
		kong.Description("Query and act on trunk-style repo configuration."),
		kong.Writers(stdout, stderr),
		kong.BindFor[io.Writer](stdout),
		kong.BindFor[Stderr](stderr),
	)
	if err != nil {
		return err
	}

	kctx, err := parser.Parse(args)
	if err != nil {
		return err
	}
	return kctx.Run()
}
