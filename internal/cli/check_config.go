package cli

import (
	"bytes"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/xunleii/rtunk/pkg/renovate"
	"github.com/xunleii/rtunk/pkg/trunk/config"
)

// lintersCmd is `rtunk linters`, symmetric with `rtunk actions {list,enable,disable}`.
type lintersCmd struct {
	List    checkListCmd    `cmd:"" default:"withargs" help:"List all linters available for the current configuration."`
	Enable  checkEnableCmd  `cmd:"" help:"Enable one or more linters."`
	Disable checkDisableCmd `cmd:"" help:"Disable one or more linters."`
}

// checkListCmd is `rtunk linters list`: enabled linters (with their pinned version), the ones
// available for this repo (matching at least one file), and with --all the rest.
type checkListCmd struct {
	All    bool   `help:"Also list the linters that match no file in this repository."`
	Format string `enum:"human,json" default:"human" help:"Output format: human or json."`
}

func (c *checkListCmd) Run(cli *CLI, stdout io.Writer) error {
	// all=true (config.ResolveAll): the full catalog, not only the enabled+used subset.
	cfg, err := resolveConfig(cli.Config, cli.CacheDir, true)
	if err != nil {
		return err
	}
	repoRoot, err := logsRepoRoot(cli)
	if err != nil {
		return err
	}
	files, err := repoFiles(repoRoot)
	if err != nil {
		return err
	}
	return writeListing(stdout, buildLintersList(cfg, repoRoot, files), c.Format, "linter", "linters enable", c.All)
}

// checkEnableCmd is `rtunk linters enable <id>[@version]...`.
type checkEnableCmd struct {
	ID []string `arg:"" help:"Linter id(s) to enable, optionally @version."`
}

func (c *checkEnableCmd) Run(cli *CLI) error {
	return editEnabled(cli, "lint", func(existing []string) []string {
		return addEnabled(existing, c.ID)
	})
}

// checkDisableCmd is `rtunk linters disable <id>...`.
type checkDisableCmd struct {
	ID []string `arg:"" help:"Linter id(s) to disable."`
}

func (c *checkDisableCmd) Run(cli *CLI) error {
	return editEnabled(cli, "lint", func(existing []string) []string {
		return removeEnabled(existing, c.ID)
	})
}

// editEnabled loads the trunk.yaml in effect, applies edit to category's (here always "lint")
// enabled: list -- creating the category/enabled nodes if entirely absent, a valid trunk.yaml
// need not pre-declare an empty list -- and writes the result back to the same file. This is
// rtunk's first config-writing code path: editing via *yaml.Node (not a struct round-trip)
// preserves comments and the source file's indentation width everywhere else in the file,
// since only the enabled sequence node's own Content is rebuilt and the encoder is set to
// re-emit at the width the file already used (other formatting choices, such as flow-vs-block
// style, still follow yaml.v3's own defaults for any node it actually rewrites).
func editEnabled(cli *CLI, category string, edit func([]string) []string) error {
	configPath := cli.Config
	if configPath == "" {
		found, err := findTrunkYAML()
		if err != nil {
			return err
		}
		configPath = found
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return err
	}
	if len(doc.Content) == 0 {
		doc.Kind = yaml.DocumentNode
		doc.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
	}
	root := doc.Content[0]

	catNode := findOrCreateMapKey(root, category)
	enabledNode := findOrCreateMapKey(catNode, "enabled")
	if enabledNode.Kind != yaml.SequenceNode {
		enabledNode.Kind = yaml.SequenceNode
		enabledNode.Tag = "!!seq"
		enabledNode.Content = nil
	}

	existing := make([]string, len(enabledNode.Content))
	hadAnnotations := false
	for i, n := range enabledNode.Content {
		existing[i] = n.Value
		if strings.HasPrefix(strings.TrimSpace(n.HeadComment), "# renovate:") {
			hadAnnotations = true
		}
	}

	updated := edit(existing)

	var cfg config.Config
	if hadAnnotations && category == "lint" {
		cfg, err = resolveConfig(configPath, cli.CacheDir, true)
		if err != nil {
			return err
		}
	}

	enabledNode.Content = make([]*yaml.Node, len(updated))
	for i, v := range updated {
		node := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
		if hadAnnotations && category == "lint" {
			bareID, _, pinned := cutVersion(v)
			if ann, knownGoodVersion, ok := renovate.ForLint(cfg, bareID); ok {
				if !pinned {
					if knownGoodVersion != "" {
						node.Value = bareID + "@" + knownGoodVersion
						node.HeadComment = "# renovate: datasource=" + ann.Datasource + " depName=" + ann.DepName
					}
				} else {
					node.HeadComment = "# renovate: datasource=" + ann.Datasource + " depName=" + ann.DepName
				}
			}
		}
		enabledNode.Content[i] = node
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(detectIndentWidth(data))
	if err := enc.Encode(&doc); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	//nolint:gosec // trunk.yaml is a repo-tracked config file, readable like every other tracked file
	return os.WriteFile(configPath, buf.Bytes(), 0o644)
}

// detectIndentWidth sniffs the leading-space width of the first indented, non-blank line in
// data, defaulting to 2 (trunk.yaml's own convention, and yaml.v3's most common real-world
// input) when no indented line is found -- e.g. an empty or single-top-level-key file.
func detectIndentWidth(data []byte) int {
	for line := range strings.SplitSeq(string(data), "\n") {
		trimmed := strings.TrimLeft(line, " ")
		if n := len(line) - len(trimmed); n > 0 && trimmed != "" {
			return n
		}
	}
	return 2
}

// findOrCreateMapKey returns mapping's value node for key, creating an empty mapping node under
// a new key entry if key is entirely absent. mapping itself is coerced to a (possibly empty)
// MappingNode first if it isn't already one -- e.g. a hand-edited "lint:" with no value parses
// as a null scalar node, and appending key/value pairs onto a non-mapping node's Content is
// silently dropped by the encoder, discarding the whole edit without error.
func findOrCreateMapKey(mapping *yaml.Node, key string) *yaml.Node {
	if mapping.Kind != yaml.MappingNode {
		mapping.Kind = yaml.MappingNode
		mapping.Tag = "!!map"
		mapping.Content = nil
	}

	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	valNode := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	mapping.Content = append(mapping.Content, keyNode, valNode)
	return valNode
}

// addEnabled appends each id in ids to existing, first dropping any existing entry whose bare id
// (ignoring an @version pin) matches one being added -- a re-enable with a different pin replaces
// the old pin instead of appending a duplicate.
func addEnabled(existing, ids []string) []string {
	out := make([]string, 0, len(existing)+len(ids))
	for _, e := range existing {
		bare, _, _ := cutVersion(e)
		keep := true
		for _, id := range ids {
			newBare, _, _ := cutVersion(id)
			if bare == newBare {
				keep = false
				break
			}
		}
		if keep {
			out = append(out, e)
		}
	}
	return append(out, ids...)
}

// removeEnabled drops every entry of existing whose bare id (ignoring an @version pin) matches
// one of ids -- ids are bare-compared too, so a version-pinned removal id (e.g. copy-pasted
// straight out of enabled: or `linters list` output) still matches an entry pinned to a
// different version.
func removeEnabled(existing, ids []string) []string {
	out := existing[:0:0]
	for _, e := range existing {
		bare, _, _ := cutVersion(e)
		drop := false
		for _, id := range ids {
			bareID, _, _ := cutVersion(id)
			if bare == bareID {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, e)
		}
	}
	return out
}
