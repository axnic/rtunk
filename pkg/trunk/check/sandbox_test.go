package check

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStageSandbox_CopyTargetsOnlyStagesGivenFiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "target.txt"), []byte("t"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sibling.txt"), []byte("s"), 0o644))

	sandboxDir, cleanup, err := stageSandbox("copy_targets", dir, []string{"target.txt"})
	require.NoError(t, err)
	defer cleanup()

	data, err := os.ReadFile(filepath.Join(sandboxDir, "target.txt"))
	require.NoError(t, err)
	assert.Equal(t, "t", string(data))

	_, err = os.Stat(filepath.Join(sandboxDir, "sibling.txt"))
	assert.True(t, os.IsNotExist(err), "sibling.txt was not a target and must not be staged")
}

func TestStageSandbox_ExpandedCopiesWholeDirectoryNonRecursive(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte("a"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.go"), []byte("b"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "c.go"), []byte("c"), 0o644))

	sandboxDir, cleanup, err := stageSandbox("expanded", dir, []string{"a.go"})
	require.NoError(t, err)
	defer cleanup()

	for _, name := range []string{"a.go", "b.go"} {
		_, err := os.Stat(filepath.Join(sandboxDir, name))
		assert.NoError(t, err, "%s must be staged (expanded copies the whole directory)", name)
	}
	_, err = os.Stat(filepath.Join(sandboxDir, "sub", "c.go"))
	assert.True(t, os.IsNotExist(err), "expanded must not recurse into subdirectories")
}

func TestStageSandbox_CleanupRemovesTempDir(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "target.txt"), []byte("t"), 0o644))

	sandboxDir, cleanup, err := stageSandbox("copy_targets", dir, []string{"target.txt"})
	require.NoError(t, err)
	cleanup()

	_, err = os.Stat(sandboxDir)
	assert.True(t, os.IsNotExist(err), "cleanup must remove the sandbox directory")
}

func TestRemapFindings_NoOpWhenBaseIsRepoRoot(t *testing.T) {
	findings := []Finding{{File: "foo.txt"}}
	remapFindings(findings, "/repo", "/repo")
	assert.Equal(t, "foo.txt", findings[0].File)
}

func TestRemapFindings_RebasesRelativeToRepoRoot(t *testing.T) {
	repoRoot := t.TempDir()
	base := filepath.Join(repoRoot, "sub", "dir")
	findings := []Finding{{File: "foo.txt"}}
	remapFindings(findings, base, repoRoot)
	assert.Equal(t, filepath.Join("sub", "dir", "foo.txt"), findings[0].File)
}
