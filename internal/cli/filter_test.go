package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/trunk/config"
)

func threeLinterConfig() config.Config {
	return config.Config{
		Lint: config.LintConfig{
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"actionlint": {Description: "lints workflows"},
					"prettier":   {Description: "formats"},
					"shellcheck": {Description: "lints shell"},
				},
			},
		},
	}
}

func TestFilterLinters_NeitherGiven_ReturnsUnchanged(t *testing.T) {
	cfg := threeLinterConfig()
	got, err := filterLinters(cfg, "", "")
	require.NoError(t, err)
	assert.Equal(t, cfg, got)
}

func TestFilterLinters_FilterAllowList(t *testing.T) {
	got, err := filterLinters(threeLinterConfig(), "actionlint,prettier", "")
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"actionlint": true, "prettier": true}, keysOf(got.Lint.Definitions))
}

func TestFilterLinters_FilterDenyList(t *testing.T) {
	got, err := filterLinters(threeLinterConfig(), "-prettier", "")
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"actionlint": true, "shellcheck": true}, keysOf(got.Lint.Definitions))
}

func TestFilterLinters_FilterMixedAllowAndDeny_IsUsageError(t *testing.T) {
	_, err := filterLinters(threeLinterConfig(), "actionlint,-prettier", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mix")
}

func TestFilterLinters_UnknownIDInFilter_IsUsageError(t *testing.T) {
	_, err := filterLinters(threeLinterConfig(), "does-not-exist", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"does-not-exist"`)
}

func TestFilterLinters_UnknownIDInExclude_IsUsageError(t *testing.T) {
	_, err := filterLinters(threeLinterConfig(), "", "does-not-exist")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"does-not-exist"`)
}

func TestFilterLinters_Exclude(t *testing.T) {
	got, err := filterLinters(threeLinterConfig(), "", "shellcheck")
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"actionlint": true, "prettier": true}, keysOf(got.Lint.Definitions))
}

func TestFilterLinters_ExcludeMultiple(t *testing.T) {
	got, err := filterLinters(threeLinterConfig(), "", "shellcheck,prettier")
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"actionlint": true}, keysOf(got.Lint.Definitions))
}

func TestFilterLinters_BothFilterAndExclude_IsUsageError(t *testing.T) {
	_, err := filterLinters(threeLinterConfig(), "actionlint", "shellcheck")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--filter and --exclude")
}

func keysOf(m map[string]config.Linter) map[string]bool {
	out := map[string]bool{}
	for k := range m {
		out[k] = true
	}
	return out
}
