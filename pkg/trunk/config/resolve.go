package config

import (
	"errors"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// pluginCategories are the plugin repository subdirs that hold plugin.yaml files
// (ARCHITECTURE.md "Plugin repository layout"). repo-tools/ is deliberately excluded: it is
// internal tooling for the plugins repo itself, not consumed by trunk/rtunk.
var pluginCategories = []string{"linters", "actions", "tools", "runtimes"}

// trunkFile is the raw shape of a trunk.yaml file, as YAML naturally decodes it (lists, not
// maps) — the lecture phase's output, before Resolve merges it into a map-keyed Config.
type trunkFile struct {
	Version string `yaml:"version"`
	CLI     struct {
		Version string `yaml:"version"`
	} `yaml:"cli"`
	Plugins struct {
		Sources []PluginSource `yaml:"sources"`
	} `yaml:"plugins"`
	Runtimes struct {
		Enabled []string `yaml:"enabled"`
	} `yaml:"runtimes"`
	Lint struct {
		Enabled []string `yaml:"enabled"`
	} `yaml:"lint"`
	Actions struct {
		Enabled  []string `yaml:"enabled"`
		Disabled []string `yaml:"disabled"`
	} `yaml:"actions"`
}

// pluginFile is the raw shape of one plugin.yaml: any mix of the section kinds below
// (ARCHITECTURE.md "a single file commonly mixes sections"). The same shape covers both a
// resource file (linters/<name>/plugin.yaml, contributing Lint.Definitions etc.) and a global
// config file (the repo-root plugin.yaml, contributing Environments; a category-root file like
// linters/plugin.yaml, contributing Lint.CommentFormats) — whichever fields a given file sets.
type pluginFile struct {
	Environments []NamedEnvironment `yaml:"environments"`
	Downloads    []Download         `yaml:"downloads"`
	Tools        struct {
		Definitions []Tool `yaml:"definitions"`
	} `yaml:"tools"`
	Lint struct {
		Definitions    []Linter        `yaml:"definitions"`
		CommentFormats []CommentFormat `yaml:"comment_formats"`
		Files          []FileType      `yaml:"files"`
	} `yaml:"lint"`
	Actions struct {
		Definitions []Action `yaml:"definitions"`
	} `yaml:"actions"`
	Runtimes struct {
		Definitions []Runtime `yaml:"definitions"`
	} `yaml:"runtimes"`
}

// Resolve reads the trunk.yaml file at path and merges in every definition contributed by its
// plugin sources — local ones read straight off disk, git ones fetched via fetchGitSource, which
// caches the parsed result (not the raw checkout) under cacheDir ("" uses the OS default cache
// dir). See the package doc for the pipeline; call Validate on the result to check enabled lists
// and dangling references. Before returning, filterEnabled trims the merged result down to what
// trunk.yaml actually enabled plus whatever those enabled definitions reference transitively — a
// plugin source's whole catalog is a poor stand-in for "the effective configuration" (mirrors
// `trunk config print`'s default view). Use ResolveAll for the untrimmed catalog (`trunk config
// print --all`). The returned Config is always populated with everything Resolve managed to read
// (and keep), even when it also returns an error — callers that only care about specific
// resources may still use it.
func Resolve(file, cacheDir string) (Config, error) {
	cfg, err := resolveMerged(file, cacheDir)
	filterEnabled(&cfg)
	return cfg, err
}

// ResolveAll is Resolve without the enabled+used trim: every definition merged from trunk.yaml's
// plugin sources, regardless of what trunk.yaml itself turns on (`trunk config print --all`).
// Environments and Lint.CommentFormats are global, non-enableable config either way, so they're
// identical between Resolve and ResolveAll.
func ResolveAll(file, cacheDir string) (Config, error) {
	return resolveMerged(file, cacheDir)
}

// resolveMerged is Resolve/ResolveAll's shared pipeline, before filterEnabled trims anything.
func resolveMerged(file, cacheDir string) (cfg Config, err error) {
	cfg = Config{
		Tools:     map[string]Tool{},
		Downloads: map[string]Download{},
	}
	cfg.Plugins.Sources = map[string]PluginSource{}
	cfg.Runtimes.Definitions = map[string]Runtime{}
	cfg.Lint.Definitions = map[string]Linter{}
	cfg.Lint.Files = map[string]FileType{}
	cfg.Actions.Definitions = map[string]Action{}

	// 1. Lecture
	tf, err := readTrunkFile(file)
	if err != nil {
		return cfg, err
	}
	cfg.Version = tf.Version
	cfg.CLI.Version = tf.CLI.Version
	cfg.Runtimes.Enabled = tf.Runtimes.Enabled
	cfg.Lint.Enabled = tf.Lint.Enabled
	cfg.Actions.Enabled = tf.Actions.Enabled
	cfg.Actions.Disabled = tf.Actions.Disabled

	var errs []error
	mergeKeyed(cfg.Plugins.Sources, tf.Plugins.Sources, func(s PluginSource) string { return s.ID }, "plugin source", &errs)

	// 2. Merge
	for _, src := range tf.Plugins.Sources {
		switch {
		case src.Local != "":
			dir := filepath.Join(filepath.Dir(file), src.Local)
			if info, statErr := os.Stat(dir); statErr != nil || !info.IsDir() {
				return cfg, &SourceNotFoundError{SourceID: src.ID, Path: dir}
			}
			if err := mergePluginRepo(&cfg, dir, &errs); err != nil {
				return cfg, err
			}
		case src.URI != "":
			defs, dupErrs, err := fetchGitSource(cacheDir, src)
			if err != nil {
				return cfg, err
			}
			errs = append(errs, dupErrs...)
			mergeSourceInto(&cfg, defs, &errs)
		default:
			return cfg, &InvalidSourceError{SourceID: src.ID}
		}
	}

	return cfg, errors.Join(errs...)
}

func readTrunkFile(path string) (trunkFile, error) {
	var tf trunkFile
	data, err := os.ReadFile(path)
	if err != nil {
		return tf, &ReadError{Path: path, Err: err}
	}
	if err := yaml.Unmarshal(data, &tf); err != nil {
		return tf, &ParseError{Path: path, Err: err}
	}
	return tf, nil
}

// mergePluginRepo walks every plugin.yaml under dir's category subdirs and merges its
// definitions into cfg, recording any duplicate keys (within dir, or against what's already in
// cfg from an earlier source) into *errs.
func mergePluginRepo(cfg *Config, dir string, errs *[]error) error {
	defs, dupErrs, err := parseSourceDir(dir)
	if err != nil {
		return err
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	setSourceRoot(defs, absDir)
	*errs = append(*errs, dupErrs...)
	mergeSourceInto(cfg, defs, errs)
	return nil
}

// parseSourceDir walks dir's category subdirs and returns everything its plugin.yaml files
// define, unmerged — the per-source shape fetchGitSource caches. dupErrs holds a *DuplicateError
// for every key repeated across the plugin.yaml files within this one dir (e.g. two files each
// defining a tool named "foo"); duplicates against another source are mergeSourceInto's job.
func parseSourceDir(dir string) (defs sourceDefs, dupErrs []error, err error) {
	defs = sourceDefs{
		Downloads: map[string]Download{},
		Tools:     map[string]Tool{},
		Lint:      map[string]Linter{},
		Files:     map[string]FileType{},
		Actions:   map[string]Action{},
		Runtimes:  map[string]Runtime{},
	}

	// The repo-root plugin.yaml (environments:) and each category's own root plugin.yaml (e.g.
	// linters/plugin.yaml's lint.comment_formats:) are optional global config, not per-resource
	// definitions (ARCHITECTURE.md "Built-in / global config") — read whichever are present.
	globalFiles := []string{filepath.Join(dir, "plugin.yaml")}
	for _, category := range pluginCategories {
		globalFiles = append(globalFiles, filepath.Join(dir, category, "plugin.yaml"))
	}
	for _, path := range globalFiles {
		pf, err := readPluginFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return sourceDefs{}, nil, err
		}
		defs.Environments = append(defs.Environments, pf.Environments...)
		defs.CommentFormats = append(defs.CommentFormats, pf.Lint.CommentFormats...)
		mergeKeyed(defs.Files, pf.Lint.Files, func(f FileType) string { return f.Name }, "file", &dupErrs)
	}

	for _, category := range pluginCategories {
		matches, err := filepath.Glob(filepath.Join(dir, category, "*", "plugin.yaml"))
		if err != nil {
			return sourceDefs{}, nil, err // malformed glob pattern; unreachable with our fixed patterns
		}
		for _, path := range matches {
			pf, err := readPluginFile(path)
			if err != nil {
				return sourceDefs{}, nil, err
			}

			// path is always dir/category/<name>/plugin.yaml (built from the Glob pattern just
			// above), so filepath.Dir(path) is always under dir -- Rel cannot fail here.
			sourceDir, relErr := filepath.Rel(dir, filepath.Dir(path))
			if relErr != nil {
				return sourceDefs{}, nil, relErr
			}
			for i := range pf.Lint.Definitions {
				pf.Lint.Definitions[i].SourceDir = sourceDir
			}
			for i := range pf.Actions.Definitions {
				pf.Actions.Definitions[i].SourceDir = sourceDir
			}

			mergeKeyed(defs.Downloads, pf.Downloads, func(d Download) string { return d.Name }, "download", &dupErrs)
			mergeKeyed(defs.Tools, pf.Tools.Definitions, func(t Tool) string { return t.Name }, "tool", &dupErrs)
			mergeKeyed(defs.Lint, pf.Lint.Definitions, func(l Linter) string { return l.Name }, "lint", &dupErrs)
			mergeKeyed(defs.Actions, pf.Actions.Definitions, func(a Action) string { return a.ID }, "action", &dupErrs)
			mergeKeyed(defs.Runtimes, pf.Runtimes.Definitions, func(r Runtime) string { return r.Type }, "runtime", &dupErrs)
		}
	}
	return defs, dupErrs, nil
}

// setSourceRoot stamps root onto every Linter defs.Lint holds -- root is where ${plugin} should
// resolve to for this one source: a local source's own directory (mergePluginRepo), or a git
// source's persisted checkout (fetchGitSource). Map values aren't addressable in Go, so this reads
// each entry, sets the field, and writes it back.
func setSourceRoot(defs sourceDefs, root string) {
	for name, l := range defs.Lint {
		l.SourceRoot = root
		defs.Lint[name] = l
	}
	for name, a := range defs.Actions {
		a.SourceRoot = root
		defs.Actions[name] = a
	}
}

// readPluginFile reads and parses one plugin.yaml. A missing file is returned as the raw
// os.ErrNotExist-wrapping error (unwrapped by errors.Is), not a *ReadError, so callers reading an
// optional global file (see globalFiles above) can tell "absent" from "unreadable".
func readPluginFile(path string) (pluginFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return pluginFile{}, err
		}
		return pluginFile{}, &ReadError{Path: path, Err: err}
	}
	var pf pluginFile
	if err := yaml.Unmarshal(data, &pf); err != nil {
		return pluginFile{}, &ParseError{Path: path, Err: err}
	}
	return pf, nil
}

// mergeSourceInto folds defs into cfg's global maps, recording a *DuplicateError for any key
// already contributed by an earlier source into *errs.
func mergeSourceInto(cfg *Config, defs sourceDefs, errs *[]error) {
	cfg.Environments = append(cfg.Environments, defs.Environments...)
	cfg.Lint.CommentFormats = append(cfg.Lint.CommentFormats, defs.CommentFormats...)
	mergeMapInto(cfg.Downloads, defs.Downloads, "download", errs)
	mergeMapInto(cfg.Tools, defs.Tools, "tool", errs)
	mergeMapInto(cfg.Lint.Definitions, defs.Lint, "lint", errs)
	mergeMapInto(cfg.Lint.Files, defs.Files, "file", errs)
	mergeMapInto(cfg.Actions.Definitions, defs.Actions, "action", errs)
	mergeMapInto(cfg.Runtimes.Definitions, defs.Runtimes, "runtime", errs)
}

// mergeMapInto copies src into dst key by key, recording a *DuplicateError for any key already
// present in dst.
func mergeMapInto[T any](dst map[string]T, src map[string]T, category string, errs *[]error) {
	for k, v := range src {
		if _, exists := dst[k]; exists {
			*errs = append(*errs, &DuplicateError{Category: category, Key: k})
		}
		dst[k] = v
	}
}

// mergeKeyed inserts each item into dst, keyed by key(item). A key already present in dst (from
// an earlier file or source) is fully overwritten — never merged field-by-field — and the
// collision is appended to *errs as a *DuplicateError.
func mergeKeyed[T any](dst map[string]T, items []T, key func(T) string, category string, errs *[]error) {
	for _, item := range items {
		k := key(item)
		if _, exists := dst[k]; exists {
			*errs = append(*errs, &DuplicateError{Category: category, Key: k})
		}
		dst[k] = item
	}
}
