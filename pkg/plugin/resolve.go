// Package plugin resolves plugins.sources entries (git repos of
// trunk-dialect plugin.yaml files, github.com/trunk-io/plugins being the
// canonical example) into a Plugin, and searches across every configured
// source (Plugins) for a given category/download/tool/linter/runtime by
// name.
//
// Resolve does two things, every call, fresh, for one source:
//
//  1. Clone: source.URI is git-cloned at its pinned source.Ref (always a
//     tag or commit SHA, never a branch — a branch moves, so it can't be a
//     cache key) into a disposable temp directory — discarded once its
//     Plugin is built; only the compiled result is meant to be cached
//     (Store, keyed by pkg/cache.Cache.Plugins()), never the clone itself.
//
//  2. Discover + merge (buildPlugin): every linters/*/plugin.yaml and
//     runtimes/*/plugin.yaml in the clone is read — trunk's own repo
//     carries 150+ of the former, one directory per linter (occasionally a
//     few, e.g. golangci-lint/plugin.yaml defines both "golangci-lint" and
//     "golangci-lint2") — and their downloads/tools/lint/runtime
//     definitions combined into one Plugin. The repo's root
//     linters/plugin.yaml is different in kind: it's not a linter, it's
//     the shared catalog of named file categories (parseCategories) every
//     other file's `files: [<category>, ...]` refers to instead of a raw
//     glob. Any name — a download group, a tool, a linter, or a runtime —
//     defined by two different plugin.yaml files is a conflict: buildPlugin
//     stops and returns an error naming both files, rather than silently
//     letting one win.
//
// A repo's config typically names several plugins.sources: Plugins (see
// plugins.go) searches each one's separately-Resolved Plugin together the
// same way buildPlugin combines files within one source — a name conflict
// across sources is caught exactly like one within a source.
//
// Resolve never persists its result — that's the caller's choice, not this
// package's. Plugin.Save/Open/Store are opt-in building blocks for whatever
// caching policy (or none) a caller wants: `rtunk check` looks for an
// existing cache (CachePath) before calling Resolve at all, and Stores what
// it built for next time; rtunk-explorer, in contrast, resolves and never
// persists anything, since there's no "next time" for a one-shot inspection.
//
// Plugins.Load ties the rest together: it translates every source's
// linters into config.LinterDefinition, dropping (with a warning) anything
// this rtunk build can't actually run — a hardcoded output type it doesn't
// parse, or an install mechanism other than a supported download/package
// manager.
//
// ponytail: since the clone is disposable, a linter definition's own
// PluginDir (config.LinterDefinition.PluginDir, resolving ${plugin} in a
// command's own Run) points at a directory that no longer exists by the
// time a cached (Store/Open'd) Plugin is actually run — fine for every
// linter seen so far (${plugin} is unused in this codebase's own fixtures
// and the real catalogs explored), but a real plugin.yaml command
// referencing a repo-bundled resource via ${plugin} would break; revisit
// (e.g. re-clone lazily, only when a command actually needs it) if that
// turns out to matter.
package plugin

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/config"
)

var shaRe = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// Build parses dir's own linters/*/plugin.yaml and runtimes/*/plugin.yaml
// files into a Plugin — the same local, network-free step Resolve uses
// after cloning. Exported for other packages' own test fixtures (a
// directory of hand-written plugin.yaml files needs no git clone) and
// tools that already have a local checkout to inspect.
func Build(dir string) (*Plugin, []string, error) {
	return buildPlugin(dir)
}

// Resolve clones source into a disposable temp directory (resolveClone)
// and builds its Plugin fresh from the clone's plugin.yaml files, then
// discards the clone. It never persists the result — see Store/Plugin.Save
// for that, opt-in (see the package doc).
func Resolve(source config.PluginSource) (*Plugin, error) {
	dir, err := resolveClone(source)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	p, warnings, err := buildPlugin(dir)
	if err != nil {
		return nil, err
	}
	p.Source = source
	p.Warnings = warnings
	return p, nil
}

// resolveClone clones source.URI at source.Ref into a fresh temp
// directory — never reused across calls, since nothing about it is cached
// (see the package doc).
func resolveClone(source config.PluginSource) (string, error) {
	if err := verifyPinnedRef(source.URI, source.Ref); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp("", "rtunk-plugin-*")
	if err != nil {
		return "", err
	}

	isTag, err := refIsTag(source.URI, source.Ref)
	if err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	if isTag {
		if err := runGit("clone", "--depth", "1", "--branch", source.Ref, source.URI, dir); err != nil {
			os.RemoveAll(dir)
			return "", fmt.Errorf("clone %s @ %s: %w", source.URI, source.Ref, err)
		}
		return dir, nil
	}
	// A commit SHA can't be shallow-cloned by ref directly: fetch, then checkout.
	if err := runGit("clone", source.URI, dir); err != nil {
		os.RemoveAll(dir)
		return "", fmt.Errorf("clone %s: %w", source.URI, err)
	}
	if err := runGit("-C", dir, "checkout", "--quiet", source.Ref); err != nil {
		os.RemoveAll(dir)
		return "", fmt.Errorf("checkout %s @ %s: %w", source.URI, source.Ref, err)
	}
	return dir, nil
}

// verifyPinnedRef rejects anything that isn't plausibly a tag or a commit
// SHA up front: never a branch, for reproducibility.
func verifyPinnedRef(uri, ref string) error {
	if ref == "" {
		return fmt.Errorf("plugins.sources %s: ref must be a tag or commit SHA, never empty/a branch", uri)
	}
	isTag, err := refIsTag(uri, ref)
	if err != nil {
		return err
	}
	if isTag || shaRe.MatchString(ref) {
		return nil
	}
	return fmt.Errorf("plugins.sources %s: ref %q is neither a tag nor a commit SHA — branches aren't reproducible", uri, ref)
}

func refIsTag(uri, ref string) (bool, error) {
	out, err := exec.Command("git", "ls-remote", "--tags", uri, ref).Output()
	if err != nil {
		return false, fmt.Errorf("ls-remote %s: %w", uri, err)
	}
	return strings.TrimSpace(string(out)) != "", nil
}

func runGit(args ...string) error {
	cmd := exec.Command("git", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, out)
	}
	return nil
}
