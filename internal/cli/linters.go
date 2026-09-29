package cli

// lintersCmd is `rtunk linters`, symmetric with `rtunk actions {list,enable,disable}`.
type lintersCmd struct {
	List    lintersListCmd    `cmd:"" default:"withargs" help:"List all linters available for the current configuration."`
	Enable  lintersEnableCmd  `cmd:"" help:"Enable one or more linters."`
	Disable lintersDisableCmd `cmd:"" help:"Disable one or more linters."`
}
