package cli

type actionsEnableCmd struct {
	ID []string `arg:"" help:"Action id(s) to enable."`
}

func (c *actionsEnableCmd) Run(cli *CLI) error {
	return editActionsEnabled(cli, c.ID, true)
}
