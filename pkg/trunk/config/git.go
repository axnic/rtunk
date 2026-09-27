package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// fetchGitSource returns everything a git plugin source (uri/ref) contributes, preferring a cache
// of the already-parsed definitions at cacheFilePath(cacheDir, src) over touching the network at
// all. Caching is keyed by uri+ref (never mutates once fetched, since ref is always a tag or SHA
// per ARCHITECTURE.md), so a cache hit is safe to reuse indefinitely. The cache file is versioned
// (cacheSchemaVersion in cache.go), so a schema change never decodes into a silently
// under-populated result.
//
// Unlike the parsed-definitions cache, the git checkout itself is also persisted, at
// checkoutDirPath(cacheDir, src) -- not just cloned and discarded -- so ${plugin}/${cwd} (a
// Command.Run or Command.Parser.Run template var resolving into this source's own directory tree,
// e.g. a converter script real trunk-io linters like trufflehog ship next to their plugin.yaml)
// has real files to point at even on a warm run that never touches git again. Every Linter this
// source contributes gets its SourceRoot stamped to this checkout's directory (see setSourceRoot);
// a cache hit re-stamps it fresh every time, since SourceRoot is deliberately excluded from the
// cached JSON (see Linter.SourceRoot's own doc comment).
//
// On a cache miss -- a decode failure, a schema mismatch, or a cache hit whose paired checkout
// directory has gone missing (e.g. someone rm -rf'd just that one directory) -- both the parsed
// cache and the checkout are dropped and regenerated together: src's ref is cloned into a
// throwaway same-filesystem temp dir under cacheDir (so the later persist is an atomic rename,
// never a cross-device copy), parsed, then renamed into place at checkoutDirPath. dupErrs carries
// any *DuplicateError found while parsing (only possible on a cold fetch: a cache hit returns the
// already-deduplicated result, so there's nothing left to report).
func fetchGitSource(cacheDir string, src PluginSource) (defs sourceDefs, dupErrs []error, err error) {
	if cacheDir == "" {
		dir, err := os.UserCacheDir()
		if err != nil {
			return sourceDefs{}, nil, &FetchError{SourceID: src.ID, URI: src.URI, Ref: src.Ref, Err: err}
		}
		cacheDir = filepath.Join(dir, "rtunk", "plugins")
	} else {
		cacheDir = filepath.Join(cacheDir, "plugins")
	}

	cacheDir, err = filepath.Abs(cacheDir)
	if err != nil {
		return sourceDefs{}, nil, &FetchError{SourceID: src.ID, URI: src.URI, Ref: src.Ref, Err: err}
	}

	cacheFile := cacheFilePath(cacheDir, src)
	checkoutDir := checkoutDirPath(cacheDir, src)

	if defs, err := loadSourceCache(cacheFile); err == nil {
		if info, statErr := os.Stat(checkoutDir); statErr == nil && info.IsDir() {
			setSourceRoot(defs, checkoutDir)
			return defs, nil, nil
		}
		// The parsed-definitions cache survived but its paired checkout didn't -- treat exactly
		// like a decode failure and fall through to regenerate both together, below.
	}
	_ = os.Remove(cacheFile) // missing is fine; corrupt/stale/orphaned is dropped so it regenerates below

	if err := os.MkdirAll(cacheDir, 0o750); err != nil {
		return sourceDefs{}, nil, &FetchError{SourceID: src.ID, URI: src.URI, Ref: src.Ref, Err: err}
	}
	// MkdirTemp'd inside cacheDir (not the OS temp dir) so the persist step below is a same-
	// filesystem os.Rename -- atomic, no cross-device copy fallback needed.
	tmpDir, err := os.MkdirTemp(cacheDir, "checkout-tmp-*")
	if err != nil {
		return sourceDefs{}, nil, &FetchError{SourceID: src.ID, URI: src.URI, Ref: src.Ref, Err: err}
	}
	persisted := false
	defer func() {
		if !persisted {
			_ = os.RemoveAll(tmpDir)
		}
	}()

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

	checkoutParentDir := filepath.Dir(checkoutDir)
	if err := os.MkdirAll(checkoutParentDir, 0o750); err != nil {
		return sourceDefs{}, nil, &FetchError{SourceID: src.ID, URI: src.URI, Ref: src.Ref, Err: err}
	}
	// checkoutDir's content is immutable once fetched (keyed by src's own pinned uri+ref, per
	// ARCHITECTURE.md) -- if it already exists, whether from a concurrent process's own cold fetch
	// racing this one, or a still-good checkout left over from before (this regeneration only
	// happens because the *parsed-definitions* JSON cache was stale/corrupt/missing, not because
	// the checkout itself was suspect), its content is equivalent to what was just cloned here. So
	// on a failed rename, just use what's already there instead of erroring -- forcibly clearing
	// the destination first (a prior RemoveAll-then-Rename here) only recreated the exact race this
	// avoids: two processes' RemoveAll+Rename pairs interleaving deterministically failed with
	// "directory not empty".
	if err := os.Rename(tmpDir, checkoutDir); err != nil {
		if info, statErr := os.Stat(checkoutDir); statErr != nil || !info.IsDir() {
			return sourceDefs{}, nil, &FetchError{SourceID: src.ID, URI: src.URI, Ref: src.Ref, Err: err}
		}
		// lost the race (or the destination was already there): fall through: this call's own
		// tmpDir is now redundant, and the deferred cleanup at the top of this function removes it.
	} else {
		persisted = true // now living at checkoutDir; nothing left at tmpDir for the deferred cleanup
	}

	setSourceRoot(defs, checkoutDir)

	if err := saveSourceCache(cacheFile, defs); err != nil {
		return sourceDefs{}, nil, &FetchError{SourceID: src.ID, URI: src.URI, Ref: src.Ref, Err: err}
	}

	return defs, dupErrs, nil
}
