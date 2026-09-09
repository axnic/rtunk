package plugin

import (
	"testing"

	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/config"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/download"
)

func testPluginForDiscovery() *Plugin {
	return &Plugin{
		Downloads: []download.Group{
			{Name: "gh-dl", Variants: []download.Variant{{OSMap: map[string]string{"linux": "linux", "macos": "darwin"}, CPUMap: map[string]string{"x86_64": "amd64", "arm_64": "arm64"}, URL: "https://example.com/${os}-${cpu}"}}},
		},
		Tools: []trunkTool{
			{Name: "eslint-tool", Runtime: "node", Package: "eslint", KnownGoodVersion: "8.0.0", Shims: []trunkShim{{Name: "eslint"}}},
			{Name: "gh-tool", Download: "gh-dl", KnownGoodVersion: "1.0.0", Shims: []trunkShim{{Name: "gh"}}},
		},
		Linters: []trunkLinter{
			{Name: "eslint", Tools: []string{"eslint-tool"}, Commands: []trunkCommand{{Name: "lint", Output: "sarif", Run: "eslint ${target}"}}},
			{Name: "govet", Commands: []trunkCommand{{Name: "lint", Output: "sarif", Run: "go vet ${target}"}}}, // no tools: reference
		},
	}
}

func TestDiscoverTools_ViaLinterReference(t *testing.T) {
	p := testPluginForDiscovery()
	tools, warnings := p.DiscoverTools(config.Tools{}, []config.PackageVersion{"eslint@8.5.0"})
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %+v", warnings)
	}
	tool, ok := tools["eslint-tool"]
	if !ok {
		t.Fatalf("expected eslint-tool discovered via the eslint linter's own tools: reference, got %+v", tools)
	}
	if tool.PackageInstall == nil || tool.PackageInstall.Version != "8.5.0" {
		t.Errorf("expected the linter's own pinned version to apply, got %+v", tool.PackageInstall)
	}
}

func TestDiscoverTools_ViaToolsEnabled(t *testing.T) {
	p := testPluginForDiscovery()
	tools, warnings := p.DiscoverTools(config.Tools{Enabled: []config.PackageVersion{"gh-tool@2.0.0"}}, nil)
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %+v", warnings)
	}
	tool, ok := tools["gh-tool"]
	if !ok {
		t.Fatalf("expected gh-tool discovered via tools.enabled, got %+v", tools)
	}
	if tool.Download == nil || tool.Download.Version != "2.0.0" {
		t.Errorf("expected the tools.enabled pinned version to apply, got %+v", tool.Download)
	}
}

func TestDiscoverTools_ToolsDefinitionsWinsOverEnabled(t *testing.T) {
	p := testPluginForDiscovery()
	toolsCfg := config.Tools{
		Definitions: []config.ToolDefinition{{Name: "gh-tool", Download: "gh-dl", KnownGoodVersion: "9.9.9", Shims: []string{"gh"}}},
		Enabled:     []config.PackageVersion{"gh-tool@2.0.0"},
	}
	tools, warnings := p.DiscoverTools(toolsCfg, nil)
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %+v", warnings)
	}
	tool := tools["gh-tool"]
	if tool.Download == nil || tool.Download.Version != "9.9.9" {
		t.Errorf("expected tools.definitions' own version to win over tools.enabled's, got %+v", tool.Download)
	}
}

func TestDiscoverTools_UnknownToolsEnabledWarns(t *testing.T) {
	p := testPluginForDiscovery()
	_, warnings := p.DiscoverTools(config.Tools{Enabled: []config.PackageVersion{"nonexistent"}}, nil)
	if len(warnings) != 1 {
		t.Fatalf("expected one warning for the unresolvable tools.enabled entry, got %+v", warnings)
	}
}

func TestDiscoverTools_DisabledLinterContributesNoTool(t *testing.T) {
	p := testPluginForDiscovery()
	// lintEnabled restricts to just govet, which has no tools: reference —
	// eslint-tool must not be discovered even though it exists in the catalog.
	tools, _ := p.DiscoverTools(config.Tools{}, []config.PackageVersion{"govet"})
	if _, ok := tools["eslint-tool"]; ok {
		t.Error("expected eslint-tool to be absent since eslint isn't enabled")
	}
}
