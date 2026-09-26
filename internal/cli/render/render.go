// Package render turns the engine's event stream into terminal output. A Renderer holds
// presentation state only (grouping, counting); it makes no decision about what to run or what
// the exit code is. See docs/superpowers/specs/2026-09-26-v0.9.1-event-stream-plain-renderer-design.md.
package render

import (
	"os"
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

// Options configures a renderer.
type Options struct {
	Command    Kind
	NoProgress bool // suppress the per-linter progress lines on stderr
	NoColor    bool // NO_COLOR is set; plain never emits color, later renderers read this
}

// Summary carries what only the CLI knows, given to Close once the stream is drained.
type Summary struct {
	Elapsed time.Duration // measured by the CLI around the engine run(s)
	RunLog  string        // runlog.Writer.Name(); "" when logging is disabled
	Skipped []string      // "linter [note]", one per skipped linter
	Changed []string      // fmt only: repoRoot-relative files reformatted (or that would be)
}

// Renderer consumes one run: Event for every engine event in arrival order (from a single
// goroutine), then Close once to write the report.
type Renderer interface {
	Event(engine.Event)
	Close(Summary) error
}

// NoColorFromEnv reports whether NO_COLOR is set and non-empty (no-color.org).
func NoColorFromEnv() bool { return os.Getenv("NO_COLOR") != "" }
