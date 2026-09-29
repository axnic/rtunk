package cli

import (
	"io"
	"os"
	"path/filepath"

	"github.com/xunleii/rtunk/pkg/cache/download"
)

// cacheCmd is `rtunk cache`: clean wipes the entire cache root (downloads, plugin sources, and
// logs alike); prune removes only what no currently-existing, currently-configured repository
// still needs (see Task 5, cachePruneCmd).
type cacheCmd struct {
	Clean cacheCleanCmd `cmd:"" help:"Remove the entire rtunk cache: downloads, plugin sources, and logs."`
	Prune cachePruneCmd `cmd:"" help:"Remove cache entries no repository currently needs."`
}

type cacheCleanCmd struct{}

// cacheSubtrees is the complete set of subtrees any rtunk code ever creates under the shared
// cache root (see download.Root, download.registryDir, and runlog's logsRoot): downloads,
// plugins, logs, registry. cache clean must remove only these, not the whole --cache-dir, since
// a user may point --cache-dir at a directory shared with other tools.
var cacheSubtrees = []string{"downloads", "plugins", "logs", "registry"}

func (c *cacheCleanCmd) Run(cli *CLI, _ io.Writer) error {
	downloadsRoot, err := download.Root(cli.CacheDir)
	if err != nil {
		return err
	}
	sharedRoot := filepath.Dir(downloadsRoot)
	for _, subtree := range cacheSubtrees {
		if err := os.RemoveAll(filepath.Join(sharedRoot, subtree)); err != nil {
			return err
		}
	}
	return nil
}

type cachePruneCmd struct{}

func (c *cachePruneCmd) Run(cli *CLI, _ io.Writer) error {
	return download.Prune(cli.CacheDir)
}
