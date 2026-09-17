package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeScratchTrunkYAML writes content into a fresh git repo's root (actions/git-hooks commands
// resolve repoRoot via `git rev-parse --show-toplevel`, so callers exercising those code paths
// need a real repo here, not a bare tempdir).
func writeScratchTrunkYAML(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, exec.Command("git", "-C", dir, "init", "-q").Run())
	path := filepath.Join(dir, "trunk.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func TestCheckEnableCmd_AddsAndPreservesComments(t *testing.T) {
	path := writeScratchTrunkYAML(t, "version: \"0.1\"\n# a leading comment, must survive\nlint:\n  enabled: []\n")

	_, stderr, err := run2(t, "--config", path, "check", "enable", "shellcheck")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(got), "# a leading comment, must survive")
	assert.Contains(t, string(got), "shellcheck")
}

func TestCheckEnableCmd_Idempotent(t *testing.T) {
	path := writeScratchTrunkYAML(t, "version: \"0.1\"\nlint:\n  enabled: [shellcheck]\n")

	_, stderr, err := run2(t, "--config", path, "check", "enable", "shellcheck")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(got), "shellcheck"))
}

func TestCheckEnableCmd_VersionPinReplacesOldPin(t *testing.T) {
	path := writeScratchTrunkYAML(t, "version: \"0.1\"\nlint:\n  enabled: [shellcheck@1.0.0]\n")

	_, stderr, err := run2(t, "--config", path, "check", "enable", "shellcheck@2.0.0")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(got), "shellcheck@2.0.0")
	assert.NotContains(t, string(got), "shellcheck@1.0.0")
}

func TestCheckEnableCmd_NoLintKeyAtAll(t *testing.T) {
	path := writeScratchTrunkYAML(t, "version: \"0.1\"\n")

	_, stderr, err := run2(t, "--config", path, "check", "enable", "shellcheck")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(got), "shellcheck")
}

func TestCheckDisableCmd_RemovesEntry(t *testing.T) {
	path := writeScratchTrunkYAML(t, "version: \"0.1\"\nlint:\n  enabled: [shellcheck, prettier]\n")

	_, stderr, err := run2(t, "--config", path, "check", "disable", "shellcheck")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.NotContains(t, string(got), "shellcheck")
	assert.Contains(t, string(got), "prettier")
}

func TestCheckDisableCmd_AbsentIsNoOp(t *testing.T) {
	path := writeScratchTrunkYAML(t, "version: \"0.1\"\nlint:\n  enabled: [prettier]\n")

	_, stderr, err := run2(t, "--config", path, "check", "disable", "shellcheck")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(got), "prettier")
}

// TestCheckEnableCmd_LintKeyWithNoValue guards against a real silent data-loss bug: a
// hand-edited/partial trunk.yaml with a bare "lint:" key (no value at all) parses that key's
// value as a null scalar node, not a mapping. Appending onto a non-mapping node's Content is
// silently ignored by the yaml.v3 encoder, so without coercing the node back to a MappingNode
// first, the whole edit vanished on write and the command still reported success.
func TestCheckEnableCmd_LintKeyWithNoValue(t *testing.T) {
	path := writeScratchTrunkYAML(t, "version: \"0.1\"\nlint:\n")

	_, stderr, err := run2(t, "--config", path, "check", "enable", "shellcheck")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(got), "shellcheck")
	assert.Contains(t, string(got), "enabled")
}

// TestCheckDisableCmd_VersionedIDRemoves guards against disable silently no-op'ing when the id
// passed on the command line still carries an @version pin (as copy-pasted straight out of
// enabled: or `check list` output) -- removeEnabled must bare-compare its own ids too, not just
// the existing entries.
func TestCheckDisableCmd_VersionedIDRemoves(t *testing.T) {
	path := writeScratchTrunkYAML(t, "version: \"0.1\"\nlint:\n  enabled: [shellcheck@1.0.0, prettier]\n")

	_, stderr, err := run2(t, "--config", path, "check", "disable", "shellcheck@1.0.0")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.NotContains(t, string(got), "shellcheck")
	assert.Contains(t, string(got), "prettier")
}

// TestCheckEnableCmd_PreservesSourceIndentWidth guards against editEnabled reformatting the
// whole file to yaml.v3's default 4-space indent (yaml.Marshal's default) instead of keeping
// the 2-space indent trunk.yaml's real-world convention (and this repo's own .trunk/trunk.yaml)
// actually uses -- untouched keys must keep their original indentation exactly.
func TestCheckEnableCmd_PreservesSourceIndentWidth(t *testing.T) {
	path := writeScratchTrunkYAML(t, "version: \"0.1\"\nruntimes:\n  enabled:\n    - node@22.18.0\nlint:\n  enabled:\n    - prettier\n")

	_, stderr, err := run2(t, "--config", path, "check", "enable", "shellcheck")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(got), "runtimes:\n  enabled:\n    - node@22.18.0\n")
	assert.Contains(t, string(got), "shellcheck")
}

func TestCheckEnableCmd_NoAnnotations_BehaviorUnchanged(t *testing.T) {
	// Same fixture/assertion as TestCheckEnableCmd_AddsAndPreservesComments -- no # renovate:
	// comment anywhere means the new annotation-aware branch in editEnabled must never trigger.
	// This is this plan's own proof the fix is genuinely opt-in.
	path := writeScratchTrunkYAML(t, "version: \"0.1\"\n# a leading comment, must survive\nlint:\n  enabled: []\n")

	_, stderr, err := run2(t, "--config", path, "check", "enable", "shellcheck")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(got), "# a leading comment, must survive")
	assert.Contains(t, string(got), "shellcheck")
	assert.NotContains(t, string(got), "# renovate:")
}

func TestCheckEnableCmd_AnnotatedSurvivor_KeepsExactComment(t *testing.T) {
	cfgPath, _ := writeToolLinterFixture(t, []string{"fixture@1.0.0"}, "fixture", "acme", "widget", "1.2.3")
	_, stderr, err := run2(t, "--config", cfgPath, "renovate", "annotate")
	require.NoError(t, err, "stderr: %s", stderr)
	before, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	require.Contains(t, string(before), "# renovate: datasource=github-releases depName=acme/widget")

	// An unrelated enable of a second, unresolvable id must not disturb fixture's own comment.
	_, stderr, err = run2(t, "--config", cfgPath, "check", "enable", "unrelated")
	require.NoError(t, err, "stderr: %s", stderr)

	after, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Contains(t, string(after), "# renovate: datasource=github-releases depName=acme/widget\n    - fixture@1.0.0\n")
}

// TestCheckEnableCmd_AnnotatedPinnedSurvivor_ReEnableBareRepinsToKnownGood guards against bug 1
// from the whole-branch review: the old survivor-shortcut restored an already-annotated entry's
// prior HeadComment verbatim but never re-applied the version-pin rule, so re-enabling an
// already-annotated, already-pinned entry bare (no @version) produced an entry that was annotated
// but unpinned -- a dead annotation Renovate's regex manager can't capture a version from. The
// fixed loop always re-resolves via renovate.ForLint, so a bare re-enable must come back both
// annotated AND re-pinned to the known_good_version.
func TestCheckEnableCmd_AnnotatedPinnedSurvivor_ReEnableBareRepinsToKnownGood(t *testing.T) {
	cfgPath, _ := writeToolLinterFixture(t, []string{"fixture@9.9.9"}, "fixture", "acme", "widget", "1.2.3")
	_, stderr, err := run2(t, "--config", cfgPath, "renovate", "annotate")
	require.NoError(t, err, "stderr: %s", stderr)

	before, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	require.Contains(t, string(before), "# renovate: datasource=github-releases depName=acme/widget\n    - fixture@9.9.9\n")

	// Re-enable bare, with no @version -- the old code kept the stale comment and the stale
	// (missing) pin; the fix must re-pin to known_good_version and keep the annotation.
	_, stderr, err = run2(t, "--config", cfgPath, "check", "enable", "fixture")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Contains(t, string(got), "# renovate: datasource=github-releases depName=acme/widget\n    - fixture@1.2.3\n")
}

func TestCheckEnableCmd_NewEntryInAnnotatedCategory_GetsFreshComment(t *testing.T) {
	cfgPath, repoRoot := writeToolLinterFixture(t, []string{"fixture"}, "fixture", "acme", "widget", "1.2.3")
	_, stderr, err := run2(t, "--config", cfgPath, "renovate", "annotate")
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

	_, stderr, err = run2(t, "--config", cfgPath, "check", "enable", "second")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Contains(t, string(got), "# renovate: datasource=github-releases depName=other/second\n    - second@4.5.6\n")
}

func TestCheckEnableCmd_UnresolvableNewEntryInAnnotatedCategory_NoCommentNoForcedPin(t *testing.T) {
	cfgPath, _ := writeToolLinterFixture(t, []string{"fixture"}, "fixture", "acme", "widget", "1.2.3")
	_, stderr, err := run2(t, "--config", cfgPath, "renovate", "annotate")
	require.NoError(t, err, "stderr: %s", stderr)

	_, stderr, err = run2(t, "--config", cfgPath, "check", "enable", "phantom")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Contains(t, string(got), "phantom\n")
	assert.NotContains(t, string(got), "phantom@")
	// phantom itself must get no annotation of its own. Note this can't be a whole-file
	// NotContains(got, "depName=") check: "fixture" is itself resolvable, so the "renovate
	// annotate" step above already wrote its own "depName=acme/widget" survivor comment into the
	// file, which TestCheckEnableCmd_AnnotatedSurvivor_KeepsExactComment requires editEnabled to
	// preserve -- so "depName=" legitimately appears elsewhere in the file. Only phantom's own
	// line is asserted comment-free here.
	lines := strings.Split(string(got), "\n")
	found := false
	for i, line := range lines {
		if strings.TrimSpace(line) == "- phantom" {
			found = true
			if i > 0 {
				assert.NotContains(t, lines[i-1], "# renovate:", "phantom must not get its own renovate annotation")
			}
			break
		}
	}
	assert.True(t, found, "phantom entry not found in output")
}
