package cli

// lintersEnableCmd is `rtunk linters enable <id>[@version]...`.
type lintersEnableCmd struct {
	ID []string `arg:"" help:"Linter id(s) to enable, optionally @version."`
}

func (c *lintersEnableCmd) Run(cli *CLI) error {
	return editEnabled(cli, "lint", func(existing []string) []string {
		return addEnabled(existing, c.ID)
	})
}
