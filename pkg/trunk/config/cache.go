package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// sourceDefs holds every definition contributed by a single plugin source, unmerged — the shape
// both the on-disk cache stores and mergeSourceInto folds into a Config.
type sourceDefs struct {
	// Environments and CommentFormats are global config, not per-id definitions: no map, just
	// concatenated across every plugin.yaml that contributes them (ARCHITECTURE.md "Built-in /
	// global config").
	Environments   []NamedEnvironment
	CommentFormats []CommentFormat

	Downloads map[string]Download
	Tools     map[string]Tool
	Lint      map[string]Linter
	Files     map[string]FileType
	Actions   map[string]Action
	Runtimes  map[string]Runtime
}

// cacheSchemaVersion must be bumped whenever sourceDefs' shape gains a field an older cache file
// wouldn't populate -- json.Unmarshal silently leaves a new field zero-valued instead of erroring,
// so a stale cache written before the field existed looks like a normal cache hit, forever, unless
// something notices the version disagrees. Bumped for the check-engine-refactor branch's
// Command.ParseRegex: a cache written before that field existed silently produced an empty
// ParseRegex for every "regex"-output command, and ParseFromRegex compiling "" matches every byte
// offset in a linter's real output -- thousands of empty findings, not an error, on every
// upgrade for anyone with a warm cache. loadSourceCache rejects a version mismatch as a decode
// failure; fetchGitSource already treats any decode failure as "drop and regenerate" (see
// git.go), so this one check is the whole fix -- no new code path.
const cacheSchemaVersion = 1

// cacheEnvelope is what actually lives on disk: sourceDefs plus the schema version it was written
// under.
type cacheEnvelope struct {
	Version int
	Defs    sourceDefs
}

// cacheFilePath returns where src's cache lives: keyed by uri+ref, since a pinned ref never
// changes content.
func cacheFilePath(cacheDir string, src PluginSource) string {
	sum := sha256.Sum256([]byte(src.URI + "@" + src.Ref))
	return filepath.Join(cacheDir, hex.EncodeToString(sum[:])+".json")
}

// loadSourceCache reads path's cached sourceDefs, rejecting (as a decode failure, same as
// malformed JSON) anything not written under the current cacheSchemaVersion -- a cache file from
// before this field existed decodes with Version's zero value, which never matches a real
// (>= 1) cacheSchemaVersion, so it's correctly treated as a miss rather than a silently
// under-populated hit.
func loadSourceCache(path string) (sourceDefs, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return sourceDefs{}, err
	}

	var env cacheEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return sourceDefs{}, err
	}
	if env.Version != cacheSchemaVersion {
		return sourceDefs{}, fmt.Errorf("config: cache schema version %d, want %d", env.Version, cacheSchemaVersion)
	}
	return env.Defs, nil
}

// saveSourceCache writes defs atomically (temp file + rename) so a crash mid-write never leaves a
// half-written file behind to be mistaken for a valid cache hit.
func saveSourceCache(path string, defs sourceDefs) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	data, err := json.Marshal(cacheEnvelope{Version: cacheSchemaVersion, Defs: defs})
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op once renamed below

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
