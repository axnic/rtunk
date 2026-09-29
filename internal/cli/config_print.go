package cli

import "io"

type configPrintCmd struct {
	Output string `help:"Output format." enum:"yaml,json" default:"yaml"`
}

func (c *configPrintCmd) Run(cli *CLI, stdout io.Writer) error {
	cfg, err := resolveConfig(cli.Config, cli.CacheDir, false)
	if err != nil {
		return err
	}
	return printValue(stdout, cfg, c.Output)
}
