package config

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"

	"github.com/goccy/go-yaml"

	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/cache"
)

// ResolveCache resolves rtunk's cache root — flag > $RTUNK_CACHE_DIR env >
// c.Cache.Dir > OS default (os.UserCacheDir(), i.e. $XDG_CACHE_HOME or
// ~/.cache on Linux, ~/Library/Caches on macOS, %LocalAppData% on Windows)
// — and returns a ready-to-use cache.Cache. This is the only place rtunk
// resolves the flag/env precedence for the cache root; every other package
// just takes the resulting cache.Cache.
func (c Config) ResolveCache(flag string) (*cache.Cache, error) {
	root := flag
	if root == "" {
		root = os.Getenv("RTUNK_CACHE_DIR")
	}
	if root == "" {
		root = c.Cache.Dir
	}
	if root == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			return nil, err
		}
		root = filepath.Join(base, "rtunk")
	}
	return cache.New(root)
}

// Lock fingerprints c's content, stable across re-marshaling (so incidental
// formatting differences in the source YAML don't change it) — used to key
// a repo's own cache area (cache.Repo.Lock) so it's invalidated whenever
// the config that produced it changes.
func (c Config) Lock() (string, error) {
	data, err := yaml.Marshal(c)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
