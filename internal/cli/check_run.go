package cli

import (
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

	"github.com/xunleii/rtunk/internal/cli/render"
	"github.com/xunleii/rtunk/pkg/run/engine"
	"github.com/xunleii/rtunk/pkg/trunk/config"
	"github.com/xunleii/rtunk/pkg/trunk/output"
)

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
	SecurityOnly      bool   `help:"Run only commands tagged is_security: true, skipping every other check."`
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
		found, err := findConfig()
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
	// configPath is <repoRoot>/.rtunk/rtunk.yaml or <repoRoot>/.trunk/trunk.yaml (findConfig's
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
	checkPredicate := func(cmd config.Command) bool {
		return !cmd.Formatter && !cmd.InPlace && (!c.SecurityOnly || cmd.IsSecurity)
	}
	events, err := engine.Run(context.Background(), env, files, checkPredicate)
	if err != nil {
		return err
	}
	// Only --fix needs pass 1 buffered instead of streamed straight to r: it's only used to decide
	// what to fix, never itself shown as the report -- a fresh pass 2 (below) is the one that
	// actually feeds r, or pass 1 is replayed into r unchanged when there was nothing to fix.
	// Feeding both passes into the same renderer would double-count every finding that survives,
	// and leave stale entries in the report for every finding a fix genuinely resolved. Without
	// --fix there's only ever one pass, so it streams straight to r as it happens -- this is what
	// lets the live view (and the plain per-linter progress lines) actually show anything instead
	// of a silent wait followed by the whole report appearing at once.
	var raw1 []engine.Event
	var findings []output.Finding
	var skipped []string
	var failed error
	if c.Fix {
		raw1 = collectEvents(events)
		findings, _, skipped, failed = drainEvents(raw1, func(engine.Event) {})
	} else {
		findings, _, skipped, failed = drainRunEvents(r.Event, events)
	}
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
		findings, _, skipped, failed = drainEvents(collectEvents(events2), r.Event)
	} else if c.Fix {
		// --fix was requested but pass 1 (buffered above) found nothing to fix -- it's the report.
		replayEvents(raw1, r.Event)
	}
	// Without --fix, pass 1 already streamed straight to r above; nothing left to replay here.
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
		_, c, s, f := drainEvents(collectEvents(events), printFn)
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
func drainRunEvents(printFn func(engine.Event), events <-chan engine.Event) (findings []output.Finding, changed []string, skipped []string, failed error) {
	a := newEventAccumulator()
	for ev := range events {
		printFn(ev)
		a.add(ev)
	}
	return a.findings, a.changed, a.skipped, a.failed
}

// drainEvents is drainRunEvents over an already-collected slice instead of a live channel -- for
// a pass buffered via collectEvents because whether it's the one to print (via printFn) isn't
// known until after it's fully drained (see check --fix's pass 1).
func drainEvents(events []engine.Event, printFn func(engine.Event)) (findings []output.Finding, changed []string, skipped []string, failed error) {
	a := newEventAccumulator()
	for _, ev := range events {
		printFn(ev)
		a.add(ev)
	}
	return a.findings, a.changed, a.skipped, a.failed
}

// eventAccumulator is drainEvents/drainRunEvents' shared per-event bookkeeping, factored out so
// drainRunEvents can call printFn on each event AS IT ARRIVES off the live channel (restoring
// real-time progress/live-view feedback for check's own non-fix pass) while drainEvents keeps
// operating on an already-collected slice for --fix's own buffer-then-decide pass 1, without
// duplicating the accumulation logic between the two.
type eventAccumulator struct {
	findings       []output.Finding
	changed        []string
	seenChanged    map[string]bool
	skipped        []string
	skippedLinters map[string]bool
	failed         error
}

func newEventAccumulator() *eventAccumulator {
	return &eventAccumulator{seenChanged: map[string]bool{}, skippedLinters: map[string]bool{}}
}

func (a *eventAccumulator) add(ev engine.Event) {
	switch ev.Phase {
	case engine.Done:
		a.findings = append(a.findings, ev.Findings...)
		for _, f := range ev.ChangedFiles {
			if !a.seenChanged[f] {
				a.seenChanged[f] = true
				a.changed = append(a.changed, f)
			}
		}
	case engine.Skipped:
		// Dedupe by linter: a linter with several unsupported commands emits one Skipped
		// event per command, but the report should name it once, not once per command.
		if !a.skippedLinters[ev.Linter] {
			a.skippedLinters[ev.Linter] = true
			a.skipped = append(a.skipped, fmt.Sprintf("%s [%s]", ev.Linter, ev.Note))
		}
	case engine.Failed:
		if a.failed == nil {
			a.failed = ev.Err
		}
	}
}
