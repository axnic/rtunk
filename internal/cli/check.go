package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/xunleii/rtunk/pkg/trunk/check"
	"github.com/xunleii/rtunk/pkg/trunk/config"
)

// checkCmd is `rtunk check`: ROADMAP.md v0.3, running enabled linters read-only.
// Bare `rtunk check [paths...]` is the default subcommand.
type checkCmd struct {
	Run     checkRunCmd     `cmd:"" default:"withargs" help:"Run enabled checks."`
	Enable  checkEnableCmd  `cmd:"" help:"Enable one or more linters."`
	Disable checkDisableCmd `cmd:"" help:"Disable one or more linters."`
	List    checkListCmd    `cmd:"" help:"List all linters available for the current configuration."`
}

// checkRunCmd is `rtunk check [paths...]`: given paths, or the whole repository if none.
type checkRunCmd struct {
	Paths []string `arg:"" optional:"" help:"Paths to check (default: whole repository)."`
}

func (c *checkRunCmd) Run(cli *CLI, stdout io.Writer, stderr Stderr) error {
	configPath := cli.Config
	if configPath == "" {
		found, err := findTrunkYAML()
		if err != nil {
			return err
		}
		configPath = found
	}
	cfg, err := resolveConfig(configPath, cli.CacheDir, false)
	if err != nil {
		return err
	}
	// configPath is <repoRoot>/.trunk/trunk.yaml (findTrunkYAML's only supported layout) --
	// repoRoot is two directories up.
	repoRoot := filepath.Dir(filepath.Dir(configPath))

	events, err := check.Run(cfg, cli.CacheDir, repoRoot, c.Paths)
	if err != nil {
		return err
	}

	var findings []check.Finding
	var skipped []string
	skippedLinters := map[string]bool{}
	var failed error
	for ev := range events {
		printEvent(stderr, ev)
		switch ev.Phase {
		case check.Done:
			findings = append(findings, ev.Findings...)
		case check.Skipped:
			// Dedupe by linter: a linter with several unsupported commands emits one Skipped
			// event per command, but the report should name it once, not once per command.
			if !skippedLinters[ev.Linter] {
				skippedLinters[ev.Linter] = true
				skipped = append(skipped, fmt.Sprintf("%s [%s]", ev.Linter, ev.Note))
			}
		case check.Failed:
			if failed == nil {
				failed = ev.Err
			}
		}
	}

	printReport(stdout, findings, skipped)
	if failed != nil {
		return failed
	}
	if len(findings) > 0 {
		return fmt.Errorf("rtunk: check found %d issue(s)", len(findings))
	}
	return nil
}

// printEvent prints every event Run streams -- including in-progress Running events -- to w
// (stderr) as it arrives, so a long check run shows live which linter and file is currently being
// checked instead of going silent until the final report. This is separate from printReport
// (stdout, the final findings summary) so stdout's contract stays exact and machine-parseable.
func printEvent(w io.Writer, ev check.Event) {
	switch ev.Phase {
	case check.Running:
		fmt.Fprintf(w, "running %s: %s\n", ev.Linter, ev.File)
	case check.Done:
		fmt.Fprintf(w, "done %s: %d issue(s)\n", ev.Linter, len(ev.Findings))
	case check.Skipped:
		fmt.Fprintf(w, "skipped %s: %s\n", ev.Linter, ev.Note)
	case check.Failed:
		fmt.Fprintf(w, "failed: %v\n", ev.Err)
	}
}

// printReport prints findings (sorted by file, then line, then column) one per line, then a
// trailing summary line -- always printed, with a skipped-linters parenthetical only when
// skipped is non-empty.
func printReport(w io.Writer, findings []check.Finding, skipped []string) {
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].File != findings[j].File {
			return findings[i].File < findings[j].File
		}
		if findings[i].Line != findings[j].Line {
			return findings[i].Line < findings[j].Line
		}
		return findings[i].Column < findings[j].Column
	})

	for _, f := range findings {
		fmt.Fprintln(w, formatFinding(f))
	}

	files := map[string]bool{}
	for _, f := range findings {
		files[f.File] = true
	}

	summary := fmt.Sprintf("%d issue(s) in %d file(s)", len(findings), len(files))
	if len(skipped) > 0 {
		sorted := append([]string(nil), skipped...)
		sort.Strings(sorted)
		summary += fmt.Sprintf(" (%d linter(s) skipped: %s)", len(sorted), strings.Join(sorted, ", "))
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, summary)
}

func formatFinding(f check.Finding) string {
	loc := f.File
	if f.Line > 0 {
		loc += fmt.Sprintf(":%d", f.Line)
		if f.Column > 0 {
			loc += fmt.Sprintf(":%d", f.Column)
		}
	}
	msg := f.Message
	if f.RuleID != "" {
		msg = fmt.Sprintf("[%s] %s", f.RuleID, msg)
	}
	if f.URL != "" {
		msg += fmt.Sprintf(" (%s)", f.URL)
	}
	return fmt.Sprintf("%s %s %s", loc, f.Severity, msg)
}

// checkListCmd is `rtunk check list`.
type checkListCmd struct{}

func (c *checkListCmd) Run(cli *CLI, stdout io.Writer) error {
	// all=true (config.ResolveAll): ROADMAP.md promises "list all linters available for the
	// current configuration", not only the enabled+used subset Resolve trims to -- otherwise the
	// enabled marker in formatLintList would be dead code (every listed line is always enabled).
	cfg, err := resolveConfig(cli.Config, cli.CacheDir, true)
	if err != nil {
		return err
	}
	fmt.Fprint(stdout, formatLintList(cfg))
	return nil
}

// formatLintList renders every linter in cfg.Lint.Definitions, one per line, "* " prefixed when
// enabled -- cfg.Lint.Enabled entries are matched by bare id (an @version pin doesn't change
// whether a linter counts as enabled).
func formatLintList(cfg config.Config) string {
	names := make([]string, 0, len(cfg.Lint.Definitions))
	for name := range cfg.Lint.Definitions {
		names = append(names, name)
	}
	sort.Strings(names)

	enabled := map[string]bool{}
	for _, e := range cfg.Lint.Enabled {
		bare, _, _ := cutVersion(e)
		enabled[bare] = true
	}

	var b strings.Builder
	for _, name := range names {
		marker := " "
		if enabled[name] {
			marker = "*"
		}
		fmt.Fprintf(&b, "%s %s  %s\n", marker, name, cfg.Lint.Definitions[name].Description)
	}
	return b.String()
}

// checkEnableCmd is `rtunk check enable <id>[@version]...`.
type checkEnableCmd struct {
	ID []string `arg:"" help:"Linter id(s) to enable, optionally @version."`
}

func (c *checkEnableCmd) Run(cli *CLI) error {
	return editEnabled(cli, "lint", func(existing []string) []string {
		return addEnabled(existing, c.ID)
	})
}

// checkDisableCmd is `rtunk check disable <id>...`.
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
	for i, n := range enabledNode.Content {
		existing[i] = n.Value
	}

	updated := edit(existing)

	enabledNode.Content = make([]*yaml.Node, len(updated))
	for i, v := range updated {
		enabledNode.Content[i] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
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
	return os.WriteFile(configPath, buf.Bytes(), 0o644)
}

// detectIndentWidth sniffs the leading-space width of the first indented, non-blank line in
// data, defaulting to 2 (trunk.yaml's own convention, and yaml.v3's most common real-world
// input) when no indented line is found -- e.g. an empty or single-top-level-key file.
func detectIndentWidth(data []byte) int {
	for _, line := range strings.Split(string(data), "\n") {
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
// straight out of enabled: or `check list` output) still matches an entry pinned to a
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
