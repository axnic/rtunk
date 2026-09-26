package cli

import (
	"io"
	"os"

	"github.com/xunleii/rtunk/internal/cli/render"
)

// newRenderer builds the renderer for a --format value ("human", "json" or "sarif"). The color
// decision is made here, once: stdout must be a terminal and NO_COLOR empty.
func newRenderer(format string, stdout, stderr io.Writer, kind render.Kind, noProgress bool) render.Renderer {
	f := render.Human
	switch format {
	case "json":
		f = render.JSON
	case "sarif":
		f = render.SARIF
	}
	return render.New(stdout, stderr, render.Options{
		Format: f, Command: kind, NoProgress: noProgress,
		Color:   render.UseColor(isTerminal(stdout), os.Getenv("NO_COLOR")),
		Version: Version,
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
