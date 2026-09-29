package cli

// checkCmd is `rtunk check`: ROADMAP.md v0.3, running enabled linters read-only. Bare `rtunk check
// [paths...]` is the default subcommand; listing and enabling linters lives in `rtunk linters`.
type checkCmd struct {
	Run checkRunCmd `cmd:"" default:"withargs" help:"Run enabled checks."`
}
