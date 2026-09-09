package plugin

import (
	"os"
	"path/filepath"
	"testing"

	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/config"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/download"
)

func TestBuildPlugin_MergesAcrossFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "linters/govet/plugin.yaml"), `
lint:
  definitions:
    - name: govet
      files: [go]
      commands:
        - {name: lint, output: regex, parse_regex: "(?P<path>.*)", run: "go vet ${target}"}
`)
	writeFile(t, filepath.Join(dir, "linters/gofmt/plugin.yaml"), `
lint:
  definitions:
    - name: gofmt
      files: [go]
      commands:
        - {name: format, output: rewrite, in_place: true, formatter: true, run: "gofmt -w ${target}"}
`)

	cat, warnings, err := buildPlugin(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %+v", warnings)
	}
	if len(cat.Linters) != 2 {
		t.Fatalf("expected both linters merged, got %+v", cat.Linters)
	}
}

// TestBuildPlugin_ConflictingLinterNameFails is the "plugin A vs plugin B"
// case: two different plugin.yaml files in the same source both defining a
// linter with the same name must fail loud, not silently let one win.
func TestBuildPlugin_ConflictingLinterNameFails(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "linters/a/plugin.yaml"), `
lint:
  definitions:
    - name: dup
      files: [go]
      commands:
        - {name: lint, output: regex, parse_regex: "(?P<path>.*)", run: "a ${target}"}
`)
	writeFile(t, filepath.Join(dir, "linters/b/plugin.yaml"), `
lint:
  definitions:
    - name: dup
      files: [go]
      commands:
        - {name: lint, output: regex, parse_regex: "(?P<path>.*)", run: "b ${target}"}
`)

	_, _, err := buildPlugin(dir)
	if err == nil {
		t.Fatal("expected a conflict error for the duplicate linter name, got nil")
	}
}

func TestBuildPlugin_ConflictingToolNameFails(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "linters/a/plugin.yaml"), `
tools:
  definitions:
    - {name: dup-tool, runtime: python, package: foo}
`)
	writeFile(t, filepath.Join(dir, "linters/b/plugin.yaml"), `
tools:
  definitions:
    - {name: dup-tool, runtime: node, package: bar}
`)

	_, _, err := buildPlugin(dir)
	if err == nil {
		t.Fatal("expected a conflict error for the duplicate tool name, got nil")
	}
}

func TestBuildPlugin_ConflictingDownloadNameFails(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "linters/a/plugin.yaml"), `
downloads:
  - name: dup-download
    downloads:
      - os: {linux: linux}
        cpu: {x86_64: amd64}
        url: https://example.com/a
`)
	writeFile(t, filepath.Join(dir, "linters/b/plugin.yaml"), `
downloads:
  - name: dup-download
    downloads:
      - os: {linux: linux}
        cpu: {x86_64: amd64}
        url: https://example.com/b
`)

	_, _, err := buildPlugin(dir)
	if err == nil {
		t.Fatal("expected a conflict error for the duplicate download group name, got nil")
	}
}

func TestBuildPlugin_MalformedFileWarnsAndSkips(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "linters/broken/plugin.yaml"), "not: [valid: yaml")
	writeFile(t, filepath.Join(dir, "linters/ok/plugin.yaml"), `
lint:
  definitions:
    - name: ok
      files: [go]
      commands:
        - {name: lint, output: regex, parse_regex: "(?P<path>.*)", run: "ok ${target}"}
`)

	cat, warnings, err := buildPlugin(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected one warning for the malformed file, got %+v", warnings)
	}
	if len(cat.Linters) != 1 || cat.Linters[0].Name != "ok" {
		t.Fatalf("expected the well-formed file's linter to still be merged, got %+v", cat.Linters)
	}
}

func TestBuildPlugin_RuntimesDirectoryOptional(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "linters/govet/plugin.yaml"), `
lint:
  definitions:
    - name: govet
      files: [go]
      commands:
        - {name: lint, output: regex, parse_regex: "(?P<path>.*)", run: "go vet ${target}"}
`)
	// No runtimes/ directory at all: plenty of real plugin sources define
	// none, so this must not be an error the way a missing linters/ is.
	cat, warnings, err := buildPlugin(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %+v", warnings)
	}
	if len(cat.Runtimes) != 0 {
		t.Errorf("expected no runtimes, got %+v", cat.Runtimes)
	}
}

// TestBuildPlugin_ParsesRealNodeRuntime is a regression test using the
// real runtimes/node/plugin.yaml (github.com/trunk-io/plugins, v1.10.2)
// verbatim: a multi-variant downloads block plus a runtimes.definitions
// entry with both runtime_environment and linter_environment blocks.
func TestBuildPlugin_ParsesRealNodeRuntime(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "linters"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "runtimes/node/plugin.yaml"), `
version: 0.1
downloads:
  - name: node
    downloads:
      - os: macos
        url: https://nodejs.org/dist/v${version}/node-v${version}-darwin-x64.tar.gz
        version: <16.0.0
        strip_components: 1
      - os:
          linux: linux
          macos: darwin
        cpu:
          x86_64: x64
          arm_64: arm64
        url: https://nodejs.org/dist/v${version}/node-v${version}-${os}-${cpu}.tar.gz
        strip_components: 1
      - os: windows
        cpu: x86_64
        url: https://nodejs.org/dist/v${version}/node-v${version}-win-x64.zip

runtimes:
  definitions:
    - type: node
      download: node
      runtime_environment:
        - name: HOME
          value: ${env.HOME:-}
        - name: PATH
          list:
            - "${runtime}/bin"
            - "${runtime}"
            - "${env.PATH}"
        - name: http_proxy
          value: ${env.http_proxy}
          optional: true
      linter_environment:
        - name: PATH
          list: ["${linter}/node_modules/.bin"]
        - name: NODE_PATH
          value: ${linter}/node_modules
      known_good_version: 22.16.0
      version_commands:
        - run: node --version
          parse_regex: ${semver}
      shims: [node, npm, npx, corepack]
`)
	cat, warnings, err := buildPlugin(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %+v", warnings)
	}
	if len(cat.Runtimes) != 1 || cat.Runtimes[0].Type != "node" {
		t.Fatalf("expected the node runtime to be captured, got %+v", cat.Runtimes)
	}
	rt := cat.Runtimes[0]
	if rt.Download != "node" || rt.KnownGoodVersion != "22.16.0" {
		t.Errorf("got %+v", rt)
	}
	if len(rt.Shims) != 4 || rt.Shims[0].Name != "node" {
		t.Errorf("expected 4 plain-string shims, got %+v", rt.Shims)
	}
	if len(rt.RuntimeEnvironment) != 3 || len(rt.LinterEnvironment) != 2 {
		t.Errorf("expected both environment blocks captured, got runtime=%+v linter=%+v", rt.RuntimeEnvironment, rt.LinterEnvironment)
	}

	group := findDownloadGroup(cat.Downloads, "node")
	if group == nil {
		t.Fatal("expected the node download group to be merged from runtimes/node/plugin.yaml")
	}
	if len(group.Variants) != 3 {
		t.Errorf("expected all 3 variants preserved, got %+v", group.Variants)
	}
}

func TestBuildPlugin_RuntimeDownloadConflictsWithLinterDownload(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "linters/dup/plugin.yaml"), `
downloads:
  - name: dup
    downloads:
      - os: {linux: linux}
        cpu: {x86_64: amd64}
        url: https://example.com/a
`)
	writeFile(t, filepath.Join(dir, "runtimes/dup/plugin.yaml"), `
downloads:
  - name: dup
    downloads:
      - os: {linux: linux}
        cpu: {x86_64: amd64}
        url: https://example.com/b
`)
	_, _, err := buildPlugin(dir)
	if err == nil {
		t.Fatal("expected a conflict error for the same download name across linters/ and runtimes/, got nil")
	}
}

func TestParseCategories_RealShapeIncludingInherit(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "linters/plugin.yaml"), `
version: 0.1
lint:
  files:
    - name: yaml
      extensions: [yaml, yml]
    - name: c-source
      extensions: [c]
    - name: c++-source
      extensions: [cc, cpp]
    - name: c/c++-source
      inherit: [c-source, c++-source]
    - name: docker
      regexes: ["(?i)(?:^|/)Dockerfile\\..+$"]
`)

	categories := parseCategories(dir)
	if len(categories) != 5 {
		t.Fatalf("expected 5 categories, got %+v", categories)
	}
	globs, ok := resolveCategory(categories, "c/c++-source", map[string]bool{})
	if !ok || len(globs) != 3 {
		t.Errorf("expected inherit resolution across the parsed categories, got %+v, ok=%v", globs, ok)
	}
}

func TestParseCategories_MissingFileReturnsNil(t *testing.T) {
	if categories := parseCategories(t.TempDir()); categories != nil {
		t.Errorf("expected nil for a source without a root linters/plugin.yaml, got %+v", categories)
	}
}

// TestPlugin_SaveOpenRoundTrip locks in that a gob-encoded Plugin
// (Plugin.Save) decodes back identically via Open — used by any consumer
// that opts into caching (this package itself never does, see Resolve's
// doc comment).
func TestPluginsMerge_CombinesDistinctSources(t *testing.T) {
	a := &Plugin{
		Source:     config.PluginSource{ID: "a"},
		Categories: map[string]fileCategoryDef{"yaml": {Name: "yaml", Extensions: []string{"yaml", "yml"}}},
		Linters:    []trunkLinter{{Name: "govet"}},
		Tools:      []trunkTool{{Name: "govet-tool"}},
		Downloads:  []download.Group{{Name: "govet-dl"}},
		Runtimes:   []trunkRuntime{{Type: "go"}},
		Warnings:   []string{"a warning"},
	}
	b := &Plugin{
		Source:     config.PluginSource{ID: "b"},
		Categories: map[string]fileCategoryDef{"yaml": {Name: "yaml", Extensions: []string{"yaml", "yml"}}}, // identical: not a conflict
		Linters:    []trunkLinter{{Name: "gofmt"}},
	}

	merged, err := Plugins{a, b}.merge()
	if err != nil {
		t.Fatal(err)
	}
	if len(merged.Linters) != 2 || len(merged.Tools) != 1 || len(merged.Downloads) != 1 || len(merged.Runtimes) != 1 {
		t.Errorf("expected everything concatenated, got %+v", merged)
	}
	if len(merged.Categories) != 1 {
		t.Errorf("expected the identical category deduplicated, got %+v", merged.Categories)
	}
	if len(merged.Warnings) != 1 {
		t.Errorf("expected a's warning carried through, got %+v", merged.Warnings)
	}
}

func TestPluginsMerge_ConflictingLinterNameAcrossSourcesFails(t *testing.T) {
	a := &Plugin{Source: config.PluginSource{ID: "a"}, Linters: []trunkLinter{{Name: "dup"}}}
	b := &Plugin{Source: config.PluginSource{ID: "b"}, Linters: []trunkLinter{{Name: "dup"}}}
	if _, err := (Plugins{a, b}).merge(); err == nil {
		t.Fatal("expected a conflict error for the same linter name across two sources, got nil")
	}
}

// TestMerge_ConflictingCategoryDivergesFails is the exception to
// TestMerge_CombinesDistinctSources' dedup case: the SAME category name
// with DIFFERENT content across sources is a real conflict, not silently
// resolved by picking either.
func TestPluginsMerge_ConflictingCategoryDivergesFails(t *testing.T) {
	a := &Plugin{Source: config.PluginSource{ID: "a"}, Categories: map[string]fileCategoryDef{"yaml": {Name: "yaml", Extensions: []string{"yaml"}}}}
	b := &Plugin{Source: config.PluginSource{ID: "b"}, Categories: map[string]fileCategoryDef{"yaml": {Name: "yaml", Extensions: []string{"yml"}}}}
	if _, err := (Plugins{a, b}).merge(); err == nil {
		t.Fatal("expected a conflict error for divergently-defined categories, got nil")
	}
}

// TestMerge_PreservesPerLinterDir is a regression test: each source's own
// linters must remember their own clone Dir (trunkLinter.Dir) through a
// Merge, since the merged Plugin has no single Dir of its own to fall
// back on for ${plugin} resolution.
func TestPluginsMerge_PreservesPerLinterDir(t *testing.T) {
	cmds := []trunkCommand{{Name: "lint", Output: "sarif", Run: "x ${target}"}}
	a := &Plugin{Source: config.PluginSource{ID: "a"}, Dir: "/cache/a", Linters: []trunkLinter{{Name: "x", Dir: "/cache/a", Commands: cmds}}}
	b := &Plugin{Source: config.PluginSource{ID: "b"}, Dir: "/cache/b", Linters: []trunkLinter{{Name: "y", Dir: "/cache/b", Commands: cmds}}}
	merged, err := Plugins{a, b}.merge()
	if err != nil {
		t.Fatal(err)
	}
	defs, _, err := merged.Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	dirs := map[string]string{}
	for _, d := range defs {
		dirs[d.Name] = d.PluginDir
	}
	if dirs["x"] != "/cache/a" || dirs["y"] != "/cache/b" {
		t.Errorf("expected each linter's own Dir preserved through Merge, got %+v", dirs)
	}
}

func TestPlugin_SaveOpenRoundTrip(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "linters/govet/plugin.yaml"), `
lint:
  definitions:
    - name: govet
      files: [go]
      commands:
        - {name: lint, output: regex, parse_regex: "(?P<path>.*)", run: "go vet ${target}"}
`)
	cat, _, err := buildPlugin(dir)
	if err != nil {
		t.Fatal(err)
	}
	cat.Source = config.PluginSource{ID: "test", URI: "https://example.com/repo", Ref: "v1.0.0"}
	cat.Dir = dir

	path := filepath.Join(t.TempDir(), "catalog.gob")
	if err := cat.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Linters) != 1 || got.Linters[0].Name != "govet" {
		t.Errorf("expected govet to survive the round trip, got %+v", got.Linters)
	}
	if got.Source != cat.Source || got.Dir != cat.Dir {
		t.Errorf("expected Source/Dir to survive the round trip, got %+v", got)
	}
}

// TestStore_UsesConventionalCachePath is a regression test: Store must
// write exactly where CachePath says, so a caller checking CachePath
// before calling Resolve (rather than always resolving fresh) finds it.
func TestStore_UsesConventionalCachePath(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "linters/govet/plugin.yaml"), `
lint:
  definitions:
    - name: govet
      files: [go]
      commands:
        - {name: lint, output: regex, parse_regex: "(?P<path>.*)", run: "go vet ${target}"}
`)
	cat, _, err := buildPlugin(dir)
	if err != nil {
		t.Fatal(err)
	}
	source := config.PluginSource{ID: "test", URI: "https://example.com/repo", Ref: "v1.0.0"}
	cat.Source = source
	cat.Dir = dir

	cacheDir := t.TempDir()
	if err := Store(cacheDir, cat); err != nil {
		t.Fatal(err)
	}
	got, err := Open(CachePath(cacheDir, source))
	if err != nil {
		t.Fatalf("expected Store to have written at CachePath, got %v", err)
	}
	if len(got.Linters) != 1 || got.Linters[0].Name != "govet" {
		t.Errorf("got %+v", got.Linters)
	}
}
