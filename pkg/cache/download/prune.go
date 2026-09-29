package download

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Prune removes every cache entry not referenced by any registry entry whose repository still
// exists on disk. A repository that no longer exists is itself dropped from the registry, along
// with everything it alone was keeping alive -- there is no age or duration to pick, per
// ROADMAP.md: staleness is "no live repository needs this," not "unused for N days."
func Prune(cacheDir string) error {
	root, err := Root(cacheDir)
	if err != nil {
		return err
	}
	dir := registryDir(root)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // nothing has ever recorded usage; nothing to prune
		}
		return err
	}

	keepRefs := map[Ref]bool{}
	keepSources := map[string]bool{}
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			continue
		}
		var entry registryEntry
		if json.Unmarshal(data, &entry) != nil {
			continue
		}
		if info, statErr := os.Stat(entry.RepoRoot); statErr != nil || !info.IsDir() {
			_ = os.Remove(path) // repository is gone: drop its entry, don't count its refs
			continue
		}
		for _, ref := range entry.Refs {
			keepRefs[ref] = true
		}
		for _, src := range entry.PluginSources {
			keepSources[src] = true
		}
	}

	sweep := func(base string, categories []string) error {
		for _, category := range categories {
			matches, globErr := filepath.Glob(filepath.Join(root, base, category, "*", "*"))
			if globErr != nil {
				return globErr
			}
			for _, m := range matches {
				id := filepath.Base(filepath.Dir(m))
				version := filepath.Base(m)
				if !keepRefs[Ref{Category: category, ID: id, Version: version}] {
					if rmErr := os.RemoveAll(m); rmErr != nil {
						return rmErr
					}
				}
			}
		}
		return nil
	}
	// action-packages installs have no shims/ counterpart -- they're consumed via their
	// node_modules/.bin dir directly, not a shim -- so only the installs sweep covers them.
	if err := sweep("installs", []string{"tools", "runtimes", "action-packages"}); err != nil {
		return err
	}
	if err := sweep("shims", []string{"tools", "runtimes"}); err != nil {
		return err
	}

	pluginsRoot := filepath.Join(filepath.Dir(root), "plugins")
	sourceFiles, _ := filepath.Glob(filepath.Join(pluginsRoot, "*.json"))
	for _, f := range sourceFiles {
		hash := strings.TrimSuffix(filepath.Base(f), ".json")
		if !keepSources[hash] {
			_ = os.Remove(f)
			_ = os.RemoveAll(filepath.Join(pluginsRoot, "checkouts", hash))
		}
	}
	return nil
}
