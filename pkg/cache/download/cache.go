// Package download implements ROADMAP.md's v0.2 milestone: fetching the tool/runtime binaries a
// resolved pkg/trunk/config.Config references, hermetically and reproducibly, into a
// content-addressed local cache. See
// docs/superpowers/specs/2026-09-10-v0.2-download-design.md for the full design.
package download

import (
	"os"
	"path/filepath"
	goruntime "runtime"
	"time"
)

// Root resolves the downloads cache root: cacheDir/downloads if cacheDir is set (mirrors
// pkg/trunk/config/git.go's own --cache-dir handling), or the OS-default
// os.UserCacheDir()/rtunk/downloads otherwise. It does not create the directory -- callers that
// need it to exist call os.MkdirAll themselves.
func Root(cacheDir string) (string, error) {
	if cacheDir != "" {
		return filepath.Join(cacheDir, "downloads"), nil
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "rtunk", "downloads"), nil
}

// BlobPath is where a fetched artifact's raw bytes live, content-addressed by sum (its own
// SHA256 hex digest) -- see FetchBlob. Nothing reads a blob from a path whose name doesn't match
// its content.
func BlobPath(root, sum string) string {
	return filepath.Join(root, "blobs", "sha256", sum)
}

// InstallDir is where one item's extracted/installed tree lives, keyed by
// category/id/version/platform since a hermetic install is platform-specific.
func InstallDir(root, category, id, version string) string {
	return filepath.Join(root, "installs", category, id, version, Platform())
}

// ShimPath is the filesystem path `rtunk where`/`rtunk exec` resolve to for one item's named
// shim.
func ShimPath(root, category, id, version, name string) string {
	return filepath.Join(root, "shims", category, id, version, name)
}

// Platform is the GOOS-GOARCH pair InstallDir keys installs by.
func Platform() string {
	return goruntime.GOOS + "-" + goruntime.GOARCH
}

// Touch marks one item as used just now, by bumping the mtime of its installs/ and shims/
// version directories. Nothing in this codebase reads those mtimes anymore -- `cache prune` (see
// prune.go) is usage-registry-based now, not age-based -- so this is a historical no-op until
// something removes its call sites. Missing directories (a system_version runtime has none) and
// errors are ignored -- recording a use must never fail a run.
func Touch(root, category, id, version string) {
	now := time.Now()
	for _, dir := range []string{
		filepath.Join(root, "installs", category, id, version),
		filepath.Join(root, "shims", category, id, version),
	} {
		_ = os.Chtimes(dir, now, now)
	}
}
