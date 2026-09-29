package security

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveRunFrom_EmptyAndParent(t *testing.T) {
	repoRoot := t.TempDir()
	sub := filepath.Join(repoRoot, "a", "b")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	target := filepath.Join(sub, "file.txt")
	require.NoError(t, os.WriteFile(target, []byte("x"), 0o644))

	for _, runFrom := range []string{"", "${parent}"} {
		dir, ok := ResolveRunFrom(runFrom, target, repoRoot, nil)
		require.True(t, ok)
		assert.Equal(t, repoRoot, dir, "runFrom=%q", runFrom)
	}
}

func TestResolveRunFrom_TargetDirectory(t *testing.T) {
	repoRoot := t.TempDir()
	sub := filepath.Join(repoRoot, "a", "b")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	target := filepath.Join(sub, "file.txt")
	require.NoError(t, os.WriteFile(target, []byte("x"), 0o644))

	dir, ok := ResolveRunFrom("${target_directory}", target, repoRoot, nil)
	require.True(t, ok)
	assert.Equal(t, sub, dir)
}

func TestResolveRunFrom_RootOrParentWith_FoundAtIntermediateLevel(t *testing.T) {
	repoRoot := t.TempDir()
	mid := filepath.Join(repoRoot, "a")
	sub := filepath.Join(mid, "b")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(mid, "go.mod"), []byte("module x"), 0o644))
	target := filepath.Join(sub, "file.go")
	require.NoError(t, os.WriteFile(target, []byte("x"), 0o644))

	dir, ok := ResolveRunFrom("${root_or_parent_with(go.mod)}", target, repoRoot, nil)
	require.True(t, ok)
	assert.Equal(t, mid, dir, "must stop at the first directory containing go.mod, not walk further")
}

func TestResolveRunFrom_RootOrParentWith_FoundAtRepoRoot(t *testing.T) {
	repoRoot := t.TempDir()
	sub := filepath.Join(repoRoot, "a", "b")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "go.mod"), []byte("module x"), 0o644))
	target := filepath.Join(sub, "file.go")
	require.NoError(t, os.WriteFile(target, []byte("x"), 0o644))

	dir, ok := ResolveRunFrom("${root_or_parent_with(go.mod)}", target, repoRoot, nil)
	require.True(t, ok)
	assert.Equal(t, repoRoot, dir)
}

func TestResolveRunFrom_RootOrParentWith_NotFoundFallsBackToRepoRoot(t *testing.T) {
	repoRoot := t.TempDir()
	sub := filepath.Join(repoRoot, "a", "b")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	target := filepath.Join(sub, "file.go")
	require.NoError(t, os.WriteFile(target, []byte("x"), 0o644))

	dir, ok := ResolveRunFrom("${root_or_parent_with(go.mod)}", target, repoRoot, nil)
	require.True(t, ok)
	assert.Equal(t, repoRoot, dir, "no go.mod anywhere -- falls back to repoRoot, not an error")
}

func TestResolveRunFrom_RootOrParentWithRegex(t *testing.T) {
	repoRoot := t.TempDir()
	mid := filepath.Join(repoRoot, "a")
	sub := filepath.Join(mid, "b")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(mid, "app.csproj"), []byte("x"), 0o644))
	target := filepath.Join(sub, "file.cs")
	require.NoError(t, os.WriteFile(target, []byte("x"), 0o644))

	dir, ok := ResolveRunFrom(`${root_or_parent_with_regex((.+\.csproj)|(.+\.sln))}`, target, repoRoot, nil)
	require.True(t, ok)
	assert.Equal(t, mid, dir)
}

func TestResolveRunFrom_RootOrParentWithAnyConfig(t *testing.T) {
	repoRoot := t.TempDir()
	mid := filepath.Join(repoRoot, "a")
	sub := filepath.Join(mid, "b")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(mid, ".mypy.ini"), []byte("x"), 0o644))
	target := filepath.Join(sub, "file.py")
	require.NoError(t, os.WriteFile(target, []byte("x"), 0o644))

	dir, ok := ResolveRunFrom("${root_or_parent_with_any_config}", target, repoRoot, []string{"mypy.ini", ".mypy.ini"})
	require.True(t, ok)
	assert.Equal(t, mid, dir, "must match the second DirectConfigs entry, not only the first")
}

func TestResolveRunFrom_UnsupportedLiteral(t *testing.T) {
	repoRoot := t.TempDir()
	target := filepath.Join(repoRoot, "file.rb")
	require.NoError(t, os.WriteFile(target, []byte("x"), 0o644))

	_, ok := ResolveRunFrom("apps", target, repoRoot, nil)
	assert.False(t, ok)
}

func TestResolveRunFrom_UnsupportedCompileCommand(t *testing.T) {
	repoRoot := t.TempDir()
	target := filepath.Join(repoRoot, "file.cpp")
	require.NoError(t, os.WriteFile(target, []byte("x"), 0o644))

	_, ok := ResolveRunFrom("${compile_command}", target, repoRoot, nil)
	assert.False(t, ok)
}
