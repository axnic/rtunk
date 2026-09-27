package download

import (
	"encoding/json"
	"os"
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
