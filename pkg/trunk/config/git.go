package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// fetchGitSource returns everything a git plugin source (uri/ref) contributes, preferring a
// cache of the already-parsed definitions at cacheDir/<sha256(uri+ref)>.json over touching the
// network at all. Caching is keyed by uri+ref (never mutates once fetched, since ref is always a
// tag or SHA per ARCHITECTURE.md), so a cache hit is safe to reuse indefinitely. The cache file is
// versioned (cacheSchemaVersion in cache.go), so a schema change never decodes into a silently
// under-populated result.
//
// On a cache miss — or a cache file that fails to decode, e.g. corrupted or from an incompatible
// rtunk version — it's dropped and regenerated: src's ref is cloned into a throwaway temp dir
// (removed once parsing finishes, never persisted itself), parsed, and the result written back to
// the cache for next time. dupErrs carries any *DuplicateError found while parsing (only possible
// on a cold fetch: a cache hit returns the already-deduplicated result, so there's nothing left to
// report).
func fetchGitSource(cacheDir string, src PluginSource) (defs sourceDefs, dupErrs []error, err error) {
	if cacheDir == "" {
		dir, err := os.UserCacheDir()
		if err != nil {
			return sourceDefs{}, nil, &FetchError{SourceID: src.ID, URI: src.URI, Ref: src.Ref, Err: err}
		}
		cacheDir = filepath.Join(dir, "rtunk", "plugins")
	}

	cacheFile := cacheFilePath(cacheDir, src)

	if defs, err := loadSourceCache(cacheFile); err == nil {
		return defs, nil, nil
	}
	_ = os.Remove(cacheFile) // missing is fine; corrupt/stale is dropped so it regenerates below

	tmpDir, err := os.MkdirTemp("", "rtunk-plugin-*")
	if err != nil {
		return sourceDefs{}, nil, &FetchError{SourceID: src.ID, URI: src.URI, Ref: src.Ref, Err: err}
	}
	defer os.RemoveAll(tmpDir) // only the parsed result is cached, never the checkout itself

	for _, args := range [][]string{
		{"init"},
		{"remote", "add", "origin", src.URI},
		{"fetch", "--depth", "1", "origin", src.Ref},
		{"checkout", "FETCH_HEAD"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = tmpDir
		if out, err := cmd.CombinedOutput(); err != nil {
			return sourceDefs{}, nil, &FetchError{SourceID: src.ID, URI: src.URI, Ref: src.Ref, Err: fmt.Errorf("%v: %s", err, out)}
		}
	}

	defs, dupErrs, err = parseSourceDir(tmpDir)
	if err != nil {
		return sourceDefs{}, nil, err
	}

	if err := saveSourceCache(cacheFile, defs); err != nil {
		return sourceDefs{}, nil, &FetchError{SourceID: src.ID, URI: src.URI, Ref: src.Ref, Err: err}
	}

	return defs, dupErrs, nil
}
