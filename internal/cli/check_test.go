package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/trunk/config"
	"github.com/xunleii/rtunk/pkg/trunk/engine"
	"github.com/xunleii/rtunk/pkg/trunk/output"
)

// TestDrainEvents_DisableUpstream_SuppressesEnabledSupersededLinterFindings covers this task's
// core case: "combined" supersedes "narrow" via DisableUpstream, both are enabled, and both
// produced a finding -- "narrow"'s finding must be dropped.
func TestDrainEvents_DisableUpstream_SuppressesEnabledSupersededLinterFindings(t *testing.T) {
	cfg := config.Config{
		Lint: config.LintConfig{
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"combined": {Commands: []config.Command{{DisableUpstream: []string{"narrow"}}}},
					"narrow":   {},
				},
			},
		},
	}
	events := []engine.Event{
		{Linter: "combined", Phase: engine.Done, Findings: []output.Finding{{Linter: "combined"}}},
		{Linter: "narrow", Phase: engine.Done, Findings: []output.Finding{{Linter: "narrow"}}},
	}

	findings, _, _, _ := drainEvents(cfg, events, func(engine.Event) {})

	var linters []string
	for _, f := range findings {
		linters = append(linters, f.Linter)
	}
	assert.Contains(t, linters, "combined")
	assert.NotContains(t, linters, "narrow")
}

// TestDrainEvents_DisableUpstream_KeepsFindings_WhenSupersededLinterNotEnabled covers a
// DisableUpstream id that isn't a key in cfg.Lint.Definitions at all (a real config could still
// reference a since-disabled id) -- the suppression logic must not error/panic, and since "narrow"
// never even ran here, its finding (which couldn't exist in practice) is irrelevant; what matters
// is that "combined"'s own finding survives untouched.
func TestDrainEvents_DisableUpstream_KeepsFindings_WhenSupersededLinterNotEnabled(t *testing.T) {
	cfg := config.Config{
		Lint: config.LintConfig{
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"combined": {Commands: []config.Command{{DisableUpstream: []string{"narrow"}}}},
				},
			},
		},
	}
	events := []engine.Event{
		{Linter: "combined", Phase: engine.Done, Findings: []output.Finding{{Linter: "combined"}}},
	}

	findings, _, _, _ := drainEvents(cfg, events, func(engine.Event) {})

	assert.Len(t, findings, 1)
	assert.Equal(t, "combined", findings[0].Linter)
}

// TestDrainEvents_DisableUpstream_KeepsFindings_WhenSupersedingLinterProducedNothing is this
// task's Review Focus item: "combined" is enabled and declares DisableUpstream: []string{"narrow"}
// but its own Done event carries zero findings -- suppression must not fire on being merely
// enabled, so "narrow"'s finding survives.
func TestDrainEvents_DisableUpstream_KeepsFindings_WhenSupersedingLinterProducedNothing(t *testing.T) {
	cfg := config.Config{
		Lint: config.LintConfig{
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"combined": {Commands: []config.Command{{DisableUpstream: []string{"narrow"}}}},
					"narrow":   {},
				},
			},
		},
	}
	events := []engine.Event{
		{Linter: "combined", Phase: engine.Done},
		{Linter: "narrow", Phase: engine.Done, Findings: []output.Finding{{Linter: "narrow"}}},
	}

	findings, _, _, _ := drainEvents(cfg, events, func(engine.Event) {})

	assert.Len(t, findings, 1)
	assert.Equal(t, "narrow", findings[0].Linter)
}

// TestDrainEvents_DisableUpstream_PrintFnAlsoSeesFiltered is the gap the final whole-branch
// review found: earlier tests only asserted drainEvents' own return value, which was filtered,
// while the events actually handed to printFn (what the real renderer -- internal/cli/render's
// report-building -- reads Findings from) were still the raw, unfiltered ones. printFn here
// records every event it receives so the test can inspect what the renderer would actually have
// seen, not just what drainEvents returns.
func TestDrainEvents_DisableUpstream_PrintFnAlsoSeesFiltered(t *testing.T) {
	cfg := config.Config{
		Lint: config.LintConfig{
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"combined": {Commands: []config.Command{{DisableUpstream: []string{"narrow"}}}},
					"narrow":   {},
				},
			},
		},
	}
	events := []engine.Event{
		{Linter: "combined", Phase: engine.Done, Findings: []output.Finding{{Linter: "combined"}}},
		{Linter: "narrow", Phase: engine.Done, Findings: []output.Finding{{Linter: "narrow"}}},
	}

	var printed []engine.Event
	findings, _, _, _ := drainEvents(cfg, events, func(ev engine.Event) { printed = append(printed, ev) })

	// The returned findings must be filtered (already covered above, re-asserted here to tie the
	// two together)...
	var linters []string
	for _, f := range findings {
		linters = append(linters, f.Linter)
	}
	assert.NotContains(t, linters, "narrow")

	// ...and so must every Done event actually handed to printFn -- the thing the real renderer
	// consumes.
	require.Len(t, printed, 2)
	for _, ev := range printed {
		for _, f := range ev.Findings {
			assert.NotEqual(t, "narrow", f.Linter, "printFn must never see a suppressed linter's finding")
		}
	}

	// events itself must not have been mutated in place -- drainEvents/suppressUpstreamEvents
	// build a fresh slice instead.
	require.Len(t, events[1].Findings, 1)
	assert.Equal(t, "narrow", events[1].Findings[0].Linter)
}
