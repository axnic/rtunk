// Package cache owns rtunk's cache root and every subdirectory under it:
// runtimes, tools, linters, and each repo's own workspace area. Flag/env/
// config precedence for the root is pkg/config's job (Config.ResolveCache),
// not this package's — Cache only ever deals with an already-decided root,
// and creates whatever directory it hands back.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
)

// Config is the "cache:" section of a .trunk/trunk.yaml or .rtunk/rtunk.yaml
// (an rtunk extension, absent from a real trunk.yaml) — defined here, not in
// pkg/config, so this package never depends on it.
type Config struct {
	// Dir overrides the cache directory. Empty means use the OS default.
	Dir string `yaml:"dir"`
}

// Cache is rtunk's cache root, resolved and ready to use.
type Cache struct {
	root string
}

// New creates root (if needed) and returns a Cache rooted there.
func New(root string) (*Cache, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &Cache{root: root}, nil
}

// Root is the cache's own root directory.
func (c *Cache) Root() string { return c.root }

// Runtimes is where managed language runtime distributions (node/python/go)
// are cached, created if needed.
func (c *Cache) Runtimes() (string, error) { return c.sub("runtime") }

// Tools is where standalone managed CLI tools (config.Tools) are cached,
// created if needed.
func (c *Cache) Tools() (string, error) { return c.sub("tools") }

// Linters is where linter/formatter binaries (config.Lint) are cached,
// created if needed.
func (c *Cache) Linters() (string, error) { return c.sub("linters") }

// Plugins is where resolved plugins.sources catalogs are cached (one .gob
// per repo+ref — see pkg/plugin.CachePath), created if needed.
func (c *Cache) Plugins() (string, error) { return c.sub("plugins") }

func (c *Cache) sub(name string) (string, error) {
	dir := filepath.Join(c.root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// Repo identifies a repo's own cache area: URL is its git remote ("" if none
// configured, Root is then the fallback identity so the cache survives a
// re-clone at a different local path when a remote IS configured); Lock is
// a fingerprint of the resolved trunk config (config.Config.Lock) so the
// area is invalidated whenever the config that produced it changes.
type Repo struct {
	URL  string
	Root string
	Lock string
}

// RepoKey derives a stable, portable identifier for repo.
func RepoKey(repo Repo) string {
	key := repo.URL
	if key == "" {
		key = repo.Root
	}
	sum := sha256.Sum256([]byte(key + "\x00" + repo.Lock))
	return hex.EncodeToString(sum[:])[:32]
}

// Workspace is repo's own cache area: <root>/repos/<RepoKey(repo)>/ — where
// a Tool/Runtime's EnsureProject symlinks its shared, version-pinned
// install for this specific repo. Created if needed.
func (c *Cache) Workspace(repo Repo) (string, error) {
	dir := filepath.Join(c.root, "repos", RepoKey(repo))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}
