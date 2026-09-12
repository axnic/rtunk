package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/trunk/config"
)

// TestResolve_InvalidSource: a plugins.sources entry has neither local: nor uri: set. Resolve
// must report an *InvalidSourceError rather than trying (and failing confusingly) to fetch it as
// a git source.
func TestResolve_InvalidSource(t *testing.T) {
	_, err := config.Resolve("testdata/trunk-invalid-source.yaml", t.TempDir())

	var invalidErr *config.InvalidSourceError
	require.ErrorAs(t, err, &invalidErr)
	assert.Equal(t, "neither", invalidErr.SourceID)
}

func TestResolve_MissingFile(t *testing.T) {
	_, err := config.Resolve("testdata/does-not-exist.yaml", t.TempDir())

	var readErr *config.ReadError
	require.ErrorAs(t, err, &readErr)
	assert.Equal(t, "testdata/does-not-exist.yaml", readErr.Path)
}

func TestResolve_InvalidYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trunk.yaml")
	require.NoError(t, os.WriteFile(path, []byte("version: [not-a-mapping\n"), 0o644))

	_, err := config.Resolve(path, t.TempDir())

	var parseErr *config.ParseError
	require.ErrorAs(t, err, &parseErr)
	assert.Equal(t, path, parseErr.Path)
}

// TestResolve_WithPluginRepo resolves a real trunk.yaml against a local plugin repository built
// from the excerpts documented in ARCHITECTURE.md (github.com/trunk-io/plugins), proving Resolve
// merges definitions from every category dir and that everything it enables/references checks
// out: this fixture is a fully valid, self-consistent config, so Resolve must return no error.
//
// The fixture's trunk.yaml only enables lint: [actionlint, prettier], actions: [commitlint],
// runtimes: [node] — Resolve's enabled+used filter must keep exactly that closure (actionlint's
// and prettier's own tools:, since they're referenced) and drop everything else the plugin repo
// merely defines: eslint/shellcheck (unreferenced tools, and shellcheck's download with them) and
// the go-mod-tidy action (defined but never enabled).
func TestResolve_WithPluginRepo(t *testing.T) {
	cfg, err := config.Resolve("testdata/trunk-with-plugins.yaml", t.TempDir())
	require.NoError(t, err)

	require.Contains(t, cfg.Downloads, "actionlint")
	assert.NotContains(t, cfg.Downloads, "shellcheck")

	assert.Contains(t, cfg.Tools, "actionlint")
	assert.Contains(t, cfg.Tools, "prettier")
	assert.NotContains(t, cfg.Tools, "eslint")
	assert.NotContains(t, cfg.Tools, "shellcheck")

	require.Contains(t, cfg.Lint.Definitions, "actionlint")
	require.Contains(t, cfg.Lint.Definitions, "prettier")

	// actionlint/prettier's own files: (github-workflow/javascript) must survive; "yaml", defined
	// but never referenced by an enabled linter, must not.
	assert.Contains(t, cfg.Lint.Files, "github-workflow")
	assert.Contains(t, cfg.Lint.Files, "javascript")
	assert.NotContains(t, cfg.Lint.Files, "yaml")

	require.Contains(t, cfg.Actions.Definitions, "commitlint")
	assert.NotContains(t, cfg.Actions.Definitions, "go-mod-tidy")

	require.Contains(t, cfg.Runtimes.Definitions, "node")
	assert.Equal(t, config.ShimList{"node", "npm", "npx", "corepack"}, cfg.Runtimes.Definitions["node"].Shims)

	// Environments (from the repo-root plugin.yaml) and Lint.CommentFormats (from linters/
	// plugin.yaml) are global config, not enableable definitions — they must survive Resolve's
	// enabled+used trim untouched, unlike everything asserted absent above.
	require.Len(t, cfg.Environments, 1)
	assert.Equal(t, "SYSTEM", cfg.Environments[0].Name)
	assert.ElementsMatch(t, []config.CommentFormat{
		{Name: "hash", LeadingDelimiter: "#"},
		{Name: "slashes-inline", LeadingDelimiter: "//"},
	}, cfg.Lint.CommentFormats)

	// SourceDir/SourceRoot let ${cwd}/${plugin} resolve into this local source's own directory
	// tree (pkg/trunk/engine's job): SourceDir is derived from the plugin.yaml's own path within
	// the source; SourceRoot is that source's own directory as mergePluginRepo resolved it.
	assert.Equal(t, filepath.Join("linters", "actionlint"), cfg.Lint.Definitions["actionlint"].SourceDir)
	assert.Equal(t, filepath.Join("testdata", "pluginrepo"), cfg.Lint.Definitions["actionlint"].SourceRoot)
}

// TestResolveAll_WithPluginRepo mirrors TestResolve_WithPluginRepo but via ResolveAll: every
// definition the fixture plugin repo contributes must survive, including eslint/shellcheck
// (unreferenced tools) and go-mod-tidy (a defined but never-enabled action) that Resolve trims.
func TestResolveAll_WithPluginRepo(t *testing.T) {
	cfg, err := config.ResolveAll("testdata/trunk-with-plugins.yaml", t.TempDir())
	require.NoError(t, err)

	assert.Contains(t, cfg.Tools, "eslint")
	assert.Contains(t, cfg.Tools, "shellcheck")
	assert.Contains(t, cfg.Downloads, "shellcheck")
	assert.Contains(t, cfg.Actions.Definitions, "go-mod-tidy")
	assert.Contains(t, cfg.Lint.Files, "yaml", "unreferenced but defined file type; Resolve trims it, ResolveAll keeps it")

	require.Len(t, cfg.Environments, 1)
	assert.Equal(t, "SYSTEM", cfg.Environments[0].Name)
	assert.Len(t, cfg.Lint.CommentFormats, 2)
}

// TestResolve_DuplicateResource: two plugin.yaml files under the same local source both define a
// tool named "foo". Resolve must report a *DuplicateError, and the later file (foo-b, sorted
// after foo-a) must win the overwrite.
func TestResolve_DuplicateResource(t *testing.T) {
	cfg, err := config.Resolve("testdata/trunk-duplicate.yaml", t.TempDir())

	var dupErr *config.DuplicateError
	require.ErrorAs(t, err, &dupErr)
	assert.Equal(t, "tool", dupErr.Category)
	assert.Equal(t, "foo", dupErr.Key)

	require.Contains(t, cfg.Tools, "foo")
	assert.Equal(t, "2.0.0", cfg.Tools["foo"].KnownGoodVersion, "later source must win the whole key, not merge fields")
}

// TestResolve_DanglingReference: a linter names a tool that no plugin.yaml defines. Resolve
// itself reads it in without complaint; Validate must report a *ReferenceError identifying
// exactly what's missing.
func TestResolve_DanglingReference(t *testing.T) {
	cfg, err := config.Resolve("testdata/trunk-dangling.yaml", t.TempDir())
	require.NoError(t, err)

	err = cfg.Validate()

	var refErr *config.ReferenceError
	require.ErrorAs(t, err, &refErr)
	assert.Equal(t, "lint", refErr.Category)
	assert.Equal(t, "orphan", refErr.Key)
	assert.Equal(t, "tools", refErr.Field)
	assert.Equal(t, "nonexistent-tool", refErr.Reference)
}

// TestResolve_SourceNotFound: a `local:` plugin source pointing nowhere is a config error — unlike
// a git source, a local source is expected to already be present, never fetched.
func TestResolve_SourceNotFound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trunk.yaml")
	require.NoError(t, os.WriteFile(path, []byte("plugins:\n  sources:\n    - id: gone\n      local: ./nowhere\n"), 0o644))

	_, err := config.Resolve(path, t.TempDir())

	var notFoundErr *config.SourceNotFoundError
	require.ErrorAs(t, err, &notFoundErr)
	assert.Equal(t, "gone", notFoundErr.SourceID)
}
