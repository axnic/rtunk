package cli

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecentFmtRun_SaveThenLoad_RoundTrips(t *testing.T) {
	cacheDir := t.TempDir()
	repoRoot := "/repo/one"
	want := recentFmtRun{
		Timestamp: time.Now().Truncate(time.Second),
		Changed:   map[string][]string{"prettier": {"a.js", "b.js"}},
	}
	require.NoError(t, saveRecentFmtRun(cacheDir, repoRoot, want))

	got, ok, err := loadRecentFmtRun(cacheDir, repoRoot)
	require.NoError(t, err)
	require.True(t, ok)
	assert.True(t, want.Timestamp.Equal(got.Timestamp))
	assert.Equal(t, want.Changed, got.Changed)
}

func TestRecentFmtRun_Load_NeverSaved_IsNotFoundNotError(t *testing.T) {
	_, ok, err := loadRecentFmtRun(t.TempDir(), "/repo/never-used")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestRecentFmtRun_DifferentRepoRoots_DoNotMix(t *testing.T) {
	cacheDir := t.TempDir()
	require.NoError(t, saveRecentFmtRun(cacheDir, "/repo/a", recentFmtRun{Timestamp: time.Now(), Changed: map[string][]string{"x": {"only-in-a.txt"}}}))

	_, ok, err := loadRecentFmtRun(cacheDir, "/repo/b")
	require.NoError(t, err)
	assert.False(t, ok, "a different repo root must never see another repo's recorded run")
}

func TestRecentFmtRun_Save_OverwritesPreviousRecord(t *testing.T) {
	cacheDir := t.TempDir()
	repoRoot := "/repo/one"
	require.NoError(t, saveRecentFmtRun(cacheDir, repoRoot, recentFmtRun{Timestamp: time.Now(), Changed: map[string][]string{"x": {"old.txt"}}}))
	require.NoError(t, saveRecentFmtRun(cacheDir, repoRoot, recentFmtRun{Timestamp: time.Now(), Changed: map[string][]string{"x": {"new.txt"}}}))

	got, ok, err := loadRecentFmtRun(cacheDir, repoRoot)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, map[string][]string{"x": {"new.txt"}}, got.Changed, "only the most recent run is ever kept, not a log")
}
