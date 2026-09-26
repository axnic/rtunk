// Package render turns the engine's event stream into terminal output in one of three formats
// (human, json, sarif). A Renderer holds
// presentation state only (grouping, counting); it makes no decision about what to run or what
// the exit code is. See docs/superpowers/specs/2026-09-26-v0.9.1-event-stream-plain-renderer-design.md.
package render

import (
	"io"
	"time"

	"github.com/xunleii/rtunk/pkg/trunk/engine"
)

// Kind is the command whose run a renderer reports.
type Kind int

// The commands a renderer knows how to report.
const (
	Check    Kind = iota // findings report
	Fmt                  // files reformatted
	FmtCheck             // files that would be reformatted (fmt --check, nothing written)
)

// Format selects the renderer.
type Format int

// The output formats.
const (
	Human Format = iota // text report, color on a TTY
	JSON                // one JSON document
	SARIF               // one SARIF 2.1.0 document (check only)
)

// Options configures a renderer.
type Options struct {
	Format     Format // Human (zero value), JSON, SARIF
	Command    Kind
	NoProgress bool   // suppress the per-linter progress lines on stderr
	Color      bool   // human only: emit ANSI color; decided by the CLI (see UseColor)
	Version    string // SARIF tool.driver.version; the CLI passes internal/cli.Version
}

// Failure is a linter that failed to run: its name and the first line of its error.
type Failure struct {
	Linter string
	Err    string
}

// Summary carries what only the CLI knows, given to Close once the stream is drained.
type Summary struct {
	Elapsed  time.Duration // measured by the CLI around the engine run(s)
	RunLog   string        // runlog.Writer.Name(); "" when logging is disabled
	Skipped  []string      // "linter [note]", one per skipped linter
	Changed  []string      // fmt only: repoRoot-relative files reformatted (or that would be)
	Failures []Failure     // failures the renderer's own event stream never saw (a formatter pass rendered elsewhere); machine formats only
	Unstable bool          // fmt --verify-stable did not converge: the command exits non-zero, so the verdict must not read as success
}

// Renderer consumes one run: Event for every engine event in arrival order (from a single
// goroutine), then Close once to write the report.
type Renderer interface {
	Event(engine.Event)
	Close(Summary) error
}

// New returns the renderer for opts.Format.
func New(stdout, stderr io.Writer, opts Options) Renderer {
	b := newBase(stdout, stderr, opts)
	switch opts.Format {
	case JSON:
		return &jsonRenderer{base: b}
	case SARIF:
		return &sarifRenderer{base: b}
	default:
		return &human{base: b}
	}
}

// UseColor reports whether ANSI color is allowed: stdout is a terminal and NO_COLOR is empty
// (no-color.org).
func UseColor(isTTY bool, noColor string) bool { return isTTY && noColor == "" }
