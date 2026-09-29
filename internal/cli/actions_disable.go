package cli

type actionsDisableCmd struct {
	ID []string `arg:"" help:"Action id(s) to disable."`
}

func (c *actionsDisableCmd) Run(cli *CLI) error {
	return editActionsEnabled(cli, c.ID, false)
}
