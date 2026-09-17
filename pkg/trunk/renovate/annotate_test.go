package renovate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/trunk/config"
)

func TestForLint_DownloadRecipe_AllGitHubEntries_OK(t *testing.T) {
	cfg := config.Config{
		Lint: config.LintConfig{CategoryConfig: config.CategoryConfig[config.Linter]{
			Definitions: map[string]config.Linter{
				"actionlint": {Tools: []string{"actionlint"}},
			},
		}},
		Tools: map[string]config.Tool{
			"actionlint": {Download: "actionlint-dl", KnownGoodVersion: "1.7.8"},
		},
		Downloads: map[string]config.Download{
			"actionlint-dl": {Downloads: []config.DownloadEntry{
				{URL: "https://github.com/rhysd/actionlint/releases/download/v${version}/actionlint_${version}_linux_amd64.tar.gz"},
				{URL: "https://github.com/rhysd/actionlint/releases/download/v${version}/actionlint_${version}_darwin_arm64.tar.gz"},
			}},
		},
	}

	ann, known, ok := ForLint(cfg, "actionlint")
	require.True(t, ok)
	assert.Equal(t, Annotation{Datasource: "github-releases", DepName: "rhysd/actionlint"}, ann)
	assert.Equal(t, "1.7.8", known)
}

func TestForLint_DownloadRecipe_MismatchedHostAcrossEntries_Skip(t *testing.T) {
	cfg := config.Config{
		Lint: config.LintConfig{CategoryConfig: config.CategoryConfig[config.Linter]{
			Definitions: map[string]config.Linter{"mixed": {Tools: []string{"mixed"}}},
		}},
		Tools: map[string]config.Tool{"mixed": {Download: "mixed-dl", KnownGoodVersion: "1.0.0"}},
		Downloads: map[string]config.Download{
			"mixed-dl": {Downloads: []config.DownloadEntry{
				{URL: "https://github.com/foo/bar/releases/download/v1/a.tar.gz"},
				{URL: "https://github.com/foo/other/releases/download/v1/b.tar.gz"},
			}},
		},
	}

	_, _, ok := ForLint(cfg, "mixed")
	assert.False(t, ok)
}

func TestForLint_DownloadRecipe_NonGitHubHost_Skip(t *testing.T) {
	cfg := config.Config{
		Lint: config.LintConfig{CategoryConfig: config.CategoryConfig[config.Linter]{
			Definitions: map[string]config.Linter{"helm": {Tools: []string{"helm"}}},
		}},
		Tools: map[string]config.Tool{"helm": {Download: "helm-dl", KnownGoodVersion: "3.14.0"}},
		Downloads: map[string]config.Download{
			"helm-dl": {Downloads: []config.DownloadEntry{
				{URL: "https://get.helm.sh/helm-v${version}-linux-amd64.tar.gz"},
			}},
		},
	}

	_, _, ok := ForLint(cfg, "helm")
	assert.False(t, ok)
}

func TestForLint_RuntimePackage_AllFiveEcosystems_OK(t *testing.T) {
	cases := []struct {
		runtime, wantDatasource string
	}{
		{"node", "npm"},
		{"python", "pypi"},
		{"php", "packagist"},
		{"go", "go"},
		{"rust", "crate"},
	}
	for _, c := range cases {
		t.Run(c.runtime, func(t *testing.T) {
			cfg := config.Config{
				Lint: config.LintConfig{CategoryConfig: config.CategoryConfig[config.Linter]{
					Definitions: map[string]config.Linter{"tool": {Tools: []string{"tool"}}},
				}},
				Tools: map[string]config.Tool{
					"tool": {Runtime: c.runtime, Package: "some/package/path", KnownGoodVersion: "9.9.9"},
				},
			}

			ann, known, ok := ForLint(cfg, "tool")
			require.True(t, ok)
			assert.Equal(t, Annotation{Datasource: c.wantDatasource, DepName: "some/package/path"}, ann)
			assert.Equal(t, "9.9.9", known)
		})
	}
}

func TestForLint_RuntimePackage_UnrecognizedEcosystem_Skip(t *testing.T) {
	cfg := config.Config{
		Lint: config.LintConfig{CategoryConfig: config.CategoryConfig[config.Linter]{
			Definitions: map[string]config.Linter{"tool": {Tools: []string{"tool"}}},
		}},
		Tools: map[string]config.Tool{
			"tool": {Runtime: "dotnet", Package: "SomePackage", KnownGoodVersion: "1.0.0"},
		},
	}

	_, _, ok := ForLint(cfg, "tool")
	assert.False(t, ok)
}

func TestForLint_ZeroTools_Skip(t *testing.T) {
	cfg := config.Config{
		Lint: config.LintConfig{CategoryConfig: config.CategoryConfig[config.Linter]{
			Definitions: map[string]config.Linter{"orphan": {Tools: nil}},
		}},
	}

	_, _, ok := ForLint(cfg, "orphan")
	assert.False(t, ok)
}

func TestForLint_TwoTools_AmbiguousSkip(t *testing.T) {
	cfg := config.Config{
		Lint: config.LintConfig{CategoryConfig: config.CategoryConfig[config.Linter]{
			Definitions: map[string]config.Linter{"multi": {Tools: []string{"a", "b"}}},
		}},
		Tools: map[string]config.Tool{
			"a": {Runtime: "node", Package: "pkg-a", KnownGoodVersion: "1.0.0"},
			"b": {Runtime: "node", Package: "pkg-b", KnownGoodVersion: "1.0.0"},
		},
	}

	_, _, ok := ForLint(cfg, "multi")
	assert.False(t, ok)
}

func TestForLint_UnknownLinterID_Skip(t *testing.T) {
	_, _, ok := ForLint(config.Config{}, "does-not-exist")
	assert.False(t, ok)
}

func TestForRuntime_DownloadRecipe_OK(t *testing.T) {
	cfg := config.Config{
		Runtimes: config.CategoryConfig[config.Runtime]{
			Definitions: map[string]config.Runtime{
				"node": {Download: "node-dl", KnownGoodVersion: "22.18.0"},
			},
		},
		Downloads: map[string]config.Download{
			"node-dl": {Downloads: []config.DownloadEntry{
				{URL: "https://github.com/nodejs-release-mirror/node/releases/download/v${version}/node.tar.gz"},
			}},
		},
	}

	ann, known, ok := ForRuntime(cfg, "node")
	require.True(t, ok)
	assert.Equal(t, Annotation{Datasource: "github-releases", DepName: "nodejs-release-mirror/node"}, ann)
	assert.Equal(t, "22.18.0", known)
}

func TestForRuntime_NonGitHubHost_Skip(t *testing.T) {
	cfg := config.Config{
		Runtimes: config.CategoryConfig[config.Runtime]{
			Definitions: map[string]config.Runtime{
				"node": {Download: "node-dl", KnownGoodVersion: "22.18.0"},
			},
		},
		Downloads: map[string]config.Download{
			"node-dl": {Downloads: []config.DownloadEntry{
				{URL: "https://nodejs.org/dist/v${version}/node.tar.gz"},
			}},
		},
	}

	_, _, ok := ForRuntime(cfg, "node")
	assert.False(t, ok)
}

func TestForRuntime_UnknownID_Skip(t *testing.T) {
	_, _, ok := ForRuntime(config.Config{}, "does-not-exist")
	assert.False(t, ok)
}

func TestForPluginSource_GitSource_OK(t *testing.T) {
	src := config.PluginSource{ID: "origin", URI: "https://github.com/trunk-io/plugins", Ref: "v1.11.0"}

	ann, ok := ForPluginSource(src)
	require.True(t, ok)
	assert.Equal(t, Annotation{Datasource: "github-tags", DepName: "trunk-io/plugins"}, ann)
}

func TestForPluginSource_LocalSource_Skip(t *testing.T) {
	src := config.PluginSource{ID: "local", Local: "../pluginrepo"}

	_, ok := ForPluginSource(src)
	assert.False(t, ok)
}

func TestForPluginSource_NonGitHubURI_Skip(t *testing.T) {
	src := config.PluginSource{ID: "gl", URI: "https://gitlab.com/example/plugins", Ref: "v1.0.0"}

	_, ok := ForPluginSource(src)
	assert.False(t, ok)
}
