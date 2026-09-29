package security

import (
	"os"
	"path/filepath"
	"regexp"
)

// runFromWithFileRE matches ${root_or_parent_with(FILE)} -- FILE is a literal filename (e.g.
// "go.mod", ".scalafmt.conf"), not a pattern.
var runFromWithFileRE = regexp.MustCompile(`^\$\{root_or_parent_with\((.+)\)\}$`)

// runFromWithRegexRE matches ${root_or_parent_with_regex(REGEX)} -- REGEX is a Go-compatible
// regular expression matched against each candidate directory's own entry names.
var runFromWithRegexRE = regexp.MustCompile(`^\$\{root_or_parent_with_regex\((.+)\)\}$`)

// ResolveRunFrom resolves runFrom (a Command.RunFrom value) into the absolute directory a command
// invocation should run from, given target (the absolute path of one matched file) and repoRoot.
// directConfigs is the owning Linter's DirectConfigs, consulted only for
// "${root_or_parent_with_any_config}". ok is false for any value this project doesn't recognize
// (e.g. "${compile_command}", a bare literal like "apps") -- the caller treats that as Skipped.
//
// See docs/superpowers/specs/2026-09-12-check-v0.3.2-runfrom-sandbox-design.md for the real
// trunk-io catalog data and reasoning behind each form, especially "${parent}" (below).
func ResolveRunFrom(runFrom, target, repoRoot string, directConfigs []string) (dir string, ok bool) {
	switch runFrom {
	case "", "${parent}":
		// rtunk has no nested-workspace model (one trunk.yaml resolves to one flat config), so
		// "the parent workspace" ${parent} most plausibly refers to degenerates to repoRoot --
		// see the design spec's reasoning. This is also already today's default.
		return repoRoot, true
	case "${target_directory}":
		return filepath.Dir(target), true
	case "${root_or_parent_with_any_config}":
		return walkUpFor(filepath.Dir(target), repoRoot, func(candidate string) bool {
			return anyFileExists(candidate, directConfigs)
		}), true
	}

	if m := runFromWithFileRE.FindStringSubmatch(runFrom); m != nil {
		filename := m[1]
		return walkUpFor(filepath.Dir(target), repoRoot, func(candidate string) bool {
			return anyFileExists(candidate, []string{filename})
		}), true
	}
	if m := runFromWithRegexRE.FindStringSubmatch(runFrom); m != nil {
		re, err := regexp.Compile(m[1])
		if err != nil {
			return "", false
		}
		return walkUpFor(filepath.Dir(target), repoRoot, func(candidate string) bool {
			return anyEntryMatches(candidate, re)
		}), true
	}

	return "", false
}

// walkUpFor walks from start up to and including repoRoot, returning the first directory for
// which match reports true. Falls back to repoRoot itself (the "root" this project's
// "root_or_parent_with*" syntax names as the ultimate fallback) if no directory matches.
func walkUpFor(start, repoRoot string, match func(dir string) bool) string {
	dir := start
	for {
		if match(dir) {
			return dir
		}
		if dir == repoRoot {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached the filesystem root without ever equaling repoRoot -- shouldn't happen
			// for a target Files() matched under repoRoot, but never loop forever.
			break
		}
		dir = parent
	}
	return repoRoot
}

// anyFileExists reports whether any of names exists directly inside dir.
func anyFileExists(dir string, names []string) bool {
	for _, name := range names {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return false
}

// anyEntryMatches reports whether any entry's name directly inside dir matches re.
func anyEntryMatches(dir string, re *regexp.Regexp) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if re.MatchString(e.Name()) {
			return true
		}
	}
	return false
}
