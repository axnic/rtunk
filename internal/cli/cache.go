package cli

import (
	"io"
	"os"
	"path/filepath"

	"github.com/xunleii/rtunk/pkg/trunk/download"
)

// cacheCmd is `rtunk cache`: clean wipes the whole downloads/ subtree, prune removes only what's
// no longer enabled+used.
type cacheCmd struct {
	Clean cacheCleanCmd `cmd:"" help:"Remove all files from the rtunk downloads cache."`
	Prune cachePruneCmd `cmd:"" help:"Remove cached files no longer referenced by the enabled config."`
}

type cacheCleanCmd struct{}

func (c *cacheCleanCmd) Run(cli *CLI, _ io.Writer) error {
	root, err := download.Root(cli.CacheDir)
	if err != nil {
		return err
	}
	return os.RemoveAll(root)
}

type cachePruneCmd struct{}

func (c *cachePruneCmd) Run(cli *CLI, _ io.Writer) error {
	cfg, err := resolveConfig(cli.Config, cli.CacheDir, false)
	if err != nil {
		return err
	}
	root, err := download.Root(cli.CacheDir)
	if err != nil {
		return err
	}

	keep := map[string]bool{}
	for id := range cfg.Tools {
		keep[download.InstallsBase(root, "tools", id)] = true
		keep[filepath.Join(root, "shims", "tools", id)] = true
	}
	for id := range cfg.Runtimes.Definitions {
		keep[download.InstallsBase(root, "runtimes", id)] = true
		keep[filepath.Join(root, "shims", "runtimes", id)] = true
	}
	return pruneUnused(root, keep)
}

// pruneUnused removes every installs/<category>/<id> and shims/<category>/<id> directory whose
// path isn't one of keepPrefixes -- the enabled+used set resolveConfig's filterEnabled already
// computed. Callers must key keepPrefixes with both the download.InstallsBase path and its
// shims/<category>/<id> counterpart for each id to keep, since a surviving install's shim
// would otherwise never match either glob's keep-check.
func pruneUnused(root string, keepPrefixes map[string]bool) error {
	for _, pattern := range []string{
		filepath.Join(root, "installs", "*", "*"),
		filepath.Join(root, "shims", "*", "*"),
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return err
		}
		for _, match := range matches {
			if keepPrefixes[match] {
				continue
			}
			if err := os.RemoveAll(match); err != nil {
				return err
			}
		}
	}
	return nil
}
