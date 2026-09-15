package cli

import (
	"fmt"
	"strings"

	"github.com/xunleii/rtunk/pkg/trunk/config"
)

// filterLinters implements check/fmt's --filter and --exclude flags: a runtime-only restriction
// on which linters run this invocation, applied by trimming cfg.Lint.Definitions before it
// reaches engine.Run (engine.Run iterates exactly that map -- see pkg/trunk/engine/engine.go's
// own Run, which ranges over env.Cfg.Lint.Definitions -- so trimming it here needs no engine
// change). Nothing is written back to trunk.yaml; this only affects the one invocation.
//
// filter is an allow-list (bare ids only) or a deny-list (every id prefixed with "-"), matching
// real trunk's own --filter semantics ("comma separated list of linters... to include or
// exclude"). Mixing bare and "-"-prefixed ids in the same --filter value is a usage error --
// trunk's own docs describe --filter as one list or the other, not both at once. exclude is
// always a deny-list (real trunk's own "--exclude: shorthand for an inverse --filter"), so its
// entries are never "-"-prefixed by the caller.
//
// Passing both filter and exclude is a usage error: they are two spellings of the same mechanism
// (real trunk's own docs call --exclude sugar for one specific --filter shape), not independently
// composable filters. Passing neither returns cfg unchanged.
func filterLinters(cfg config.Config, filter, exclude string) (config.Config, error) {
	if filter == "" && exclude == "" {
		return cfg, nil
	}
	if filter != "" && exclude != "" {
		return config.Config{}, fmt.Errorf("rtunk: --filter and --exclude are mutually exclusive")
	}

	var keep map[string]bool // nil means "allow-list mode": keep only these ids
	var drop map[string]bool // deny-list mode: keep everything except these ids

	if filter != "" {
		ids := strings.Split(filter, ",")
		isDenyList := strings.HasPrefix(ids[0], "-")
		keep = map[string]bool{}
		drop = map[string]bool{}
		for _, raw := range ids {
			isDeny := strings.HasPrefix(raw, "-")
			if isDeny != isDenyList {
				return config.Config{}, fmt.Errorf("rtunk: --filter cannot mix included and excluded linters (%q)", filter)
			}
			id := strings.TrimPrefix(raw, "-")
			if _, ok := cfg.Lint.Definitions[id]; !ok {
				return config.Config{}, fmt.Errorf("rtunk: --filter: unknown linter %q", id)
			}
			if isDeny {
				drop[id] = true
			} else {
				keep[id] = true
			}
		}
		if isDenyList {
			keep = nil // switch to deny-list mode below
		} else {
			drop = nil
		}
	} else {
		drop = map[string]bool{}
		for _, id := range strings.Split(exclude, ",") {
			if _, ok := cfg.Lint.Definitions[id]; !ok {
				return config.Config{}, fmt.Errorf("rtunk: --exclude: unknown linter %q", id)
			}
			drop[id] = true
		}
	}

	trimmed := map[string]config.Linter{}
	for id, def := range cfg.Lint.Definitions {
		switch {
		case keep != nil:
			if keep[id] {
				trimmed[id] = def
			}
		case drop != nil:
			if !drop[id] {
				trimmed[id] = def
			}
		}
	}
	cfg.Lint.Definitions = trimmed
	return cfg, nil
}
