// Package download's registry.go implements ROADMAP.md v0.11's usage-tracking cache prune: one
// file per repository, overwritten wholesale on every RecordUsage call, recording exactly what
// that repository's just-resolved config.Config currently needs. cache prune (see Prune, added
// alongside this in the same milestone) reads every such file back to decide what's still in use.
package download

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/xunleii/rtunk/pkg/trunk/config"
)

// registryEntry is one repository's most recently recorded set of cache entries in use.
type registryEntry struct {
	RepoRoot      string
	Refs          []Ref
	PluginSources []string // config.SourceHash(src) for every git plugin source cfg currently resolves
}

// registryDir is where one entry per repository lives, sibling to downloads/ under the shared
// cache root (root is a downloads.Root(cacheDir) result; its parent is that shared root -- the
// same trick pkg/run/runlog's own logsRoot already uses for the same purpose).
func registryDir(root string) string {
	return filepath.Join(filepath.Dir(root), "registry")
}

// registryPath is one repository's own entry file, keyed by the sha256 hex of its absolute root
// -- the same repo-identity idiom pkg/run/runlog's repoKey, pkg/run/actions's historyPath, and
// internal/cli's recentFmtRunPath each already use independently.
func registryPath(root, repoRoot string) string {
	sum := sha256.Sum256([]byte(repoRoot))
	return filepath.Join(registryDir(root), hex.EncodeToString(sum[:])+".json")
}

// resolvedRefs is every tool/runtime cfg currently resolves, with concrete versions -- unlike
// Pending, it is not filtered to "not yet installed": RecordUsage needs the complete current
// need, installed or not, so a not-yet-downloaded item is still protected from a concurrent
// prune.
func resolvedRefs(cfg config.Config) []Ref {
	refs := make([]Ref, 0, len(cfg.Tools)+len(cfg.Runtimes.Definitions))
	for id, tool := range cfg.Tools {
		version := ResolveVersion(cfg.Lint.Enabled, id, tool.KnownGoodVersion)
		refs = append(refs, Ref{Category: "tools", ID: id, Version: version})
	}
	for id, rt := range cfg.Runtimes.Definitions {
		if rt.SystemVersion != "" {
			continue // never downloaded; nothing in the cache to keep for it
		}
		version := ResolveVersion(cfg.Runtimes.Enabled, id, rt.KnownGoodVersion)
		refs = append(refs, Ref{Category: "runtimes", ID: id, Version: version})
	}
	for _, action := range cfg.Actions.Definitions {
		if action.PackagesFile == "" {
			continue
		}
		// Keyed by the manifest's own content hash, not the action's ID: that's what
		// fetchActionPackagesRef installs by (two actions sharing one manifest share one on-disk
		// install), and it's the only identity Prune's generic installs/<category>/*/* sweep can
		// match back against that on-disk layout (installs/action-packages/<hash>/manifest). A
		// manifest that can't be read (e.g. a plugin checkout gone stale) is skipped rather than
		// failing the whole registry write -- RecordUsage is best-effort by convention.
		hash, err := actionPackagesHash(action)
		if err != nil {
			continue
		}
		refs = append(refs, Ref{Category: "action-packages", ID: hash, Version: "manifest"})
	}
	return refs
}

// RecordUsage overwrites repoRoot's registry entry with exactly what cfg currently resolves.
// Best-effort by convention: every caller (engine.Run) ignores a non-nil return, the same
// convention Touch already uses for cache bookkeeping -- a bookkeeping failure must never fail a
// real run.
func RecordUsage(cacheDir, repoRoot string, cfg config.Config) error {
	root, err := Root(cacheDir)
	if err != nil {
		return err
	}
	dir := registryDir(root)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}

	var sources []string
	for _, src := range cfg.Plugins.Sources {
		if src.Local != "" {
			continue // local source: nothing fetched into the cache to track
		}
		sources = append(sources, config.SourceHash(src))
	}

	data, err := json.Marshal(registryEntry{RepoRoot: repoRoot, Refs: resolvedRefs(cfg), PluginSources: sources})
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // no-op once renamed below
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), registryPath(root, repoRoot))
}
