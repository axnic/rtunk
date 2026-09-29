package cli

// pluginsCmd is `rtunk plugins`: print dumps the full merged plugin catalog (a registry dump, can
// be very large) instead of only what is enabled and used. Replaces `config print --all`.
type pluginsCmd struct {
	Print pluginsPrintCmd `cmd:"" help:"Print all configuration available across all plugins, resolved."`
}
