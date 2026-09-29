package download

import (
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/trunk/config"
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

// bareBinaryConfig is a download-recipe config serving one extension-less (bare binary) blob for
// both a "shellcheck" tool and a "shellcheck" runtime -- no archive needed.
func bareBinaryConfig(url string) config.Config {
	dl := config.Download{Downloads: []config.DownloadEntry{{
		OS:  config.OSSpec{"linux": "linux", "macos": "macos", "windows": "windows"},
		CPU: config.OSSpec{"x86_64": "x86_64", "arm_64": "arm_64"},
		URL: url,
	}}}
	return config.Config{
		Downloads: map[string]config.Download{"shellcheck": dl},
		Tools: map[string]config.Tool{
			"shellcheck": {Name: "shellcheck", Download: "shellcheck", KnownGoodVersion: "1.0.0", Shims: []string{"shellcheck"}},
		},
		Runtimes: config.CategoryConfig[config.Runtime]{
			Definitions: map[string]config.Runtime{
				"shellcheck": {Type: "shellcheck", Download: "shellcheck", KnownGoodVersion: "0.9.0", Shims: []string{"shellcheck"}},
			},
		},
	}
}

func TestDownload_ConcurrentInstall_SecondRepoFailsFast(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("#!/bin/sh\n"))
	}))
	defer srv.Close()
	cfg := bareBinaryConfig(srv.URL + "/shellcheck")

	cacheDir := t.TempDir()
	root, err := Root(cacheDir)
	require.NoError(t, err)
	release, err := acquireInstallLock(InstallDir(root, "runtimes", "shellcheck", "0.9.0"), "/repo/a")
	require.NoError(t, err)
	defer release()

	events, err := Download(cfg, cacheDir, "/repo/b", Ref{Category: "runtimes", ID: "shellcheck"})
	require.NoError(t, err)
	var failed bool
	for ev := range events {
		if ev.Phase == Failed {
			failed = true
			assert.Contains(t, ev.Err.Error(), "/repo/a")
			assert.Contains(t, ev.Err.Error(), "retry later")
		}
	}
	assert.True(t, failed, "a second repo racing an already-locked install must fail, not wait")
	assert.Zero(t, hits.Load(), "a locked-out install must never start fetching")
}

// TestDownload_ConcurrentSameRef_NeverRacesBlobRemoval is the regression test for Task 2's
// review finding: fetchDownload removes its blob right after installing, so a second concurrent
// installer of the identical item (same URL, same blobPath) could ENOENT on a blob the first
// already removed. With the install lock, only one installer ever reaches FetchBlob; every other
// one either fails fast on the lock or, arriving after the winner finished, sees it Cached.
func TestDownload_ConcurrentSameRef_NeverRacesBlobRemoval(t *testing.T) {
	t.Run("second installer fails fast while the first holds the lock", func(t *testing.T) {
		var hits atomic.Int32
		entered, unblock := make(chan struct{}), make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if hits.Add(1) == 1 {
				close(entered)
				<-unblock // hold the first installer mid-fetch, lock held
			}
			_, _ = w.Write([]byte("#!/bin/sh\n"))
		}))
		defer srv.Close()
		var once sync.Once
		release := func() { once.Do(func() { close(unblock) }) }
		defer release() // runs before srv.Close: a failed assertion below must not hang on the held handler
		cfg := bareBinaryConfig(srv.URL + "/shellcheck")
		cacheDir := t.TempDir()
		ref := Ref{Category: "tools", ID: "shellcheck"}

		first, err := Download(cfg, cacheDir, "/repo/a", ref)
		require.NoError(t, err)
		var firstPhases []Phase
		firstDone := make(chan struct{})
		go func() {
			defer close(firstDone)
			for ev := range first {
				assert.NoError(t, ev.Err)
				firstPhases = append(firstPhases, ev.Phase)
			}
		}()
		<-entered

		second, err := Download(cfg, cacheDir, "/repo/b", ref)
		require.NoError(t, err)
		var secondErr error
		for ev := range second {
			if ev.Phase == Failed {
				secondErr = ev.Err
			}
		}
		require.Error(t, secondErr)
		assert.Contains(t, secondErr.Error(), "/repo/a")

		release()
		<-firstDone
		assert.Contains(t, firstPhases, Done)
		assert.Equal(t, int32(1), hits.Load(), "only the lock holder may fetch")
	})

	t.Run("unsynchronized racers never ENOENT and fetch exactly once", func(t *testing.T) {
		for range 20 {
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hits.Add(1)
				_, _ = w.Write([]byte("#!/bin/sh\n"))
			}))
			cfg := bareBinaryConfig(srv.URL + "/shellcheck")
			cacheDir := t.TempDir()
			ref := Ref{Category: "tools", ID: "shellcheck"}

			const racers = 8
			var wg sync.WaitGroup
			var dones atomic.Int32
			for i := range racers {
				wg.Go(func() {
					events, err := Download(cfg, cacheDir, fmt.Sprintf("/repo/%d", i), ref)
					if !assert.NoError(t, err) {
						return
					}
					for ev := range events {
						switch ev.Phase {
						case Done:
							dones.Add(1)
						case Failed:
							assert.NotErrorIs(t, ev.Err, fs.ErrNotExist, "a racer must never ENOENT on a removed blob")
							assert.Contains(t, ev.Err.Error(), "already installing")
						}
					}
				})
			}
			wg.Wait()
			srv.Close()
			assert.Equal(t, int32(1), dones.Load(), "exactly one racer installs")
			assert.Equal(t, int32(1), hits.Load(), "exactly one racer fetches")
		}
	})
}

// A lock left behind by an earlier, killed rtunk whose PID we now reuse (always the case for PID 1
// in a container) must be reclaimed, not reported as held by ourselves forever.
func TestAcquireInstallLock_OwnPIDLeftoverIsReclaimed(t *testing.T) {
	installDir := filepath.Join(t.TempDir(), "tools", "eslint", "1.0.0")
	lockPath := installDir + ".lock"
	require.NoError(t, os.MkdirAll(filepath.Dir(lockPath), 0o755))
	claim := fmt.Sprintf(`{"PID":%d,"Repo":"/repo/crashed"}`, os.Getpid())
	require.NoError(t, os.WriteFile(lockPath, []byte(claim), 0o644))

	release, err := acquireInstallLock(installDir, "/repo/c")
	require.NoError(t, err, "a leftover carrying our own PID that we don't hold must be reclaimed")
	release()
	assert.NoFileExists(t, lockPath)
}
