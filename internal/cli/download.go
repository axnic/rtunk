package cli

import (
	"fmt"
	"io"

	"github.com/xunleii/rtunk/pkg/trunk/download"
)

// downloadCmd is `rtunk download`: bare fetches everything enabled+used in the resolved config;
// with Category+ID, one targeted item (optionally `@version`, parsed by cutVersion below).
type downloadCmd struct {
	Category string `arg:"" optional:"" default:"" enum:"tools,runtimes,lint,actions,plugins," help:"Resource category."`
	ID       string `arg:"" optional:"" help:"Resource id, optionally @version."`
}

// Run resolves the trunk.yaml in effect, fetches the requested ref(s) (or everything
// enabled+used, for the bare `rtunk download` form), and streams one line per download.Event to
// stdout as it arrives -- so a slow fetch shows progress rather than going silent until it's done.
func (c *downloadCmd) Run(cli *CLI, stdout io.Writer) error {
	cfg, err := resolveConfig(cli.Config, cli.CacheDir, false)
	if err != nil {
		return err
	}

	var refs []download.Ref
	if c.Category != "" {
		id, version, _ := cutVersion(c.ID)
		refs = []download.Ref{{Category: c.Category, ID: id, Version: version}}
	}

	events, err := download.Download(cfg, cli.CacheDir, refs...)
	if err != nil {
		return err
	}
	var failed error
	for ev := range events {
		_, _ = fmt.Fprintln(stdout, formatEvent(ev))
		if ev.Phase == download.Failed {
			failed = ev.Err
		}
	}
	return failed
}

// cutVersion splits an `id[@version]` CLI argument, mirroring how trunk.yaml's own enabled:
// entries are parsed (pkg/trunk/config's enabledIDs/checkEnabled).
func cutVersion(s string) (id, version string, pinned bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == '@' {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}

// formatEvent renders one download.Event as a single line of progress output -- why a switch
// over Phase rather than a %+v dump: Progress events fire once per chunk and would otherwise
// flood stdout with the full Event struct on every one.
func formatEvent(ev download.Event) string {
	switch ev.Phase {
	case download.Started:
		return fmt.Sprintf("%s %s: starting", ev.Ref.Category, ev.Ref.ID)
	case download.Progress:
		return fmt.Sprintf("%s %s: %d/%d bytes", ev.Ref.Category, ev.Ref.ID, ev.Bytes, ev.Total)
	case download.Cached:
		return fmt.Sprintf("%s %s: cached", ev.Ref.Category, ev.Ref.ID)
	case download.Done:
		return fmt.Sprintf("%s %s: done", ev.Ref.Category, ev.Ref.ID)
	case download.Failed:
		return fmt.Sprintf("%s %s: failed: %v", ev.Ref.Category, ev.Ref.ID, ev.Err)
	default:
		return fmt.Sprintf("%s %s: unknown phase %d", ev.Ref.Category, ev.Ref.ID, ev.Phase)
	}
}
