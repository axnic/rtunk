package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/xunleii/rtunk/pkg/trunk/check"
	"github.com/xunleii/rtunk/pkg/trunk/config"
)

// checkCmd is `rtunk check`: ROADMAP.md v0.3, running enabled linters read-only. Bare `rtunk
// check [paths...]` is the default subcommand. Task 5 adds the Enable/Disable fields once
// checkEnableCmd/checkDisableCmd exist -- defining all four fields here would leave this task's
// own commit referencing not-yet-defined types, unable to build standalone.
type checkCmd struct {
	Run  checkRunCmd  `cmd:"" default:"withargs" help:"Run enabled checks."`
	List checkListCmd `cmd:"" help:"List all linters available for the current configuration."`
}

// checkRunCmd is `rtunk check [paths...]`: given paths, or the whole repository if none.
type checkRunCmd struct {
	Paths []string `arg:"" optional:"" help:"Paths to check (default: whole repository)."`
}

func (c *checkRunCmd) Run(cli *CLI, stdout io.Writer) error {
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
	for ev := range events {
		switch ev.Phase {
		case check.Done:
			findings = append(findings, ev.Findings...)
		case check.Skipped:
			skipped = append(skipped, fmt.Sprintf("%s [%s]", ev.Linter, ev.Note))
		case check.Failed:
			return ev.Err
		}
	}

	printReport(stdout, findings, skipped)
	if len(findings) > 0 {
		return fmt.Errorf("rtunk: check found %d issue(s)", len(findings))
	}
	return nil
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
	cfg, err := resolveConfig(cli.Config, cli.CacheDir, false)
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
