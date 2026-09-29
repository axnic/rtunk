package cli

// actionsCmd is `rtunk actions`: ROADMAP.md v0.5.
type actionsCmd struct {
	List    actionsListCmd    `cmd:"" default:"withargs" help:"List actions available for the current configuration."`
	Enable  actionsEnableCmd  `cmd:"" help:"Enable one or more actions."`
	Disable actionsDisableCmd `cmd:"" help:"Disable one or more actions."`
	Run     actionsRunCmd     `cmd:"" help:"Run an action on demand, or every action a git hook triggers."`
	History actionsHistoryCmd `cmd:"" help:"Show recent action runs."`
}
