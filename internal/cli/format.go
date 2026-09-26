package cli

import (
	"io"
	"os"

	"github.com/xunleii/rtunk/internal/cli/render"
)

// progressOpts is what the user asked for on the progress side of a run.
type progressOpts struct {
	NoProgress bool // --no-progress: no per-linter lines and no live view
	ASCII      bool // --ascii: ASCII glyphs in the live view
	LiveHeight int  // --live-height / RTUNK_LIVE_HEIGHT; 0 means half the terminal
}

// newRenderer builds the renderer for a --format value ("human", "json" or "sarif"), reading the
// environment once: stdout being a terminal and NO_COLOR decide color, stderr being a terminal
// and TERM decide the live view, the locale decides ASCII glyphs.
func newRenderer(format string, stdout, stderr io.Writer, kind render.Kind, p progressOpts) render.Renderer {
	utf8 := render.IsUTF8Locale(os.Getenv("LC_ALL"), os.Getenv("LC_CTYPE"), os.Getenv("LANG"))
	return buildRenderer(format, stdout, stderr, kind, p, isTerminal(stderr), os.Getenv("TERM"), utf8)
}

// buildRenderer is newRenderer with the environment decisions passed in, so they can be tested.
// With the live view active the inner renderer gets NoProgress (its plain lines would collide with
// the area) and is wrapped; otherwise the v0.9.1 plain progress lines are untouched.
func buildRenderer(format string, stdout, stderr io.Writer, kind render.Kind, p progressOpts, stderrIsTTY bool, term string, utf8 bool) render.Renderer {
	f := render.Human
	switch format {
	case "json":
		f = render.JSON
	case "sarif":
		f = render.SARIF
	}
	liveOn := render.LiveEnabled(stderrIsTTY, p.NoProgress, term)
	inner := render.New(stdout, stderr, render.Options{
		Format: f, Command: kind, NoProgress: p.NoProgress || liveOn,
		Color:   render.UseColor(isTerminal(stdout), os.Getenv("NO_COLOR")),
		Version: Version,
	})
	if !liveOn {
		return inner
	}
	size := func() (int, int) { return 80, 24 }
	if file, ok := stderr.(*os.File); ok {
		size = func() (int, int) { return render.TermSize(file) }
	}
	return render.NewLive(inner, render.LiveOptions{
		Out: stderr, Size: size, Height: p.LiveHeight, ASCII: p.ASCII || !utf8, Command: kind,
	})
}

// isTerminal reports whether w is a character device (a terminal). ModeCharDevice is also true
// for /dev/null: harmless for color, and replaced by golang.org/x/term with the live view.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
