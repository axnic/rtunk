package cli

// lintersDisableCmd is `rtunk linters disable <id>...`.
type lintersDisableCmd struct {
	ID []string `arg:"" help:"Linter id(s) to disable."`
}

func (c *lintersDisableCmd) Run(cli *CLI) error {
	return editEnabled(cli, "lint", func(existing []string) []string {
		return removeEnabled(existing, c.ID)
	})
}
