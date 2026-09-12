package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSaveLoadSourceCache_RoundTrips(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")

	want := sourceDefs{
		Lint: map[string]Linter{"foo": {Name: "foo"}},
	}
	require.NoError(t, saveSourceCache(path, want))

	got, err := loadSourceCache(path)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

// TestLoadSourceCache_RejectsWrongSchemaVersion covers the real regression this test exists to
// prevent: a cache file written before Command.ParseRegex existed (or any future field added
// without a version bump) decodes without error into a JSON envelope missing the new field --
// json.Unmarshal leaves it zero-valued rather than failing -- so version-checking is the only
// thing that turns that silent under-population into a cache miss instead of a corrupted hit.
func TestLoadSourceCache_RejectsWrongSchemaVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")

	data, err := json.Marshal(cacheEnvelope{
		Version: cacheSchemaVersion - 1,
		Defs:    sourceDefs{Lint: map[string]Linter{"foo": {Name: "foo"}}},
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o644))

	_, err = loadSourceCache(path)
	assert.Error(t, err, "a cache written under an older schema version must be treated as a miss, not a hit with silently-zeroed new fields")
}

// TestLoadSourceCache_RejectsPreVersioningCacheFile covers the exact real-world shape: a cache
// file written by a version of this project that predates cacheEnvelope entirely -- a flat
// sourceDefs JSON object with no "Version"/"Defs" wrapping at all.
func TestLoadSourceCache_RejectsPreVersioningCacheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")

	// The pre-versioning on-disk shape: sourceDefs marshaled directly, no envelope.
	data, err := json.Marshal(sourceDefs{Lint: map[string]Linter{"foo": {Name: "foo"}}})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o644))

	_, err = loadSourceCache(path)
	assert.Error(t, err, "a pre-versioning flat sourceDefs file must decode to Version 0, which never matches a real cacheSchemaVersion")
}
