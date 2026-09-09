// Package workspace resolves a repo's config against a plugin catalog
// (pkg/plugin.Plugins) into a ready-to-run Workspace: enabled linter
// definitions, every tool they need, and every runtime any of them
// depend on — installed (pkg/runtime.Ensure) and bound to this repo's own
// cache area.
package workspace

import (
	"golang.org/x/sync/semaphore"

	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/internal/gitutil"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/internal/progress"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/cache"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/config"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/linter"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/plugin"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/runtime"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/shim"
)

// Workspace is ps (every resolved plugins.sources entry) resolved against
// a repo's config, ready to install and run.
type Workspace struct {
	// Linters are the resolved, enabled linter definitions:
	// cfg.Lint.Definitions (custom, hand-authored), overridden by ps's own
	// plugin-sourced definitions on a name collision — filtered by
	// cfg.Lint.Enabled/Disabled. Look one up by name and pass it to
	// NewLinter, alongside this invocation's own semaphore/progress
	// tracker, to get something to Run.
	Linters map[string]config.LinterDefinition
	// Tools is every tool DiscoverTools found for cfg.
	Tools map[string]plugin.Tool
	// Runtimes is every runtime needed — cfg.Runtimes.Enabled plus any
	// enabled linter's own resolved Runtime not already listed there —
	// already installed (pkg/runtime.Ensure): a package-installed
	// linter's own tool needs its runtime present before it can install
	// (see pkg/tool.EnsureOneProject).
	Runtimes map[string]shim.Shim
	// LintersDir is where linter binaries are cached (pkg/cache's
	// Cache.Linters() result).
	LintersDir string
	// ProjectCacheDir is this repo's own cache area (pkg/cache's
	// Cache.Workspace, keyed by cache.RepoKey) — where a Tool/Runtime's
	// EnsureProject symlinks its shared, version-pinned install.
	ProjectCacheDir string
	// Warnings collected while resolving (Plugins.Load's own,
	// DiscoverTools', an unresolvable runtime...).
	Warnings []string
}

// Resolve builds a Workspace from ps and cfg, for the repo rooted at root
// (used, alongside cfg's own fingerprint, to key ProjectCacheDir —
// c.Workspace/cache.RepoKey — to this specific repo and config).
func Resolve(cfg *config.Config, c *cache.Cache, ps plugin.Plugins, root string) (*Workspace, error) {
	defs, warnings, err := ps.Load(cfg.Lint.Enabled)
	if err != nil {
		return nil, err
	}

	linters := map[string]config.LinterDefinition{}
	for _, d := range cfg.Lint.Definitions { // custom, hand-authored
		linters[d.Name] = d
	}
	for _, d := range defs { // plugin-sourced
		linters[d.Name] = d
	}
	for _, pv := range cfg.Lint.Disabled { // always wins, even over a custom definition
		delete(linters, pv.Name())
	}
	if len(cfg.Lint.Enabled) > 0 {
		for name := range linters {
			if !enabledContains(cfg.Lint.Enabled, name) {
				delete(linters, name)
			}
		}
	}

	tools, toolWarnings := ps.DiscoverTools(cfg.Tools, cfg.Lint.Enabled)
	warnings = append(warnings, toolWarnings...)

	runtimes, runtimeWarnings, err := runtime.Ensure(c, ps, runtimesNeeded(cfg.Runtimes.Enabled, linters))
	if err != nil {
		return nil, err
	}
	warnings = append(warnings, runtimeWarnings...)

	lock, err := cfg.Lock()
	if err != nil {
		return nil, err
	}
	projectCacheDir, err := c.Workspace(cache.Repo{URL: gitutil.RemoteURL(root), Root: root, Lock: lock})
	if err != nil {
		return nil, err
	}
	lintersDir, err := c.Linters()
	if err != nil {
		return nil, err
	}
	return &Workspace{
		Linters:         linters,
		Tools:           tools,
		Runtimes:        runtimes,
		LintersDir:      lintersDir,
		ProjectCacheDir: projectCacheDir,
		Warnings:        warnings,
	}, nil
}

// runtimesNeeded is cfg.Runtimes.Enabled (explicit, pinned versions
// honored) plus every distinct runtime an enabled linter's own resolved
// definition needs but didn't already list — auto-discovered entries
// default to the catalog's own known_good_version (an empty
// PackageVersion.Version()).
func runtimesNeeded(enabled []config.PackageVersion, linters map[string]config.LinterDefinition) []config.PackageVersion {
	seen := map[string]bool{}
	refs := append([]config.PackageVersion{}, enabled...)
	for _, pv := range enabled {
		seen[pv.Name()] = true
	}
	for _, d := range linters {
		if d.Runtime == "" || seen[d.Runtime] {
			continue
		}
		seen[d.Runtime] = true
		refs = append(refs, config.PackageVersion(d.Runtime))
	}
	return refs
}

// NewLinter looks up name in ws.Linters and returns something to Run,
// bound to ws's LintersDir/Runtimes/ProjectCacheDir — sem/prog are
// supplied here rather than stored on Workspace since they're scoped to
// one invocation (a check's own --jobs bound and progress tracker), not
// to the resolved Workspace itself, which the same invocation's check and
// fmt phases share.
func (ws *Workspace) NewLinter(name string, sem *semaphore.Weighted, prog *progress.Tracker) (*linter.Linter, bool) {
	def, ok := ws.Linters[name]
	if !ok {
		return nil, false
	}
	l := linter.NewLinter(def, ws.LintersDir, ws.Runtimes, sem, prog)
	l.ProjectCacheDir = ws.ProjectCacheDir
	return l, true
}

func enabledContains(enabled []config.PackageVersion, name string) bool {
	for _, e := range enabled {
		if e.Name() == name {
			return true
		}
	}
	return false
}
