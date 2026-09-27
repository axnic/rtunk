package download

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAcquireInstallLock_SecondLiveClaimFailsFast(t *testing.T) {
	installDir := filepath.Join(t.TempDir(), "tools", "eslint", "1.0.0")

	release, err := acquireInstallLock(installDir, "/repo/a")
	require.NoError(t, err)
	defer release()

	_, err = acquireInstallLock(installDir, "/repo/b")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "/repo/a")
	assert.Contains(t, err.Error(), strconv.Itoa(os.Getpid()))
}

func TestAcquireInstallLock_StaleLockIsReclaimedImmediately(t *testing.T) {
	installDir := filepath.Join(t.TempDir(), "tools", "eslint", "1.0.0")
	lockPath := installDir + ".lock"
	require.NoError(t, os.MkdirAll(filepath.Dir(lockPath), 0o755))
	// A PID essentially guaranteed not to be running: max_pid+1 territory on any real system.
	require.NoError(t, os.WriteFile(lockPath, []byte(`{"PID":999999999,"Repo":"/repo/dead"}`), 0o644))

	release, err := acquireInstallLock(installDir, "/repo/c")
	require.NoError(t, err, "a stale lock must be reclaimed immediately, never waited on")
	release()
}

func TestAcquireInstallLock_ReleaseAllowsReclaim(t *testing.T) {
	installDir := filepath.Join(t.TempDir(), "tools", "eslint", "1.0.0")

	release, err := acquireInstallLock(installDir, "/repo/a")
	require.NoError(t, err)
	release()

	release2, err := acquireInstallLock(installDir, "/repo/b")
	require.NoError(t, err)
	release2()
}
