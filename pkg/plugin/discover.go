package plugin

import (
	"fmt"

	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/config"
)

// DiscoverTools resolves the full set of Tools a config needs from p:
// every custom tools.Definitions entry (hand-authored; its own Download
// string, if set, resolved against p's own merged Downloads — the only
// place a repo-level "download" name could mean anything, since a real
// trunk.yaml never defines its own top-level downloads: section), every
// tools.Enabled entry matched against p.Tools, and every tool an enabled
// linter (lintEnabled, matched against p.Linters) itself declares via its
// own `tools:` reference — trunk's own linters name a tool rather than
// embedding an install recipe directly, so "every tool lint.enabled
// needs" means walking through them too.
//
// A tool a later step would re-discover keeps its first resolution:
// tools.Definitions wins over tools.Enabled wins over a linter's own
// reference, matching how a repo's explicit declarations should take
// precedence over one merely inferred from an enabled linter's dependency.
func (p *Plugin) DiscoverTools(toolsCfg config.Tools, lintEnabled []config.PackageVersion) (map[string]Tool, []string) {
	found := map[string]Tool{}
	var warnings []string

	for _, td := range toolsCfg.Definitions {
		t, w := p.resolveToolDefinition(td)
		if w != "" {
			warnings = append(warnings, w)
		}
		found[td.Name] = t
	}

	for _, pv := range toolsCfg.Enabled {
		name := pv.Name()
		if _, ok := found[name]; ok {
			continue
		}
		tt := findTool(p.Tools, name)
		if tt == nil {
			warnings = append(warnings, fmt.Sprintf("tools.enabled %s: not found in this catalog's tools", name))
			continue
		}
		t, w := resolveTool(*tt, p.Downloads, name, pv.Version())
		if w != "" {
			warnings = append(warnings, w)
		}
		found[name] = t
	}

	for _, ld := range p.Linters {
		if len(lintEnabled) > 0 && !enabledContains(lintEnabled, ld.Name) {
			continue
		}
		for _, toolName := range ld.Tools {
			if _, ok := found[toolName]; ok {
				continue
			}
			tt := findTool(p.Tools, toolName)
			if tt == nil {
				continue // Plugin.Load already warns about this when it runs
			}
			t, w := resolveTool(*tt, p.Downloads, toolName, enabledVersion(lintEnabled, ld.Name))
			if w != "" {
				warnings = append(warnings, w)
			}
			found[toolName] = t
		}
	}

	return found, warnings
}

// resolveToolDefinition resolves a hand-authored tools.definitions entry
// (config.ToolDefinition) into a Tool.
func (p *Plugin) resolveToolDefinition(td config.ToolDefinition) (Tool, string) {
	if td.Download == "" {
		return Tool{Name: td.Name}, ""
	}
	shims := make([]trunkShim, len(td.Shims))
	for i, s := range td.Shims {
		shims[i] = trunkShim{Name: s}
	}
	tt := trunkTool{Name: td.Name, Download: td.Download, KnownGoodVersion: td.KnownGoodVersion, Shims: shims}
	return resolveTool(tt, p.Downloads, td.Name, "")
}
