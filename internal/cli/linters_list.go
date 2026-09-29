package cli

import "io"

// lintersListCmd is `rtunk linters list`: enabled linters (with their pinned version), the ones
// available for this repo (matching at least one file), and with --all the rest.
type lintersListCmd struct {
	All    bool   `help:"Also list the linters that match no file in this repository."`
	Format string `enum:"human,json" default:"human" help:"Output format: human or json."`
}

func (c *lintersListCmd) Run(cli *CLI, stdout io.Writer) error {
	// all=true (config.ResolveAll): the full catalog, not only the enabled+used subset.
	cfg, err := resolveConfig(cli.Config, cli.CacheDir, true)
	if err != nil {
		return err
	}
	repoRoot, err := logsRepoRoot(cli)
	if err != nil {
		return err
	}
	files, err := repoFiles(repoRoot)
	if err != nil {
		return err
	}
	return writeListing(stdout, buildLintersList(cfg, repoRoot, files), c.Format, "linter", "linters enable", c.All)
}
