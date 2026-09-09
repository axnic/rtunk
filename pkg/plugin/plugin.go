package plugin

import (
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/goccy/go-yaml"

	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/config"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/download"
)

// Plugin is one resolved plugins.sources entry's compiled content: every
// linters/*/plugin.yaml and runtimes/*/plugin.yaml file's
// downloads/tools/linter/runtime definitions, merged (Resolve's
// discover+merge step). Several Plugins (one per configured source) are
// searched together via Plugins, not merged into a single structure — see
// plugins.go.
type Plugin struct {
	// Source/Dir identify which plugins.sources entry this came from and
	// the resolved local clone root Resolve built it from (Dir is only
	// meaningful transiently, during Resolve — the clone itself isn't
	// cached, see resolve.go).
	Source config.PluginSource
	Dir    string
	// Warnings collected while building this Plugin (e.g. a malformed
	// linters/*/plugin.yaml file, skipped rather than failing the whole
	// resolve) — Plugin.Load's own returned warnings include these.
	Warnings []string
	// Categories is the repo's root linters/plugin.yaml file-category
	// catalog (name -> definition), nil if that file doesn't exist.
	Categories map[string]fileCategoryDef
	Downloads  []download.Group
	Tools      []trunkTool
	Linters    []trunkLinter
	// Runtimes comes from the repo's runtimes/*/plugin.yaml files, if any
	// (optional — unlike linters/, plenty of plugin sources define none).
	Runtimes []trunkRuntime
}

// Save gob-encodes p to path, creating path's parent directory if needed.
func (p *Plugin) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return gob.NewEncoder(f).Encode(p)
}

// Open reads a gob-encoded Plugin from path (as written by Plugin.Save or
// Store) — no network access, no rebuilding.
func Open(path string) (*Plugin, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var p Plugin
	if err := gob.NewDecoder(f).Decode(&p); err != nil {
		return nil, err
	}
	return &p, nil
}

var nonAlnum = regexp.MustCompile(`[^a-zA-Z0-9]+`)

// CachePath is the conventional cache location for source's compiled
// Plugin under pluginsDir (pkg/cache's Cache.Plugins() result): one .gob
// per repo+ref (e.g. "https---github.com-trunk-io-plugins/v1.10.2.gob").
// Store uses it; a caller wanting to check for a cache hit before calling
// Resolve at all (see the package doc) uses it too.
func CachePath(pluginsDir string, source config.PluginSource) string {
	repo := nonAlnum.ReplaceAllString(source.URI, "-")
	return filepath.Join(pluginsDir, repo, source.Ref+".gob")
}

// Store saves p at its own Source's conventional cache path under
// pluginsDir — the opt-in counterpart to Resolve never persisting anything
// on its own; see the package doc for why.
func Store(pluginsDir string, p *Plugin) error {
	return p.Save(CachePath(pluginsDir, p.Source))
}

// buildPlugin walks dir/linters (required) and dir/runtimes (optional —
// plenty of plugin sources define none) in a resolved clone, parses the
// root linters/plugin.yaml for file categories, and merges every
// linters/<name>/plugin.yaml and runtimes/<name>/plugin.yaml's
// downloads/tools/linter/runtime definitions into one Plugin. A malformed
// file is skipped with a warning; a name (download group, tool, linter, or
// runtime) defined by two different files is a hard error naming both.
// Each linter remembers dir as its own Dir (trunkLinter.Dir), for
// ${plugin} resolution even after several Plugins get merged together.
func buildPlugin(dir string) (*Plugin, []string, error) {
	p := &Plugin{Categories: parseCategories(dir)}
	seen := map[string]map[string]string{"download": {}, "tool": {}, "linter": {}, "runtime": {}}

	lintWarnings, err := walkPluginFiles(dir, "linters", true, func(path string, raw trunkPlugin) error {
		if err := mergeDownloads(p, seen, path, raw.Downloads); err != nil {
			return err
		}
		for _, t := range raw.Tools.Definitions {
			if err := claim(seen["tool"], "tool", t.Name, path); err != nil {
				return err
			}
			p.Tools = append(p.Tools, t)
		}
		for _, l := range raw.Lint.Definitions {
			if err := claim(seen["linter"], "linter", l.Name, path); err != nil {
				return err
			}
			l.Dir = dir
			p.Linters = append(p.Linters, l)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	runtimeWarnings, err := walkPluginFiles(dir, "runtimes", false, func(path string, raw trunkPlugin) error {
		if err := mergeDownloads(p, seen, path, raw.Downloads); err != nil {
			return err
		}
		for _, r := range raw.Runtimes.Definitions {
			if err := claim(seen["runtime"], "runtime", r.Type, path); err != nil {
				return err
			}
			p.Runtimes = append(p.Runtimes, r)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	return p, append(lintWarnings, runtimeWarnings...), nil
}

// walkPluginFiles calls fn with every parsed <dir>/<subdir>/<name>/plugin.yaml
// (not <subdir>/plugin.yaml itself, which is the categories/comment_formats
// file for linters/, or simply absent for runtimes/) — a malformed file
// produces a warning rather than failing the whole walk. required controls
// whether a missing <dir>/<subdir> directory itself is an error (true for
// linters/, since a plugin source without any is pointless) or simply
// "nothing here" (false for runtimes/, which plenty of sources omit).
func walkPluginFiles(dir, subdir string, required bool, fn func(path string, raw trunkPlugin) error) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(dir, subdir))
	if err != nil {
		if !required && os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s/%s: %w", dir, subdir, err)
	}

	var warnings []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(dir, subdir, e.Name(), "plugin.yaml")
		data, err := os.ReadFile(path)
		if err != nil {
			continue // not every <subdir>/<name>/ entry necessarily has a plugin.yaml
		}
		var raw trunkPlugin
		if err := yaml.Unmarshal(data, &raw); err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		if err := fn(path, raw); err != nil {
			return warnings, err
		}
	}
	return warnings, nil
}

// mergeDownloads claims and normalizes every download group in downloads
// (found at path) into p.Downloads.
func mergeDownloads(p *Plugin, seen map[string]map[string]string, path string, downloads []trunkDownloadGroup) error {
	for _, d := range downloads {
		if err := claim(seen["download"], "download", d.Name, path); err != nil {
			return err
		}
		variants := make([]download.Variant, len(d.Downloads))
		for i, v := range d.Downloads {
			variants[i] = normalizeVariant(v)
		}
		p.Downloads = append(p.Downloads, download.Group{
			Name:             d.Name,
			Args:             d.Args,
			RenameSingleFile: d.RenameSingleFile,
			Variants:         variants,
		})
	}
	return nil
}

// claim records name (of the given kind) as defined by path, or fails if a
// different path already claimed it — two plugin.yaml files disagreeing
// about what a name means is a conflict, not a first-one-wins situation.
func claim(seen map[string]string, kind, name, path string) error {
	if name == "" {
		return nil
	}
	if prev, ok := seen[name]; ok {
		return fmt.Errorf("conflicting %s %q: defined in both %s and %s", kind, name, prev, path)
	}
	seen[name] = path
	return nil
}

// parseCategories reads the repo's root linters/plugin.yaml — trunk's own
// shared catalog of named file categories (github.com/trunk-io/plugins'
// linters/plugin.yaml is the canonical example), returning nil if the
// source doesn't follow that convention.
func parseCategories(dir string) map[string]fileCategoryDef {
	data, err := os.ReadFile(filepath.Join(dir, "linters", "plugin.yaml"))
	if err != nil {
		return nil
	}
	var root struct {
		Lint struct {
			Files []fileCategoryDef `yaml:"files"`
		} `yaml:"lint"`
	}
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil
	}
	categories := make(map[string]fileCategoryDef, len(root.Lint.Files))
	for _, c := range root.Lint.Files {
		categories[c.Name] = c
	}
	return categories
}
