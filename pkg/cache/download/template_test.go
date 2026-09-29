package download_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/cache/download"
)

func TestTemplateURL(t *testing.T) {
	got := download.TemplateURL(
		"https://github.com/koalaman/shellcheck/releases/download/v${version}/shellcheck-v${version}.${os}.${cpu}.tar.xz",
		"0.11.0", "linux", "x86_64", nil,
	)
	want := "https://github.com/koalaman/shellcheck/releases/download/v0.11.0/shellcheck-v0.11.0.linux.x86_64.tar.xz"
	assert.Equal(t, want, got)
}

// TestTemplateURL_WithExtraArgs proves a Download's own derived args (see ResolveArgs) substitute
// into a URL alongside the built-in ${version}/${os}/${cpu} vars -- real taplo's own ${semver}.
func TestTemplateURL_WithExtraArgs(t *testing.T) {
	got := download.TemplateURL(
		"https://github.com/tamasfe/taplo/releases/download/${semver}/taplo-${os}-${cpu}.gz",
		"release-cli-0.10.0", "darwin", "aarch64",
		map[string]string{"semver": "0.10.0"},
	)
	want := "https://github.com/tamasfe/taplo/releases/download/0.10.0/taplo-darwin-aarch64.gz"
	assert.Equal(t, want, got)
}

// TestResolveArgs_StripsReleaseTagPrefix proves ResolveArgs' real-world job: taplo's own real
// GitHub releases are tagged like "release-cli-0.10.0" or "release-taplo-cli-0.10.0", but its
// release ASSETS are named using the bare "0.10.0" -- args: extracts that via a regex capture
// group, independent of what the arg's own map key happens to be named.
func TestResolveArgs_StripsReleaseTagPrefix(t *testing.T) {
	args := map[string]string{
		"semver": "${version}=>(?:release-cli-|release-taplo-cli-)?(?P<semver>.*)",
	}

	got, err := download.ResolveArgs(args, "release-cli-0.10.0", "darwin", "aarch64")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"semver": "0.10.0"}, got)

	// A version with no recognized prefix at all must still match (the (?:...)? group is
	// optional) -- confirms the regex doesn't require the prefix to be present.
	got, err = download.ResolveArgs(args, "0.9.0", "darwin", "aarch64")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"semver": "0.9.0"}, got)
}

func TestResolveArgs_EmptyArgsReturnsEmptyMap(t *testing.T) {
	got, err := download.ResolveArgs(nil, "1.0.0", "linux", "x86_64")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestResolveArgs_InvalidSyntaxIsAnError(t *testing.T) {
	_, err := download.ResolveArgs(map[string]string{"semver": "${version}"}, "1.0.0", "linux", "x86_64")
	assert.ErrorContains(t, err, `expected "<template>=><regex>"`)
}

func TestResolveArgs_NonMatchingRegexIsAnError(t *testing.T) {
	_, err := download.ResolveArgs(map[string]string{"semver": "${version}=>^v(?P<semver>.*)$"}, "1.0.0", "linux", "x86_64")
	assert.ErrorContains(t, err, "did not match")
}
