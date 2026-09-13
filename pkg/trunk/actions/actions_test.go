package actions_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/trunk/actions"
	"github.com/xunleii/rtunk/pkg/trunk/config"
)

func testConfig() config.Config {
	return config.Config{
		Actions: config.CategoryConfig[config.Action]{
			Enabled: []string{"commitlint", "trunk-announce"},
			Definitions: map[string]config.Action{
				"commitlint":     {ID: "commitlint", Triggers: []config.Trigger{{GitHooks: []string{"commit-msg"}}}},
				"trunk-announce": {ID: "trunk-announce", Triggers: []config.Trigger{{GitHooks: []string{"post-checkout", "post-merge"}}}},
			},
		},
	}
}

func TestResolve_NoHook_ReturnsAllSortedByID(t *testing.T) {
	got := actions.Resolve(testConfig(), "")
	require.Len(t, got, 2)
	assert.Equal(t, "commitlint", got[0].ID)
	assert.Equal(t, "trunk-announce", got[1].ID)
}

func TestResolve_ByHook_FiltersToMatchingTrigger(t *testing.T) {
	got := actions.Resolve(testConfig(), "post-merge")
	require.Len(t, got, 1)
	assert.Equal(t, "trunk-announce", got[0].ID)
}

func TestResolve_ByHook_NoMatch_ReturnsEmpty(t *testing.T) {
	got := actions.Resolve(testConfig(), "pre-push")
	assert.Empty(t, got)
}
