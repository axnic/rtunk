package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/xunleii/rtunk/pkg/trunk/download"
)

// whereCmd is `rtunk where`: prints a shim's filesystem path, never downloading -- errors if the
// item isn't already cached, per the spec's CLI surface.
type whereCmd struct {
	Category string `arg:"" enum:"tools,runtimes,lint,actions,plugins" help:"Resource category."`
	ID       string `arg:"" help:"Resource id, optionally @version."`
}

func (c *whereCmd) Run(cli *CLI, stdout io.Writer) error {
	cfg, err := resolveConfig(cli.Config, cli.CacheDir, false)
	if err != nil {
		return err
	}
	id, version, pinned := cutVersion(c.ID)
	if !pinned {
		version, err = resolvedVersionFor(cfg, c.Category, id)
		if err != nil {
			return err
		}
	}

	root, err := download.Root(cli.CacheDir)
	if err != nil {
		return err
	}
	shimName := id
	shimPath := download.ShimPath(root, c.Category, id, version, shimName)
	if _, err := os.Stat(shimPath); err != nil {
		return fmt.Errorf("rtunk: %s %s@%s not downloaded, run `rtunk download %s %s` first", c.Category, id, version, c.Category, c.ID)
	}
	_, _ = fmt.Fprintln(stdout, shimPath)
	return nil
}
