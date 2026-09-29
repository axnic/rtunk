// Package runlog persists one JSONL log per rtunk run: every command a run launched (argv, cwd,
// PATH prefix), the environment it inherited (secret-looking values masked), each raw output
// stream, the parser step, and the findings that resulted -- enough to replay by hand what
// happened and to understand how raw tool output became findings. See
// docs/superpowers/specs/2026-09-26-run-logs-design.md.
package runlog
