package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"

	"github.com/xunleii/rtunk/pkg/cache/download"
	"github.com/xunleii/rtunk/pkg/trunk/config"
)

// configCmd is `rtunk config`: currently just print, the fully resolved configuration.
type configCmd struct {
	Print printCmd `cmd:"" help:"Print the fully resolved configuration."`
}

type printCmd struct {
	Output string `help:"Output format." enum:"yaml,json" default:"yaml"`
}

func (c *printCmd) Run(cli *CLI, stdout io.Writer) error {
	cfg, err := resolveConfig(cli.Config, cli.CacheDir, false)
	if err != nil {
		return err
	}
	return printValue(stdout, cfg, c.Output)
}

// pluginsCmd is `rtunk plugins`: print dumps the full merged plugin catalog (a registry dump, can
// be very large) instead of only what is enabled and used. Replaces `config print --all`.
type pluginsCmd struct {
	Print pluginsPrintCmd `cmd:"" help:"Print all configuration available across all plugins, resolved."`
}

type pluginsPrintCmd struct {
	Output string `help:"Output format." enum:"yaml,json" default:"yaml"`
}

func (c *pluginsPrintCmd) Run(cli *CLI, stdout io.Writer) error {
	cfg, err := resolveConfig(cli.Config, cli.CacheDir, true)
	if err != nil {
		return err
	}
	return printValue(stdout, cfg, c.Output)
}

// resolveConfig finds (unless configPath is already set) and resolves the trunk.yaml in effect.
// all selects ResolveAll (the full merged catalog) over Resolve (enabled+used only) -- only
// `plugins print` and `linters list`/`actions list` set it.
func resolveConfig(configPath, cacheDir string, all bool) (config.Config, error) {
	if configPath == "" {
		found, err := findTrunkYAML()
		if err != nil {
			return config.Config{}, err
		}
		configPath = found
	}
	if all {
		return config.ResolveAll(configPath, cacheDir)
	}
	return config.Resolve(configPath, cacheDir)
}

// checkDeprecations hard-refuses cfg if it enables a legacy-shaped linter (entry #8), else prints
// a warning to stderr for every enabled linter/command carrying a deprecated: message (entry #9).
// Called by check/fmt's Run right after resolveConfig, "before any execution starts" -- read-only
// inspection commands (config print, linters list) call resolveConfig directly and skip this, so a
// broken configuration can still be inspected in order to fix it.
func checkDeprecations(cfg config.Config, stderr io.Writer) error {
	warnings, err := cfg.CheckDeprecations()
	for _, w := range warnings {
		_, _ = fmt.Fprintln(stderr, "rtunk: warning:", w)
	}
	return err
}

// resolvedVersionFor is the version `where`/`exec` (Tasks 12/13) resolve for category+id when the
// CLI arg wasn't pinned with @version -- mirrors fetchToolRef/fetchRuntimeRef's own resolution
// (pkg/cache/download.Download) so both commands predict the exact cache path Download would use,
// without invoking it. "actions" has no KnownGoodVersion of its own (only Actions.Enabled's own
// @version pin, if any); "lint"/"plugins" resolve to a linter/plugin, not a concrete tool or
// runtime build, so there is no path to predict -- reject rather than guess.
func resolvedVersionFor(cfg config.Config, category, id string) (string, error) {
	switch category {
	case "runtimes":
		return download.ResolveVersion(cfg.Runtimes.Enabled, id, cfg.Runtimes.Definitions[id].KnownGoodVersion), nil
	case "tools":
		return download.ResolveVersion(cfg.Lint.Enabled, id, cfg.Tools[id].KnownGoodVersion), nil
	case "actions":
		return download.ResolveVersion(cfg.Actions.Enabled, id, ""), nil
	default:
		return "", fmt.Errorf("rtunk: %s has no resolvable version; pin one with %s@<version>", category, id)
	}
}

// printValue marshals v as YAML (every definition's field tags, e.g. Linter/Tool/Runtime, are
// already YAML tags matching plugin.yaml's own vocabulary) and, for --output json, round-trips
// that through yaml.Unmarshal into a generic any -- yaml.v3 decodes mappings into
// map[string]interface{}, so the result re-marshals as JSON with the same field names, no
// separate set of json tags to keep in sync.
func printValue(w io.Writer, v any, format string) error {
	data, err := yaml.Marshal(v)
	if err != nil {
		return err
	}

	switch format {
	case "", "yaml":
		_, err := w.Write(data)
		return err
	case "json":
		var generic any
		if err := yaml.Unmarshal(data, &generic); err != nil {
			return err
		}
		js, err := json.MarshalIndent(generic, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(w, string(js))
		return err
	default:
		return fmt.Errorf("invalid --output %q: must be yaml or json", format)
	}
}
