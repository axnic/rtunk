package upgrade_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/upgrade"
)

func TestLatestRelease_ParsesTagAndAssets(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/repos/xunleii/rtunk/releases/latest", r.URL.Path)
		_, _ = fmt.Fprint(w, `{"tag_name":"v0.6.0","assets":[{"name":"rtunk_darwin_arm64","browser_download_url":"`+srv.URL+`/rtunk_darwin_arm64"}]}`)
	}))
	defer srv.Close()

	rel, err := upgrade.LatestRelease(srv.URL, "xunleii", "rtunk")
	require.NoError(t, err)
	assert.Equal(t, "v0.6.0", rel.TagName)
	require.Len(t, rel.Assets, 1)
	assert.Equal(t, "rtunk_darwin_arm64", rel.Assets[0].Name)
}

func TestLatestRelease_HTTPErrorIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := upgrade.LatestRelease(srv.URL, "xunleii", "rtunk")
	require.Error(t, err)
}

func TestAssetName_MatchesGoosGoarch(t *testing.T) {
	assert.Equal(t, "rtunk_darwin_arm64", upgrade.AssetName("darwin", "arm64"))
	assert.Equal(t, "rtunk_linux_amd64", upgrade.AssetName("linux", "amd64"))
}

func TestAvailable_DifferentTagIsAvailable(t *testing.T) {
	rel := upgrade.Release{TagName: "v0.7.0"}
	newVersion, ok := upgrade.Available(rel, "v0.6.0")
	assert.True(t, ok)
	assert.Equal(t, "v0.7.0", newVersion)
}

func TestAvailable_SameTagIsNotAvailable(t *testing.T) {
	rel := upgrade.Release{TagName: "v0.6.0"}
	_, ok := upgrade.Available(rel, "v0.6.0")
	assert.False(t, ok)
}

func TestAvailable_DevCurrentVersionIsNeverAvailable(t *testing.T) {
	rel := upgrade.Release{TagName: "v0.6.0"}
	_, ok := upgrade.Available(rel, "dev")
	assert.False(t, ok)
}

// TestApply_ReplacesTargetAndNewBinaryRuns is a live self-replace regression test: builds a real
// tiny Go binary fixture that prints a distinguishable string, serves it over a real local HTTP
// server, applies it over a DIFFERENT starting file at targetPath, then actually executes
// targetPath and asserts the NEW binary's own output -- proving the rename-over-a-path pattern
// genuinely works end to end, not just that bytes landed on disk.
func TestApply_ReplacesTargetAndNewBinaryRuns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Apply is unsupported on windows")
	}

	src := `package main
import "fmt"
func main() { fmt.Println("new-binary-v2") }
`
	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "fixture.go")
	require.NoError(t, os.WriteFile(srcPath, []byte(src), 0o644))
	binPath := filepath.Join(srcDir, "fixture")
	out, err := exec.Command("go", "build", "-o", binPath, srcPath).CombinedOutput()
	require.NoError(t, err, "building fixture binary: %s", out)

	binData, err := os.ReadFile(binPath)
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(binData)
	}))
	defer srv.Close()

	targetDir := t.TempDir()
	targetPath := filepath.Join(targetDir, "rtunk")
	require.NoError(t, os.WriteFile(targetPath, []byte("old-binary-placeholder"), 0o755))

	cacheDir := t.TempDir()
	err = upgrade.Apply(cacheDir, srv.URL+"/rtunk_fixture", targetPath)
	require.NoError(t, err)

	info, err := os.Stat(targetPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())

	out, err = exec.Command(targetPath).Output()
	require.NoError(t, err)
	assert.Equal(t, "new-binary-v2\n", string(out))
}

func TestApply_Windows_ReturnsExplicitError(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("this test only exercises the windows-specific branch")
	}
	err := upgrade.Apply(t.TempDir(), "http://example.invalid/asset", filepath.Join(t.TempDir(), "rtunk.exe"))
	require.Error(t, err)
}
