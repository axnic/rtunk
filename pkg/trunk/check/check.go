// Package check implements ROADMAP.md's v0.3 milestone: running enabled linters against source
// files and reporting findings, read-only. The actual job-queue engine, file matching, and
// RunFrom/SandboxType resolution live in pkg/trunk/engine (shared with a future pkg/trunk/fmt) --
// this package is the check-specific policy on top of it: run every non-formatter command. See
// docs/superpowers/specs/2026-09-12-check-engine-refactor-design.md for the full design.
package check

import (
	"context"

	"github.com/xunleii/rtunk/pkg/trunk/config"
	"github.com/xunleii/rtunk/pkg/trunk/engine"
)

// Run runs every enabled linter's non-formatter command against the files matched under paths,
// streaming one engine.Event per linter that had something to report. See engine.Run for the
// full contract (cancellation, concurrency, event semantics).
func Run(ctx context.Context, env engine.Env, paths []string) (<-chan engine.Event, error) {
	return engine.Run(ctx, env, paths, func(c config.Command) bool { return !c.Formatter })
}
