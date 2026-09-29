package cli

// gitHooksCmd is `rtunk git-hooks`: ROADMAP.md v0.5. "sync" is real trunk's own subcommand name
// for the equivalent operation ("sync git hooks with trunk.yaml"); it is idempotent (re-running it
// just rewrites the same hook files). "unsync" removes what sync installed.
type gitHooksCmd struct {
	Sync   gitHooksInstallCmd   `cmd:"" help:"Install git hooks for enabled actions."`
	Unsync gitHooksUninstallCmd `cmd:"" help:"Remove rtunk-installed git hooks."`
}
