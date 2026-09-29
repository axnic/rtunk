package cli

import "io"

type pluginsPrintCmd struct {
	Output string `help:"Output format." enum:"yaml,json" default:"yaml"`
}

func (c *pluginsPrintCmd) Run(cli *CLI, stdout io.Writer) error {
	cfg, err := resolveConfig(cli.Config, cli.CacheDir, true)
	if err != nil {
		return err
	}
	return printValue(stdout, cfg, c.Output)
}
