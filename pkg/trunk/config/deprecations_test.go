package config_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/trunk/config"
)

func TestCheckDeprecations_LegacyShape_Refused(t *testing.T) {
	cfg := config.Config{Lint: config.LintConfig{CategoryConfig: config.CategoryConfig[config.Linter]{
		Definitions: map[string]config.Linter{
			"rubocop-fmt": {
				Name: "rubocop-fmt", LegacyType: "rewrite", LegacyCommand: []string{"rubocop", "--fix-layout", "${target}"},
				Deprecated: "rubocop-fmt is now handled by rubocop. Please delete rubocop-fmt from your config",
			},
		},
	}}}

	_, err := cfg.CheckDeprecations()
	require.Error(t, err)
	var shapeErr *config.LegacyLinterShapeError
	require.True(t, errors.As(err, &shapeErr))
	assert.Equal(t, "rubocop-fmt", shapeErr.LinterID)
	assert.Contains(t, err.Error(), "rubocop")
}

func TestCheckDeprecations_LegacyShape_NoDeprecatedMessage_StillRefused(t *testing.T) {
	cfg := config.Config{Lint: config.LintConfig{CategoryConfig: config.CategoryConfig[config.Linter]{
		Definitions: map[string]config.Linter{
			"old": {Name: "old", LegacyType: "rewrite", LegacyCommand: []string{"old-tool"}},
		},
	}}}

	_, err := cfg.CheckDeprecations()
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "<nil>")
}

func TestCheckDeprecations_ModernShape_NeverRefused(t *testing.T) {
	cfg := config.Config{Lint: config.LintConfig{CategoryConfig: config.CategoryConfig[config.Linter]{
		Definitions: map[string]config.Linter{
			"black": {Name: "black", Commands: []config.Command{{Name: "format"}}},
		},
	}}}

	_, err := cfg.CheckDeprecations()
	require.NoError(t, err)
}

func TestCheckDeprecations_DeprecatedCommand_Warns(t *testing.T) {
	cfg := config.Config{Lint: config.LintConfig{CategoryConfig: config.CategoryConfig[config.Linter]{
		Definitions: map[string]config.Linter{
			"eslint": {Name: "eslint", Commands: []config.Command{
				{Name: "lint-legacy", Deprecated: "lint-legacy is now handled by lint. Please remove it from your config"},
				{Name: "lint"},
			}},
		},
	}}}

	warnings, err := cfg.CheckDeprecations()
	require.NoError(t, err)
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "eslint")
	assert.Contains(t, warnings[0], "lint-legacy")
}

func TestCheckDeprecations_NotEnabled_NoWarning(t *testing.T) {
	// Resolve already trims cfg.Lint.Definitions to enabled-only -- CheckDeprecations must not
	// need its own enabled-list check, since a definition that survived filterEnabled is, by
	// construction, enabled.
	cfg := config.Config{Lint: config.LintConfig{CategoryConfig: config.CategoryConfig[config.Linter]{
		Definitions: map[string]config.Linter{}, // as if filterEnabled dropped everything
	}}}

	warnings, err := cfg.CheckDeprecations()
	require.NoError(t, err)
	assert.Empty(t, warnings)
}
