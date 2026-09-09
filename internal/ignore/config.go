// Package ignore implements SPECS.md §9: config-based path ignoring
// (lint.ignore, this file) and inline rtunk-ignore/trunk-ignore directives
// (directive.go, filter.go).
package ignore

import (
	"fmt"

	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/config"
)

// MatchesConfigIgnore reports whether path is ignored for linterName by
// entries (lint.ignore, SPECS.md §9.2): a gitignore-style last-match-wins
// evaluation, in config order, across all entries whose Linters include
// "ALL" or linterName — a later "!"-prefixed pattern un-ignores a path an
// earlier pattern excluded.
func MatchesConfigIgnore(path, linterName string, entries []config.LintIgnore) (bool, error) {
	ignored := false
	for _, e := range entries {
		if !appliesTo(e.Linters, linterName) {
			continue
		}
		for _, p := range e.Paths {
			re, err := globToRegexp(p.Pattern())
			if err != nil {
				return false, fmt.Errorf("invalid lint.ignore pattern %q: %w", p, err)
			}
			if re.MatchString(path) {
				ignored = !p.Negated()
			}
		}
	}
	return ignored, nil
}

func appliesTo(linters []string, name string) bool {
	for _, l := range linters {
		if l == "ALL" || l == name {
			return true
		}
	}
	return false
}

// FilterPaths removes paths ignored for linterName per entries.
func FilterPaths(paths []string, linterName string, entries []config.LintIgnore) ([]string, error) {
	var out []string
	for _, p := range paths {
		ignored, err := MatchesConfigIgnore(p, linterName, entries)
		if err != nil {
			return nil, err
		}
		if !ignored {
			out = append(out, p)
		}
	}
	return out, nil
}
