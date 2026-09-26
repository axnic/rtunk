// Package runlog persists one JSONL log per rtunk run: every command a run launched (argv, cwd,
// PATH prefix), the environment it inherited (secret-looking values masked), each raw output
// stream, the parser step, and the findings that resulted -- enough to replay by hand what
// happened and to understand how raw tool output became findings. See
// docs/superpowers/specs/2026-09-26-run-logs-design.md.
package runlog

import "github.com/xunleii/rtunk/pkg/trunk/output"

// Event kinds -- the value of Event.T.
const (
	KindRunStart   = "run_start"
	KindInvocation = "invocation"
	KindOutput     = "output"
	KindExit       = "exit"
	KindParser     = "parser"
	KindFindings   = "findings"
	KindLinterEnd  = "linter_end"
	KindRunEnd     = "run_end"
)

// Event is one JSONL line. T selects the kind; only the fields that kind documents are set, the
// rest are omitted. One flat struct is shared by the writer and the reader so the two cannot
// drift apart. ID ties together every line of one command invocation (allocated by
// Writer.NextID, starting at 1, so an omitted id means "none").
type Event struct {
	T  string `json:"t"`
	TS string `json:"ts,omitempty"`
	ID int    `json:"id,omitempty"`

	// run_start (Argv and Cwd also on invocation; Argv also on parser). The process environment is
	// deliberately never recorded: a log is meant to be shared.
	Rtunk       string   `json:"rtunk,omitempty"`
	Argv        []string `json:"argv,omitempty"`
	Cwd         string   `json:"cwd,omitempty"`
	RepoRoot    string   `json:"repo_root,omitempty"`
	Config      string   `json:"config,omitempty"`
	Concurrency int      `json:"concurrency,omitempty"`
	DryRun      bool     `json:"dry_run,omitempty"`

	// invocation (Template also on parser; Linter also on findings and linter_end)
	Linter       string            `json:"linter,omitempty"`
	ToolVersions map[string]string `json:"tool_versions,omitempty"`
	Template     string            `json:"template,omitempty"`
	Files        []string          `json:"files,omitempty"`
	Sandbox      string            `json:"sandbox,omitempty"`
	PathPrefix   string            `json:"path_prefix,omitempty"`

	// output (Data and Truncated also on parser)
	Stream    string `json:"stream,omitempty"`
	Data      string `json:"data,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`

	// exit (Code and Ms also on parser)
	Code       *int   `json:"code,omitempty"`
	Ms         int64  `json:"ms,omitempty"`
	ParsedFrom string `json:"parsed_from,omitempty"`
	StdinFrom  string `json:"stdin_from,omitempty"`

	// findings
	Findings []output.Finding `json:"findings,omitempty"`

	// linter_end (Err also on parser)
	Phase   string   `json:"phase,omitempty"`
	Note    string   `json:"note,omitempty"`
	Err     string   `json:"err,omitempty"`
	Changed []string `json:"changed,omitempty"`

	// run_end
	Status string `json:"status,omitempty"`
}
