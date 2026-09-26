package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/xunleii/rtunk/pkg/trunk/download"
)

// whereCmd is `rtunk toolbox where {runtime,tools} <id>[@version]`: prints the absolute path of
// the item's install directory, never downloading -- errors if the item isn't already cached.
type whereCmd struct {
	Category string `arg:"" enum:"runtime,tools" help:"Resource category."`
	ID       string `arg:"" help:"Resource id, optionally @version."`
}

func (c *whereCmd) Run(cli *CLI, stdout io.Writer) error {
	cfg, err := resolveConfig(cli.Config, cli.CacheDir, false)
	if err != nil {
		return err
	}
	category := toolboxCategory(c.Category)
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
	dir := download.InstallDir(root, category, id, version)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return fmt.Errorf("rtunk: %s %s@%s not downloaded, run `rtunk toolbox download %s %s` first", category, id, version, c.Category, c.ID)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(stdout, abs)
	return nil
}
