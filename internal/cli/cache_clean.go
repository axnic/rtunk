package cli

import (
	"io"
	"os"
	"path/filepath"

	"github.com/xunleii/rtunk/pkg/cache/download"
)

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
