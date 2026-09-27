package config

import (
	"fmt"
	"sort"
)

// LegacyLinterShapeError is returned by CheckDeprecations when an enabled linter still uses the
// old, single type/command declaration shape instead of a commands: list -- rtunk has nowhere to
// resolve that shape into a runnable command, so it is refused outright rather than silently
// resolving to a linter with zero commands that runs, matches files, and reports success.
type LegacyLinterShapeError struct {
	LinterID   string
	Deprecated string // the linter's own deprecated: message, when it has one
}

func (e *LegacyLinterShapeError) Error() string {
	msg := fmt.Sprintf("rtunk: %s uses the old, unsupported single-command declaration shape", e.LinterID)
	if e.Deprecated != "" {
		msg += ": " + e.Deprecated
	}
	return msg
}

// CheckDeprecations scans cfg's enabled linters (cfg.Lint.Definitions, already trimmed to
// enabled-only by Resolve) for the legacy single-command shape and for any deprecated: message.
// err is a *LegacyLinterShapeError for the first enabled linter (in id order) using the legacy
// shape; warnings names every enabled linter or command carrying a deprecated: message, in id
// order, independent of err.
func (cfg Config) CheckDeprecations() (warnings []string, err error) {
	ids := make([]string, 0, len(cfg.Lint.Definitions))
	for id := range cfg.Lint.Definitions {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		l := cfg.Lint.Definitions[id]
		if l.LegacyType != "" || len(l.LegacyCommand) > 0 {
			if err == nil {
				err = &LegacyLinterShapeError{LinterID: id, Deprecated: l.Deprecated}
			}
			continue
		}
		if l.Deprecated != "" {
			warnings = append(warnings, fmt.Sprintf("%s: %s", id, l.Deprecated))
		}
		for _, cmd := range l.Commands {
			if cmd.Deprecated != "" {
				warnings = append(warnings, fmt.Sprintf("%s %s: %s", id, cmd.Name, cmd.Deprecated))
			}
		}
	}
	return warnings, err
}
