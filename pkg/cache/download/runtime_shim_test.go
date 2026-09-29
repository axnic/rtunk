package download_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/cache/download"
	"github.com/xunleii/rtunk/pkg/trunk/config"
)

func TestQuoteOne(t *testing.T) {
	cases := map[string]string{
		"plain": "'plain'",
		"":      "''",
		"a'b":   `'a'\''b'`,
		"a'b'c": `'a'\''b'\''c'`,
	}
	for in, want := range cases {
		if got := download.QuoteOne(in); got != want {
			t.Errorf("QuoteOne(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestQuoteAll(t *testing.T) {
	got := download.QuoteAll([]string{"plain", "a'b"})
	assert.Equal(t, []string{"'plain'", `'a'\''b'`}, got)
}

func TestResolveRuntimeShimDir_CacheHit_NoFetch(t *testing.T) {
	cacheDir := t.TempDir()
	root, err := download.Root(cacheDir)
	require.NoError(t, err)
	shimPath := download.ShimPath(root, "runtimes", "python", "3.11.0", "python3")
	require.NoError(t, os.MkdirAll(filepath.Dir(shimPath), 0o755))
	require.NoError(t, os.WriteFile(shimPath, nil, 0o755))

	cfg := config.Config{Runtimes: config.CategoryConfig[config.Runtime]{
		Definitions: map[string]config.Runtime{"python": {KnownGoodVersion: "3.11.0", Shims: []string{"python3"}}},
	}}

	var events int
	dir, err := download.ResolveRuntimeShimDir(cfg, root, cacheDir, "/repo", "python", func(download.Event) { events++ })
	require.NoError(t, err)
	assert.Equal(t, filepath.Dir(shimPath), dir)
	assert.Zero(t, events, "an already-cached shim must never trigger a fetch or emit any event")
}

func TestResolveRuntimeShimDir_MissingRuntime_ReturnsError(t *testing.T) {
	_, err := download.ResolveRuntimeShimDir(config.Config{}, t.TempDir(), t.TempDir(), "/repo", "nope", nil)
	require.Error(t, err)
}

func TestResolveRuntimeShimDir_FetchesAndTouchesOnlyAfterSuccess(t *testing.T) {
	archive := tarGzBytes(t, "tool-1.0.0", "shellcheck", "#!/bin/sh\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer srv.Close()

	cfg := config.Config{
		Downloads: map[string]config.Download{
			"shellcheck": {Downloads: []config.DownloadEntry{{
				OS:  config.OSSpec{"linux": "linux", "macos": "macos", "windows": "windows"},
				CPU: config.OSSpec{"x86_64": "x86_64", "arm_64": "arm_64"},
				URL: srv.URL + "/shellcheck.tar.gz", StripComponents: 1,
			}}},
		},
		Tools: map[string]config.Tool{},
		Runtimes: config.CategoryConfig[config.Runtime]{
			Definitions: map[string]config.Runtime{
				"shellcheck": {Type: "shellcheck", Download: "shellcheck", KnownGoodVersion: "1.0.0", Shims: []string{"shellcheck"}},
			},
		},
	}

	cacheDir := t.TempDir()
	root, err := download.Root(cacheDir)
	require.NoError(t, err)

	var phases []download.Phase
	dir, err := download.ResolveRuntimeShimDir(cfg, root, cacheDir, "/repo", "shellcheck", func(ev download.Event) {
		phases = append(phases, ev.Phase)
	})
	require.NoError(t, err)
	shimPath := download.ShimPath(root, "runtimes", "shellcheck", "1.0.0", "shellcheck")
	assert.Equal(t, filepath.Dir(shimPath), dir)
	assert.FileExists(t, shimPath)
	assert.Contains(t, phases, download.Started, "onEvent must have been called at least once (Started, at minimum)")

	// A second, DIFFERENT and not-yet-cached runtime whose fetch fails must not have its install
	// dir's mtime bumped -- Touch must never run when the fetch failed.
	failSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failSrv.Close()

	failCfg := config.Config{
		Downloads: map[string]config.Download{
			"shfmt": {Downloads: []config.DownloadEntry{{
				OS:  config.OSSpec{"linux": "linux", "macos": "macos", "windows": "windows"},
				CPU: config.OSSpec{"x86_64": "x86_64", "arm_64": "arm_64"},
				URL: failSrv.URL + "/shfmt.tar.gz", StripComponents: 1,
			}}},
		},
		Tools: map[string]config.Tool{},
		Runtimes: config.CategoryConfig[config.Runtime]{
			Definitions: map[string]config.Runtime{
				"shfmt": {Type: "shfmt", Download: "shfmt", KnownGoodVersion: "1.0.0", Shims: []string{"shfmt"}},
			},
		},
	}

	installDir := download.InstallDir(root, "runtimes", "shfmt", "1.0.0")
	_, err = download.ResolveRuntimeShimDir(failCfg, root, cacheDir, "/repo", "shfmt", nil)
	require.Error(t, err)
	_, statErr := os.Stat(installDir)
	assert.True(t, os.IsNotExist(statErr), "a failed fetch must never create/touch the install dir")
}
