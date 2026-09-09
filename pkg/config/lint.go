package config

import (
	"fmt"
	"path/filepath"

	"golang.org/x/sync/errgroup"
)

// Lint is where linters/formatters are defined, enabled, disabled, ignored,
// and triggered. https://docs.trunk.io/code-quality/overview/getting-started/configuration/lint
type Lint struct {
	// Definitions are custom linters or overrides of existing ones, merged
	// by name over the bundled/plugin definitions.
	// https://docs.trunk.io/code-quality/linters/custom-linters
	Definitions []LinterDefinition `yaml:"definitions"`
	// Enabled is a list of "name@version" entries. Empty means "no restriction"
	// (bundled defaults apply) rather than "nothing enabled" — see pkg/plugin's Workspace.Resolve.
	Enabled []PackageVersion `yaml:"enabled"`
	// Disabled always wins over Enabled.
	Disabled []PackageVersion `yaml:"disabled"`
	// Ignore excludes paths from specific (or ALL) linters, in addition to .gitignore.
	// https://docs.trunk.io/code-quality/overview/linters/ignoring-issues-and-files
	Ignore []LintIgnore `yaml:"ignore"`
	// Triggers run specific linters on specific paths regardless of the git diff scope.
	Triggers []LintTrigger `yaml:"triggers"`
}

// Validate checks every format-constrained string this section carries:
// Enabled/Disabled package versions, Ignore/Trigger glob patterns, and each
// Definitions entry's own fields.
func (l Lint) Validate() error {
	var eg errgroup.Group
	eg.Go(func() error {
		for _, pv := range l.Enabled {
			if err := pv.Validate(); err != nil {
				return fmt.Errorf("lint.enabled: %w", err)
			}
		}
		return nil
	})
	eg.Go(func() error {
		for _, pv := range l.Disabled {
			if err := pv.Validate(); err != nil {
				return fmt.Errorf("lint.disabled: %w", err)
			}
		}
		return nil
	})
	eg.Go(func() error {
		for _, ig := range l.Ignore {
			for _, p := range ig.Paths {
				if err := p.Validate(); err != nil {
					return fmt.Errorf("lint.ignore: %w", err)
				}
			}
		}
		return nil
	})
	eg.Go(func() error {
		for _, tr := range l.Triggers {
			for _, p := range tr.Paths {
				if err := p.Validate(); err != nil {
					return fmt.Errorf("lint.triggers: %w", err)
				}
			}
			for _, p := range tr.Targets {
				if err := p.Validate(); err != nil {
					return fmt.Errorf("lint.triggers: %w", err)
				}
			}
		}
		return nil
	})
	eg.Go(func() error {
		for _, def := range l.Definitions {
			if err := def.Validate(); err != nil {
				return err
			}
		}
		return nil
	})
	return eg.Wait()
}

// LintIgnore excludes Paths (globs, "!" negates) from Linters ("ALL" or specific names).
type LintIgnore struct {
	Linters []string      `yaml:"linters"`
	Paths   []GlobPattern `yaml:"paths"`
}

// LintTrigger forces Linters to run on Targets whenever Paths change, regardless of git diff scope.
type LintTrigger struct {
	Linters []string      `yaml:"linters"`
	Paths   []GlobPattern `yaml:"paths"`
	Targets []GlobPattern `yaml:"targets"`
}

// LinterDefinition is one entry of lint.definitions, or a bundled/plugin
// definition.yaml.
// https://docs.trunk.io/code-quality/linters/custom-linters
type LinterDefinition struct {
	// Name identifies this linter, matched against Lint.Enabled/Disabled
	// ("@version" suffixes there are ignored).
	Name string `yaml:"name"`
	// Files are glob patterns (against the basename) this linter applies to,
	// or "ALL". https://docs.trunk.io/code-quality/overview/getting-started/configuration/lint/auto-enable
	Files []GlobPattern `yaml:"files"`
	// Runtime names a Runtimes.Enabled entry this linter's own binary needs
	// to run (e.g. "python" for a pip-installed tool) — distinct from a
	// command's own Parser.Runtime. Empty means a standalone binary.
	Runtime string `yaml:"runtime"`
	// Download describes how to fetch this linter's binary from a GitHub
	// release when it isn't a managed-runtime package (rtunk extension —
	// real trunk.yaml never declares this per-repo).
	Download *Download `yaml:"download"`
	// PackageInstall describes how to fetch this linter's binary via a
	// language package manager (npm/pip) instead of a GitHub release —
	// mutually exclusive with Download (rtunk extension).
	PackageInstall *PackageInstall `yaml:"package_install"`
	// DirectConfigs are filenames that, if present at the repo root, are the
	// linter's own project-level config file (e.g. ".yamllint.yaml").
	DirectConfigs []string `yaml:"direct_configs"`
	// ConfigFlag is the CLI flag this linter's binary uses to point at a
	// DirectConfigs match (e.g. "-c" for yamllint) — rtunk extension: trunk's
	// own plugin.yaml never declares this (verified empirically: trunk's own
	// yamllint plugin.yaml has no such field, yet real trunk invokes yamllint
	// with `-c <path>` — that flag is hardcoded per-linter in trunk's closed
	// binary, not derivable from the declarative schema). Empty means no
	// known flag, so DirectConfigs is captured but not acted on.
	ConfigFlag string `yaml:"config_flag"`
	// PluginDir is the resolved local clone root of the plugins.source this
	// definition came from (rtunk extension, never read from YAML — set by
	// pkg/plugin after Resolve): resolves a command's or its Parser's
	// ${plugin} template variable (SPECS.md §7.2). Empty for bundled/local
	// definitions, which have no such concept.
	PluginDir string    `yaml:"-"`
	Commands  []Command `yaml:"commands"`
}

// Matches reports whether path is in scope for this definition's Files globs.
func (d LinterDefinition) Matches(path string) bool {
	base := filepath.Base(path)
	for _, pattern := range d.Files {
		if pattern == "ALL" {
			return true
		}
		if ok, _ := filepath.Match(string(pattern), base); ok {
			return true
		}
	}
	return false
}

// Validate checks this definition's own format-constrained fields (Files
// globs and each command's ParseRegex).
func (d LinterDefinition) Validate() error {
	for _, f := range d.Files {
		if err := f.Validate(); err != nil {
			return fmt.Errorf("%s: files: %w", d.Name, err)
		}
	}
	for _, c := range d.Commands {
		if c.ParseRegex == "" {
			continue
		}
		if err := c.ParseRegex.Validate(); err != nil {
			return fmt.Errorf("%s/%s: %w", d.Name, c.Name, err)
		}
	}
	return nil
}

// Download resolves a GitHub release asset to install this linter's binary
// hermetically: HTTPS-only, checksum-verified before use.
type Download struct {
	// GitHub is "owner/repo". Ignored when URL is set.
	GitHub string `yaml:"github"`
	// Version is the pinned release version, without a leading "v".
	Version string `yaml:"version"`
	// Asset is the release asset filename template, appended to
	// github.com/<GitHub>/releases/download/v<Version>/. Supports ${version},
	// ${os} (runtime.GOOS), ${arch}/${cpu} (runtime.GOARCH, remapped via
	// ArchMap), ${ext} ("zip" on windows, "tar.gz" otherwise). Ignored when
	// URL is set.
	Asset string `yaml:"asset"`
	// URL is a full download URL template (same placeholders as Asset),
	// used instead of the GitHub+Asset convention when a release doesn't
	// follow it — e.g. trunk's real trufflehog definition gives a complete
	// per-OS/CPU URL rather than owner/repo + filename.
	URL string `yaml:"url"`
	// Checksums is the release asset filename template for the "<sha256>  <filename>"
	// checksums file published alongside the binaries. Empty means no
	// verification is performed (only when the source itself publishes none).
	Checksums string `yaml:"checksums"`
	// ArchMap renames Go's GOARCH to this project's release-asset arch name
	// (e.g. {"amd64": "x64"}). Unmapped values pass through unchanged.
	ArchMap map[string]string `yaml:"arch_map"`
	// OSMap renames Go's GOOS to this project's release-asset OS name
	// (e.g. {"darwin": "macos"}). Unmapped values pass through unchanged.
	OSMap map[string]string `yaml:"os_map"`
	// Bin is the executable's name inside the downloaded archive.
	Bin string `yaml:"bin"`
	// StripComponents strips this many leading path segments from every
	// archive entry before extracting (tar's own --strip-components) —
	// needed when the archive wraps everything in one top-level directory
	// (e.g. a real node distribution's node-v22.16.0-darwin-arm64/bin/...)
	// rather than putting the binary at the archive root. 0 (the default)
	// is right for a flat single-binary archive (most linter tools).
	StripComponents int `yaml:"strip_components"`
	// ExtraArgs are extra named values substituteDownload folds into
	// ${name} placeholders in URL/Asset/Checksums, alongside the built-in
	// ${version}/${os}/${cpu}/${ext} — e.g. taplo's ${semver}, a regex-derived
	// version string (see pkg/plugin's computeArgs).
	ExtraArgs map[string]string `yaml:"extra_args"`
	// RenameSingleFile means the download is the tool's single binary
	// itself, compressed (gzip in every real example), rather than an
	// archive with directory structure — it's decompressed straight to
	// destDir/Bin instead of going through tar/zip extraction.
	RenameSingleFile bool `yaml:"rename_single_file"`
}

// PackageInstall resolves a linter's binary via a managed runtime's own
// package manager (rtunk extension) — the install mechanism most of trunk's
// real plugin.yaml linters actually use (npm/pip), rather than a GitHub
// release binary. pkg/runtime installs a fully hermetic runtime first
// (never the ambient machine's own node/python/...); pkg/tool+pkg/shim run
// this package manager on top of it.
type PackageInstall struct {
	// Runtime is "node", "python", "go", "ruby", or "php" (pkg/shim's own
	// installers — see its registry).
	Runtime string `yaml:"runtime"`
	// Package is the package manager's package name, e.g. "markdownlint-cli".
	Package string `yaml:"package"`
	// Version is the pinned package version.
	Version string `yaml:"version"`
	// Shims are the executable name(s) exposed after install (the first is
	// used to resolve the linter's binary).
	Shims []string `yaml:"shims"`
}

// Command describes exactly what binary to run, with what arguments, in
// what context, and how to interpret its result.
// https://docs.trunk.io/cli/configuration/lint/commands
type Command struct {
	// Name of this command (lint, format, analyze...).
	Name string `yaml:"name"`
	// Run is the command line template, e.g. "ruff check ${target}".
	Run string `yaml:"run"`
	// Target selects what ${target} expands to: "${file}", "${parent}", etc.
	Target string `yaml:"target"`
	// RunFrom is the working directory this command executes in.
	RunFrom string `yaml:"run_from"`
	Stdin   bool   `yaml:"stdin"`
	// Output is the raw-output format: sarif | regex | pass_fail | rewrite |
	// lsp_json | arcanist | a linter-specific hardcoded type.
	// https://docs.trunk.io/references/cli/configuration/lint/output
	Output string `yaml:"output"`
	// Parser optionally transforms the raw output before Output parsing.
	// https://docs.trunk.io/code-quality/overview/getting-started/configuration/lint/output-parsing
	Parser *Parser `yaml:"parser"`
	// ParseRegex is used when Output is "regex": named groups path
	// (required), line, col, severity, code, message.
	ParseRegex RegexPattern `yaml:"parse_regex"`
	// ReadOutputFrom is stdout | stderr | tmp_file.
	ReadOutputFrom string `yaml:"read_output_from"`
	// SuccessCodes lists exit codes considered "ran successfully"
	// (regardless of issues found). Exactly one of SuccessCodes/ErrorCodes
	// should be set.
	SuccessCodes []int `yaml:"success_codes"`
	// ErrorCodes lists exit codes considered "execution failed"; everything
	// else is success.
	ErrorCodes []int `yaml:"error_codes"`
	// NoIssuesCodes lets rtunk skip parsing entirely when the tool signals
	// "no issues" via a specific exit code.
	NoIssuesCodes []int `yaml:"no_issues_codes"`
	// Batch passes multiple files in a single invocation when true.
	Batch bool `yaml:"batch"`
	// Formatter marks this command as run by `rtunk fmt`, not `rtunk check`.
	Formatter bool `yaml:"formatter"`
	// InPlace means the tool rewrites the target file itself (e.g. `gofmt -w`);
	// rtunk detects the change by hashing before/after instead of parsing output.
	InPlace bool `yaml:"in_place"`
	// CacheResults caches this command's output keyed on file+linter version+config.
	CacheResults bool `yaml:"cache_results"`
	// Idempotent false means the cached result is re-verified after the
	// cache's result_ttl even if the cache key still matches.
	Idempotent bool `yaml:"idempotent"`
	// MaxConcurrency caps parallel invocations of this command (0 = no limit).
	MaxConcurrency int `yaml:"max_concurrency"`
	// Platforms restricts this command to specific GOOS values (empty = all).
	Platforms []string `yaml:"platforms"`
	// Version constrains which tool version this command definition matches
	// (a linter can have multiple commands for different tool version ranges).
	Version string `yaml:"version"`
	// Severity is the default Diagnostic severity for issues from this
	// command: note | warning | error (rtunk extension; default "warning"
	// when empty — real trunk.yaml infers this from the linter's own rule
	// metadata instead).
	Severity string `yaml:"severity"`
	// Environment lists extra environment variables/PATH entries for this invocation.
	Environment []EnvVar `yaml:"environment"`
}

// Parser transforms a command's raw output before Output parsing.
// https://docs.trunk.io/code-quality/overview/getting-started/configuration/lint/output-parsing
type Parser struct {
	// Runtime this parser script needs (distinct from LinterDefinition.Runtime).
	Runtime string `yaml:"runtime"`
	Run     string `yaml:"run"`
}

// EnvVar sets or extends (List) an environment variable for a command invocation.
type EnvVar struct {
	Name string   `yaml:"name"`
	List []string `yaml:"list"`
}
