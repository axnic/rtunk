package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/xunleii/rtunk/pkg/trunk/actions"
	"github.com/xunleii/rtunk/pkg/trunk/config"
)

// actionsCmd is `rtunk actions`: ROADMAP.md v0.5.
type actionsCmd struct {
	List    actionsListCmd    `cmd:"" default:"withargs" help:"List actions available for the current configuration."`
	Enable  actionsEnableCmd  `cmd:"" help:"Enable one or more actions."`
	Disable actionsDisableCmd `cmd:"" help:"Disable one or more actions."`
	Run     actionsRunCmd     `cmd:"" help:"Run an action on demand, or every action a git hook triggers."`
	History actionsHistoryCmd `cmd:"" help:"Show recent action runs."`
}

type actionsListCmd struct{}

func (c *actionsListCmd) Run(cli *CLI, stdout io.Writer) error {
	cfg, err := resolveConfig(cli.Config, cli.CacheDir, true)
	if err != nil {
		return err
	}
	fmt.Fprint(stdout, formatActionsList(cfg))
	return nil
}

// formatActionsList mirrors formatLintList (check.go) exactly: sorted names, "*" prefix when
// enabled, one line each.
func formatActionsList(cfg config.Config) string {
	names := make([]string, 0, len(cfg.Actions.Definitions))
	for name := range cfg.Actions.Definitions {
		names = append(names, name)
	}
	sort.Strings(names)

	enabled := map[string]bool{}
	for _, e := range cfg.Actions.Enabled {
		bare, _, _ := cutVersion(e)
		enabled[bare] = true
	}

	var b strings.Builder
	for _, name := range names {
		marker := " "
		if enabled[name] {
			marker = "*"
		}
		fmt.Fprintf(&b, "%s %s  %s\n", marker, name, cfg.Actions.Definitions[name].Description)
	}
	return b.String()
}

type actionsEnableCmd struct {
	ID []string `arg:"" help:"Action id(s) to enable."`
}

func (c *actionsEnableCmd) Run(cli *CLI) error {
	return editActionsEnabled(cli, c.ID, true)
}

type actionsDisableCmd struct {
	ID []string `arg:"" help:"Action id(s) to disable."`
}

func (c *actionsDisableCmd) Run(cli *CLI) error {
	return editActionsEnabled(cli, c.ID, false)
}

// editActionsEnabled keeps actions.enabled/actions.disabled in sync in one file write: enabling
// adds to enabled and removes from disabled (and vice versa for disabling), mirroring what the
// user's own real trunk CLI does to this repo's .trunk/trunk.yaml. Reuses check.go's own
// findOrCreateMapKey/detectIndentWidth/addEnabled/removeEnabled (same package, unexported) rather
// than editEnabled itself, which only ever touches one list.
func editActionsEnabled(cli *CLI, ids []string, enable bool) error {
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
	catNode := findOrCreateMapKey(root, "actions")

	enabledNode := findOrCreateSeqKey(catNode, "enabled")
	disabledNode := findOrCreateSeqKey(catNode, "disabled")

	enabledList := nodeStrings(enabledNode)
	disabledList := nodeStrings(disabledNode)

	if enable {
		enabledList = addEnabled(enabledList, ids)
		disabledList = removeEnabled(disabledList, ids)
	} else {
		disabledList = addEnabled(disabledList, ids)
		enabledList = removeEnabled(enabledList, ids)
	}

	setNodeStrings(enabledNode, enabledList)
	setNodeStrings(disabledNode, disabledList)

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

func findOrCreateSeqKey(mapping *yaml.Node, key string) *yaml.Node {
	n := findOrCreateMapKey(mapping, key)
	if n.Kind != yaml.SequenceNode {
		n.Kind = yaml.SequenceNode
		n.Tag = "!!seq"
		n.Content = nil
	}
	// Force block style even when the source used flow style (e.g. "enabled: []") -- an existing
	// flow-style sequence node otherwise keeps rendering inline (e.g. "enabled: [commitlint]")
	// since only Content is rebuilt above, not Style.
	n.Style = 0
	return n
}

func nodeStrings(n *yaml.Node) []string {
	out := make([]string, len(n.Content))
	for i, c := range n.Content {
		out[i] = c.Value
	}
	return out
}

func setNodeStrings(n *yaml.Node, values []string) {
	n.Content = make([]*yaml.Node, len(values))
	for i, v := range values {
		n.Content[i] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
	}
}

// actionsRunCmd is `rtunk actions run <id> [-- args...]` or `rtunk actions run --hook <name> [--
// args...]`. Only one Args-shaped positional field exists (mirroring execCmd's own passthrough
// pattern in exec.go) so Kong never has to arbitrate between a bare ID and a --hook flag sharing
// the same positional slot: in ID mode, Args[0] IS the id; in --hook mode, Args (if any) are
// forwarded as-is.
type actionsRunCmd struct {
	Hook string   `help:"Run every enabled action triggered by this git hook, instead of a single action by id."`
	Args []string `arg:"" optional:"" passthrough:"" help:"<action-id> [-- args...] when --hook is not given; otherwise just the args to forward."`
}

func (c *actionsRunCmd) Run(cli *CLI, stdout io.Writer, stderr Stderr) error {
	args := c.Args
	var id string
	if c.Hook == "" {
		if len(args) == 0 {
			return fmt.Errorf("rtunk: actions run: specify an action id or --hook")
		}
		id = args[0]
		args = args[1:]
	}
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}

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
	repoRoot, err := gitRepoRoot(filepath.Dir(configPath))
	if err != nil {
		return err
	}

	var matched []config.Action
	if id != "" {
		a, ok := cfg.Actions.Definitions[id]
		if !ok {
			return fmt.Errorf("rtunk: actions run: unknown action %q", id)
		}
		matched = []config.Action{a}
	} else {
		matched = actions.Resolve(cfg, c.Hook)
	}

	var stdin io.Reader
	for _, a := range matched {
		if strings.Contains(a.Run, "${hook_stdin_path}") {
			stdin = os.Stdin
			break
		}
	}

	for _, a := range matched {
		opts := actions.RunOptions{CacheDir: cli.CacheDir, RepoRoot: repoRoot, Hook: c.Hook, Args: args, Stdin: stdin}
		result, runErr := actions.Run(context.Background(), cfg, a, opts, stdout, stderr)
		if result.Skipped {
			fmt.Fprintf(stderr, "skipped %s: non-interactive context\n", a.ID)
			continue
		}
		if runErr != nil {
			return runErr
		}
	}
	return nil
}

type actionsHistoryCmd struct {
	ID    string `help:"Restrict to one action id."`
	Limit int    `aliases:"count" help:"Maximum entries to show." default:"20"`
}

func (c *actionsHistoryCmd) Run(cli *CLI, stdout io.Writer) error {
	configPath := cli.Config
	if configPath == "" {
		found, err := findTrunkYAML()
		if err != nil {
			return err
		}
		configPath = found
	}
	repoRoot, err := gitRepoRoot(filepath.Dir(configPath))
	if err != nil {
		return err
	}

	entries, err := actions.History(cli.CacheDir, repoRoot, c.ID, c.Limit)
	if err != nil {
		return err
	}
	for _, e := range entries {
		status := fmt.Sprintf("exit %d", e.ExitCode)
		if e.Skipped {
			status = "skipped"
		}
		hook := e.Hook
		if hook == "" {
			hook = "manual"
		}
		fmt.Fprintf(stdout, "%s  %s  %s  %s  %s\n",
			e.StartedAt.Format(time.RFC3339), e.ActionID, hook, status, e.Duration.Round(time.Millisecond))
	}
	return nil
}
