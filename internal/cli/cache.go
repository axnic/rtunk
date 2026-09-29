package cli

// cacheCmd is `rtunk cache`: clean wipes the entire cache root (downloads, plugin sources, and
// logs alike); prune removes only what no currently-existing, currently-configured repository
// still needs (see cachePruneCmd).
type cacheCmd struct {
	Clean cacheCleanCmd `cmd:"" help:"Remove the entire rtunk cache: downloads, plugin sources, and logs."`
	Prune cachePruneCmd `cmd:"" help:"Remove cache entries no repository currently needs."`
}
