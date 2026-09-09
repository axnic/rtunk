package config

import (
	"golang.org/x/sync/errgroup"

	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/cache"
)

// Config is the root of a .trunk/trunk.yaml or .rtunk/rtunk.yaml file.
// https://docs.trunk.io/references/cli/configuration
type Config struct {
	// Version of this config file's schema.
	Version float64 `yaml:"version"`

	// CLI pins the rtunk/trunk binary version this repo expects, and default
	// per-command flags. https://docs.trunk.io/cli/configuration/cli
	CLI CLI `yaml:"cli"`

	// Cache is an rtunk-only extension (absent from a real trunk.yaml):
	// overrides the cache directory. Defined in pkg/cache, not here, so
	// pkg/cache never needs to depend on this package.
	Cache cache.Config `yaml:"cache"`

	// Repo controls git-aware target resolution: which branch is "trunk"
	// and how the upstream is determined.
	// https://docs.trunk.io/code-quality/overview/getting-started/configuration/repo
	Repo Repo `yaml:"repo"`

	// Runtimes are the managed language runtimes (node, python, go...)
	// available to linters that declare a matching LinterDefinition.Runtime.
	// https://docs.trunk.io/code-quality/overview/getting-started/runtimes
	Runtimes Runtimes `yaml:"runtimes"`

	// Plugins declares remote definition sources, resolved in order and
	// merged under the repo's own lint/tools/actions sections.
	// https://docs.trunk.io/code-quality/overview/getting-started/configuration/plugins
	Plugins Plugins `yaml:"plugins"`

	// Lint is where linters/formatters are defined, enabled, disabled,
	// ignored, and triggered.
	// https://docs.trunk.io/code-quality/overview/getting-started/configuration/lint
	Lint Lint `yaml:"lint"`

	// Tools are additional managed CLI tools, independent of linting.
	// https://docs.trunk.io/code-quality/overview/getting-started/tools
	Tools Tools `yaml:"tools"`

	// Actions are automations (git hooks, triggers).
	// https://docs.trunk.io/code-quality/overview/getting-started/actions
	Actions Actions `yaml:"actions"`

	// Telemetry is always false for rtunk — present only for readability/
	// documentation, never actually read or honored if true.
	Telemetry bool `yaml:"telemetry"`
}

// Validate checks every format-constrained string field (package/version
// references, glob patterns, regexes) this Config carries.
func (c Config) Validate() error {
	var eg errgroup.Group
	eg.Go(c.Runtimes.Validate)
	eg.Go(c.Tools.Validate)
	eg.Go(c.Lint.Validate)
	return eg.Wait()
}

// CLI pins the expected binary version and injects default per-command args.
// https://docs.trunk.io/cli/configuration/cli
type CLI struct {
	// Version of the rtunk/trunk binary this repo expects.
	Version string `yaml:"version"`
	// Options are default extra args injected per command.
	Options []CLIOption `yaml:"options"`
}

// CLIOption injects Args into every invocation of the listed Commands.
type CLIOption struct {
	// Commands this option applies to ("ALL" or a list of command names).
	Commands []string `yaml:"commands"`
	// Args appended to the resolved command line.
	Args []string `yaml:"args"`
}

// Repo controls git-aware target resolution: by default, only files changed
// vs TrunkBranch are linted/formatted.
// https://docs.trunk.io/code-quality/overview/getting-started/configuration/repo
type Repo struct {
	// TrunkBranch is the reference branch changed-file diffs are computed against.
	TrunkBranch string `yaml:"trunk_branch"`
	// RemoteHint disambiguates the canonical remote (e.g. "github.com/org/repo").
	RemoteHint string `yaml:"remote_hint"`
	// UseBranchUpstream compares against the current branch's git upstream
	// instead of TrunkBranch.
	UseBranchUpstream bool `yaml:"use_branch_upstream"`
}
