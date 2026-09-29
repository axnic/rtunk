package cli

// configCmd is `rtunk config`: currently just print, the fully resolved configuration.
type configCmd struct {
	Print configPrintCmd `cmd:"" help:"Print the fully resolved configuration."`
}
