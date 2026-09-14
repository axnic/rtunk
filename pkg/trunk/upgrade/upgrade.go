// Package upgrade checks rtunk's own GitHub Releases for a newer version and, if the user asks,
// downloads and installs it over the currently-running binary. Never touches linters/tools/
// runtimes/plugins -- those are reproducibly pinned via trunk.yaml's own enabled: lists (AGENTS.md
// "Reproducibility"), not something an "upgrade" command silently bumps.
package upgrade

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"

	"github.com/xunleii/rtunk/pkg/trunk/download"
)

// Release is the subset of GitHub's release API response this package needs.
type Release struct {
	TagName string  `json:"tag_name"`
	Assets  []Asset `json:"assets"`
}

// Asset is one file attached to a Release.
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// LatestRelease fetches owner/repo's latest GitHub release. apiBase defaults to
// "https://api.github.com" when "" -- tests pass an httptest.Server URL instead, so this is never
// a live network call outside real CLI usage.
func LatestRelease(apiBase, owner, repo string) (Release, error) {
	if apiBase == "" {
		apiBase = "https://api.github.com"
	}
	url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", apiBase, owner, repo)
	resp, err := http.Get(url) //nolint:noctx // matches pkg/trunk/download.FetchBlob's own v0.2-era choice, no context plumbing yet
	if err != nil {
		return Release{}, fmt.Errorf("upgrade: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("upgrade: %s: unexpected status %s", url, resp.Status)
	}

	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return Release{}, fmt.Errorf("upgrade: decoding release response: %w", err)
	}
	return rel, nil
}

// AssetName is the release-asset filename this platform's rtunk binary is published as: a bare
// binary, no archive -- rtunk is already "a single static binary" per AGENTS.md.
func AssetName(goos, goarch string) string {
	return fmt.Sprintf("rtunk_%s_%s", goos, goarch)
}

// Available reports whether rel's own TagName differs from currentVersion -- GitHub's "latest
// release" endpoint already returns the most recently published non-prerelease release, so no
// semver ordering logic is needed to trust "different" as "an upgrade." currentVersion == "dev"
// (rtunk's own build-time-unresolved default -- see cmd/rtunk/main.go) always reports false: there
// is nothing meaningful to compare a "dev" build against.
func Available(rel Release, currentVersion string) (newVersion string, available bool) {
	if currentVersion == "dev" {
		return "", false
	}
	if rel.TagName == currentVersion || strings.TrimPrefix(rel.TagName, "v") == strings.TrimPrefix(currentVersion, "v") {
		return "", false
	}
	return rel.TagName, true
}

// Apply downloads assetURL (through download.FetchBlob's existing content-addressed blob cache --
// no new HTTP-download code path) and atomically replaces targetPath with it, chmod +x. Windows is
// unsupported: it cannot rename a file over a currently-running executable's own path at all,
// unlike Unix (the OS keeps the old inode open for the still-running process; only a future
// invocation sees the new file) -- this project has already ruled out full Windows shim support
// elsewhere (v0.2).
func Apply(cacheDir, assetURL, targetPath string) error {
	if runtime.GOOS == "windows" {
		return fmt.Errorf("upgrade: self-replace is not supported on windows")
	}

	root, err := download.Root(cacheDir)
	if err != nil {
		return err
	}
	blobPath, err := download.FetchBlob(root, assetURL, nil)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(blobPath)
	if err != nil {
		return err
	}

	tmpPath := targetPath + ".new"
	if err := os.WriteFile(tmpPath, data, 0o755); err != nil {
		return err
	}
	return os.Rename(tmpPath, targetPath)
}
