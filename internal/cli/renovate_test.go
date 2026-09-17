package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeToolLinterFixture is writeLinterFixture's (check_run_test.go) tools+lint extension: it
// writes a plugin repo whose plugin.yaml declares a top-level downloads: recipe (real shape --
// see pkg/trunk/config/testdata/pluginrepo/linters/shellcheck/plugin.yaml -- a flat list, NOT
// nested under definitions:), a tools.definitions[] entry referencing it via download:, and a
// lint.definitions[] entry referencing that tool via tools: [<toolName>] -- the minimal real
// shape renovate.ForLint's Linter->Tools[0]->Tool bridge needs. Also writes a second
// plugins.sources[] entry (git-shaped: uri+ref, no local) purely as *yaml.Node content for
// annotateDoc to walk -- it is never resolved by config.ResolveAll (Local is the only source
// config.Resolve actually reads in this repo's test fixtures; see the shape below), so this
// stays hermetic (no network fetch).
func writeToolLinterFixture(t *testing.T, enabled []string, toolName, owner, repo, knownGoodVersion string) (cfgPath, repoRoot string) {
	t.Helper()
	repoRoot = t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, ".trunk"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "pluginrepo", "linters", "fixture"), 0o755))

	enabledYAML := ""
	for _, e := range enabled {
		enabledYAML += "    - " + e + "\n"
	}
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, ".trunk", "trunk.yaml"), []byte(`version: "0.1"
plugins:
  sources:
    - id: local
      local: ../pluginrepo
    - id: origin
      uri: https://github.com/`+owner+`/`+repo+`
      ref: v1.0.0
lint:
  enabled:
`+enabledYAML), 0o644))

	pluginYAML := fmt.Sprintf(`downloads:
  - name: %[1]s-download
    version: 1.0.0
    downloads:
      - os: { linux: linux, macos: macos, windows: windows }
        cpu: { x86_64: x86_64, arm_64: arm_64 }
        url: https://github.com/%[2]s/%[3]s/releases/download/v${version}/%[1]s.tar.gz
tools:
  definitions:
    - name: %[1]s
      download: %[1]s-download
      known_good_version: %[4]s
lint:
  definitions:
    - name: %[1]s
      files: [ALL]
      tools: [%[1]s]
      description: fixture linter
      commands:
        - name: lint
          run: echo unused
          output: xml
`, toolName, owner, repo, knownGoodVersion)
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "pluginrepo", "linters", "fixture", "plugin.yaml"), []byte(pluginYAML), 0o644))
	return filepath.Join(repoRoot, ".trunk", "trunk.yaml"), repoRoot
}

func TestRenovateAnnotate_UnpinnedResolvableEntry_GetsCommentAndPin(t *testing.T) {
	cfgPath, _ := writeToolLinterFixture(t, []string{"fixture"}, "fixture", "acme", "widget", "1.2.3")

	_, stderr, err := run2(t, "--config", cfgPath, "renovate", "annotate")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Contains(t, string(got), "# renovate: datasource=github-releases depName=acme/widget\n    - fixture@1.2.3\n")
}

func TestRenovateAnnotate_AlreadyPinnedEntry_GetsCommentKeepsVersion(t *testing.T) {
	cfgPath, _ := writeToolLinterFixture(t, []string{"fixture@9.9.9"}, "fixture", "acme", "widget", "1.2.3")

	_, stderr, err := run2(t, "--config", cfgPath, "renovate", "annotate")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Contains(t, string(got), "# renovate: datasource=github-releases depName=acme/widget\n    - fixture@9.9.9\n")
}

func TestRenovateAnnotate_UnresolvableLinter_LeftUntouched(t *testing.T) {
	// A linter enabled with no matching definition at all is never resolvable (no Tools[]
	// bridge exists) -- must be left exactly as-is, no comment, no forced pin.
	cfgPath, _ := writeToolLinterFixture(t, []string{"fixture", "phantom"}, "fixture", "acme", "widget", "1.2.3")

	_, stderr, err := run2(t, "--config", cfgPath, "renovate", "annotate")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Contains(t, string(got), "phantom\n")
	assert.NotContains(t, string(got), "phantom@")
}

func TestRenovateAnnotate_PluginSourceRef_GetsCommentOnKeyLine(t *testing.T) {
	cfgPath, _ := writeToolLinterFixture(t, nil, "fixture", "acme", "widget", "1.2.3")

	_, stderr, err := run2(t, "--config", cfgPath, "renovate", "annotate")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Contains(t, string(got), "# renovate: datasource=github-tags depName=acme/widget\n      ref: v1.0.0\n")
}

func TestRenovateAnnotate_LocalPluginSource_NeverAnnotated(t *testing.T) {
	cfgPath, _ := writeToolLinterFixture(t, nil, "fixture", "acme", "widget", "1.2.3")

	_, stderr, err := run2(t, "--config", cfgPath, "renovate", "annotate")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.NotContains(t, string(got), "depName=local")
}

func TestRenovateAnnotate_PrintsSummary(t *testing.T) {
	cfgPath, _ := writeToolLinterFixture(t, []string{"fixture"}, "fixture", "acme", "widget", "1.2.3")

	stdout, stderr, err := run2(t, "--config", cfgPath, "renovate", "annotate")
	require.NoError(t, err, "stderr: %s", stderr)
	assert.Contains(t, stdout, "annotated lint/fixture")
	assert.Contains(t, stdout, "annotated plugins.sources/origin")
	// writeToolLinterFixture always declares a second plugins.sources[] entry, "local" (a Local
	// source, per its own doc comment) -- that one is always skipped (renovate.ForPluginSource
	// reports ok=false for any Local source), so the fixture used by this whole file always
	// produces 2 annotated (lint/fixture, plugins.sources/origin) + 1 skipped
	// (plugins.sources/local), never 0 skipped.
	assert.Contains(t, stdout, "skipped plugins.sources/local")
	assert.Contains(t, stdout, "2 annotated, 1 skipped")
}

func TestRenovateConfig_PrintsSnippet(t *testing.T) {
	stdout, stderr, err := run2(t, "renovate", "config")
	require.NoError(t, err, "stderr: %s", stderr)
	assert.Equal(t, renovateConfigSnippet, stdout)
}
