package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/xunleii/rtunk/pkg/trunk/config"
)

// writeToolLinterFixture is writeLinterFixture's (check_run_test.go) tools+lint extension: it
// writes a plugin repo whose plugin.yaml declares a top-level downloads: recipe (real shape --
// see pkg/trunk/config/testdata/pluginrepo/linters/shellcheck/plugin.yaml -- a flat list, NOT
// nested under definitions:), a tools.definitions[] entry referencing it via download:, and a
// lint.definitions[] entry referencing that tool via tools: [<toolName>] -- the minimal real
// shape renovate.ForLint's Linter->Tools[0]->Tool bridge needs. plugins.sources[] declares only
// the local source (like writeLinterFixture in check_run_test.go): config.ResolveAll genuinely
// fetches every declared git-shaped source, so a fake origin: entry here would break hermetic
// testing by hitting the network. The owner/repo/knownGoodVersion params still feed the
// downloads: URL and known_good_version below. Plugin-source-ref annotation coverage lives in
// TestAnnotateDoc_PluginSourceRef_GetsCommentOnKeyNode / TestAnnotateDoc_LocalPluginSource_
// NeverAnnotated instead, calling annotateDoc directly in-memory.
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

func TestAnnotateDoc_PluginSourceRef_GetsCommentOnKeyNode(t *testing.T) {
	data := []byte(`plugins:
  sources:
    - id: origin
      uri: https://github.com/acme/widget
      ref: v1.0.0
`)
	var doc yaml.Node
	require.NoError(t, yaml.Unmarshal(data, &doc))

	cfg := config.Config{
		Plugins: struct {
			Sources map[string]config.PluginSource
		}{
			Sources: map[string]config.PluginSource{
				"origin": {ID: "origin", URI: "https://github.com/acme/widget", Ref: "v1.0.0"},
			},
		},
	}

	report := annotateDoc(&doc, cfg)
	assert.Equal(t, []string{"plugins.sources/origin"}, report.Annotated)

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	require.NoError(t, enc.Encode(&doc))
	require.NoError(t, enc.Close())
	assert.Contains(t, buf.String(), "# renovate: datasource=github-tags depName=acme/widget\n      ref: v1.0.0\n")
}

func TestAnnotateDoc_LocalPluginSource_NeverAnnotated(t *testing.T) {
	data := []byte(`plugins:
  sources:
    - id: local
      local: ../pluginrepo
`)
	var doc yaml.Node
	require.NoError(t, yaml.Unmarshal(data, &doc))

	cfg := config.Config{
		Plugins: struct {
			Sources map[string]config.PluginSource
		}{
			Sources: map[string]config.PluginSource{
				"local": {ID: "local", Local: "../pluginrepo"},
			},
		},
	}

	report := annotateDoc(&doc, cfg)
	assert.Empty(t, report.Annotated)
	assert.Equal(t, []string{"plugins.sources/local: no confident datasource (local source or non-GitHub URI)"}, report.Skipped)
}

func TestRenovateAnnotate_PrintsSummary(t *testing.T) {
	cfgPath, _ := writeToolLinterFixture(t, []string{"fixture"}, "fixture", "acme", "widget", "1.2.3")

	stdout, stderr, err := run2(t, "--config", cfgPath, "renovate", "annotate")
	require.NoError(t, err, "stderr: %s", stderr)
	assert.Contains(t, stdout, "annotated lint/fixture")
	assert.Contains(t, stdout, "skipped plugins.sources/local")
	assert.Contains(t, stdout, "1 annotated, 1 skipped")
}

func TestRenovateConfig_PrintsSnippet(t *testing.T) {
	stdout, stderr, err := run2(t, "renovate", "config")
	require.NoError(t, err, "stderr: %s", stderr)
	assert.Equal(t, renovateConfigSnippet, stdout)
}
