package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/xunleii/rtunk/pkg/trunk/config"
	"github.com/xunleii/rtunk/pkg/trunk/renovate"
)

// renovateCmd is `rtunk renovate`: ROADMAP.md's post-v1.0 addition, generating Renovate
// annotations for trunk.yaml's version pins (see
// docs/superpowers/specs/2026-09-17-renovate-annotations-design.md).
type renovateCmd struct {
	Annotate renovateAnnotateCmd `cmd:"" help:"Annotate trunk.yaml's version pins for Renovate."`
	Config   renovateConfigCmd   `cmd:"" help:"Print the Renovate regexManagers config to add."`
}

type renovateAnnotateCmd struct{}

func (c *renovateAnnotateCmd) Run(cli *CLI, stdout io.Writer) error {
	configPath := cli.Config
	if configPath == "" {
		found, err := findTrunkYAML()
		if err != nil {
			return err
		}
		configPath = found
	}
	cfg, err := resolveConfig(configPath, cli.CacheDir, true)
	if err != nil {
		return err
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return err
	}

	report := annotateDoc(&doc, cfg)

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(detectIndentWidth(data))
	if err := enc.Encode(&doc); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	if err := os.WriteFile(configPath, buf.Bytes(), 0o644); err != nil {
		return err
	}

	printRenovateReport(stdout, report)
	return nil
}

// renovateReport summarizes what `rtunk renovate annotate` did.
type renovateReport struct {
	Annotated []string // "category/id" entries newly or already correctly annotated
	Skipped   []string // "category/id: reason" entries left untouched
}

func printRenovateReport(w io.Writer, r renovateReport) {
	sorted := append([]string(nil), r.Annotated...)
	sort.Strings(sorted)
	for _, a := range sorted {
		fmt.Fprintln(w, "annotated", a)
	}
	sortedSkipped := append([]string(nil), r.Skipped...)
	sort.Strings(sortedSkipped)
	for _, s := range sortedSkipped {
		fmt.Fprintln(w, "skipped", s)
	}
	fmt.Fprintf(w, "\n%d annotated, %d skipped\n", len(r.Annotated), len(r.Skipped))
}

// findMapKey returns mapping's key and value nodes for key, or ok=false if key is absent or
// mapping isn't a mapping -- a read-only counterpart to check.go's findOrCreateMapKey (which
// creates missing keys); annotateDoc must never author a new section a file didn't already have.
// Both nodes are returned because annotatePluginSources needs the KEY node specifically -- see
// this plan's own Global Constraints for why (HeadComment on a mapping's VALUE node renders
// wrong and doesn't round-trip; the KEY node is the one that works, verified against the real
// vendored yaml.v3).
func findMapKey(mapping *yaml.Node, key string) (keyNode, valueNode *yaml.Node, ok bool) {
	if mapping.Kind != yaml.MappingNode {
		return nil, nil, false
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i], mapping.Content[i+1], true
		}
	}
	return nil, nil, false
}

// annotateDoc walks doc's lint.enabled, runtimes.enabled, and plugins.sources[] nodes in place
// (doc is the parsed *yaml.Node document root, same tree shape editEnabled already parses),
// annotating every entry renovate.ForLint/ForRuntime/ForPluginSource resolves and rewriting an
// unpinned lint/runtimes entry to id@knownGoodVersion. cfg must already be resolved via
// config.ResolveAll (the full catalog, independent of what's currently enabled) -- annotating an
// entry needs its definition regardless of enabled state. A section entirely absent from doc (no
// lint:, no runtimes:, no plugins:) is left absent -- this function never authors a new empty
// section, unlike editEnabled's own find-or-create convention.
func annotateDoc(doc *yaml.Node, cfg config.Config) renovateReport {
	var report renovateReport
	if len(doc.Content) == 0 {
		return report
	}
	root := doc.Content[0]

	annotateEnabledSeq(root, "lint", &report, func(id string) (renovate.Annotation, string, bool) {
		return renovate.ForLint(cfg, id)
	})
	annotateEnabledSeq(root, "runtimes", &report, func(id string) (renovate.Annotation, string, bool) {
		return renovate.ForRuntime(cfg, id)
	})
	annotatePluginSources(root, cfg, &report)

	return report
}

// annotateEnabledSeq handles one category's "enabled:" sequence (a flat list of "id" or
// "id@version" scalars) -- shared by lint and runtimes, which have the identical node shape.
// resolve is renovate.ForLint or renovate.ForRuntime, already closed over cfg by the caller.
func annotateEnabledSeq(root *yaml.Node, category string, report *renovateReport, resolve func(id string) (renovate.Annotation, string, bool)) {
	_, catNode, ok := findMapKey(root, category)
	if !ok {
		return
	}
	_, enabledNode, ok := findMapKey(catNode, "enabled")
	if !ok || enabledNode.Kind != yaml.SequenceNode {
		return
	}

	for _, entry := range enabledNode.Content {
		id, _, pinned := cutVersion(entry.Value)
		ann, knownGoodVersion, ok := resolve(id)
		label := category + "/" + id
		if !ok {
			report.Skipped = append(report.Skipped, label+": no confident datasource")
			continue
		}
		if !pinned {
			if knownGoodVersion == "" {
				report.Skipped = append(report.Skipped, label+": no known_good_version to pin")
				continue
			}
			entry.Value = id + "@" + knownGoodVersion
		}
		entry.HeadComment = "# renovate: datasource=" + ann.Datasource + " depName=" + ann.DepName
		report.Annotated = append(report.Annotated, label)
	}
}

// annotatePluginSources handles plugins.sources[] -- a sequence of {id, uri, ref, local?}
// mappings, structurally different from lint/runtimes' flat "id@version" scalars: the comment
// goes on the "ref:" KEY node within each source's own mapping (see this plan's Global
// Constraints on why the key node, not the value node).
func annotatePluginSources(root *yaml.Node, cfg config.Config, report *renovateReport) {
	_, pluginsNode, ok := findMapKey(root, "plugins")
	if !ok {
		return
	}
	_, sourcesNode, ok := findMapKey(pluginsNode, "sources")
	if !ok || sourcesNode.Kind != yaml.SequenceNode {
		return
	}

	for _, entry := range sourcesNode.Content {
		_, idVal, ok := findMapKey(entry, "id")
		if !ok {
			continue
		}
		id := idVal.Value
		label := "plugins.sources/" + id
		src, exists := cfg.Plugins.Sources[id]
		if !exists {
			report.Skipped = append(report.Skipped, label+": not found in resolved config")
			continue
		}
		ann, ok := renovate.ForPluginSource(src)
		if !ok {
			report.Skipped = append(report.Skipped, label+": no confident datasource (local source or non-GitHub URI)")
			continue
		}
		refKey, _, ok := findMapKey(entry, "ref")
		if !ok {
			report.Skipped = append(report.Skipped, label+": no ref: to annotate")
			continue
		}
		refKey.HeadComment = "# renovate: datasource=" + ann.Datasource + " depName=" + ann.DepName
		report.Annotated = append(report.Annotated, label)
	}
}
