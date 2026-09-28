package download

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/trunk/config"
)

func TestRecordUsage_WritesRegistryEntry(t *testing.T) {
	cacheDir := t.TempDir()
	var cfg config.Config
	cfg.Tools = map[string]config.Tool{"eslint": {KnownGoodVersion: "1.0.0"}}
	cfg.Plugins.Sources = map[string]config.PluginSource{
		"trunk": {ID: "trunk", URI: "https://example.invalid/trunk-io/plugins", Ref: "v1"},
		"local": {ID: "local", Local: "/some/local/dir"},
	}
	repoRoot := "/repo/one"

	require.NoError(t, RecordUsage(cacheDir, repoRoot, cfg))

	root, err := Root(cacheDir)
	require.NoError(t, err)
	data, err := os.ReadFile(registryPath(root, repoRoot))
	require.NoError(t, err)
	var entry registryEntry
	require.NoError(t, json.Unmarshal(data, &entry))

	assert.Equal(t, repoRoot, entry.RepoRoot)
	assert.Contains(t, entry.Refs, Ref{Category: "tools", ID: "eslint", Version: "1.0.0"})
	assert.Equal(t, []string{config.SourceHash(cfg.Plugins.Sources["trunk"])}, entry.PluginSources,
		"the local source must never appear -- it has no cache file to track")
}

func TestRecordUsage_RecordsActionPackagesRefByContentHash(t *testing.T) {
	cacheDir := t.TempDir()
	manifest := filepath.Join(t.TempDir(), "package.json")
	content := []byte(`{"dependencies":{"@commitlint/cli":"^19.0"}}`)
	require.NoError(t, os.WriteFile(manifest, content, 0o644))

	var cfg config.Config
	cfg.Actions.Definitions = map[string]config.Action{
		"commitlint":  {ID: "commitlint", Runtime: "node", PackagesFile: manifest},
		"go-mod-tidy": {ID: "go-mod-tidy"}, // no packages_file -- must produce no Ref
	}
	repoRoot := "/repo/three"

	require.NoError(t, RecordUsage(cacheDir, repoRoot, cfg))

	root, err := Root(cacheDir)
	require.NoError(t, err)
	data, err := os.ReadFile(registryPath(root, repoRoot))
	require.NoError(t, err)
	var entry registryEntry
	require.NoError(t, json.Unmarshal(data, &entry))

	sum := sha256.Sum256(content)
	wantRef := Ref{Category: "action-packages", ID: hex.EncodeToString(sum[:]), Version: "manifest"}
	assert.Contains(t, entry.Refs, wantRef)
	assert.Len(t, entry.Refs, 1, "an action with no packages_file must produce no action-packages Ref")
}

func TestRecordUsage_SkipsSystemVersionRuntime(t *testing.T) {
	cacheDir := t.TempDir()
	var cfg config.Config
	cfg.Runtimes.Definitions = map[string]config.Runtime{"php": {SystemVersion: "*"}}
	repoRoot := "/repo/two"

	require.NoError(t, RecordUsage(cacheDir, repoRoot, cfg))

	root, err := Root(cacheDir)
	require.NoError(t, err)
	data, err := os.ReadFile(registryPath(root, repoRoot))
	require.NoError(t, err)
	var entry registryEntry
	require.NoError(t, json.Unmarshal(data, &entry))
	assert.Empty(t, entry.Refs, "a system_version runtime is never installed into the cache; nothing to keep for it")
}
