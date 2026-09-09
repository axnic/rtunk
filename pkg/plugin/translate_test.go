package plugin

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/config"
)

// yamlCategory mirrors a couple of real entries from trunk-io/plugins' root
// linters/plugin.yaml, including one that only inherits (no own extensions)
// and one that's regex-only (no extensions/filenames at all).
var yamlCategory = map[string]fileCategoryDef{
	"yaml":         {Name: "yaml", Extensions: []string{"yaml", "yml"}},
	"go":           {Name: "go", Extensions: []string{"go"}},
	"c/c++-source": {Name: "c/c++-source", Inherit: []string{"c-source", "c++-source"}},
	"c-source":     {Name: "c-source", Extensions: []string{"c"}},
	"c++-source":   {Name: "c++-source", Extensions: []string{"cc", "cpp"}},
	"docker":       {Name: "docker", Regexes: []string{`(?i)(?:^|/)Dockerfile\..+$`}},
}

func TestResolveCategory_Direct(t *testing.T) {
	globs, ok := resolveCategory(yamlCategory, "yaml", map[string]bool{})
	if !ok || len(globs) != 2 || globs[0] != "*.yaml" || globs[1] != "*.yml" {
		t.Errorf("got %+v, ok=%v", globs, ok)
	}
}

func TestResolveCategory_Inherit(t *testing.T) {
	globs, ok := resolveCategory(yamlCategory, "c/c++-source", map[string]bool{})
	if !ok {
		t.Fatal("expected inherit-only category to resolve")
	}
	want := map[config.GlobPattern]bool{"*.c": true, "*.cc": true, "*.cpp": true}
	if len(globs) != len(want) {
		t.Fatalf("got %+v", globs)
	}
	for _, g := range globs {
		if !want[g] {
			t.Errorf("unexpected glob %q", g)
		}
	}
}

func TestResolveCategory_RegexOnlyIsUnresolved(t *testing.T) {
	if _, ok := resolveCategory(yamlCategory, "docker", map[string]bool{}); ok {
		t.Error("a regex-only category can't produce a GlobPattern, expected ok=false")
	}
}

func TestResolveCategory_UnknownIsUnresolved(t *testing.T) {
	if _, ok := resolveCategory(yamlCategory, "nonexistent", map[string]bool{}); ok {
		t.Error("expected an unknown category to be unresolved")
	}
}

func TestTranslateFiles(t *testing.T) {
	globs, warnings := translateFiles([]string{"ALL"}, yamlCategory)
	if len(globs) != 1 || globs[0] != "ALL" || len(warnings) != 0 {
		t.Errorf("ALL: got %+v, warnings=%+v", globs, warnings)
	}

	globs, warnings = translateFiles([]string{"yaml"}, yamlCategory)
	if len(globs) != 2 || globs[0] != "*.yaml" || globs[1] != "*.yml" || len(warnings) != 0 {
		t.Errorf("yaml: got %+v, warnings=%+v", globs, warnings)
	}

	// Unresolved (unknown, or regex-only like "docker") falls back to a
	// guessed "*.<name>" glob, with a warning explaining why.
	globs, warnings = translateFiles([]string{"rust"}, yamlCategory)
	if len(globs) != 1 || globs[0] != "*.rust" || len(warnings) != 1 {
		t.Errorf("unrecognized category fallback: got %+v, warnings=%+v", globs, warnings)
	}
}

func TestTranslateLinter_RegexCommandKept(t *testing.T) {
	ld := trunkLinter{
		Name:  "git-diff-check",
		Files: []string{"ALL"},
		Commands: []trunkCommand{
			{Name: "lint", Output: "regex", ParseRegex: "(?P<path>.*):(?P<line>\\d+):(?P<message>.*)", SuccessCodes: []int{0, 1, 2}, Run: "git diff --check ${target}"},
		},
	}
	def, warnings := translateLinter(ld, nil, nil, nil, "")
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %+v", warnings)
	}
	if len(def.Commands) != 1 || def.Commands[0].Output != "regex" || def.Commands[0].Target != "${file}" {
		t.Fatalf("got %+v", def)
	}
}

// TestTranslateLinter_GoFilesGroupByParent is a regression test: a real run
// of golangci-lint2 with per-file targeting produced hundreds of bogus
// "undefined: X" errors, because a lone .go file lacks its package's sibling
// files — Go linters need "${parent}" grouping, not "${file}".
func TestTranslateLinter_GoFilesGroupByParent(t *testing.T) {
	ld := trunkLinter{
		Name:  "golangci-lint2",
		Files: []string{"go"},
		Commands: []trunkCommand{
			{Name: "lint", Output: "sarif", Run: "golangci-lint run --v2 ${target}"},
		},
	}
	def, _ := translateLinter(ld, nil, nil, nil, "")
	if len(def.Commands) != 1 || def.Commands[0].Target != "${parent}" {
		t.Fatalf("expected ${parent} grouping for a go-files linter, got %+v", def)
	}
}

// TestTranslateLinter_HardcodedParsersAccepted locks in that the three
// linter-specific output types verified against a real run of each tool
// (markdownlint, taplo, checkov's sarif_uri) now translate without warnings.
func TestTranslateLinter_HardcodedParsersAccepted(t *testing.T) {
	for _, output := range []string{"markdownlint", "taplo", "sarif_uri"} {
		ld := trunkLinter{
			Name:     "x",
			Commands: []trunkCommand{{Name: "lint", Output: output, Run: "x ${target}"}},
		}
		def, warnings := translateLinter(ld, nil, nil, nil, "")
		if len(warnings) != 0 {
			t.Errorf("output %q: expected no warnings, got %+v", output, warnings)
		}
		if len(def.Commands) != 1 {
			t.Errorf("output %q: expected the command to translate, got %+v", output, def.Commands)
		}
	}
}

func TestTranslateLinter_NonRegexCommandSkippedWithWarning(t *testing.T) {
	ld := trunkLinter{
		Name:     "some-linter",
		Files:    []string{"toml"},
		Commands: []trunkCommand{{Name: "lint", Output: "some-proprietary-format", Run: "some-linter ${target}"}},
	}
	def, warnings := translateLinter(ld, nil, nil, nil, "")
	if len(def.Commands) != 0 {
		t.Errorf("expected the custom-output command to be skipped, got %+v", def.Commands)
	}
	// One warning for the unresolved "toml" category (no categories map
	// given), one for the unsupported output.
	if len(warnings) != 2 {
		t.Fatalf("expected two warnings, got %+v", warnings)
	}
}

func TestTranslateLinter_InPlaceCommandIgnoresOutputType(t *testing.T) {
	// Real example: taplo/gofmt-as-a-plugin format commands use output:
	// rewrite, which the engine never actually parses for in_place commands.
	ld := trunkLinter{
		Name:  "taplo",
		Files: []string{"toml"},
		Commands: []trunkCommand{
			{Name: "format", Output: "rewrite", InPlace: true, Formatter: true, Run: "taplo format ${target}"},
		},
	}
	def, warnings := translateLinter(ld, nil, nil, nil, "")
	// "toml" has no categories map here either, so it falls back with a warning.
	if len(warnings) != 1 {
		t.Errorf("expected one (files-fallback) warning, got %+v", warnings)
	}
	if len(def.Commands) != 1 || !def.Commands[0].InPlace {
		t.Fatalf("expected the in_place command to translate despite output: rewrite, got %+v", def)
	}
}

func TestTranslateLinter_PythonPackageInstall(t *testing.T) {
	ld := trunkLinter{
		Name:          "yamllint",
		Files:         []string{"yaml"},
		Tools:         []string{"yamllint"},
		DirectConfigs: []string{".yamllint", ".yamllint.yaml", ".yamllint.yml"},
		Commands: []trunkCommand{
			{Output: "regex", ParseRegex: "(?P<path>.*):(?P<line>\\d+):(?P<message>.*)", Run: "yamllint ${target}"},
		},
	}
	tools := []trunkTool{{Name: "yamllint", Runtime: "python", Package: "yamllint", Shims: []trunkShim{{Name: "yamllint"}}, KnownGoodVersion: "1.37.1"}}
	def, warnings := translateLinter(ld, tools, nil, yamlCategory, "")
	if len(warnings) != 0 {
		t.Fatalf("python/npm installs are supported now, expected no warnings, got %+v", warnings)
	}
	if def.PackageInstall == nil || def.PackageInstall.Package != "yamllint" || def.PackageInstall.Version != "1.37.1" {
		t.Fatalf("expected PackageInstall to be set, got %+v", def.PackageInstall)
	}
	if len(def.Commands) != 1 {
		t.Fatalf("expected the regex command to translate, got %+v", def.Commands)
	}
	if def.ConfigFlag != "-c" || len(def.DirectConfigs) != 3 {
		t.Fatalf("expected the hardcoded yamllint config flag and captured direct_configs, got flag=%q configs=%+v", def.ConfigFlag, def.DirectConfigs)
	}
	if len(def.Files) != 2 || def.Files[0] != "*.yaml" || def.Files[1] != "*.yml" {
		t.Errorf("expected the real yaml category's globs, got %+v", def.Files)
	}
}

func TestTranslateLinter_GoPackageInstall(t *testing.T) {
	ld := trunkLinter{
		Name:  "golangci-lint2",
		Files: []string{"go"},
		Tools: []string{"golangci-lint2"},
		Commands: []trunkCommand{
			{Output: "sarif", Run: "golangci-lint run ${target}"},
		},
	}
	tools := []trunkTool{{Name: "golangci-lint2", Runtime: "go", Package: "github.com/golangci/golangci-lint/v2/cmd/golangci-lint", Shims: []trunkShim{{Name: "golangci-lint"}}, KnownGoodVersion: "2.12.2"}}
	def, warnings := translateLinter(ld, tools, nil, yamlCategory, "")
	if len(warnings) != 0 {
		t.Fatalf("go install is supported now, expected no warnings, got %+v", warnings)
	}
	if def.PackageInstall == nil || def.PackageInstall.Runtime != "go" {
		t.Fatalf("expected PackageInstall to be set for go, got %+v", def.PackageInstall)
	}
	if len(def.Commands) != 1 {
		t.Fatalf("expected the sarif command to translate, got %+v", def.Commands)
	}
}

func TestTranslateLinter_UnsupportedPackageManagerWarns(t *testing.T) {
	ld := trunkLinter{
		Name:  "some-perl-linter",
		Files: []string{"perl"},
		Tools: []string{"some-perl-linter"},
		Commands: []trunkCommand{
			{Output: "sarif", Run: "some-perl-linter ${target}"},
		},
	}
	tools := []trunkTool{{Name: "some-perl-linter", Runtime: "perl", Package: "some-perl-linter"}}
	def, warnings := translateLinter(ld, tools, nil, nil, "")
	if def.PackageInstall != nil {
		t.Errorf("perl install isn't supported, PackageInstall should stay nil, got %+v", def.PackageInstall)
	}
	// One warning for the unresolved "perl" category (no categories map),
	// one for the unsupported perl package manager.
	if len(warnings) != 2 {
		t.Fatalf("expected two warnings, got %+v", warnings)
	}
}

func TestLoad_WalksLintersDirectory(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "linters/git-diff-check/plugin.yaml"), `
lint:
  definitions:
    - name: git-diff-check
      files: [ALL]
      commands:
        - name: lint
          output: regex
          parse_regex: (?P<path>.*):(?P<line>-?\d+):(?P<message>.*)
          success_codes: [0, 1, 2]
          run: git diff --check ${target}
`)
	writeFile(t, filepath.Join(dir, "linters/some-linter/plugin.yaml"), `
lint:
  definitions:
    - name: some-linter
      files: [toml]
      commands:
        - name: lint
          output: some-proprietary-format
          run: some-linter ${target}
`)

	defs, warnings, err := load(t, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 1 || defs[0].Name != "git-diff-check" {
		t.Fatalf("expected only git-diff-check to survive translation, got %+v", defs)
	}
	// unsupported output + unresolved "toml" category (no root plugin.yaml here).
	if len(warnings) != 2 {
		t.Fatalf("expected two warnings, got %+v", warnings)
	}
}

func TestLoad_RestrictsToEnabled(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "linters/git-diff-check/plugin.yaml"), `
lint:
  definitions:
    - name: git-diff-check
      files: [ALL]
      commands:
        - {name: lint, output: regex, parse_regex: "(?P<path>.*)", run: "git diff --check ${target}"}
`)
	writeFile(t, filepath.Join(dir, "linters/markdownlint/plugin.yaml"), `
lint:
  definitions:
    - name: markdownlint
      files: [markdown]
      commands:
        - {name: lint, output: regex, parse_regex: "(?P<path>.*)", run: "markdownlint ${target}"}
`)

	defs, warnings, err := load(t, dir, []config.PackageVersion{"markdownlint@0.45.0"})
	if err != nil {
		t.Fatal(err)
	}
	// unresolved "markdown" category (no root plugin.yaml here).
	if len(warnings) != 1 {
		t.Fatalf("expected one (files-fallback) warning, got %+v", warnings)
	}
	if len(defs) != 1 || defs[0].Name != "markdownlint" {
		t.Fatalf("expected only the enabled markdownlint (not git-diff-check), got %+v", defs)
	}
}

// TestLoad_MultipleDefinitionsPerFile is a regression test: a single
// plugin.yaml can define several differently-named linters (real example:
// trunk-io/plugins' linters/golangci-lint/plugin.yaml defines both
// "golangci-lint" and "golangci-lint2") — filtering by directory name before
// parsing would silently drop "golangci-lint2" even though it's enabled.
func TestLoad_MultipleDefinitionsPerFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "linters/golangci-lint/plugin.yaml"), `
lint:
  definitions:
    - name: golangci-lint
      files: [go]
      commands:
        - {name: lint, output: regex, parse_regex: "(?P<path>.*)", run: "golangci-lint run ${target}"}
    - name: golangci-lint2
      files: [go]
      commands:
        - {name: lint, output: regex, parse_regex: "(?P<path>.*)", run: "golangci-lint run --v2 ${target}"}
`)

	defs, _, err := load(t, dir, []config.PackageVersion{"golangci-lint2@2.12.2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 1 || defs[0].Name != "golangci-lint2" {
		t.Fatalf("expected golangci-lint2 to survive (directory is named golangci-lint), got %+v", defs)
	}
}

// TestTranslateLinter_PinnedVersionWinsOverPluginDefault is a regression
// test: this repo's own trunk.yaml pins golangci-lint2@2.12.2, but the
// plugin's own known_good_version snapshot was an older, no-longer-valid
// 2.6.1 — the repo's pin must always win.
func TestTranslateLinter_PinnedVersionWinsOverPluginDefault(t *testing.T) {
	ld := trunkLinter{
		Name:  "golangci-lint2",
		Tools: []string{"golangci-lint2"},
		Commands: []trunkCommand{
			{Output: "sarif", Run: "golangci-lint run ${target}"},
		},
	}
	tools := []trunkTool{{Name: "golangci-lint2", Runtime: "go", Package: "github.com/golangci/golangci-lint/v2/cmd/golangci-lint", Shims: []trunkShim{{Name: "golangci-lint"}}, KnownGoodVersion: "2.6.1"}}
	def, _ := translateLinter(ld, tools, nil, nil, "2.12.2")
	if def.PackageInstall == nil || def.PackageInstall.Version != "2.12.2" {
		t.Fatalf("expected the pinned version to override the plugin default, got %+v", def.PackageInstall)
	}
}

// TestTranslateLinter_PlatformFilteringFirstMatchWins is a regression test:
// checkov's real plugin.yaml declares two commands both named "lint" — one
// restricted to platforms: [windows] (checkov.cmd), one unrestricted (plain
// checkov) — running both unconditionally made the Windows-only variant
// fail with "exec format error" on macOS/Linux.
func TestTranslateLinter_PlatformFilteringFirstMatchWins(t *testing.T) {
	ld := trunkLinter{
		Name: "checkov",
		Commands: []trunkCommand{
			{Name: "lint", Output: "sarif_uri", Run: "checkov.cmd -f ${target}", Platforms: []string{"windows"}},
			{Name: "lint", Output: "sarif_uri", Run: "checkov -f ${target}"},
		},
	}
	def, warnings := translateLinter(ld, nil, nil, nil, "")
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %+v", warnings)
	}
	if len(def.Commands) != 1 {
		t.Fatalf("expected exactly one command (the windows-only one filtered out), got %+v", def.Commands)
	}
	if def.Commands[0].Run != "checkov -f ${target}" {
		t.Errorf("expected the platform-unrestricted command to win, got %q", def.Commands[0].Run)
	}
}

func TestPlatformMatches(t *testing.T) {
	if !platformMatches(nil) {
		t.Error("no platforms restriction should always match")
	}
	if !platformMatches([]string{"macos", "linux"}) && runtime.GOOS == "darwin" {
		t.Error("expected macos to match darwin")
	}
	if platformMatches([]string{"windows"}) && runtime.GOOS != "windows" {
		t.Error("expected windows-only to not match a non-windows GOOS")
	}
}

// TestLoad_ObjectShapedShims is a regression test using the real
// graphql-schema-linter/terragrunt plugin.yaml shape (github.com/axnic/
// trunk-plugins, and trunk-io/plugins upstream): shims is a list of
// {name, target} objects there instead of plain strings — trunkTool.Shims
// being []string made yaml.Unmarshal fail on the whole file ("cannot
// unmarshal map[string]interface{} into ... Shims of type string"),
// dropping every linter in it, not just the one with the unusual shape.
func TestLoad_ObjectShapedShims(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "linters/graphql-schema-linter/plugin.yaml"), `
tools:
  definitions:
    - name: graphql-schema-linter
      runtime: node
      package: graphql-schema-linter
      known_good_version: 3.0.1
      shims:
        - name: graphql-schema-linter
          target: graphql-schema-linter
lint:
  definitions:
    - name: graphql-schema-linter
      tools: [graphql-schema-linter]
      files: [ALL]
      commands:
        - name: lint
          output: sarif
          run: graphql-schema-linter -f json ${target}
          success_codes: [0, 1]
`)
	defs, warnings, err := load(t, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %+v", warnings)
	}
	if len(defs) != 1 || defs[0].Name != "graphql-schema-linter" {
		t.Fatalf("expected graphql-schema-linter to translate, got %+v", defs)
	}
	if defs[0].PackageInstall == nil || len(defs[0].PackageInstall.Shims) != 1 || defs[0].PackageInstall.Shims[0] != "graphql-schema-linter" {
		t.Errorf("expected the object-shaped shim's name to resolve, got %+v", defs[0].PackageInstall)
	}
}

// TestLoad_TrufflehogRealDownloadBlock is a regression test using the real
// trufflehog plugin.yaml shape (github.com/trunk-io/plugins, both v1.10.2
// and main): its tool references a top-level `downloads:` block via
// `download: trufflehog`, not `package:` — earlier code silently left it
// with no install mechanism at all (no warning, no Download, "executable
// not found" at run time) because it only checked `package`.
func TestLoad_TrufflehogRealDownloadBlock(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "linters/trufflehog/plugin.yaml"), `
downloads:
  - name: trufflehog
    downloads:
      - os:
          linux: linux
          macos: darwin
          windows: windows
        cpu:
          x86_64: amd64
          arm_64: arm64
        url: https://github.com/trufflesecurity/trufflehog/releases/download/v${version}/trufflehog_${version}_${os}_${cpu}.tar.gz
tools:
  definitions:
    - name: trufflehog
      download: trufflehog
      shims: [trufflehog]
      known_good_version: 3.90.13
lint:
  definitions:
    - name: trufflehog
      files: [ALL]
      tools: [trufflehog]
      commands:
        - name: lint
          output: sarif
          run: trufflehog filesystem --json --fail --only-verified --no-update ${target}
          success_codes: [0, 183]
`)
	defs, warnings, err := load(t, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %+v", warnings)
	}
	if len(defs) != 1 {
		t.Fatalf("expected trufflehog to translate, got %+v", defs)
	}
	dl := defs[0].Download
	if dl == nil {
		t.Fatal("expected Download to be resolved from the top-level downloads: block")
	}
	if dl.Version != "3.90.13" || dl.Bin != "trufflehog" {
		t.Errorf("unexpected Download: %+v", dl)
	}
	// Resolved for the current machine (single-entry, GOOS/GOARCH-keyed —
	// what pkg/tool's osName/archName expect), not the raw multi-key
	// trunk-family map from the YAML. trufflehog's own map happens to
	// substitute the same string as runtime.GOOS/GOARCH themselves.
	if dl.OSMap[runtime.GOOS] != runtime.GOOS || dl.ArchMap[runtime.GOARCH] != runtime.GOARCH {
		t.Errorf("expected os_map/arch_map resolved for %s/%s, got %+v", runtime.GOOS, runtime.GOARCH, dl)
	}
	if !strings.Contains(dl.URL, "${version}") || !strings.Contains(dl.URL, "${os}") {
		t.Errorf("expected the URL template preserved verbatim, got %q", dl.URL)
	}
}

// load builds dir's catalog (no git clone, no cache — the equivalent of
// Resolve's discover+merge steps only, for tests that hand-write
// linters/*/plugin.yaml fixtures directly) and translates it, mirroring
// what a real caller does with Resolve(...).Load(enabled).
func load(t *testing.T, dir string, enabled []config.PackageVersion) ([]config.LinterDefinition, []string, error) {
	t.Helper()
	cat, _, err := buildPlugin(dir)
	if err != nil {
		return nil, nil, err
	}
	return cat.Load(enabled)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
