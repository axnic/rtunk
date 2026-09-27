package cli

import (
	"io"
	"os"
	"path/filepath"

	"github.com/xunleii/rtunk/pkg/trunk/download"
)

// cacheCmd is `rtunk cache`: clean wipes the entire cache root (downloads, plugin sources, and
// logs alike); prune removes only what no currently-existing, currently-configured repository
// still needs (see Task 5, cachePruneCmd).
type cacheCmd struct {
	Clean cacheCleanCmd `cmd:"" help:"Remove the entire rtunk cache: downloads, plugin sources, and logs."`
	Prune cachePruneCmd `cmd:"" help:"Remove cache entries no repository currently needs."`
}

type cacheCleanCmd struct{}

func (c *cacheCleanCmd) Run(cli *CLI, _ io.Writer) error {
	downloadsRoot, err := download.Root(cli.CacheDir)
	if err != nil {
		return err
	}
	return os.RemoveAll(filepath.Dir(downloadsRoot))
}

type cachePruneCmd struct{}

func (c *cachePruneCmd) Run(cli *CLI, _ io.Writer) error {
	return download.Prune(cli.CacheDir)
}
