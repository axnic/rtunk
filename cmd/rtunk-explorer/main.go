// Command rtunk-explorer inspects a trunk-dialect plugin repo's compiled
// catalog — the single merged structure pkg/plugin builds out of every
// linters/*/plugin.yaml in that repo (see that package's doc comment for
// the full retrieval/merge pipeline) — without running any linter.
//
// Point it at either:
//   - <uri>@<ref>: resolved into a throwaway temp directory, discarded once
//     this command exits — nothing is cached across runs (pkg/plugin never
//     caches its own result; this tool has no reason to opt into caching a
//     one-shot inspection).
//   - a glob matching one or more existing catalog files (e.g. real ones
//     `rtunk check` cached under its own cache dir) — read directly, no
//     network access.
//
// The catalog is printed as YAML, JSON, or gob (--output).
package main

import (
	"encoding/gob"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/spf13/cobra"

	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/config"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/plugin"
)

func main() {
	var output string
	cmd := &cobra.Command{
		Use:   "rtunk-explorer <uri>@<ref> | <cache-file-glob>",
		Short: "Inspect a trunk-dialect plugin repo's compiled catalog, without running any linter",
		Long: `rtunk-explorer resolves a plugins.sources-style repo (github.com/trunk-io/plugins
being the canonical example) and dumps its compiled catalog: every
linters/*/plugin.yaml file in it, merged into one structure — the same
thing rtunk itself builds internally before translating it into runnable
linter definitions (see the pkg/plugin package doc for that pipeline).

Use it to answer questions like: "does this repo even parse?", "what does
rtunk see for linter X after merging?", "why did a linter/tool/download
name collide across two files?", or to look at an already-cached catalog
another rtunk run left behind, without touching the network.

The argument is one of:

  <uri>@<ref>
        A git URI and a pinned ref (a tag or commit SHA — never a branch,
        same rule rtunk itself enforces). Cloned into a throwaway temp
        directory that's removed before this command exits: nothing is
        cached across runs, since a one-shot inspection has no "next time"
        to cache for.

  <cache-file-glob>
        A glob matching one or more existing *.catalog.gob files (e.g. ones
        a real "rtunk check" already cached under its own cache directory).
        Read directly — no network access at all.

Examples:
  rtunk-explorer https://github.com/trunk-io/plugins@v1.10.2
  rtunk-explorer ~/.cache/rtunk/plugins/*/*.catalog.gob -o json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, args[0], output)
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "yaml", "output format: yaml (readable), json (scriptable), or gob (rtunk's own cache format — re-readable by this same tool)")
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func run(cmd *cobra.Command, arg, output string) error {
	if output != "yaml" && output != "json" && output != "gob" {
		return fmt.Errorf("unsupported --output %q (want yaml, json, or gob)", output)
	}
	if strings.Contains(arg, "@") {
		return exploreSource(cmd, arg, output)
	}
	return exploreCacheGlob(cmd, arg, output)
}

// exploreSource resolves uri@ref (pkg/plugin.Resolve clones into its own
// disposable temp directory, removed before it returns) and dumps its
// merged catalog.
func exploreSource(cmd *cobra.Command, arg, output string) error {
	uri, ref, err := splitRef(arg)
	if err != nil {
		return err
	}
	source := config.PluginSource{ID: deriveID(uri), URI: uri, Ref: ref}
	cat, err := plugin.Resolve(source)
	if err != nil {
		return err
	}
	for _, w := range cat.Warnings {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", w)
	}
	return dumpCatalog(cmd, cat, output)
}

// exploreCacheGlob loads one or more existing catalog files matching
// pattern directly, without any network access, printing each as its own
// document (yaml/json documents separated by "---"; gob values are simply
// concatenated — the format is already self-delimiting, one Decode call per
// Encode call reads them back in order).
func exploreCacheGlob(cmd *cobra.Command, pattern, output string) error {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return fmt.Errorf("glob %q: %w", pattern, err)
	}
	if len(matches) == 0 {
		return fmt.Errorf("no cache file matches %q", pattern)
	}
	for i, path := range matches {
		if i > 0 && output != "gob" {
			fmt.Fprintln(cmd.OutOrStdout(), "---")
		}
		cat, err := plugin.Open(path)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if err := dumpCatalog(cmd, cat, output); err != nil {
			return err
		}
	}
	return nil
}

func dumpCatalog(cmd *cobra.Command, cat any, output string) error {
	switch output {
	case "json":
		// Round-tripped through yaml.Marshal/Unmarshal first (rather than
		// json.Marshal(cat) directly) so JSON keys match the catalog's
		// "yaml" struct tags instead of falling back to bare Go field
		// names — one source of key names for every output format.
		yamlData, err := yaml.Marshal(cat)
		if err != nil {
			return err
		}
		var generic any
		if err := yaml.Unmarshal(yamlData, &generic); err != nil {
			return err
		}
		data, err := json.MarshalIndent(generic, "", "  ")
		if err != nil {
			return err
		}
		if _, err := cmd.OutOrStdout().Write(data); err != nil {
			return err
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout())
		return err
	case "gob":
		return gob.NewEncoder(cmd.OutOrStdout()).Encode(cat)
	default: // "yaml"
		data, err := yaml.Marshal(cat)
		if err != nil {
			return err
		}
		_, err = cmd.OutOrStdout().Write(data)
		return err
	}
}

// splitRef splits "uri@ref" on the LAST "@" — an SSH-style URI
// (git@github.com:org/repo) can itself contain one.
func splitRef(arg string) (uri, ref string, err error) {
	i := strings.LastIndex(arg, "@")
	if i < 0 || i == len(arg)-1 {
		return "", "", fmt.Errorf("expected <uri>@<ref>, got %q", arg)
	}
	return arg[:i], arg[i+1:], nil
}

// deriveID turns a git URI into a filesystem-safe directory name for the
// temp clone — only used for that pathing, never shown to the user.
func deriveID(uri string) string {
	id := strings.TrimSuffix(uri, ".git")
	id = strings.NewReplacer("https://", "", "http://", "", "/", "-", ":", "-", "@", "-").Replace(id)
	return id
}
