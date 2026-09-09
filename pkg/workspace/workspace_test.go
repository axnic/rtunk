package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/cache"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/config"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/plugin"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// testPlugins builds a Plugins fixture with govet (lint) and gofmt
// (format, in_place) linters — neither declares a runtime, so Resolve
// never attempts a network install for them; runtimesNeeded is exercised
// separately, without requiring a real download.
func testPlugins(t *testing.T) plugin.Plugins {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "linters/govet/plugin.yaml"), `
lint:
  definitions:
    - name: govet
      files: [ALL]
      commands:
        - {name: lint, output: sarif, run: "go vet ${target}"}
`)
	writeFile(t, filepath.Join(dir, "linters/gofmt/plugin.yaml"), `
lint:
  definitions:
    - name: gofmt
      files: [ALL]
      commands:
        - {name: format, output: rewrite, in_place: true, formatter: true, run: "gofmt -w ${target}"}
`)
	p, warnings, err := plugin.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %+v", warnings)
	}
	return plugin.Plugins{p}
}

func testCache(t *testing.T) *cache.Cache {
	t.Helper()
	c, err := cache.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestResolve_MergesCustomAndPluginLinters(t *testing.T) {
	ps := testPlugins(t)
	cfg := &config.Config{
		Lint: config.Lint{
			Definitions: []config.LinterDefinition{{Name: "custom-linter", Files: []config.GlobPattern{"ALL"}, Commands: []config.Command{{Name: "lint", Output: "sarif", Run: "custom ${target}"}}}},
		},
	}
	ws, err := Resolve(cfg, testCache(t), ps, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ws.Linters["govet"]; !ok {
		t.Error("expected the plugin-sourced govet linter")
	}
	if _, ok := ws.Linters["custom-linter"]; !ok {
		t.Error("expected the custom hand-authored linter")
	}
}

func TestResolve_DisabledAlwaysWinsOverCustom(t *testing.T) {
	ps := testPlugins(t)
	cfg := &config.Config{
		Lint: config.Lint{
			Definitions: []config.LinterDefinition{{Name: "govet", Files: []config.GlobPattern{"ALL"}, Commands: []config.Command{{Name: "lint", Output: "sarif", Run: "custom-govet ${target}"}}}},
			Disabled:    []config.PackageVersion{"govet"},
		},
	}
	ws, err := Resolve(cfg, testCache(t), ps, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ws.Linters["govet"]; ok {
		t.Error("expected govet disabled even though a custom definition of the same name exists")
	}
}

func TestResolve_EnabledRestricts(t *testing.T) {
	ps := testPlugins(t)
	cfg := &config.Config{Lint: config.Lint{Enabled: []config.PackageVersion{"gofmt"}}}
	ws, err := Resolve(cfg, testCache(t), ps, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(ws.Linters) != 1 || ws.Linters["gofmt"].Name != "gofmt" {
		t.Errorf("expected only gofmt, got %+v", ws.Linters)
	}
}

func TestResolve_UnresolvableRuntimeWarnsNotFails(t *testing.T) {
	ps := testPlugins(t)
	cfg := &config.Config{Runtimes: config.Runtimes{Enabled: []config.PackageVersion{"nonexistent"}}}
	ws, err := Resolve(cfg, testCache(t), ps, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(ws.Warnings) == 0 {
		t.Fatalf("expected at least one warning, got %+v", ws.Warnings)
	}
	if _, ok := ws.Runtimes["nonexistent"]; ok {
		t.Error("expected no entry for the unresolvable runtime")
	}
}

func TestWorkspace_NewLinter(t *testing.T) {
	ps := testPlugins(t)
	ws, err := Resolve(&config.Config{}, testCache(t), ps, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	l, ok := ws.NewLinter("govet", nil, nil)
	if !ok {
		t.Fatal("expected govet to be found")
	}
	if l.LintersDir != ws.LintersDir || l.ProjectCacheDir != ws.ProjectCacheDir {
		t.Errorf("expected the constructed Linter bound to the workspace's own cache dirs, got %+v", l)
	}
	if _, ok := ws.NewLinter("nonexistent", nil, nil); ok {
		t.Error("expected nonexistent linter lookup to fail")
	}
}

func TestRuntimesNeeded(t *testing.T) {
	enabled := []config.PackageVersion{"node@20.0.0"}
	linters := map[string]config.LinterDefinition{
		"eslint": {Name: "eslint", Runtime: "node"}, // already covered by enabled
		"ruff":   {Name: "ruff", Runtime: "python"}, // auto-discovered
		"govet":  {Name: "govet"},                   // no runtime, ignored
	}
	got := runtimesNeeded(enabled, linters)
	if len(got) != 2 {
		t.Fatalf("expected 2 refs (node pinned once, python auto-discovered), got %+v", got)
	}
	names := map[string]string{}
	for _, pv := range got {
		names[pv.Name()] = pv.Version()
	}
	if names["node"] != "20.0.0" {
		t.Errorf("expected node's pinned version preserved, got %+v", names)
	}
	if v, ok := names["python"]; !ok || v != "" {
		t.Errorf("expected python auto-discovered with no pinned version, got %+v", names)
	}
}
