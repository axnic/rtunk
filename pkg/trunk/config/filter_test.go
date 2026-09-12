package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestFilterEnabled exercises the reference graph filterEnabled walks directly on a hand-built
// Config, independent of any plugin.yaml fixture: an enabled linter pulls in its tool, that tool's
// runtime and download, an enabled action pulls in its own runtime, and that runtime's download —
// while everything unreferenced (an unrelated tool/runtime/download/action/linter) is dropped.
func TestFilterEnabled(t *testing.T) {
	cfg := Config{
		// Global, non-enableable config — filterEnabled must leave both untouched, unlike every
		// enabled:/Definitions pair below.
		Environments: []NamedEnvironment{{Name: "SYSTEM"}},
		Lint: LintConfig{
			CategoryConfig: CategoryConfig[Linter]{
				Enabled: []string{"eslint@8.10.0"}, // pinned: filterEnabled must strip the @version
				Definitions: map[string]Linter{
					"eslint": {Name: "eslint", Tools: []string{"eslint-bin"}, Files: []string{"github-workflow"}},
					"unused": {Name: "unused", Tools: []string{"orphan-tool"}, Files: []string{"orphan-file"}},
				},
			},
			CommentFormats: []CommentFormat{{Name: "hash", LeadingDelimiter: "#"}},
			Files: map[string]FileType{
				"github-workflow": {Name: "github-workflow", Inherit: []string{"yaml"}},
				"yaml":            {Name: "yaml"},
				"orphan-file":     {Name: "orphan-file"},
			},
		},
		Actions: CategoryConfig[Action]{
			Enabled: []string{"commitlint"},
			Definitions: map[string]Action{
				"commitlint": {ID: "commitlint", Runtime: "python"},
				"unused":     {ID: "unused", Runtime: "go"},
			},
		},
		Runtimes: CategoryConfig[Runtime]{
			// "node" is never in an enabled: list — it's only reachable via eslint-bin's tool
			// definition below, proving the tool -> runtime hop works.
			Definitions: map[string]Runtime{
				"node":   {Type: "node", Download: "node"},
				"python": {Type: "python", Download: "python"},
				"go":     {Type: "go", Download: "go"},
			},
		},
		Tools: map[string]Tool{
			"eslint-bin":  {Name: "eslint-bin", Runtime: "node"},
			"orphan-tool": {Name: "orphan-tool", Download: "orphan-download"},
		},
		Downloads: map[string]Download{
			"node":            {Name: "node"},
			"python":          {Name: "python"},
			"go":              {Name: "go"},
			"orphan-download": {Name: "orphan-download"},
		},
	}

	filterEnabled(&cfg)

	assert.Equal(t, map[string]Linter{"eslint": cfg.Lint.Definitions["eslint"]}, cfg.Lint.Definitions)
	assert.Equal(t, map[string]Action{"commitlint": cfg.Actions.Definitions["commitlint"]}, cfg.Actions.Definitions)
	assert.Equal(t, map[string]Tool{"eslint-bin": cfg.Tools["eslint-bin"]}, cfg.Tools)
	assert.ElementsMatch(t, []string{"node", "python"}, keysOf(cfg.Runtimes.Definitions))
	assert.ElementsMatch(t, []string{"node", "python"}, keysOf(cfg.Downloads))
	assert.Equal(t, []NamedEnvironment{{Name: "SYSTEM"}}, cfg.Environments)
	assert.Equal(t, []CommentFormat{{Name: "hash", LeadingDelimiter: "#"}}, cfg.Lint.CommentFormats)
	// "yaml" is only reachable via github-workflow's inherit: — proves the closure is followed,
	// not just files: itself; "orphan-file" is dropped since "unused" was never enabled.
	assert.ElementsMatch(t, []string{"github-workflow", "yaml"}, keysOf(cfg.Lint.Files))
}

func keysOf[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestFilterEnabled_ParserRuntimeTransitivelyKept: a Command.Parser.Runtime must be pulled in the
// same way a Tool's or Action's Runtime already is -- an enabled linter whose only command sets
// Parser.Runtime (no Tool/Action needs that runtime at all) must still keep it, not have Resolve's
// trim silently drop the very runtime its parser script needs.
func TestFilterEnabled_ParserRuntimeTransitivelyKept(t *testing.T) {
	cfg := Config{
		Lint: LintConfig{
			CategoryConfig: CategoryConfig[Linter]{
				Enabled: []string{"trufflehog"},
				Definitions: map[string]Linter{
					"trufflehog": {
						Name: "trufflehog",
						Commands: []Command{
							{Name: "lint", Run: "trufflehog ${target}", Parser: &Parser{Runtime: "python", Run: "convert.py"}},
						},
					},
				},
			},
		},
		Runtimes: CategoryConfig[Runtime]{
			Definitions: map[string]Runtime{
				"python": {Type: "python", Download: "python"},
			},
		},
		Downloads: map[string]Download{
			"python": {Name: "python"},
		},
	}

	filterEnabled(&cfg)

	assert.Contains(t, cfg.Runtimes.Definitions, "python")
	assert.Contains(t, cfg.Downloads, "python")
}
