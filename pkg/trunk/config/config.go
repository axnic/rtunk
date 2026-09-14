package config

// Config is a fully resolved trunk.yaml: the repo's own enabled lists, plus every definition
// merged in from its local plugin sources, keyed by each resource's own id/name so existence and
// duplication can be checked in O(1) rather than by scanning a list.
type Config struct {
	Version string
	CLI     struct {
		Version string
	}
	Plugins struct {
		Sources map[string]PluginSource
	}

	// Environments are global env var groups contributed by a plugin repo's own root
	// plugin.yaml `environments:` — not trunk.yaml-enableable, always in effect, and never
	// trimmed by filterEnabled (unlike Runtimes/Lint/Actions.Definitions).
	Environments []NamedEnvironment

	Runtimes CategoryConfig[Runtime]
	Lint     LintConfig
	Actions  CategoryConfig[Action]

	// Tools and Downloads have no trunk.yaml `enabled:` list of their own; they are referenced
	// by name from lint/runtime definitions instead (ARCHITECTURE.md `tools:`/`downloads:`).
	Tools     map[string]Tool
	Downloads map[string]Download
}

// PluginSource is one entry of plugins.sources: either a git source ({id, uri, ref}) or a local
// filesystem source ({id, local}).
type PluginSource struct {
	ID    string `yaml:"id"`
	URI   string `yaml:"uri,omitempty"`
	Ref   string `yaml:"ref,omitempty"`
	Local string `yaml:"local,omitempty"`
}

// CategoryConfig is the {enabled: [...]} shape shared by runtimes/lint/actions in trunk.yaml,
// plus the merged Definitions resolved from plugin sources (never present in trunk.yaml itself),
// keyed by each definition's own id/name.
type CategoryConfig[T any] struct {
	Enabled     []string
	Disabled    []string // only trunkFile.Actions parses this today (see resolve.go) -- advisory only, never read by filterEnabled
	Definitions map[string]T
}

// LintConfig is CategoryConfig[Linter] plus comment_formats: (global, never trimmed, as above)
// and files:, the file-type registry a plugin repo's own linters/plugin.yaml contributes
// (ARCHITECTURE.md "Built-in / global config") — keyed by name like Tools/Downloads, since
// `lint.definitions[].files: [...]` values reference it by id, and trimmed by filterEnabled down
// to what a kept linter's files: (plus any FileType.Inherit chain) actually uses.
// `yaml:",inline"` is required on the embedded field: yaml.v3 does not auto-promote anonymous
// struct fields the way Go itself (or encoding/json) does, so without it Enabled/Definitions
// would nest under a spurious "categoryconfig:" key instead of sitting directly under "lint:".
type LintConfig struct {
	CategoryConfig[Linter] `yaml:",inline"`
	CommentFormats         []CommentFormat     `yaml:"comment_formats,omitempty"`
	Files                  map[string]FileType `yaml:"files,omitempty"`
}
