package download_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/trunk/config"
	"github.com/xunleii/rtunk/pkg/trunk/download"
)

func TestMatchEntry(t *testing.T) {
	entries := []config.DownloadEntry{
		{
			OS:  config.OSSpec{"linux": "linux"},
			CPU: config.OSSpec{"arm_64": "aarch64", "x86_64": "x86_64"},
			URL: "linux-url",
		},
		{
			OS:  config.OSSpec{"macos": "macos"},
			CPU: config.OSSpec{"x86_64": "x86_64"},
			URL: "macos-url",
		},
	}

	entry, osVal, cpuVal, ok := download.MatchEntry(entries, "linux", "arm64", "")
	require.True(t, ok)
	assert.Equal(t, "linux-url", entry.URL)
	assert.Equal(t, "linux", osVal)
	assert.Equal(t, "aarch64", cpuVal)

	_, _, _, ok = download.MatchEntry(entries, "windows", "amd64", "")
	assert.False(t, ok, "no windows entry declared")

	_, _, _, ok = download.MatchEntry(entries, "macos", "arm64", "")
	assert.False(t, ok, "macos entry only declares x86_64")
}

// TestMatchEntry_VersionRange pins down a real production bug: python-build-standalone's plugin
// recipe lists multiple entries for the same OS/CPU, each gated to a version range via
// DownloadEntry.Version (e.g. "<=3.10.17" pointing at an older dated release, "<=3.14.4" at a
// newer one) -- MatchEntry used to return the first OS/CPU match regardless of that range,
// silently picking a release tag that doesn't host the actually-requested version, and 404ing.
func TestMatchEntry_VersionRange(t *testing.T) {
	entries := []config.DownloadEntry{
		{
			OS:      config.OSSpec{"macos": "apple-darwin"},
			CPU:     config.OSSpec{"arm_64": "aarch64"},
			URL:     "https://example.com/20250409/cpython-${version}.tar.gz",
			Version: "<=3.10.17",
		},
		{
			OS:      config.OSSpec{"macos": "apple-darwin"},
			CPU:     config.OSSpec{"arm_64": "aarch64"},
			URL:     "https://example.com/20260414/cpython-${version}.tar.gz",
			Version: "<=3.14.4",
		},
	}

	entry, _, _, ok := download.MatchEntry(entries, "darwin", "arm64", "3.14.4")
	require.True(t, ok)
	assert.Equal(t, "https://example.com/20260414/cpython-${version}.tar.gz", entry.URL,
		"3.14.4 satisfies only the second entry's <=3.14.4 range, not the first's <=3.10.17")

	entry, _, _, ok = download.MatchEntry(entries, "darwin", "arm64", "3.9.1")
	require.True(t, ok)
	assert.Equal(t, "https://example.com/20250409/cpython-${version}.tar.gz", entry.URL,
		"3.9.1 satisfies the first entry's range too, and it comes first -- first-match-wins still applies among satisfying entries")

	_, _, _, ok = download.MatchEntry(entries, "darwin", "arm64", "4.0.0")
	assert.False(t, ok, "4.0.0 satisfies neither entry's range")

	// An entry with no Version constraint always matches, same as before this fix.
	unconstrained := []config.DownloadEntry{{
		OS: config.OSSpec{"linux": "linux"}, CPU: config.OSSpec{"x86_64": "x86_64"}, URL: "linux-url",
	}}
	_, _, _, ok = download.MatchEntry(unconstrained, "linux", "amd64", "99.99.99")
	assert.True(t, ok, "an entry with no Version field must match any version")
}

// TestMatchEntry_VersionRange_AllOperators exercises VersionSatisfies' other four operators
// (">=", "<", ">", "=") -- TestMatchEntry_VersionRange only covers "<=" -- plus its fallback for a
// version string VersionSatisfies can't parse, which degrades to "matches" rather than rejecting.
func TestMatchEntry_VersionRange_AllOperators(t *testing.T) {
	entryWith := func(op string) []config.DownloadEntry {
		return []config.DownloadEntry{{
			OS: config.OSSpec{"linux": "linux"}, CPU: config.OSSpec{"x86_64": "x86_64"},
			URL: op + "-url", Version: op + "2.0.0",
		}}
	}

	_, _, _, ok := download.MatchEntry(entryWith(">="), "linux", "amd64", "2.0.0")
	assert.True(t, ok, "2.0.0 >= 2.0.0")
	_, _, _, ok = download.MatchEntry(entryWith(">="), "linux", "amd64", "1.9.9")
	assert.False(t, ok, "1.9.9 is not >= 2.0.0")

	_, _, _, ok = download.MatchEntry(entryWith("<"), "linux", "amd64", "1.9.9")
	assert.True(t, ok, "1.9.9 < 2.0.0")
	_, _, _, ok = download.MatchEntry(entryWith("<"), "linux", "amd64", "2.0.0")
	assert.False(t, ok, "2.0.0 is not < 2.0.0")

	_, _, _, ok = download.MatchEntry(entryWith(">"), "linux", "amd64", "2.0.1")
	assert.True(t, ok, "2.0.1 > 2.0.0")
	_, _, _, ok = download.MatchEntry(entryWith(">"), "linux", "amd64", "2.0.0")
	assert.False(t, ok, "2.0.0 is not > 2.0.0")

	_, _, _, ok = download.MatchEntry(entryWith("="), "linux", "amd64", "2.0.0")
	assert.True(t, ok, "2.0.0 = 2.0.0")
	_, _, _, ok = download.MatchEntry(entryWith("="), "linux", "amd64", "2.0.1")
	assert.False(t, ok, "2.0.1 != 2.0.0")

	// A non-numeric (e.g. pre-release) version component can't be compared -- VersionSatisfies
	// degrades to "matches" rather than guessing wrong and rejecting an otherwise-good entry.
	_, _, _, ok = download.MatchEntry(entryWith(">="), "linux", "amd64", "2.0.0a6")
	assert.True(t, ok, "an incomparable version must fall back to matching, not rejecting")
}
