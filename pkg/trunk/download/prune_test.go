package download

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeRegistryEntry(t *testing.T, cacheDir string, entry registryEntry) {
	t.Helper()
	root, err := Root(cacheDir)
	require.NoError(t, err)
	dir := registryDir(root)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	data, err := json.Marshal(entry)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(registryPath(root, entry.RepoRoot), data, 0o644))
}

func TestPrune_NoRegistry_IsNoop(t *testing.T) {
	cacheDir := t.TempDir()
	require.NoError(t, Prune(cacheDir))
}

func TestPrune_DropsEntryAndInstallsForGoneRepo(t *testing.T) {
	cacheDir := t.TempDir()
	root, err := Root(cacheDir)
	require.NoError(t, err)

	goneRepo := filepath.Join(t.TempDir(), "gone") // never created -- simulates a removed repo
	writeRegistryEntry(t, cacheDir, registryEntry{
		RepoRoot: goneRepo,
		Refs:     []Ref{{Category: "tools", ID: "eslint", Version: "1.0.0"}},
	})
	installDir := InstallDir(root, "tools", "eslint", "1.0.0")
	require.NoError(t, os.MkdirAll(installDir, 0o755))

	require.NoError(t, Prune(cacheDir))

	assert.NoDirExists(t, installDir)
	entries, err := os.ReadDir(registryDir(root))
	require.NoError(t, err)
	assert.Empty(t, entries, "the gone repo's own registry file must be dropped")
}

func TestPrune_KeepsInstallForExistingRepoAndDropsUnreferenced(t *testing.T) {
	cacheDir := t.TempDir()
	root, err := Root(cacheDir)
	require.NoError(t, err)
	liveRepo := t.TempDir() // exists

	writeRegistryEntry(t, cacheDir, registryEntry{
		RepoRoot: liveRepo,
		Refs:     []Ref{{Category: "tools", ID: "eslint", Version: "1.0.0"}},
	})
	kept := InstallDir(root, "tools", "eslint", "1.0.0")
	unreferenced := InstallDir(root, "tools", "actionlint", "2.0.0")
	require.NoError(t, os.MkdirAll(kept, 0o755))
	require.NoError(t, os.MkdirAll(unreferenced, 0o755))

	require.NoError(t, Prune(cacheDir))

	assert.DirExists(t, kept)
	assert.NoDirExists(t, unreferenced)
}

func TestPrune_UnionsKeepSetAcrossLiveRepos(t *testing.T) {
	cacheDir := t.TempDir()
	root, err := Root(cacheDir)
	require.NoError(t, err)
	repoA, repoB := t.TempDir(), t.TempDir()

	writeRegistryEntry(t, cacheDir, registryEntry{RepoRoot: repoA, PluginSources: []string{"sharedhash"}})
	writeRegistryEntry(t, cacheDir, registryEntry{RepoRoot: repoB, Refs: []Ref{{Category: "tools", ID: "black", Version: "1.0.0"}}})

	pluginsDir := filepath.Join(filepath.Dir(root), "plugins")
	require.NoError(t, os.MkdirAll(pluginsDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pluginsDir, "sharedhash.json"), []byte("{}"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(pluginsDir, "checkouts", "sharedhash"), 0o755))
	require.NoError(t, os.MkdirAll(InstallDir(root, "tools", "black", "1.0.0"), 0o755))

	require.NoError(t, Prune(cacheDir))

	assert.FileExists(t, filepath.Join(pluginsDir, "sharedhash.json"), "repoA still needs this source")
	assert.DirExists(t, filepath.Join(pluginsDir, "checkouts", "sharedhash"))
	assert.DirExists(t, InstallDir(root, "tools", "black", "1.0.0"), "repoB still needs this tool")
}
