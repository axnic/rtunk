package actions_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/trunk/actions"
)

func TestHistory_AppendThenRead_MostRecentFirst(t *testing.T) {
	cacheDir := t.TempDir()
	repoRoot := "/repo/one"

	r1 := actions.Result{ActionID: "commitlint", StartedAt: time.Now(), ExitCode: 0}
	r2 := actions.Result{ActionID: "commitlint", StartedAt: time.Now().Add(time.Second), ExitCode: 1}
	require.NoError(t, actions.AppendHistory(cacheDir, repoRoot, r1))
	require.NoError(t, actions.AppendHistory(cacheDir, repoRoot, r2))

	got, err := actions.History(cacheDir, repoRoot, "", 0)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, 1, got[0].ExitCode, "most recent entry (r2) must come first")
	assert.Equal(t, 0, got[1].ExitCode)
}

func TestHistory_FilterByActionID(t *testing.T) {
	cacheDir := t.TempDir()
	repoRoot := "/repo/two"
	require.NoError(t, actions.AppendHistory(cacheDir, repoRoot, actions.Result{ActionID: "a", StartedAt: time.Now()}))
	require.NoError(t, actions.AppendHistory(cacheDir, repoRoot, actions.Result{ActionID: "b", StartedAt: time.Now()}))

	got, err := actions.History(cacheDir, repoRoot, "b", 0)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "b", got[0].ActionID)
}

func TestHistory_BoundedTo200Entries(t *testing.T) {
	cacheDir := t.TempDir()
	repoRoot := "/repo/three"
	for i := 0; i < 205; i++ {
		require.NoError(t, actions.AppendHistory(cacheDir, repoRoot, actions.Result{ActionID: "x", ExitCode: i, StartedAt: time.Now()}))
	}
	got, err := actions.History(cacheDir, repoRoot, "", 0)
	require.NoError(t, err)
	require.Len(t, got, 200)
	assert.Equal(t, 204, got[0].ExitCode, "the newest entry must survive truncation")
	assert.Equal(t, 5, got[199].ExitCode, "the oldest surviving entry after dropping the first 5")
}

func TestHistory_EmptyWhenNeverAppended(t *testing.T) {
	got, err := actions.History(t.TempDir(), "/repo/never-used", "", 0)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestHistory_DifferentRepoRoots_DoNotMix(t *testing.T) {
	cacheDir := t.TempDir()
	require.NoError(t, actions.AppendHistory(cacheDir, "/repo/a", actions.Result{ActionID: "only-in-a", StartedAt: time.Now()}))
	got, err := actions.History(cacheDir, "/repo/b", "", 0)
	require.NoError(t, err)
	assert.Empty(t, got, "history is keyed by repo root -- a different repo must see none of it")
}
