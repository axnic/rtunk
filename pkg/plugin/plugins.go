package plugin

import (
	"fmt"
	"reflect"

	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/config"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/download"
)

// Plugins is every resolved plugins.sources entry, searched together — the
// effective aggregate a Workspace resolves from every configured source.
// A name (category/download/tool/linter/runtime) defined by two different
// sources is a conflict, reported by whichever lookup finds it, naming
// both sources as "repo@ref" — the same "no source silently wins" rule
// buildPlugin already enforces within one source's own files, just
// surfaced per-lookup instead of by an upfront Merge.
type Plugins []*Plugin

// label identifies p in a conflict error: its own plugins.sources id,
// "@ref" appended when set (a Merge/test-built Plugin may have neither).
func label(p *Plugin) string {
	if p.Source.Ref == "" {
		return p.Source.ID
	}
	return p.Source.ID + "@" + p.Source.Ref
}

// merge flattens ps into a single Plugin: Categories deduplicated when
// identical (a genuine divergence is a conflict), every other list
// concatenated with the same per-name conflict check buildPlugin already
// applies within one source. Every exported lookup below funnels through
// this rather than a caller managing a merged Plugin of its own — kept
// unexported so Plugins, not a bare *Plugin, is the only public
// multi-source surface.
func (ps Plugins) merge() (*Plugin, error) {
	merged := &Plugin{Categories: map[string]fileCategoryDef{}}
	seen := map[string]map[string]string{"download": {}, "tool": {}, "linter": {}, "runtime": {}}
	categoryOwner := map[string]string{}

	for _, p := range ps {
		l := label(p)
		for name, c := range p.Categories {
			if owner, ok := categoryOwner[name]; ok {
				if !reflect.DeepEqual(merged.Categories[name], c) {
					return nil, fmt.Errorf("conflicting category %q: differently defined in both %s and %s", name, owner, l)
				}
				continue
			}
			categoryOwner[name] = l
			merged.Categories[name] = c
		}
		for _, d := range p.Downloads {
			if err := claim(seen["download"], "download", d.Name, l); err != nil {
				return nil, err
			}
			merged.Downloads = append(merged.Downloads, d)
		}
		for _, t := range p.Tools {
			if err := claim(seen["tool"], "tool", t.Name, l); err != nil {
				return nil, err
			}
			merged.Tools = append(merged.Tools, t)
		}
		for _, ld := range p.Linters {
			if err := claim(seen["linter"], "linter", ld.Name, l); err != nil {
				return nil, err
			}
			merged.Linters = append(merged.Linters, ld)
		}
		for _, r := range p.Runtimes {
			if err := claim(seen["runtime"], "runtime", r.Type, l); err != nil {
				return nil, err
			}
			merged.Runtimes = append(merged.Runtimes, r)
		}
		merged.Warnings = append(merged.Warnings, p.Warnings...)
	}
	return merged, nil
}

// Category looks up name across every source in ps.
func (ps Plugins) Category(name string) (*fileCategoryDef, error) {
	m, err := ps.merge()
	if err != nil {
		return nil, err
	}
	c, ok := m.Categories[name]
	if !ok {
		return nil, fmt.Errorf("category %q not found", name)
	}
	return &c, nil
}

// Download resolves name (pinned at version, targeting bin — see
// pkg/download.Resolve) across every source in ps.
func (ps Plugins) Download(name, version, bin string) (*config.Download, error) {
	m, err := ps.merge()
	if err != nil {
		return nil, err
	}
	group := findDownloadGroup(m.Downloads, name)
	if group == nil {
		return nil, fmt.Errorf("download %q not found", name)
	}
	dl, reason := download.Resolve(*group, version, bin)
	if dl == nil {
		return nil, fmt.Errorf("download %q: %s", name, reason)
	}
	return dl, nil
}

// Tool resolves name (pinned at version, tt's own KnownGoodVersion if
// empty) across every source in ps.
func (ps Plugins) Tool(name, version string) (Tool, error) {
	m, err := ps.merge()
	if err != nil {
		return Tool{}, err
	}
	tt := findTool(m.Tools, name)
	if tt == nil {
		return Tool{}, fmt.Errorf("tool %q not found", name)
	}
	t, reason := resolveTool(*tt, m.Downloads, name, version)
	if reason != "" {
		return t, fmt.Errorf("%s", reason)
	}
	return t, nil
}

// Runtime resolves name (pinned at version, its own KnownGoodVersion if
// empty) across every source in ps.
func (ps Plugins) Runtime(name, version string) (*Runtime, error) {
	m, err := ps.merge()
	if err != nil {
		return nil, err
	}
	return m.RuntimeFor(name, version)
}

// Linter resolves name (pinned at version) across every source in ps,
// translated into a config.LinterDefinition.
func (ps Plugins) Linter(name, version string) (*config.LinterDefinition, error) {
	m, err := ps.merge()
	if err != nil {
		return nil, err
	}
	enabled := []config.PackageVersion{config.PackageVersion(name)}
	if version != "" {
		enabled[0] = config.PackageVersion(name + "@" + version)
	}
	defs, _, err := m.Load(enabled)
	if err != nil {
		return nil, err
	}
	for _, d := range defs {
		if d.Name == name {
			return &d, nil
		}
	}
	return nil, fmt.Errorf("linter %q not found", name)
}

// Load translates every enabled linter across every source in ps into
// config.LinterDefinition — see Plugin.Load.
func (ps Plugins) Load(enabled []config.PackageVersion) ([]config.LinterDefinition, []string, error) {
	m, err := ps.merge()
	if err != nil {
		return nil, nil, err
	}
	return m.Load(enabled)
}

// DiscoverTools resolves every tool needed across every source in ps for
// cfg — see Plugin.DiscoverTools.
func (ps Plugins) DiscoverTools(toolsCfg config.Tools, lintEnabled []config.PackageVersion) (map[string]Tool, []string) {
	m, err := ps.merge()
	if err != nil {
		return nil, []string{err.Error()}
	}
	return m.DiscoverTools(toolsCfg, lintEnabled)
}
