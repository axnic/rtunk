package runlog

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// Render writes events as the human-readable text `rtunk logs show` prints: the same information
// as the JSONL, laid out so a run can be followed top to bottom.
func Render(w io.Writer, events []Event) {
	p := func(format string, a ...any) { _, _ = fmt.Fprintf(w, format, a...) }
	for _, ev := range events {
		switch ev.T {
		case KindRunStart:
			p("run %s  (rtunk %s)\n", ev.TS, ev.Rtunk)
			p("  argv: %s\n  cwd: %s\n  repo: %s\n", strings.Join(ev.Argv, " "), ev.Cwd, ev.RepoRoot)
			if ev.Config != "" {
				p("  config: %s\n", ev.Config)
			}
			if ev.Concurrency > 0 {
				p("  concurrency: %d\n", ev.Concurrency)
			}
			if ev.DryRun {
				p("  dry run: yes\n")
			}
		case KindInvocation:
			p("\n#%d %s\n", ev.ID, ev.Linter)
			p("  $ %s\n", shellLine(ev.Argv))
			if ev.Template != "" && ev.Template != shellLine(ev.Argv) {
				p("  template: %s\n", ev.Template)
			}
			p("  cwd: %s\n", ev.Cwd)
			if ev.PathPrefix != "" {
				p("  PATH prefix: %s\n", ev.PathPrefix)
			}
			if len(ev.ToolVersions) > 0 {
				p("  tools: %s\n", joinSorted(ev.ToolVersions, "@"))
			}
			if ev.Sandbox != "" {
				p("  sandbox: %s\n", ev.Sandbox)
			}
			if len(ev.Files) > 0 {
				p("  files: %s\n", strings.Join(ev.Files, ", "))
			}
		case KindOutput:
			p("  %s%s:\n%s", ev.Stream, truncatedMark(ev.Truncated), indent(ev.Data, "    "))
		case KindExit:
			p("  exit %d in %dms", deref(ev.Code), ev.Ms)
			if ev.ParsedFrom != "" {
				p(" (parsing %s)", ev.ParsedFrom)
			}
			p("\n")
		case KindParser:
			p("  parser: %s\n", shellLine(ev.Argv))
			p("    reads %s, exit %d in %dms\n", ev.StdinFrom, deref(ev.Code), ev.Ms)
			if ev.Err != "" {
				p("    error: %s\n", ev.Err)
			}
			if ev.Data != "" {
				p("    output%s:\n%s", truncatedMark(ev.Truncated), indent(ev.Data, "      "))
			}
		case KindFindings:
			p("  findings: %d\n", len(ev.Findings))
			for _, f := range ev.Findings {
				p("    %s:%d %s [%s] %s\n", f.File, f.Line, f.Severity, f.RuleID, f.Message)
			}
		case KindLinterEnd:
			p("  -> %s %s", ev.Linter, ev.Phase)
			if ev.Note != "" {
				p(" (%s)", ev.Note)
			}
			if ev.Err != "" {
				p(": %s", ev.Err)
			}
			p("\n")
		case KindRunEnd:
			p("\nstatus: %s in %dms\n", ev.Status, ev.Ms)
		}
	}
}

func joinSorted(m map[string]string, sep string) string {
	parts := make([]string, 0, len(m))
	for k, v := range m {
		parts = append(parts, k+sep+v)
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

// shellLine shows an argv the way it was typed: `sh -c <script>` collapses to the script itself.
func shellLine(argv []string) string {
	if len(argv) == 3 && argv[0] == "sh" && argv[1] == "-c" {
		return argv[2]
	}
	return strings.Join(argv, " ")
}

func indent(s, prefix string) string {
	if s == "" {
		return ""
	}
	s = strings.TrimSuffix(s, "\n")
	return prefix + strings.ReplaceAll(s, "\n", "\n"+prefix) + "\n"
}

func truncatedMark(truncated bool) string {
	if truncated {
		return " (truncated)"
	}
	return ""
}

func deref(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}
