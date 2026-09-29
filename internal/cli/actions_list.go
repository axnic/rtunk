package cli

import "io"

type actionsListCmd struct {
	Format string `enum:"human,json" default:"human" help:"Output format: human or json."`
}

func (c *actionsListCmd) Run(cli *CLI, stdout io.Writer) error {
	cfg, err := resolveConfig(cli.Config, cli.CacheDir, true)
	if err != nil {
		return err
	}
	return writeListing(stdout, buildActionsList(cfg), c.Format, "action", "actions enable", false)
}
