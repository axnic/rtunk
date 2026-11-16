package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLintersEnableCmd_NoID_NonTerminal_ErrorsInsteadOfHanging: `linters enable` with no id args
// falls into the interactive picker, which must refuse to run (not block reading raw key bytes
// from stdin) when stdin/stdout aren't a real terminal -- the case in every test and CI run.
func TestLintersEnableCmd_NoID_NonTerminal_ErrorsInsteadOfHanging(t *testing.T) {
	path := writeScratchTrunkYAML(t, "version: \"0.1\"\nlint:\n  enabled: []\n")

	_, _, err := run2(t, "--config", path, "linters", "enable")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "terminal")
}

func TestLintersEnableCmd_AddsAndPreservesComments(t *testing.T) {
	path := writeScratchWithPlugins(t, "version: \"0.1\"\n# a leading comment, must survive\nlint:\n  enabled: []\n")

	_, stderr, err := run2(t, "--config", path, "linters", "enable", "actionlint")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(got), "# a leading comment, must survive")
	assert.Contains(t, string(got), "actionlint")
}

func TestLintersEnableCmd_Idempotent(t *testing.T) {
	path := writeScratchWithPlugins(t, "version: \"0.1\"\nlint:\n  enabled: [actionlint]\n")

	_, stderr, err := run2(t, "--config", path, "linters", "enable", "actionlint")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(got), "actionlint"))
}

func TestLintersEnableCmd_VersionPinReplacesOldPin(t *testing.T) {
	path := writeScratchWithPlugins(t, "version: \"0.1\"\nlint:\n  enabled: [actionlint@1.0.0]\n")

	_, stderr, err := run2(t, "--config", path, "linters", "enable", "actionlint@2.0.0")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(got), "actionlint@2.0.0")
	assert.NotContains(t, string(got), "actionlint@1.0.0")
}

func TestLintersEnableCmd_NoLintKeyAtAll(t *testing.T) {
	path := writeScratchWithPlugins(t, "version: \"0.1\"\n")

	_, stderr, err := run2(t, "--config", path, "linters", "enable", "actionlint")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(got), "actionlint")
}

// TestLintersEnableCmd_LintKeyWithNoValue guards against a real silent data-loss bug: a
// hand-edited/partial trunk.yaml with a bare "lint:" key (no value at all) parses that key's
// value as a null scalar node, not a mapping. Appending onto a non-mapping node's Content is
// silently ignored by the yaml.v3 encoder, so without coercing the node back to a MappingNode
// first, the whole edit vanished on write and the command still reported success.
func TestLintersEnableCmd_LintKeyWithNoValue(t *testing.T) {
	path := writeScratchWithPlugins(t, "version: \"0.1\"\nlint:\n")

	_, stderr, err := run2(t, "--config", path, "linters", "enable", "actionlint")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(got), "actionlint")
	assert.Contains(t, string(got), "enabled")
}

// TestLintersEnableCmd_PreservesSourceIndentWidth guards against editEnabled reformatting the
// whole file to yaml.v3's default 4-space indent (yaml.Marshal's default) instead of keeping
// the 2-space indent trunk.yaml's real-world convention (and this repo's own .trunk/trunk.yaml)
// actually uses -- untouched keys must keep their original indentation exactly.
func TestLintersEnableCmd_PreservesSourceIndentWidth(t *testing.T) {
	path := writeScratchWithPlugins(t, "version: \"0.1\"\nruntimes:\n  enabled:\n    - node@22.18.0\nlint:\n  enabled:\n    - prettier\n")

	_, stderr, err := run2(t, "--config", path, "linters", "enable", "actionlint")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(got), "runtimes:\n  enabled:\n    - node@22.18.0\n")
	assert.Contains(t, string(got), "actionlint")
}

func TestLintersEnableCmd_NoAnnotations_BehaviorUnchanged(t *testing.T) {
	// Same fixture/assertion as TestLintersEnableCmd_AddsAndPreservesComments -- no # renovate:
	// comment anywhere means the new annotation-aware branch in editEnabled must never trigger.
	// This is this plan's own proof the fix is genuinely opt-in.
	path := writeScratchWithPlugins(t, "version: \"0.1\"\n# a leading comment, must survive\nlint:\n  enabled: []\n")

	_, stderr, err := run2(t, "--config", path, "linters", "enable", "actionlint")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(got), "# a leading comment, must survive")
	assert.Contains(t, string(got), "actionlint")
	assert.NotContains(t, string(got), "# renovate:")
}

func TestLintersEnableCmd_AnnotatedSurvivor_KeepsExactComment(t *testing.T) {
	cfgPath, _ := writeToolLinterFixture(t, []string{"fixture@1.0.0"})
	_, stderr, err := run2(t, "--config", cfgPath, "renovate", "enable")
	require.NoError(t, err, "stderr: %s", stderr)
	before, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	require.Contains(t, string(before), "# renovate: datasource=github-releases depName=acme/widget")

	// A re-enable of the same pin must not disturb fixture's own comment.
	_, stderr, err = run2(t, "--config", cfgPath, "linters", "enable", "fixture@1.0.0")
	require.NoError(t, err, "stderr: %s", stderr)

	after, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Contains(t, string(after), "# renovate: datasource=github-releases depName=acme/widget\n    - fixture@1.0.0\n")
}

// TestLintersEnableCmd_AnnotatedPinnedSurvivor_ReEnableBareRepinsToKnownGood guards against bug 1
// from the whole-branch review: the old survivor-shortcut restored an already-annotated entry's
// prior HeadComment verbatim but never re-applied the version-pin rule, so re-enabling an
// already-annotated, already-pinned entry bare (no @version) produced an entry that was annotated
// but unpinned -- a dead annotation Renovate's regex manager can't capture a version from. The
// fixed loop always re-resolves via renovate.ForLint, so a bare re-enable must come back both
// annotated AND re-pinned to the known_good_version.
func TestLintersEnableCmd_AnnotatedPinnedSurvivor_ReEnableBareRepinsToKnownGood(t *testing.T) {
	cfgPath, _ := writeToolLinterFixture(t, []string{"fixture@9.9.9"})
	_, stderr, err := run2(t, "--config", cfgPath, "renovate", "enable")
	require.NoError(t, err, "stderr: %s", stderr)

	before, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	require.Contains(t, string(before), "# renovate: datasource=github-releases depName=acme/widget\n    - fixture@9.9.9\n")

	// Re-enable bare, with no @version -- the old code kept the stale comment and the stale
	// (missing) pin; the fix must re-pin to known_good_version and keep the annotation.
	_, stderr, err = run2(t, "--config", cfgPath, "linters", "enable", "fixture")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Contains(t, string(got), "# renovate: datasource=github-releases depName=acme/widget\n    - fixture@1.2.3\n")
}

func TestLintersEnableCmd_NewEntryInAnnotatedCategory_GetsFreshComment(t *testing.T) {
	cfgPath, repoRoot := writeToolLinterFixture(t, []string{"fixture"})
	_, stderr, err := run2(t, "--config", cfgPath, "renovate", "enable")
	require.NoError(t, err, "stderr: %s", stderr)

	// Add a second, independently-resolvable tool+linter to the same fixture repo before
	// enabling it, so editEnabled's own config.ResolveAll (triggered by the existing annotation
	// on "fixture") can actually resolve it too.
	secondPluginYAML := `downloads:
  - name: second-download
    version: 1.0.0
    downloads:
      - os: { linux: linux, macos: macos, windows: windows }
        cpu: { x86_64: x86_64, arm_64: arm_64 }
        url: https://github.com/other/second/releases/download/v${version}/second.tar.gz
tools:
  definitions:
    - name: second
      download: second-download
      known_good_version: 4.5.6
lint:
  definitions:
    - name: second
      files: [ALL]
      tools: [second]
      description: second fixture linter
      commands:
        - name: lint
          run: echo unused
          output: xml
`
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "pluginrepo", "linters", "second"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "pluginrepo", "linters", "second", "plugin.yaml"), []byte(secondPluginYAML), 0o644))

	_, stderr, err = run2(t, "--config", cfgPath, "linters", "enable", "second")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Contains(t, string(got), "# renovate: datasource=github-releases depName=other/second\n    - second@4.5.6\n")
}

// writeScratchWithPlugins is writeScratchTrunkYAML with the fixture plugin repo (defines
// actionlint, commitlint, ...) wired in as a plugin source; content must start with the
// `version: "0.1"` line.
func writeScratchWithPlugins(t *testing.T, content string) string {
	t.Helper()
	abs, err := filepath.Abs("../../pkg/trunk/config/testdata/pluginrepo")
	require.NoError(t, err)
	const head = "version: \"0.1\"\n"
	require.True(t, strings.HasPrefix(content, head))
	path := writeScratchTrunkYAML(t, content)
	abs, err = filepath.EvalSymlinks(abs)
	require.NoError(t, err)
	base, err := filepath.EvalSymlinks(filepath.Dir(path)) // the scratch git repo's root, as git reports it
	require.NoError(t, err)
	rel, err := filepath.Rel(base, abs) // local: paths resolve relative to the git repo root
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte(head+"plugins:\n  sources:\n    - id: trunk\n      local: "+rel+"\n"+strings.TrimPrefix(content, head)), 0o644))
	return path
}

// Unknown ids (no plugin defines them) must be rejected naming the id, with the config untouched --
// including the name part of a name@version, and when mixed with a valid id.
func TestLintersEnableCmd_UnknownID_Rejected(t *testing.T) {
	for _, args := range [][]string{{"nope"}, {"nope@1.2.3"}, {"actionlint", "nope"}} {
		path := writeScratchWithPlugins(t, "version: \"0.1\"\nlint:\n  enabled: []\n")
		before, err := os.ReadFile(path)
		require.NoError(t, err)

		_, _, err = run2(t, append([]string{"--config", path, "linters", "enable"}, args...)...)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nope")
		assert.NotContains(t, err.Error(), "nope@")
		assert.Contains(t, err.Error(), "linters list")

		after, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, string(before), string(after))
	}
}

// A defined linter that is already enabled is not "unknown".
func TestLintersEnableCmd_DefinedAndAlreadyEnabled_OK(t *testing.T) {
	path := writeScratchWithPlugins(t, "version: \"0.1\"\nlint:\n  enabled: [actionlint]\n")
	_, stderr, err := run2(t, "--config", path, "linters", "enable", "actionlint@1.0.0")
	require.NoError(t, err, "stderr: %s", stderr)
}

// TestLintersEnableCmd_GoInstalledTool_KeepsExtractVersion: a tool installed through the go
// runtime is annotated with extractVersion (go module versions are "v"-prefixed, a pin is not);
// rewriting the enabled list must keep that part of the comment, not just datasource and depName.
func TestLintersEnableCmd_GoInstalledTool_KeepsExtractVersion(t *testing.T) {
	cfgPath, repoRoot := writeToolLinterFixture(t, []string{"fixture@0.5.0"})
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "pluginrepo", "linters", "fixture", "plugin.yaml"), []byte(`runtimes:
  definitions:
    - type: go
      known_good_version: 1.2.0
      system_version: allowed
tools:
  definitions:
    - name: fixture
      runtime: go
      package: github.com/acme/fixture
      known_good_version: 0.5.0
lint:
  definitions:
    - name: fixture
      files: [ALL]
      tools: [fixture]
      description: go-installed fixture linter
      commands:
        - name: lint
          run: echo unused
          output: xml
`), 0o644))

	_, stderr, err := run2(t, "--config", cfgPath, "renovate", "enable")
	require.NoError(t, err, "stderr: %s", stderr)
	const comment = "# renovate: datasource=go depName=github.com/acme/fixture extractVersion=^v(?<version>.+)$\n    - fixture@0.5.0\n"
	before, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	require.Contains(t, string(before), comment)

	// Re-enabling the same pin rewrites the list; the comment must come back whole.
	_, stderr, err = run2(t, "--config", cfgPath, "linters", "enable", "fixture@0.5.0")
	require.NoError(t, err, "stderr: %s", stderr)
	after, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Contains(t, string(after), comment)
}
