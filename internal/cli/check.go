package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/xunleii/rtunk/internal/cli/render"
	"github.com/xunleii/rtunk/pkg/trunk/config"
	"github.com/xunleii/rtunk/pkg/trunk/engine"
	"github.com/xunleii/rtunk/pkg/trunk/output"
	"github.com/xunleii/rtunk/pkg/trunk/renovate"
)

// checkCmd is `rtunk check`: ROADMAP.md v0.3, running enabled linters read-only. Bare `rtunk check
// [paths...]` is the default subcommand; listing and enabling linters lives in `rtunk linters`.
type checkCmd struct {
	Run checkRunCmd `cmd:"" default:"withargs" help:"Run enabled checks."`
}

// lintersCmd is `rtunk linters`, symmetric with `rtunk actions {list,enable,disable}`.
type lintersCmd struct {
	List    checkListCmd    `cmd:"" default:"withargs" help:"List all linters available for the current configuration."`
	Enable  checkEnableCmd  `cmd:"" help:"Enable one or more linters."`
	Disable checkDisableCmd `cmd:"" help:"Disable one or more linters."`
}

// checkRunCmd is `rtunk check [paths...]`: given paths, or the whole repository if none.
type checkRunCmd struct {
	Paths      []string `arg:"" optional:"" help:"Paths to check (default: changed files, see --from)."`
	NoProgress bool     `help:"Do not print the per-linter progress lines on stderr."`
	ASCII      bool     `name:"ascii" help:"Use ASCII glyphs in the live view."`
	LiveHeight int      `help:"Maximum height of the live view in lines (default: half the terminal, minimum 3)." env:"RTUNK_LIVE_HEIGHT"`
	Format     string   `enum:"human,sarif,json" default:"human" help:"Output format: human, sarif (for CI) or json."`
	From       string   `help:"Diff base for the default file selection (e.g. origin/main, for CI)."`
	Jobs       int      `short:"j" help:"Number of parallel linter workers (default: number of CPUs)."`
	// FormatBeforeCheck runs every enabled formatter before checking (today's "format, then
	// check" sequence, opt-in instead of implicit -- see AGENTS.md's "Plain check does not run
	// formatters" divergence and inconsistencies.md entry #4).
	FormatBeforeCheck bool   `help:"Run every formatter, then check the reformatted files."`
	Fix               bool   `short:"y" help:"Apply linter fixes (fix commands and finding-level autofixes) to what checking found, then report what remains."`
	VerifyStable      bool   `help:"With --format-before-check, verify the formatting result is stable instead of a single pass."`
	Filter            string `help:"Comma-separated linter id allow-list, or --filter=-id,-id... deny-list (trunk compatibility)."`
	Exclude           string `help:"Comma-separated linter id deny-list; shorthand for an inverse --filter (trunk compatibility)."`
	// NoFix is accepted for trunk compatibility and has no effect: check's default (neither
	// --fix nor --format-before-check) already applies no fix. Note: --fix wins if both are
	// given.
	NoFix bool `short:"n" help:"Accepted for trunk compatibility; has no effect. Note: --fix always wins if both are given."`
	// PrintFailures is accepted for trunk compatibility and has no effect: check already always
	// prints Failed events to stderr unconditionally (the FAILURES section and progress lines, see internal/cli/render).
	PrintFailures bool `help:"Accepted for trunk compatibility; check already always prints failures, this has no effect."`
}

func (c *checkRunCmd) Run(cli *CLI, stdout io.Writer, stderr Stderr, argv Argv) error {
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
	if err := checkDeprecations(cfg, stderr); err != nil {
		return err
	}
	cfg, err = filterLinters(cfg, c.Filter, c.Exclude)
	if err != nil {
		return err
	}
	// configPath is <repoRoot>/.rtunk/rtunk.yaml or <repoRoot>/.trunk/trunk.yaml (findTrunkYAML's
	// only supported layouts) -- repoRoot is two directories up either way.
	repoRoot := filepath.Dir(filepath.Dir(configPath))

	files, err := resolvePaths(repoRoot, c.Paths, c.From)
	if errors.Is(err, errNoFiles) {
		_, _ = fmt.Fprintln(stderr, "rtunk: no files to check")
		return nil
	}
	if err != nil {
		return err
	}

	jobs := c.Jobs
	if jobs <= 0 {
		jobs = runtime.NumCPU()
	}
	env := engine.Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: cli.CacheDir, Concurrency: jobs}

	log := startLog(cli, "check", repoRoot, configPath, argv, jobs, false, stderr)
	runFailed := true // cleared once the run reaches its normal end; an early error return keeps it
	defer func() { log.End(runFailed) }()
	env.Log = log

	// --format-before-check runs every Formatter command first (the exact same selection `rtunk
	// fmt` uses) and lets it finish writing before the checking pass below ever reads the same
	// files -- an issue the formatter genuinely fixed is, by definition, no longer wrong by the
	// time the checking commands run, so it never appears in the findings this command reports. A
	// Failed event in this pass is recorded but does not abort: the checking pass below still
	// runs, matching this project's existing per-linter failure isolation (one linter's Failed
	// event has never stopped its siblings from running).
	var fixFailed error
	var fixChanged, fixSkipped []string
	var fixFailures []render.Failure // machine formats: the document must explain a non-zero exit
	if c.FormatBeforeCheck {
		started := time.Now()
		// Under a machine format the formatter pass has no stdout report of its own (one document
		// per run); its progress still streams and it still counts toward the exit code.
		fixOut := stdout
		if c.Format != "human" {
			fixOut = io.Discard
		}
		fixR := newRenderer("human", fixOut, stderr, render.Fmt, progressOpts{c.NoProgress, c.ASCII, c.LiveHeight})
		onFix := func(ev engine.Event) {
			fixR.Event(ev)
			if ev.Phase == engine.Failed {
				fixFailures = append(fixFailures, render.FailureFrom(ev))
			}
		}
		var ffErr error
		if c.VerifyStable {
			fixChanged, fixSkipped, ffErr = runStableFormat(context.Background(), env, files, onFix)
		} else {
			fixChanged, fixSkipped, ffErr = runFormatOnce(context.Background(), env, files, repoRoot, stderr, onFix)
		}
		if isUnstable(ffErr) {
			fixFailures = append(fixFailures, render.Failure{Linter: "fmt", Err: "did not converge"})
		}
		fixFailed = ffErr
		_ = fixR.Close(render.Summary{Elapsed: time.Since(started), RunLog: log.Name(), Skipped: fixSkipped, Changed: fixChanged, Unstable: isUnstable(ffErr)})
	}

	started := time.Now()
	r := newRenderer(c.Format, stdout, stderr, render.Check, progressOpts{c.NoProgress, c.ASCII, c.LiveHeight})
	checkPredicate := func(cmd config.Command) bool { return !cmd.Formatter && !cmd.InPlace }
	events, err := engine.Run(context.Background(), env, files, checkPredicate)
	if err != nil {
		return err
	}
	// Buffered, not streamed straight to r: under --fix, pass 1 is only used to decide what to
	// fix, never shown as the report -- a fresh pass 2 (below) is the one that actually feeds r,
	// or pass 1 is replayed into r unchanged when there was nothing to fix. Feeding both passes
	// into the same renderer would double-count every finding that survives, and leave stale
	// entries in the report for every finding a fix genuinely resolved.
	raw1 := collectEvents(events)
	findings, _, skipped, failed := drainEvents(cfg, raw1, func(engine.Event) {})
	// A finding's own File is repoRoot-relative (whatever the output parser produced) or already
	// absolute; --fix needs an absolute, in-repo, real file to hand to engine.Run and
	// ApplyInlineFixes (os.ReadFile/WriteFile). validFixTargets both absolutizes and drops
	// anything that isn't a safe fix target (empty/".", outside repoRoot, missing, or a
	// directory) -- it does not remove the finding from the report, only from fix eligibility.
	fixable := validFixTargets(findings, repoRoot)

	// --fix applies every finding's own inline fix, then every enabled fix command (in-place, not
	// a formatter -- ROADMAP.md v0.10 "Fix-only linters actually fix"), to what the checking pass
	// above just found, then re-runs the same checking pass so the report reflects whatever
	// remains -- ROADMAP.md v0.10 "check --fix means linter fixes only". Inline fixes land first:
	// they were computed against pass 1's own file content, so applying them before any fix
	// command rewrites the same file (which would shift or invalidate those byte ranges) keeps
	// them valid. Fix commands are scoped to each finding's own reporting linter -- a linter's fix
	// command has no business rewriting a file only some OTHER linter flagged (e.g. two
	// files:[ALL] linters enabled together must not have one's fix command clobber a file only
	// the other one found something in). Nothing to apply when there are no findings: firing
	// every enabled fix command over every file regardless of findings would defeat "applied to
	// what the checking pass found." A fix command's own Failed event does not abort, matching
	// --format-before-check's own tolerance above, but is still surfaced in the final error/report
	// exactly like --format-before-check's own fixFailed.
	var fixCmdChanged, fixCmdSkipped []string
	var fixCmdFailed error
	var fixCmdFailures []render.Failure
	var inlineFixed []string
	if c.Fix && len(fixable) > 0 {
		inlineFixed, err = engine.ApplyInlineFixes(fixable)
		if err != nil {
			return err
		}
		fixR := newRenderer("human", io.Discard, stderr, render.Fmt, progressOpts{c.NoProgress, c.ASCII, c.LiveHeight})
		onFixCmd := func(ev engine.Event) {
			fixR.Event(ev)
			if ev.Phase == engine.Failed {
				fixCmdFailures = append(fixCmdFailures, render.FailureFrom(ev))
			}
		}
		fixCmdChanged, fixCmdSkipped, fixCmdFailed = runFixCommandsPerLinter(context.Background(), cfg, env, fixable, onFixCmd)
		_ = fixR.Close(render.Summary{})
	}
	if c.Fix && len(findings) > 0 {
		events2, err := engine.Run(context.Background(), env, files, checkPredicate)
		if err != nil {
			return err
		}
		findings, _, skipped, failed = drainEvents(cfg, collectEvents(events2), r.Event)
	} else {
		// raw1 was only drained above through a no-op printFn (line ~159), so it still carries
		// every suppressed linter's findings -- filter it the same way drainEvents itself does
		// before replaying it into the real renderer, or a superseded linter's findings would
		// reappear in the report here even though drainEvents already excluded them from findings.
		replayEvents(suppressUpstreamEvents(cfg, raw1), r.Event)
	}
	runFailed = failed != nil || runFailedBy(fixFailed) || runFailedBy(fixCmdFailed)

	sum := render.Summary{Elapsed: time.Since(started), RunLog: log.Name(), Skipped: skipped}
	if c.Format != "human" { // the machine document carries every writing pass too
		sum.Changed = mergeSortedUnique(fixChanged, mergeSortedUnique(fixCmdChanged, inlineFixed))
		sum.Skipped = mergeSortedUnique(sum.Skipped, mergeSortedUnique(fixSkipped, fixCmdSkipped))
		sum.Failures = mergeFailures(fixFailures, fixCmdFailures)
	}
	_ = r.Close(sum)
	if fixFailed != nil {
		return fixFailed
	}
	if fixCmdFailed != nil {
		return fixCmdFailed
	}
	if failed != nil {
		return errors.New("check: a linter failed to run")
	}
	if len(findings) > 0 {
		return fmt.Errorf("rtunk: check found %d issue(s)", len(findings))
	}
	return nil
}

// mergeFailures concatenates two Failure lists -- unlike file lists, failures are never
// deduplicated (two distinct passes failing for the same linter are two distinct facts to report).
func mergeFailures(a, b []render.Failure) []render.Failure {
	if len(b) == 0 {
		return a
	}
	return append(append([]render.Failure{}, a...), b...)
}

// collectEvents drains events into a slice, for a pass whose outcome isn't known to be the final
// report until after it's fully run (see check --fix's pass 1).
func collectEvents(events <-chan engine.Event) []engine.Event {
	var out []engine.Event
	for ev := range events {
		out = append(out, ev)
	}
	return out
}

// replayEvents re-delivers a buffered pass's events to printFn -- used when that pass turns out
// to be the final report after all (check --fix found nothing to fix, or --fix wasn't given).
func replayEvents(events []engine.Event, printFn func(engine.Event)) {
	for _, ev := range events {
		printFn(ev)
	}
}

// validFixTargets returns the subset of findings whose File is a safe, real fix target:
// non-empty, not a bare ".", resolves (once made repoRoot-absolute) to a path inside repoRoot, and
// names an existing regular file, not a directory. A finding failing any of these is not itself
// dropped from the report -- only excluded from fix eligibility, so it simply remains reported as
// unresolved. Every File is also normalized to its repoRoot-absolute form on the way out.
func validFixTargets(findings []output.Finding, repoRoot string) []output.Finding {
	out := make([]output.Finding, 0, len(findings))
	for _, f := range findings {
		path := f.File
		if path == "" || path == "." {
			continue
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(repoRoot, path)
		}
		if rel, err := filepath.Rel(repoRoot, path); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue // outside repoRoot -- never a fix target, however a tool reported it
		}
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue // missing (stale) or a directory -- never a fixable file
		}
		f.File = path
		out = append(out, f)
	}
	return out
}

// runFixCommandsPerLinter runs every enabled fix command (in-place, not a formatter) over the
// files each reporting linter itself flagged -- never over a file only some OTHER linter found
// something in, even when both are enabled with the same broad files: scope. Reuses engine.Run's
// existing InPlace change-detection (already keyed on InPlace alone, not Formatter) once per
// linter; a fix command's own Failed event is recorded (via printFn) but does not itself abort the
// loop, matching --format-before-check's own pre-existing tolerance of a Failed formatter.
func runFixCommandsPerLinter(ctx context.Context, cfg config.Config, env engine.Env, findings []output.Finding, printFn func(engine.Event)) (changed, skipped []string, failed error) {
	filesByLinter := map[string][]string{}
	var linterIDs []string
	for _, f := range findings {
		if _, ok := filesByLinter[f.Linter]; !ok {
			linterIDs = append(linterIDs, f.Linter)
		}
		filesByLinter[f.Linter] = append(filesByLinter[f.Linter], f.File)
	}
	sort.Strings(linterIDs)

	fixPredicate := func(cmd config.Command) bool { return cmd.InPlace && !cmd.Formatter }
	for _, id := range linterIDs {
		def, ok := cfg.Lint.Definitions[id]
		if !ok {
			continue
		}
		subEnv := env
		subEnv.Cfg.Lint.Definitions = map[string]config.Linter{id: def}
		files := mergeSortedUnique(nil, filesByLinter[id])
		sort.Strings(files)
		events, err := engine.Run(ctx, subEnv, files, fixPredicate)
		if err != nil {
			return changed, skipped, err
		}
		_, c, s, f := drainEvents(cfg, collectEvents(events), printFn)
		changed = mergeSortedUnique(changed, c)
		skipped = mergeSortedUnique(skipped, s)
		if f != nil {
			failed = f
		}
	}
	return changed, skipped, failed
}

// drainRunEvents streams every event from events through printFn as it arrives and accumulates
// its terminal Done/Skipped/Failed outcomes -- shared by check's own reporting pass and check
// --fix's earlier formatter pass, which differ only in which of findings/changed the caller goes
// on to use (the other is simply empty: a Formatter command has no Findings to report, and a
// non-Formatter one has no ChangedFiles).
//
// changed is deduplicated here, in the aggregator, before it's returned: Event.ChangedFiles is
// only deduplicated within a single linter's own Done event, so two different linters (e.g.
// prettier and markdownlint both formatting the same .md file -- a real, reachable case in this
// repo's own trunk.yaml) can each report the same path changed. Deduping at the point where
// changed is assembled, rather than in whichever printer happens to consume it, means every
// future consumer of this return value (a --json mode, an exit-code counter, a future fmt
// --check) inherits the guarantee instead of having to re-implement it.
func drainRunEvents(cfg config.Config, printFn func(engine.Event), events <-chan engine.Event) (findings []output.Finding, changed []string, skipped []string, failed error) {
	return drainEvents(cfg, collectEvents(events), printFn)
}

// drainEvents is drainRunEvents over an already-collected slice instead of a live channel -- for
// a pass buffered via collectEvents because whether it's the one to print (via printFn) isn't
// known until after it's fully drained (see check --fix's pass 1).
//
// events is filtered via suppressUpstreamEvents BEFORE printFn ever sees any of it: printFn is
// what actually renders the human/json/sarif report (internal/cli/render/base.go reads
// ev.Findings straight off each Done event as it's printed), so filtering only the returned
// findings slice -- as this used to do -- left a suppressed linter's findings out of the count
// but still visible in the printed report. Filtering events up front means printFn and the
// accumulation below agree by construction.
func drainEvents(cfg config.Config, events []engine.Event, printFn func(engine.Event)) (findings []output.Finding, changed []string, skipped []string, failed error) {
	events = suppressUpstreamEvents(cfg, events)

	skippedLinters := map[string]bool{}
	seenChanged := map[string]bool{}
	for _, ev := range events {
		printFn(ev)
		switch ev.Phase {
		case engine.Done:
			findings = append(findings, ev.Findings...)
			for _, f := range ev.ChangedFiles {
				if !seenChanged[f] {
					seenChanged[f] = true
					changed = append(changed, f)
				}
			}
		case engine.Skipped:
			// Dedupe by linter: a linter with several unsupported commands emits one Skipped
			// event per command, but the report should name it once, not once per command.
			if !skippedLinters[ev.Linter] {
				skippedLinters[ev.Linter] = true
				skipped = append(skipped, fmt.Sprintf("%s [%s]", ev.Linter, ev.Note))
			}
		case engine.Failed:
			if failed == nil {
				failed = ev.Err
			}
		}
	}
	return findings, changed, skipped, failed
}

// suppressUpstreamEvents returns a copy of events with every Done event's Findings stripped of
// any finding from a linter another enabled linter's own Command declares as DisableUpstream --
// but only once the superseding linter has itself produced at least one finding across the whole
// batch (see supersededLinters), matching drainEvents' pre-fix behavior of suppressing whole
// linters, not fine-grained per-finding matching (see docs/architecture/inconsistencies.md for
// why). Every other event field, and every non-Done event, passes through unchanged. events'
// backing array is never mutated -- a fresh output slice (and, for any Done event whose Findings
// actually changed, a fresh Findings slice) is built instead, since events may be a buffered pass
// check --fix still needs unfiltered elsewhere (raw1's own findings-only use).
func suppressUpstreamEvents(cfg config.Config, events []engine.Event) []engine.Event {
	suppress := supersededLinters(cfg, events)
	if len(suppress) == 0 {
		return events
	}

	out := make([]engine.Event, len(events))
	for i, ev := range events {
		if ev.Phase != engine.Done || len(ev.Findings) == 0 {
			out[i] = ev
			continue
		}
		findings := make([]output.Finding, 0, len(ev.Findings))
		for _, f := range ev.Findings {
			if !suppress[f.Linter] {
				findings = append(findings, f)
			}
		}
		ev.Findings = findings
		out[i] = ev
	}
	return out
}

// supersededLinters scans every Done event's Findings once to find which linter ids another
// enabled linter's own Command supersedes via DisableUpstream -- shared by suppressUpstreamEvents
// so the produced/suppress map-building logic exists in exactly one place. A linter only counts
// as "produced" (and so only suppresses its own DisableUpstream target) once it has actually
// reported at least one finding; being merely enabled isn't enough -- a "combined" linter that ran
// clean this time must not silently hide a real "narrow" finding.
func supersededLinters(cfg config.Config, events []engine.Event) map[string]bool {
	produced := map[string]bool{}
	for _, ev := range events {
		if ev.Phase != engine.Done {
			continue
		}
		for _, f := range ev.Findings {
			produced[f.Linter] = true
		}
	}

	suppress := map[string]bool{}
	for id, l := range cfg.Lint.Definitions {
		if !produced[id] {
			continue // this superseding linter found nothing; don't suppress on its behalf
		}
		for _, cmd := range l.Commands {
			for _, upstream := range cmd.DisableUpstream {
				if produced[upstream] {
					suppress[upstream] = true
				}
			}
		}
	}
	return suppress
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
