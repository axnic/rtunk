package plugin

import (
	"fmt"
	"runtime"

	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/config"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/download"
)

// trunkPlugin models trunk's real plugin.yaml dialect
// (github.com/trunk-io/plugins) — meaningfully different from rtunk's own
// schema: tool install recipes live in a separate `tools.definitions` list,
// referenced by name from `lint.definitions[].tools`, rather than embedded
// directly per-linter like config.LinterDefinition.Download.
type trunkPlugin struct {
	// Downloads are named GitHub-release recipes, referenced by name from
	// tools.definitions[].download (distinct from tools.definitions[].package,
	// which is a language package-manager install instead) or from
	// runtimes.definitions[].download.
	Downloads []trunkDownloadGroup `yaml:"downloads"`
	Tools     struct {
		Definitions []trunkTool `yaml:"definitions"`
	} `yaml:"tools"`
	Lint struct {
		Definitions []trunkLinter `yaml:"definitions"`
	} `yaml:"lint"`
	// Runtimes is only ever populated by files under a repo's runtimes/
	// directory (real example: runtimes/node/plugin.yaml) — reusing this
	// struct for both linters/ and runtimes/ files rather than having two
	// nearly-identical parse types, since a file only ever populates one
	// side or the other in practice.
	Runtimes struct {
		Definitions []trunkRuntime `yaml:"definitions"`
	} `yaml:"runtimes"`
}

// trunkRuntime models one runtimes.definitions[] entry (real example:
// runtimes/node/plugin.yaml) — a managed language runtime (node/python/go)
// tools can be installed via, distinct from trunkTool (which installs a
// single CLI binary, not a whole runtime).
type trunkRuntime struct {
	// Type identifies this runtime ("node", "python", "go"...), matched
	// against Runtimes.Enabled/a tool's own Runtime field.
	Type string `yaml:"type"`
	// Download references a trunkPlugin.Downloads[].Name, like
	// trunkTool.Download.
	Download string `yaml:"download"`
	// RuntimeEnvironment is the environment to run the runtime's own
	// binaries (node/npm/...) standalone: ${runtime} resolves to this
	// runtime's own cached install directory.
	RuntimeEnvironment []envVarDef `yaml:"runtime_environment"`
	// LinterEnvironment is the (different) environment a linter that uses
	// this runtime needs at run time: ${linter} resolves to that linter's
	// own installed directory (e.g. node's linter_environment points
	// NODE_PATH/PATH at the linter's own node_modules, not the runtime's).
	LinterEnvironment []envVarDef `yaml:"linter_environment"`
	KnownGoodVersion  string      `yaml:"known_good_version"`
	Shims             []trunkShim `yaml:"shims"`
}

type trunkTool struct {
	Name             string      `yaml:"name"`
	Runtime          string      `yaml:"runtime"`
	Package          string      `yaml:"package"`
	Download         string      `yaml:"download"` // references a trunkPlugin.Downloads[].Name
	Shims            []trunkShim `yaml:"shims"`
	KnownGoodVersion string      `yaml:"known_good_version"`
}

// trunkShim decodes one shims: list entry — either a plain string ("name")
// or, in some real plugin.yaml files (graphql-schema-linter, terragrunt),
// an object ({name, target}) when the exposed shim name and the installed
// binary's own name need distinguishing. Only Name is read here: every
// real example seen so far has Target == Name, and nothing in this
// package's install/lookup path needs the distinction yet.
type trunkShim struct {
	Name string
}

func (s *trunkShim) UnmarshalYAML(unmarshal func(any) error) error {
	var name string
	if err := unmarshal(&name); err == nil {
		s.Name = name
		return nil
	}
	var obj struct {
		Name string `yaml:"name"`
	}
	if err := unmarshal(&obj); err != nil {
		return err
	}
	s.Name = obj.Name
	return nil
}

// trunkDownloadGroup is one named entry of the top-level `downloads:` list.
// Real trunk plugin.yaml files nest several version-ranged Variants per
// group (e.g. taplo has one per historical release-URL convention,
// resolveDownloadGroup picks the one matching both the current platform
// and a target version). Only used transiently during YAML decode:
// buildPlugin normalizes it into a gob-safe DownloadGroup before it reaches
// the cached Plugin.
type trunkDownloadGroup struct {
	Name string `yaml:"name"`
	// Args derives extra named template variables from a regex over an
	// already-known one — real shape (taplo): {"semver": "${version}=>(?:release-cli-|release-taplo-cli-)?(?P<semver>.*)"},
	// stripping a historical release-tag prefix off a pinned version to get
	// the bare semver a newer release URL convention needs. See computeArgs.
	Args map[string]string `yaml:"args"`
	// RenameSingleFile means each variant's download is the tool's single
	// binary itself, compressed (real examples are all gzip, named ${target}.gz)
	// rather than an archive with a directory structure — extractSingleFile
	// decompresses it directly to destDir/<bin>, skipping tar/zip handling.
	RenameSingleFile bool                   `yaml:"rename_single_file"`
	Downloads        []trunkDownloadVariant `yaml:"downloads"`
}

// trunkDownloadVariant's OS/CPU are inconsistently shaped across real
// plugin.yaml files: trufflehog's is a remapping table (map[string]string,
// one URL template covers every OS/CPU), taplo's is a single fixed string
// per variant (one variant per OS/CPU, no remapping) — interface{} defers
// the decision to normalizeVariant/resolveDownloadGroup, which only
// resolves the map shape.
type trunkDownloadVariant struct {
	OS              any
	CPU             any
	Version         string `yaml:"version"`
	URL             string `yaml:"url"`
	StripComponents int    `yaml:"strip_components"`
}

// UnmarshalYAML lets OS/CPU decode as either shape (goccy/go-yaml applies
// the "os"/"cpu" tags via this method instead of struct tags, since the
// field type is `any` rather than a concrete type with normal tag matching).
func (v *trunkDownloadVariant) UnmarshalYAML(unmarshal func(any) error) error {
	var raw struct {
		OS              any    `yaml:"os"`
		CPU             any    `yaml:"cpu"`
		Version         string `yaml:"version"`
		URL             string `yaml:"url"`
		StripComponents int    `yaml:"strip_components"`
	}
	if err := unmarshal(&raw); err != nil {
		return err
	}
	*v = trunkDownloadVariant(raw)
	return nil
}

type trunkLinter struct {
	Name          string         `yaml:"name"`
	Files         []string       `yaml:"files"`
	Tools         []string       `yaml:"tools"`
	Commands      []trunkCommand `yaml:"commands"`
	DirectConfigs []string       `yaml:"direct_configs"`
	// Dir is this linter's own plugin source clone root, set by
	// buildPlugin (never read from YAML) — kept per-linter rather than on
	// the containing Plugin so it survives Merge combining several
	// Plugins (each may have its own Dir) into one.
	Dir string `yaml:"-"`
}

type trunkCommand struct {
	Name           string         `yaml:"name"`
	Output         string         `yaml:"output"`
	Run            string         `yaml:"run"`
	ParseRegex     string         `yaml:"parse_regex"`
	SuccessCodes   []int          `yaml:"success_codes"`
	ReadOutputFrom string         `yaml:"read_output_from"`
	Formatter      bool           `yaml:"formatter"`
	InPlace        bool           `yaml:"in_place"`
	Platforms      []string       `yaml:"platforms"`
	Parser         *config.Parser `yaml:"parser"`
}

// fileCategoryDef mirrors one entry of the repo's root linters/plugin.yaml
// `lint.files` list — trunk's real, authoritative catalog of named file
// categories (github.com/trunk-io/plugins/blob/main/linters/plugin.yaml),
// used to resolve a linter's `files: [<category>, ...]` entries into globs.
type fileCategoryDef struct {
	Name       string   `yaml:"name"`
	Extensions []string `yaml:"extensions"`
	Filenames  []string `yaml:"filenames"`
	Inherit    []string `yaml:"inherit"`
	// Regexes-only categories (e.g. "docker", "github-workflow", "ALL")
	// can't be expressed as a config.GlobPattern — resolveCategory reports
	// them unresolved, and translateFiles falls back to a guessed glob.
	Regexes []string `yaml:"regexes"`
}

// platformGOOS maps trunk's platform names to Go's runtime.GOOS.
var platformGOOS = map[string]string{
	"windows": "windows",
	"linux":   "linux",
	"macos":   "darwin",
}

// platformGOARCH maps trunk's CPU family names to Go's runtime.GOARCH.
var platformGOARCH = map[string]string{
	"x86_64": "amd64",
	"arm_64": "arm64",
}

// platformMatches reports whether one of platforms (empty = all platforms)
// names the current OS.
func platformMatches(platforms []string) bool {
	if len(platforms) == 0 {
		return true
	}
	for _, p := range platforms {
		if platformGOOS[p] == runtime.GOOS {
			return true
		}
	}
	return false
}

// resolveCategory resolves name against categories (the root
// linters/plugin.yaml catalog) into globs: its own extensions/filenames
// plus whatever its inherited categories resolve to. ok is false when name
// isn't in categories, or resolves to zero globs (a regex-only category —
// translateFiles falls back to a guess in that case). seen guards against
// an inherit cycle.
func resolveCategory(categories map[string]fileCategoryDef, name string, seen map[string]bool) ([]config.GlobPattern, bool) {
	if seen[name] {
		return nil, false
	}
	seen[name] = true
	cat, ok := categories[name]
	if !ok {
		return nil, false
	}
	var out []config.GlobPattern
	for _, ext := range cat.Extensions {
		out = append(out, config.GlobPattern("*."+ext))
	}
	for _, fn := range cat.Filenames {
		out = append(out, config.GlobPattern(fn))
	}
	for _, parent := range cat.Inherit {
		if globs, ok := resolveCategory(categories, parent, seen); ok {
			out = append(out, globs...)
		}
	}
	return out, len(out) > 0
}

// translateFiles resolves files (trunk's `files:` entries — either "ALL" or
// named categories) against categories into globs, falling back to an
// unverified "*.<name>" guess (with a warning) for anything categories
// doesn't resolve — an unknown category, or a regex-only one no
// config.GlobPattern can express.
func translateFiles(files []string, categories map[string]fileCategoryDef) ([]config.GlobPattern, []string) {
	var out []config.GlobPattern
	var warnings []string
	for _, f := range files {
		if f == "ALL" {
			return []config.GlobPattern{"ALL"}, warnings
		}
		if globs, ok := resolveCategory(categories, f, map[string]bool{}); ok {
			out = append(out, globs...)
			continue
		}
		warnings = append(warnings, fmt.Sprintf("file category %q not resolved from linters/plugin.yaml, guessing \"*.%s\"", f, f))
		out = append(out, config.GlobPattern("*."+f))
	}
	return out, warnings
}

// targetFor picks the engine's target grouping for a translated command.
// trunk's real dialect always batches every matched file into one call; for
// most languages, invoking one file at a time instead (rtunk's "${file}") is
// merely less efficient. Go is different: a single file lacks its package's
// sibling files, so a per-package Go tool (golangci-lint2 confirmed by a
// real run: isolated files produced hundreds of bogus "undefined: X"
// errors) needs "${parent}" (per-directory) grouping to see a whole package
// — anything less is wrong, not just slow.
func targetFor(files []string) string {
	for _, f := range files {
		if f == "go" {
			return "${parent}"
		}
	}
	return "${file}"
}

// Load translates p's linters into config.LinterDefinition, but only for
// linters named in enabled (an empty enabled means "no restriction",
// matching Workspace.Resolve's convention) — a real plugins.sources repo's
// catalog is huge (150+ linters in trunk's own), and translating/warning
// about ones the repo never asked for would be pure noise. Commands this
// rtunk build can't run (a custom hardcoded output type, or an install
// mechanism other than a supported download/package manager) are skipped
// with a warning rather than silently dropped. The returned warnings
// include p.Warnings (collected while building the Plugin).
func (p *Plugin) Load(enabled []config.PackageVersion) ([]config.LinterDefinition, []string, error) {
	var defs []config.LinterDefinition
	warnings := append([]string{}, p.Warnings...)
	for _, ld := range p.Linters {
		if len(enabled) > 0 && !enabledContains(enabled, ld.Name) {
			continue
		}
		// The repo's own pinned "name@version" always wins over the
		// plugin's bundled known_good_version default — rtunk's whole
		// reproducibility model is "the repo's config decides the
		// version," and the two can legitimately diverge (this repo's
		// trunk.yaml pins golangci-lint2@2.12.2; the v1.10.2 plugin
		// snapshot's own default was an older 2.6.1).
		def, w := translateLinter(ld, p.Tools, p.Downloads, p.Categories, enabledVersion(enabled, ld.Name))
		def.PluginDir = ld.Dir // resolves ${plugin} in a command's own run or its Parser.Run
		warnings = append(warnings, w...)
		if len(def.Commands) > 0 {
			defs = append(defs, def)
		}
	}
	return defs, warnings, nil
}

// configFlag is a hardcoded, per-linter lookup of the CLI flag that points
// a tool at a custom config file — necessarily hardcoded (see
// config.LinterDefinition.ConfigFlag), and necessarily incomplete: only
// entries verified against the tool's own --help go here. Extend as more
// linters' direct_configs are wired up.
var configFlag = map[string]string{
	"yamllint":     "-c",
	"markdownlint": "-c", // verified via `markdownlint --help`
}

// supportedOutput lists the Output values pkg/plugin.parseOutput knows how
// to parse: the generic types plus the linter-specific hardcoded ones,
// verified against a real run of that exact tool.
var supportedOutput = map[string]bool{
	"regex":        true,
	"sarif":        true,
	"sarif_uri":    true, // checkov: same thing, verified empirically (see pkg/plugin.parseOutput)
	"markdownlint": true,
	"taplo":        true,
}

func translateLinter(ld trunkLinter, tools []trunkTool, downloads []download.Group, categories map[string]fileCategoryDef, pinnedVersion string) (config.LinterDefinition, []string) {
	files, warnings := translateFiles(ld.Files, categories)
	def := config.LinterDefinition{
		Name:          ld.Name,
		Files:         files,
		DirectConfigs: ld.DirectConfigs,
		ConfigFlag:    configFlag[ld.Name],
	}

	if len(ld.Tools) > 0 {
		if tool := findTool(tools, ld.Tools[0]); tool != nil {
			def.Runtime = tool.Runtime
			resolved, w := resolveTool(*tool, downloads, ld.Name, pinnedVersion)
			if w != "" {
				warnings = append(warnings, w)
			}
			def.Download = resolved.Download
			def.PackageInstall = resolved.PackageInstall
		}
	}

	// Real example: checkov declares two commands both named "lint" — one
	// restricted to platforms: [windows] (using checkov.cmd), one with no
	// restriction (plain checkov) — running both unconditionally made the
	// Windows-only variant fail with "exec format error" on macOS. Only the
	// first command (by name) that both matches the current platform and
	// has a supported output wins.
	accepted := map[string]bool{}
	for _, c := range ld.Commands {
		name := orDefault(c.Name, "lint")
		if accepted[name] || !platformMatches(c.Platforms) {
			continue
		}
		// in_place commands (formatters that rewrite the file themselves)
		// never reach output parsing in the engine (pkg/plugin.run returns
		// before parseOutput) — Output is irrelevant for them, so "rewrite"
		// (and anything else) translates fine as long as InPlace is set.
		if !c.InPlace && !supportedOutput[c.Output] {
			warnings = append(warnings, fmt.Sprintf("%s/%s: unsupported output type %q, skipping this command", ld.Name, name, c.Output))
			continue
		}
		accepted[name] = true
		def.Commands = append(def.Commands, config.Command{
			Name:           name,
			Run:            c.Run,
			Target:         targetFor(ld.Files),
			Output:         c.Output,
			ParseRegex:     config.RegexPattern(c.ParseRegex),
			SuccessCodes:   c.SuccessCodes,
			ReadOutputFrom: c.ReadOutputFrom,
			Formatter:      c.Formatter,
			InPlace:        c.InPlace,
			Parser:         c.Parser,
		})
	}
	return def, warnings
}

func findTool(tools []trunkTool, name string) *trunkTool {
	for i := range tools {
		if tools[i].Name == name {
			return &tools[i]
		}
	}
	return nil
}

func findDownloadGroup(groups []download.Group, name string) *download.Group {
	for i := range groups {
		if groups[i].Name == name {
			return &groups[i]
		}
	}
	return nil
}

// normalizeVariant converts one trunk-dialect download variant (v.OS/v.CPU
// decode as `any` — see trunkDownloadVariant's own doc comment) into the
// gob-safe, dialect-independent download.Variant pkg/download's Resolve
// consumes.
func normalizeVariant(v trunkDownloadVariant) download.Variant {
	nv := download.Variant{Constraint: v.Version, URL: v.URL, StripComponents: v.StripComponents}
	if m, ok := toStringMap(v.OS); ok {
		nv.OSMap = m
	} else if s, ok := v.OS.(string); ok {
		nv.OSFixed = s
	}
	if m, ok := toStringMap(v.CPU); ok {
		nv.CPUMap = m
	} else if s, ok := v.CPU.(string); ok {
		nv.CPUFixed = s
	}
	return nv
}

// toStringMap accepts a YAML value already decoded as map[string]interface{}
// (goccy/go-yaml's generic shape for a mapping node) and converts it to
// map[string]string; ok is false for any other shape (e.g. a plain string).
func toStringMap(v any) (map[string]string, bool) {
	raw, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	out := make(map[string]string, len(raw))
	for k, val := range raw {
		s, ok := val.(string)
		if !ok {
			return nil, false
		}
		out[k] = s
	}
	return out, true
}

func shimNames(shims []trunkShim) []string {
	names := make([]string, len(shims))
	for i, s := range shims {
		names[i] = s.Name
	}
	return names
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func enabledContains(enabled []config.PackageVersion, name string) bool {
	for _, e := range enabled {
		if e.Name() == name {
			return true
		}
	}
	return false
}

// enabledVersion returns the "@version" pin for name in enabled, or "" if
// name isn't there or has no pin.
func enabledVersion(enabled []config.PackageVersion, name string) string {
	for _, e := range enabled {
		if e.Name() == name {
			return e.Version()
		}
	}
	return ""
}
