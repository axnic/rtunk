package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/xunleii/rtunk/pkg/trunk/download"
)

// execCmd is `rtunk toolbox exec|x {runtime,tools} <id>[@version] -- <cmd> [<args>...]`: downloads
// the item first if it isn't already cached (via the same Download engine as `toolbox download`),
// then runs <cmd> from it. <cmd> is the item's own shim when it equals <id>, otherwise an
// executable found inside the install dir (e.g. `python` from a python runtime). Without
// --interactive stdin is not bound; with it, stdin/stdout are the terminal's (a python or node
// shell).
type execCmd struct {
	Category    string   `arg:"" enum:"runtime,tools" help:"Resource category."`
	ID          string   `arg:"" help:"Resource id, optionally @version."`
	Interactive bool     `help:"Bind stdin and stdout to the terminal."`
	Args        []string `arg:"" passthrough:"" help:"Command to run, then its arguments."`
}

// Run's stderr param is typed Stderr (not io.Writer) so Kong's by-type DI binds it to the
// separate Stderr binding registered in cli.go, rather than collapsing onto the stdout binding
// both params would otherwise share.
func (c *execCmd) Run(cli *CLI, stdout io.Writer, stderr Stderr) error {
	// Kong's passthrough positional keeps a literal "--" separator as Args[0] when the caller wrote
	// one (it only pops "--" for a non-passthrough next positional) -- strip it so <cmd> is the
	// caller's command, not the separator that introduced it.
	args := c.Args
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	if len(args) == 0 {
		return fmt.Errorf("rtunk: toolbox exec needs a command after `--`")
	}

	category := toolboxCategory(c.Category)
	cfg, err := resolveConfig(cli.Config, cli.CacheDir, false)
	if err != nil {
		return err
	}
	id, version, pinned := cutVersion(c.ID)
	if !pinned {
		version, err = resolvedVersionFor(cfg, category, id)
		if err != nil {
			return err
		}
	}

	root, err := download.Root(cli.CacheDir)
	if err != nil {
		return err
	}
	shimPath := download.ShimPath(root, category, id, version, id)
	if _, statErr := os.Stat(shimPath); statErr != nil {
		repoRoot, err := logsRepoRoot(cli)
		if err != nil {
			return err
		}
		events, err := download.Download(cfg, cli.CacheDir, repoRoot, download.Ref{Category: category, ID: id, Version: version})
		if err != nil {
			return err
		}
		for ev := range events {
			if ev.Phase == download.Failed {
				return ev.Err
			}
		}
	}

	download.Touch(root, category, id, version)
	target := shimPath
	if args[0] != id {
		// ponytail: an executable found directly in the install dir runs without the runtime's
		// PATH/env wiring that the shim carries; thread it through if a tool needs it.
		if target, err = download.FindShimTarget(download.InstallDir(root, category, id, version), args[0]); err != nil {
			return err
		}
	}

	cmd := exec.Command(target, args[1:]...)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if c.Interactive {
		cmd.Stdin, cmd.Stdout = os.Stdin, os.Stdout
	}
	return cmd.Run()
}
