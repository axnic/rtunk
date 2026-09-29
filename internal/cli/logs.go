package cli

// logsCmd is `rtunk logs`: reads back, and cleans, the per-run JSONL logs that check, fmt and
// actions run write (pkg/run/runlog). Every subcommand but `clean --all` is scoped to the
// current repository.
type logsCmd struct {
	List  logsListCmd  `cmd:"" default:"withargs" help:"List this repository's recent runs."`
	Show  logsShowCmd  `cmd:"" help:"Show one run's log (default: the latest)."`
	Clean logsCleanCmd `cmd:"" help:"Delete this repository's run logs."`
}
