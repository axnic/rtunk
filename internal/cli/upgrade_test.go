package cli

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func releaseServer(t *testing.T, tagName string, assetBody []byte) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/xunleii/rtunk/releases/latest":
			assetName := fmt.Sprintf("rtunk_%s_%s", runtime.GOOS, runtime.GOARCH)
			_, _ = fmt.Fprintf(w, `{"tag_name":%q,"assets":[{"name":%q,"browser_download_url":%q}]}`,
				tagName, assetName, srv.URL+"/"+assetName)
		default:
			_, _ = w.Write(assetBody)
		}
	}))
	return srv
}

func TestUpgradeCmd_AlreadyUpToDate(t *testing.T) {
	srv := releaseServer(t, "v1.0.0", nil)
	defer srv.Close()

	old := Version
	Version = "v1.0.0"
	oldBase := githubAPIBase
	githubAPIBase = srv.URL
	t.Cleanup(func() { Version = old; githubAPIBase = oldBase })

	stdout, _, err := run2(t, "upgrade")
	require.NoError(t, err)
	assert.Contains(t, stdout, "up to date")
	assert.Contains(t, stdout, "v1.0.0")
}

func TestUpgradeCmd_Check_AvailableExitsNonZero(t *testing.T) {
	srv := releaseServer(t, "v2.0.0", nil)
	defer srv.Close()

	old := Version
	Version = "v1.0.0"
	oldBase := githubAPIBase
	githubAPIBase = srv.URL
	t.Cleanup(func() { Version = old; githubAPIBase = oldBase })

	stdout, _, err := run2(t, "upgrade", "--check")
	require.Error(t, err)
	assert.Contains(t, stdout+err.Error(), "v1.0.0")
	assert.Contains(t, stdout+err.Error(), "v2.0.0")
}

// TestUpgradeCmd_DryRunAlias_MatchesCheckFlag: --dry-run is an alias for the existing --check
// field (identical real-trunk semantics: "detect available upgrades, but do not apply changes").
func TestUpgradeCmd_DryRunAlias_MatchesCheckFlag(t *testing.T) {
	srv := releaseServer(t, "v2.0.0", nil)
	defer srv.Close()

	old := Version
	Version = "v1.0.0"
	oldBase := githubAPIBase
	githubAPIBase = srv.URL
	t.Cleanup(func() { Version = old; githubAPIBase = oldBase })

	checkOut, _, checkErr := run2(t, "upgrade", "--check")
	dryRunOut, _, dryRunErr := run2(t, "upgrade", "--dry-run")
	require.Error(t, checkErr)
	require.Error(t, dryRunErr)
	assert.Equal(t, checkErr, dryRunErr)
	assert.Equal(t, checkOut, dryRunOut)
}

func TestUpgradeCmd_AppliesAndReports(t *testing.T) {
	assetBody := []byte("new-binary-bytes")
	srv := releaseServer(t, "v2.0.0", assetBody)
	defer srv.Close()

	old := Version
	Version = "v1.0.0"
	oldBase := githubAPIBase
	githubAPIBase = srv.URL
	t.Cleanup(func() { Version = old; githubAPIBase = oldBase })

	targetDir := t.TempDir()
	targetPath := filepath.Join(targetDir, "rtunk")
	require.NoError(t, os.WriteFile(targetPath, []byte("old"), 0o755))
	oldExecutable := selfExecutablePath
	selfExecutablePath = func() (string, error) { return targetPath, nil }
	t.Cleanup(func() { selfExecutablePath = oldExecutable })

	cacheDir := t.TempDir()
	stdout, stderr, err := run2(t, "--cache-dir", cacheDir, "upgrade")
	require.NoError(t, err, "stderr: %s", stderr)
	assert.Contains(t, stdout, "v1.0.0")
	assert.Contains(t, stdout, "v2.0.0")

	got, err := os.ReadFile(targetPath)
	require.NoError(t, err)
	assert.Equal(t, "new-binary-bytes", string(got))
}

func TestUpgradeCmd_DevVersion_SkipsWithoutError(t *testing.T) {
	srv := releaseServer(t, "v2.0.0", nil)
	defer srv.Close()

	old := Version
	Version = "dev"
	oldBase := githubAPIBase
	githubAPIBase = srv.URL
	t.Cleanup(func() { Version = old; githubAPIBase = oldBase })

	stdout, _, err := run2(t, "upgrade")
	require.NoError(t, err)
	assert.Contains(t, stdout, "cannot determine current version")
}
