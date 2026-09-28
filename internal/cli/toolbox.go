package cli

// toolboxCmd is `rtunk toolbox`: the internal commands, callable but absent from the default help.
type toolboxCmd struct {
	Download downloadCmd `cmd:"" help:"Download one runtime or tool into the local cache."`
	Exec     execCmd     `cmd:"" aliases:"x" help:"Run a command from a runtime or tool, downloading it first if missing."`
	Where    whereCmd    `cmd:"" help:"Print a cached item's install directory."`
}
