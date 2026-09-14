package config

import "gopkg.in/yaml.v3"

// Download is a reusable, OS/CPU-templated download recipe (ARCHITECTURE.md `downloads:`).
type Download struct {
	Name    string `yaml:"name"`
	Version string `yaml:"version"`
	// Args declares extra template variables derived from ${version} (or ${os}/${cpu}) via a
	// regex, for a URL to reference alongside the built-in vars -- real catalog example: taplo's
	// own recipe strips a release-tag prefix trunk's real GitHub tags carry (e.g.
	// "release-cli-0.10.0") down to the bare semver GitHub actually names its release assets with,
	// via `args: {semver: "${version}=>(?:release-cli-|release-taplo-cli-)?(?P<semver>.*)"}`. Each
	// value is "<template>=><regex>"; see download.ResolveArgs for the actual resolution logic
	// (this package only carries the raw recipe, unparsed).
	Args      map[string]string `yaml:"args,omitempty"`
	Downloads []DownloadEntry   `yaml:"downloads"`
}

// DownloadEntry is one os/cpu-specific variant of a Download recipe.
type DownloadEntry struct {
	OS              OSSpec `yaml:"os"`
	CPU             OSSpec `yaml:"cpu"`
	URL             string `yaml:"url"`
	StripComponents int    `yaml:"strip_components,omitempty"`
	Executable      bool   `yaml:"executable,omitempty"`
	Version         string `yaml:"version,omitempty"`
}

// OSSpec is an os/cpu selector: either a bare name ("macos"), or a map from trunk's own
// vocabulary (linux, macos, windows, x86_64, arm_64) to upstream's naming. A bare name decodes
// to a single key mapping to itself.
type OSSpec map[string]string

func (s *OSSpec) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		*s = OSSpec{node.Value: node.Value}
		return nil
	}
	var m map[string]string
	if err := node.Decode(&m); err != nil {
		return err
	}
	*s = m
	return nil
}

// Tool is a downloadable/runnable tool (ARCHITECTURE.md `tools:`), fetched either via a runtime's
// package manager (Runtime+Package) or a Download recipe. The two are mutually exclusive.
type Tool struct {
	Name             string   `yaml:"name"`
	Runtime          string   `yaml:"runtime,omitempty"`
	Package          string   `yaml:"package,omitempty"`
	Download         string   `yaml:"download,omitempty"`
	Shims            ShimList `yaml:"shims,omitempty"`
	KnownGoodVersion string   `yaml:"known_good_version,omitempty"`
}

// ShimList is the shims: field's value. Most entries are a bare executable name, but some
// (e.g. github.com/trunk-io/plugins tools/bazel-differ/plugin.yaml) are {name, target} objects
// aliasing the exposed shim name to a different underlying binary; only the exposed name is kept.
type ShimList []string

func (s *ShimList) UnmarshalYAML(node *yaml.Node) error {
	var raw []yaml.Node
	if err := node.Decode(&raw); err != nil {
		return err
	}
	out := make([]string, 0, len(raw))
	for _, n := range raw {
		if n.Kind == yaml.ScalarNode {
			out = append(out, n.Value)
			continue
		}
		var obj struct {
			Name string `yaml:"name"`
		}
		if err := n.Decode(&obj); err != nil {
			return err
		}
		out = append(out, obj.Name)
	}
	*s = out
	return nil
}

// Linter is a linter definition (ARCHITECTURE.md `lint:`), tying files, tools, and commands
// together.
type Linter struct {
	Name               string          `yaml:"name"`
	Files              []string        `yaml:"files,omitempty"`
	Tools              []string        `yaml:"tools,omitempty"`
	MainTool           string          `yaml:"main_tool,omitempty"`
	Description        string          `yaml:"description,omitempty"`
	Commands           []Command       `yaml:"commands,omitempty"`
	DirectConfigs      []string        `yaml:"direct_configs,omitempty"`
	AffectsCache       []string        `yaml:"affects_cache,omitempty"`
	IssueURLFormat     string          `yaml:"issue_url_format,omitempty"`
	SuggestIf          string          `yaml:"suggest_if,omitempty"`
	KnownGoodVersion   string          `yaml:"known_good_version,omitempty"`
	KnownBadVersions   []string        `yaml:"known_bad_versions,omitempty"`
	SupportedPlatforms []string        `yaml:"supported_platforms,omitempty"`
	RunTimeout         string          `yaml:"run_timeout,omitempty"`
	CacheResults       *bool           `yaml:"cache_results,omitempty"`
	VersionCommand     *VersionCommand `yaml:"version_command,omitempty"`

	// SourceDir is this linter's own directory, relative to its plugin source's root (e.g.
	// "linters/trufflehog") -- how ${cwd} resolves relative to ${plugin} in a Command.Run or
	// Command.Parser.Run. Stable across machines/cacheDir, so safe to cache; set by
	// parseSourceDir. yaml:"-" blocks it from ever being read out of an actual plugin.yaml file --
	// it is derived from the file's own path, never authored.
	SourceDir string `yaml:"-"`
	// SourceRoot is the absolute local directory ${plugin} resolves to for this linter's
	// Run/Parser.Run strings: a local plugin source's own directory, or a git source's persisted
	// checkout (see fetchGitSource). Recomputed fresh from the *current* cacheDir on every Resolve
	// call -- json:"-" keeps it out of the on-disk cache, so a cacheDir override (or a checkout
	// later rebuilt at a new path) never leaves a stale absolute path baked into cached JSON.
	SourceRoot string `yaml:"-" json:"-"`
}

// Command is one invocation of a Linter (ARCHITECTURE.md `commands[]`): a checker command by
// default, or a formatter when InPlace+Formatter are set.
type Command struct {
	Name           string `yaml:"name"`
	Run            string `yaml:"run"`
	Output         string `yaml:"output,omitempty"`
	SuccessCodes   []int  `yaml:"success_codes,omitempty"`
	ErrorCodes     []int  `yaml:"error_codes,omitempty"`
	Batch          bool   `yaml:"batch,omitempty"`
	ReadOutputFrom string `yaml:"read_output_from,omitempty"`
	SandboxType    string `yaml:"sandbox_type,omitempty"`
	RunFrom        string `yaml:"run_from,omitempty"`
	ParseRegex     string `yaml:"parse_regex,omitempty"`
	Version        string `yaml:"version,omitempty"`
	InPlace        bool   `yaml:"in_place,omitempty"`
	Formatter      bool   `yaml:"formatter,omitempty"`
	// Enabled defaults a command on (nil) or explicitly off (real catalog example: ruff's own
	// "format" command sets false, since ruff-format competes with black) -- distinct from
	// Linter-level enable/disable (trunk.yaml's lint.enabled: list), which this field does not
	// touch. rtunk has no trunk.yaml-level override for a single command's own Enabled today
	// (a real gap, deliberately out of scope); this field only ever reflects what the plugin
	// source's own catalog data says.
	Enabled *bool   `yaml:"enabled,omitempty"`
	Parser  *Parser `yaml:"parser,omitempty"`
}

// Parser converts a tool's native output into trunk's normalized shape, for tools with no native
// structured output mode.
type Parser struct {
	Runtime string `yaml:"runtime"`
	Run     string `yaml:"run"`
}

// VersionCommand detects an installed tool/runtime's version.
type VersionCommand struct {
	Run        string `yaml:"run"`
	ParseRegex string `yaml:"parse_regex"`
}

// Action is a git-hook or file-change-triggered automation (ARCHITECTURE.md `actions:`).
type Action struct {
	ID           string        `yaml:"id"`
	DisplayName  string        `yaml:"display_name,omitempty"`
	Description  string        `yaml:"description,omitempty"`
	Runtime      string        `yaml:"runtime,omitempty"`
	PackagesFile string        `yaml:"packages_file,omitempty"`
	Run          string        `yaml:"run,omitempty"`
	Triggers     []Trigger     `yaml:"triggers,omitempty"`
	Interactive  Interactivity `yaml:"interactive,omitempty"`
	// NotifyOnError is nil when the plugin.yaml omits it -- every real trunk-io/plugins action
	// that sets it explicitly sets it to false (to suppress the implied default), so nil is
	// treated as "true" by pkg/trunk/actions.Run, mirroring Command.Enabled's own *bool
	// "unset vs. explicit false" convention.
	NotifyOnError *bool `yaml:"notify_on_error,omitempty"`
	// Environment contributes extra process env vars beyond runtime/PATH (real catalog example:
	// actions/git/plugin.yaml's git-lfs action passes SSH_AUTH_SOCK/SSH_AGENT_PID through this
	// way). Resolved via download.BuildEnv, same as Runtime.RuntimeEnvironment/LinterEnvironment.
	Environment []EnvironmentEntry `yaml:"environment,omitempty"`

	// SourceDir/SourceRoot mirror Linter's own fields exactly (see Linter's doc comments) -- an
	// action's Run/Environment can reference ${cwd}/${plugin} the same way a linter's Command can.
	SourceDir  string `yaml:"-"`
	SourceRoot string `yaml:"-" json:"-"`
}

// Interactivity is Action.Interactive: bare `true`, or the literal string "optional".
type Interactivity string

func (i *Interactivity) UnmarshalYAML(node *yaml.Node) error {
	*i = Interactivity(node.Value)
	return nil
}

// Trigger is one alternative way an Action can fire.
type Trigger struct {
	GitHooks []string  `yaml:"git_hooks,omitempty"`
	Files    []string  `yaml:"files,omitempty"`
	Schedule *Schedule `yaml:"schedule,omitempty"`
}

// Schedule is a periodic background trigger: either the {interval, delay} object form, or a bare
// duration string as shorthand for {interval: <value>} (e.g. github.com/trunk-io/plugins
// actions/git-blame-ignore-revs/plugin.yaml's `schedule: 24h`).
type Schedule struct {
	Interval string `yaml:"interval"`
	Delay    string `yaml:"delay,omitempty"`
}

func (s *Schedule) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		s.Interval = node.Value
		return nil
	}
	type rawSchedule Schedule // avoid infinite recursion into this UnmarshalYAML
	return node.Decode((*rawSchedule)(s))
}

// Runtime is a language runtime definition (ARCHITECTURE.md `runtimes:`).
type Runtime struct {
	Type               string             `yaml:"type"`
	Download           string             `yaml:"download,omitempty"`
	SystemVersion      string             `yaml:"system_version,omitempty"`
	KnownGoodVersion   string             `yaml:"known_good_version,omitempty"`
	Shims              ShimList           `yaml:"shims,omitempty"`
	VersionCommands    []VersionCommand   `yaml:"version_commands,omitempty"`
	RuntimeEnvironment []EnvironmentEntry `yaml:"runtime_environment,omitempty"`
	LinterEnvironment  []EnvironmentEntry `yaml:"linter_environment,omitempty"`
}

// EnvironmentEntry is one environment variable/PATH entry contributed by a Runtime.
type EnvironmentEntry struct {
	Name     string   `yaml:"name"`
	List     []string `yaml:"list,omitempty"`
	Value    string   `yaml:"value,omitempty"`
	Optional bool     `yaml:"optional,omitempty"`
}

// NamedEnvironment is one named group of EnvironmentEntry, contributed by a plugin repo's own
// root plugin.yaml `environments:` (ARCHITECTURE.md "Plugin repository layout") — global,
// non-trunk.yaml-enableable config always in effect, unlike Runtime/Linter definitions.
type NamedEnvironment struct {
	Name        string             `yaml:"name"`
	Environment []EnvironmentEntry `yaml:"environment"`
}

// FileType is one named entry of the file-type registry (ARCHITECTURE.md `lint.files:`) — what
// `lint.definitions[].files: [...]` values reference. Matched against a real file by any of
// Extensions/Filenames/Regexes/Shebangs (the last against a `#!/usr/bin/env <shebang>` line, for
// extensionless scripts); RequiredYAMLKeys further narrows a YAML match (e.g. distinguishing
// `cloudformation` from plain `yaml`). Comments names CommentFormat entries this file type uses
// for trunk-ignore detection. Inherit composes other named FileTypes in (e.g. `bazel` inheriting
// `bazel-build`/`bazel-workspace`/`bazel-module`) instead of repeating their fields.
type FileType struct {
	Name             string   `yaml:"name"`
	Extensions       []string `yaml:"extensions,omitempty"`
	Filenames        []string `yaml:"filenames,omitempty"`
	Regexes          []string `yaml:"regexes,omitempty"`
	Shebangs         []string `yaml:"shebangs,omitempty"`
	RequiredYAMLKeys []string `yaml:"required_yaml_keys,omitempty"`
	Comments         []string `yaml:"comments,omitempty"`
	Inherit          []string `yaml:"inherit,omitempty"`
}

// CommentFormat is one named comment-delimiter style (`hash`, `slashes-block`, ...), contributed
// by a plugin repo's own category-root plugin.yaml (e.g. linters/plugin.yaml's `lint.
// comment_formats:`), used to detect trunk-ignore-style comments (ARCHITECTURE.md "Built-in /
// global config") — global, non-trunk.yaml-enableable config always in effect.
type CommentFormat struct {
	Name              string `yaml:"name"`
	LeadingDelimiter  string `yaml:"leading_delimiter"`
	TrailingDelimiter string `yaml:"trailing_delimiter,omitempty"`
}
