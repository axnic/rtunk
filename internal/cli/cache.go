package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

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

// cachePruneCmd removes every installed item (installs/<category>/<id>/<version> and its shims)
// whose mtime is older than --older-than; download.Touch bumps that mtime on every use. There is
// deliberately no project registry, so "unreferenced by every repo" is not detectable.
type cachePruneCmd struct {
	OlderThan string `required:"" help:"Remove entries unused for longer than this duration (e.g. 30d, 12h)."`
}

func (c *cachePruneCmd) Run(cli *CLI, _ io.Writer) error {
	age, err := parseAge(c.OlderThan)
	if err != nil {
		return err
	}
	root, err := download.Root(cli.CacheDir)
	if err != nil {
		return err
	}
	return pruneOlderThan(root, time.Now().Add(-age))
}

// parseAge is time.ParseDuration plus a "d" (days) suffix.
func parseAge(s string) (time.Duration, error) {
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("invalid --older-than %q", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("invalid --older-than %q", s)
	}
	return d, nil
}

// pruneOlderThan removes every installs/<category>/<id>/<version> and shims/<category>/<id>/
// <version> directory last used before cutoff.
func pruneOlderThan(root string, cutoff time.Time) error {
	for _, pattern := range []string{
		filepath.Join(root, "installs", "*", "*", "*"),
		filepath.Join(root, "shims", "*", "*", "*"),
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return err
		}
		for _, match := range matches {
			if info, err := os.Stat(match); err != nil || !info.ModTime().Before(cutoff) {
				continue
			}
			if err := os.RemoveAll(match); err != nil {
				return err
			}
		}
	}
	return nil
}
