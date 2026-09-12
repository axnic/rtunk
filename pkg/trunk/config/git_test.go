package config_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/trunk/config"
)

// gitFixture copies srcDir into a temp git repo tagged v1.0.0 and returns a PluginSource pointing
// at it, so tests can exercise fetchGitSource's real git plumbing — and the caching around it —
// without any network access: git treats a local path exactly like a remote.
func gitFixture(t *testing.T, srcDir string) config.PluginSource {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.CopyFS(dir, os.DirFS(srcDir)))

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=rtunk-test", "GIT_AUTHOR_EMAIL=rtunk-test@example.com",
			"GIT_COMMITTER_NAME=rtunk-test", "GIT_COMMITTER_EMAIL=rtunk-test@example.com")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}
	run("init", "-q")
	run("add", "-A")
	run("commit", "-q", "-m", "fixture")
	// -m/--no-sign: the user's global gitconfig may have tag.gpgSign=true, which turns a bare
	// `git tag` into an annotated+signed tag needing both a message and a working GPG setup —
	// neither of which this hermetic fixture should depend on.
	run("tag", "-m", "fixture", "--no-sign", "v1.0.0")

	return config.PluginSource{ID: "fixture", URI: dir, Ref: "v1.0.0"}
}

// trunkYAMLFor writes a minimal trunk.yaml with src as its only plugin source, enabling
// lintEnabled/actionsEnabled/runtimesEnabled (any of which may be nil — Resolve now trims the
// merged config down to enabled+used, so a test that needs a specific definition to survive into
// the returned Config must enable it here), and returns its path.
func trunkYAMLFor(t *testing.T, src config.PluginSource, lintEnabled, actionsEnabled, runtimesEnabled []string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "trunk.yaml")

	var sb strings.Builder
	fmt.Fprintf(&sb, "version: \"0.1\"\nplugins:\n  sources:\n    - id: %q\n      uri: %q\n      ref: %q\n",
		src.ID, src.URI, src.Ref)
	for _, section := range []struct {
		name    string
		enabled []string
	}{{"lint", lintEnabled}, {"actions", actionsEnabled}, {"runtimes", runtimesEnabled}} {
		if len(section.enabled) == 0 {
			continue
		}
		fmt.Fprintf(&sb, "%s:\n  enabled:\n", section.name)
		for _, id := range section.enabled {
			fmt.Fprintf(&sb, "    - %s\n", id)
		}
	}

	require.NoError(t, os.WriteFile(path, []byte(sb.String()), 0o644))
	return path
}

// TestResolve_GitSource clones a local git fixture (built from the same plugin repo excerpts as
// TestResolve_WithPluginRepo) and resolves against it, and confirms the fetch leaves a cache file
// AND a persisted checkout behind under cacheDir, with the Linter's own SourceRoot/SourceDir
// pointing at real files inside that checkout.
func TestResolve_GitSource(t *testing.T) {
	src := gitFixture(t, "testdata/pluginrepo")
	cacheDir := t.TempDir()
	trunkYAML := trunkYAMLFor(t, src, []string{"actionlint"}, []string{"commitlint"}, []string{"node"})

	cfg, err := config.Resolve(trunkYAML, cacheDir)
	require.NoError(t, err)

	assert.Contains(t, cfg.Tools, "actionlint")
	assert.Contains(t, cfg.Lint.Definitions, "actionlint")
	assert.Contains(t, cfg.Actions.Definitions, "commitlint")
	assert.Contains(t, cfg.Runtimes.Definitions, "node")

	cacheFiles, err := filepath.Glob(filepath.Join(cacheDir, "*.json"))
	require.NoError(t, err)
	assert.Len(t, cacheFiles, 1, "fetch must leave exactly one parsed-definitions cache file behind")

	// The full checkout, not just the parsed-definitions cache, must be persisted -- this is what
	// lets ${plugin}/${cwd} resolve to real files on a later warm run.
	checkouts, err := filepath.Glob(filepath.Join(cacheDir, "checkouts", "*"))
	require.NoError(t, err)
	require.Len(t, checkouts, 1, "fetch must persist exactly one checkout directory")
	assert.Equal(t, checkouts[0], cfg.Lint.Definitions["actionlint"].SourceRoot,
		"a git-sourced Linter's SourceRoot must point at its persisted checkout")
	assert.Equal(t, filepath.Join("linters", "actionlint"), cfg.Lint.Definitions["actionlint"].SourceDir)
	assert.FileExists(t, filepath.Join(checkouts[0], "linters", "actionlint", "plugin.yaml"),
		"the persisted checkout must contain real files, not an empty directory")
}

// TestResolve_GitSource_DuplicateResource proves duplicate detection also fires for a git source
// (not just a local one, see TestResolve_DuplicateResource): two plugin.yaml files in the same
// fixture repo both define a tool named "foo", and the later one must win.
func TestResolve_GitSource_DuplicateResource(t *testing.T) {
	src := gitFixture(t, "testdata/pluginrepo-duplicate")

	cfg, err := config.Resolve(trunkYAMLFor(t, src, []string{"use-foo"}, nil, nil), t.TempDir())

	var dupErr *config.DuplicateError
	require.ErrorAs(t, err, &dupErr)
	assert.Equal(t, "tool", dupErr.Category)
	assert.Equal(t, "foo", dupErr.Key)
	assert.Equal(t, "2.0.0", cfg.Tools["foo"].KnownGoodVersion)
}

// TestResolve_GitSource_CacheHit: once a source is cached, a second Resolve must reuse the cache
// instead of fetching again — proven here by deleting the fixture repo between the two calls, so
// a real second fetch would fail.
func TestResolve_GitSource_CacheHit(t *testing.T) {
	src := gitFixture(t, "testdata/pluginrepo")
	cacheDir := t.TempDir()
	trunkYAML := trunkYAMLFor(t, src, nil, nil, nil)

	cfg1, err := config.Resolve(trunkYAML, cacheDir)
	require.NoError(t, err)

	require.NoError(t, os.RemoveAll(src.URI))

	cfg2, err := config.Resolve(trunkYAML, cacheDir)
	require.NoError(t, err)
	assert.Equal(t, cfg1, cfg2)
}

// TestResolve_GitSource_CorruptCache_Regenerates: a cache file that fails to decode must be
// dropped and rebuilt from a real fetch, not trusted or treated as fatal.
func TestResolve_GitSource_CorruptCache_Regenerates(t *testing.T) {
	src := gitFixture(t, "testdata/pluginrepo")
	cacheDir := t.TempDir()
	trunkYAML := trunkYAMLFor(t, src, nil, nil, nil)

	cfg1, err := config.Resolve(trunkYAML, cacheDir)
	require.NoError(t, err)

	cacheFiles, err := filepath.Glob(filepath.Join(cacheDir, "*.json"))
	require.NoError(t, err)
	require.Len(t, cacheFiles, 1)
	require.NoError(t, os.WriteFile(cacheFiles[0], []byte("not valid json"), 0o644))

	cfg2, err := config.Resolve(trunkYAML, cacheDir)
	require.NoError(t, err)
	assert.Equal(t, cfg1, cfg2)
}

// TestResolve_GitSource_CorruptCache_FetchFails: same as above, but the fixture repo is also gone
// by the second Resolve — the dropped cache must surface as a real *FetchError, not a silent
// empty result.
func TestResolve_GitSource_CorruptCache_FetchFails(t *testing.T) {
	src := gitFixture(t, "testdata/pluginrepo")
	cacheDir := t.TempDir()
	trunkYAML := trunkYAMLFor(t, src, nil, nil, nil)

	_, err := config.Resolve(trunkYAML, cacheDir)
	require.NoError(t, err)

	cacheFiles, err := filepath.Glob(filepath.Join(cacheDir, "*.json"))
	require.NoError(t, err)
	require.Len(t, cacheFiles, 1)
	cacheFile := cacheFiles[0]
	require.NoError(t, os.WriteFile(cacheFile, []byte("not valid json"), 0o644))
	require.NoError(t, os.RemoveAll(src.URI))

	_, err = config.Resolve(trunkYAML, cacheDir)

	var fetchErr *config.FetchError
	require.ErrorAs(t, err, &fetchErr)
	assert.Equal(t, "fixture", fetchErr.SourceID)
	_, statErr := os.Stat(cacheFile)
	assert.True(t, os.IsNotExist(statErr), "corrupt cache file must be removed, not left behind")
}

// TestResolve_GitSource_MissingCheckoutTriggersRefetch: a valid, decodable parsed-definitions
// cache whose paired checkout directory has been deleted (e.g. an operator manually cleaned it up)
// must not be trusted as a hit -- SourceRoot pointing at a directory that no longer exists would
// make ${plugin}/${cwd} resolve to nothing. Deleting the fixture repo between calls would make a
// real re-fetch fail outright, but here it's left alone: the point is only to prove the cache
// entry gets rebuilt, so the assertions below check the checkout is a NEW directory with the
// linter's fields pointing at it.
func TestResolve_GitSource_MissingCheckoutTriggersRefetch(t *testing.T) {
	src := gitFixture(t, "testdata/pluginrepo")
	cacheDir := t.TempDir()
	trunkYAML := trunkYAMLFor(t, src, []string{"actionlint"}, nil, nil)

	_, err := config.Resolve(trunkYAML, cacheDir)
	require.NoError(t, err)

	require.NoError(t, os.RemoveAll(filepath.Join(cacheDir, "checkouts")))

	cfg2, err := config.Resolve(trunkYAML, cacheDir)
	require.NoError(t, err)

	checkouts, err := filepath.Glob(filepath.Join(cacheDir, "checkouts", "*"))
	require.NoError(t, err)
	require.Len(t, checkouts, 1, "the missing checkout must be regenerated")
	assert.Equal(t, checkouts[0], cfg2.Lint.Definitions["actionlint"].SourceRoot)
	assert.Equal(t, filepath.Join("linters", "actionlint"), cfg2.Lint.Definitions["actionlint"].SourceDir)
}

// TestResolve_GitSource_CacheHitDoesNotReclone proves a genuine cache hit (parsed-definitions
// cache AND its checkout both present) never touches git again -- not just that it doesn't error
// (TestResolve_GitSource_CacheHit already proves that), but that the checkout directory itself is
// left untouched, via its mtime.
func TestResolve_GitSource_CacheHitDoesNotReclone(t *testing.T) {
	src := gitFixture(t, "testdata/pluginrepo")
	cacheDir := t.TempDir()
	trunkYAML := trunkYAMLFor(t, src, []string{"actionlint"}, nil, nil)

	_, err := config.Resolve(trunkYAML, cacheDir)
	require.NoError(t, err)

	checkouts, err := filepath.Glob(filepath.Join(cacheDir, "checkouts", "*"))
	require.NoError(t, err)
	require.Len(t, checkouts, 1)
	before, err := os.Stat(checkouts[0])
	require.NoError(t, err)

	require.NoError(t, os.RemoveAll(src.URI)) // a real second clone would now fail outright

	_, err = config.Resolve(trunkYAML, cacheDir)
	require.NoError(t, err)

	after, err := os.Stat(checkouts[0])
	require.NoError(t, err)
	assert.Equal(t, before.ModTime(), after.ModTime(), "a genuine cache hit must not touch the persisted checkout")
}
