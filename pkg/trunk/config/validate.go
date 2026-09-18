package config

import (
	"errors"
	"strings"
)

// Validate checks that everything referenced by id (a tool's download/runtime, a runtime's
// download, a linter's tools, and trunk.yaml's own enabled lists) actually exists among what was
// merged into cfg, returning every problem joined together via errors.Join. It is a separate
// step from Resolve, not run by it — callers that only need what was actually read can skip it.
func (cfg *Config) Validate() error {
	var errs []error //nolint:prealloc // each append's length depends on cfg's contents, no cheap upper bound to size against
	errs = append(errs, checkEnabled("runtime", cfg.Runtimes.Enabled, cfg.Runtimes.Definitions)...)
	errs = append(errs, checkEnabled("lint", cfg.Lint.Enabled, cfg.Lint.Definitions)...)
	errs = append(errs, checkEnabled("action", cfg.Actions.Enabled, cfg.Actions.Definitions)...)
	errs = append(errs, validateReferences(cfg)...)
	return errors.Join(errs...)
}

// checkEnabled reports a *ReferenceError for every entry of enabled (a trunk.yaml `enabled:`
// list, each optionally pinned as `id@version`) whose id has no matching key in defs.
func checkEnabled[T any](category string, enabled []string, defs map[string]T) []error {
	var errs []error
	for _, e := range enabled {
		id, _, _ := strings.Cut(e, "@")
		if _, ok := defs[id]; !ok {
			errs = append(errs, &ReferenceError{Category: category, Key: e, Field: "enabled", Reference: id})
		}
	}
	return errs
}

// validateReferences reports a *ReferenceError for every by-id reference (a tool's
// download/runtime, a runtime's download, a linter's tools/files, a file type's inherit) that
// doesn't resolve to a key actually present in cfg.
func validateReferences(cfg *Config) []error {
	var errs []error

	for name, t := range cfg.Tools {
		if t.Runtime != "" {
			if _, ok := cfg.Runtimes.Definitions[t.Runtime]; !ok {
				errs = append(errs, &ReferenceError{Category: "tool", Key: name, Field: "runtime", Reference: t.Runtime})
			}
		}
		if t.Download != "" {
			if _, ok := cfg.Downloads[t.Download]; !ok {
				errs = append(errs, &ReferenceError{Category: "tool", Key: name, Field: "download", Reference: t.Download})
			}
		}
	}

	for typ, r := range cfg.Runtimes.Definitions {
		if r.Download != "" {
			if _, ok := cfg.Downloads[r.Download]; !ok {
				errs = append(errs, &ReferenceError{Category: "runtime", Key: typ, Field: "download", Reference: r.Download})
			}
		}
	}

	for name, l := range cfg.Lint.Definitions {
		for _, toolName := range l.Tools {
			if _, ok := cfg.Tools[toolName]; !ok {
				errs = append(errs, &ReferenceError{Category: "lint", Key: name, Field: "tools", Reference: toolName})
			}
		}
		for _, fileName := range l.Files {
			if _, ok := cfg.Lint.Files[fileName]; !ok {
				errs = append(errs, &ReferenceError{Category: "lint", Key: name, Field: "files", Reference: fileName})
			}
		}
	}

	for name, f := range cfg.Lint.Files {
		for _, inh := range f.Inherit {
			if _, ok := cfg.Lint.Files[inh]; !ok {
				errs = append(errs, &ReferenceError{Category: "file", Key: name, Field: "inherit", Reference: inh})
			}
		}
	}

	return errs
}
