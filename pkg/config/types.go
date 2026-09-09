package config

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// PackageVersion is a "name" or "name@version" reference — the format used
// everywhere a linter, runtime, or tool is enabled, disabled, or pinned by
// name (Lint.Enabled, Lint.Disabled, Runtimes.Enabled, Tools.Enabled).
type PackageVersion string

// Validate reports whether p is a well-formed "name" or "name@version".
func (p PackageVersion) Validate() error {
	s := string(p)
	if s == "" {
		return fmt.Errorf("package version: empty")
	}
	if strings.Count(s, "@") > 1 {
		return fmt.Errorf("package version %q: more than one '@'", s)
	}
	name, version, hasAt := strings.Cut(s, "@")
	if name == "" {
		return fmt.Errorf("package version %q: empty name", s)
	}
	if hasAt && version == "" {
		return fmt.Errorf("package version %q: empty version after '@'", s)
	}
	return nil
}

// Name is the part before "@", or the whole string when unpinned.
func (p PackageVersion) Name() string {
	name, _, _ := strings.Cut(string(p), "@")
	return name
}

// Version is the part after "@", or "" when unpinned.
func (p PackageVersion) Version() string {
	_, version, _ := strings.Cut(string(p), "@")
	return version
}

// GlobPattern is a gitignore-style glob, optionally prefixed with "!" to
// negate a preceding match (LintIgnore.Paths, LintTrigger.Paths/Targets,
// LinterDefinition.Files — "ALL" is itself a valid pattern there, matching
// every file).
type GlobPattern string

// Validate reports whether g, once any leading "!" is stripped, is a
// non-empty and syntactically valid glob (path/filepath.Match's syntax —
// the actual matcher, internal/ignore's, additionally supports "**" as any-
// depth, which filepath.Match doesn't reject either, it just treats it as
// two "*" wildcards).
func (g GlobPattern) Validate() error {
	pattern := g.Pattern()
	if pattern == "" {
		return fmt.Errorf("glob pattern %q: empty", string(g))
	}
	if _, err := filepath.Match(pattern, ""); err != nil {
		return fmt.Errorf("glob pattern %q: %w", string(g), err)
	}
	return nil
}

// Negated reports whether g starts with "!" (un-ignore/exclude semantics).
func (g GlobPattern) Negated() bool {
	return strings.HasPrefix(string(g), "!")
}

// Pattern is g with any leading "!" stripped.
func (g GlobPattern) Pattern() string {
	return strings.TrimPrefix(string(g), "!")
}

// RegexPattern is a Go-syntax regular expression (Command.ParseRegex, used
// when Output is "regex").
type RegexPattern string

// Validate reports whether p compiles as a Go regular expression.
func (p RegexPattern) Validate() error {
	if p == "" {
		return fmt.Errorf("regex pattern: empty")
	}
	if _, err := regexp.Compile(string(p)); err != nil {
		return fmt.Errorf("regex pattern %q: %w", string(p), err)
	}
	return nil
}
