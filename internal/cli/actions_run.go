package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/xunleii/rtunk/pkg/git"
	"github.com/xunleii/rtunk/pkg/run/actions"
	"github.com/xunleii/rtunk/pkg/run/runlog"
	"github.com/xunleii/rtunk/pkg/trunk/config"
)

// actionsRunCmd is `rtunk actions run <id> [-- args...]` or `rtunk actions run --hook <name> [--
// args...]`. Only one Args-shaped positional field exists (mirroring toolbox_exec.go's own
// passthrough pattern) so Kong never has to arbitrate between a bare ID and a --hook flag sharing
// the same positional slot: in ID mode, Args[0] IS the id; in --hook mode, Args (if any) are
// forwarded as-is.
type actionsRunCmd struct {
	Hook string   `help:"Run every enabled action triggered by this git hook, instead of a single action by id."`
	Args []string `arg:"" optional:"" passthrough:"" help:"<action-id> [-- args...] when --hook is not given; otherwise just the args to forward."`
}

func (c *actionsRunCmd) Run(cli *CLI, stdout io.Writer, stderr Stderr, argv Argv) error {
	args := c.Args
	var id string
	if c.Hook == "" {
		if len(args) == 0 {
			return fmt.Errorf("rtunk: actions run: specify an action id or --hook")
		}
		id = args[0]
		args = args[1:]
	}
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}

	configPath := cli.Config
	if configPath == "" {
		found, err := findConfig()
		if err != nil {
			return err
		}
		configPath = found
	}
	cfg, err := resolveConfig(configPath, cli.CacheDir, false)
	if err != nil {
		return err
	}
	repoRoot, err := git.RepoRoot(filepath.Dir(configPath))
	if err != nil {
		return err
	}

	var matched []config.Action
	if id != "" {
		a, ok := cfg.Actions.Definitions[id]
		if !ok {
			return fmt.Errorf("rtunk: actions run: unknown action %q", id)
		}
		matched = []config.Action{a}
	} else {
		matched = actions.Resolve(cfg, c.Hook)
	}

	var stdin io.Reader
	for _, a := range matched {
		if strings.Contains(a.Run, "${hook_stdin_path}") {
			stdin = os.Stdin
			break
		}
	}

	// One log for the whole invocation: a --hook run may run several actions, each its own
	// invocation inside the same file. Keyed like check/fmt (config's grandparent directory), not by
	// repoRoot above (the git root), so `rtunk logs` finds all three commands' runs together.
	// No log when stdin is a terminal: Tee would turn the action's stdout/stderr into pipes, which
	// breaks colors and TTY-only prompts for an action a person is running by hand. A nil log is a
	// valid no-op writer. Nor when nothing matched (a stale hook): an empty log would only evict
	// real check/fmt runs from the retention.
	var log *runlog.Writer
	if len(matched) > 0 && !stdinIsTerminal() {
		log = startLog(cli, "actions-run", filepath.Dir(filepath.Dir(configPath)), configPath, argv, 1, false, stderr)
	}
	runFailed := true // cleared once every action has run; an error return keeps it
	defer func() { log.End(runFailed) }()

	for _, a := range matched {
		opts := actions.RunOptions{CacheDir: cli.CacheDir, RepoRoot: repoRoot, Hook: c.Hook, Args: args, Stdin: stdin, Log: log}
		result, runErr := actions.Run(context.Background(), cfg, a, opts, stdout, stderr)
		if result.Skipped {
			_, _ = fmt.Fprintf(stderr, "skipped %s: non-interactive context\n", a.ID)
			continue
		}
		if runErr != nil {
			return runErr
		}
	}
	runFailed = false
	return nil
}
