package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/xunleii/rtunk/pkg/run/engine"
	"github.com/xunleii/rtunk/pkg/trunk/config"
)

// collectChangedByLinter drains events like drainRunEvents, but preserves per-linter attribution
// -- map[linterName][]repoRoot-relative changed file -- the information a flat, deduplicated
// aggregate (drainRunEvents' own ChangedFiles) discards, needed here to name which linters are
// responsible when a file doesn't stabilize. Used only for runStableFormat's two real (writing)
// rounds, where attribution is meaningful; its two dry-run rounds use drainRunEvents' own flat
// dedup instead, since they only need "is this file still unstable at all." Skipped events are
// tracked the same way drainRunEvents tracks them (deduped by linter), so a real round that skips
// a linter still surfaces in the final report even when nothing else changed.
func collectChangedByLinter(printFn func(engine.Event), events <-chan engine.Event) (changed map[string][]string, skipped []string, failed error) {
	changed = map[string][]string{}
	skippedLinters := map[string]bool{}
	for ev := range events {
		printFn(ev)
		switch ev.Phase {
		case engine.Done:
			if len(ev.ChangedFiles) > 0 {
				changed[ev.Linter] = ev.ChangedFiles
			}
		case engine.Skipped:
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
	return changed, skipped, failed
}

// mergeSortedUnique merges a and b into one slice with duplicates removed -- used to combine
// skipped-linter reports across runStableFormat's multiple rounds, since the same always-skipped
// linter would otherwise be reported once per round it appears in.
func mergeSortedUnique(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range a {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, s := range b {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// dedupeSortedFiles flattens a map[linter][]file's values into one deduplicated, sorted []string
// -- used to turn collectChangedByLinter's per-linter attribution into the same flat shape
// render.Summary.Changed expects.
func dedupeSortedFiles(byLinter map[string][]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, files := range byLinter {
		for _, f := range files {
			if !seen[f] {
				seen[f] = true
				out = append(out, f)
			}
		}
	}
	sort.Strings(out)
	return out
}

// mergeChangedByLinter merges b's entries into a fresh map combining both -- used to report the
// full set of files touched across both of runStableFormat's real rounds, not just the second.
func mergeChangedByLinter(a, b map[string][]string) map[string][]string {
	out := make(map[string][]string, len(a)+len(b))
	for linter, files := range a {
		out[linter] = append(out[linter], files...)
	}
	for linter, files := range b {
		out[linter] = append(out[linter], files...)
	}
	return out
}

// unstableError builds the final error when fmt fails to converge: for each file still unstable
// after both real rounds, names every linter whose own round-1 or round-2 ChangedFiles included
// it -- the "suspects," without attempting to determine which one is actually at fault (either
// could be the one undoing the other's fix; both are equally implicated). 0 or 1 suspects can
// legitimately happen -- the dry-run sandbox is staged inside repoRoot precisely so a genuine
// ancestor-directory config walk (e.g. real prettier's own algorithm) still finds repoRoot-level
// config, but a formatter reading config from some directory strictly between its own target and
// repoRoot is still invisible to the sandbox (see engine.go's own "Known limitation" comment) --
// that narrower residual case can still make round 2's dry-run check disagree with what any real
// round actually wrote, so it gets its own honest wording instead of a misleading or empty
// "conflicting" label.
func unstableError(stillUnstable []string, round1, round2 map[string][]string) error {
	var b strings.Builder
	_, _ = fmt.Fprintln(&b, "fmt did not converge after 2 attempts. Still unstable:")
	for _, f := range stillUnstable {
		var suspects []string
		for linter, files := range round1 {
			if slices.Contains(files, f) {
				suspects = append(suspects, linter)
			}
		}
		for linter, files := range round2 {
			if slices.Contains(files, f) && !slices.Contains(suspects, linter) {
				suspects = append(suspects, linter)
			}
		}
		sort.Strings(suspects)
		switch len(suspects) {
		case 0:
			_, _ = fmt.Fprintf(&b, "  %s (no formatter reported changing this file in either real round -- likely a dry-run/real mismatch, e.g. a formatter reading config the dry-run sandbox couldn't see)\n", f)
		case 1:
			_, _ = fmt.Fprintf(&b, "  %s (only %s reported changing this file -- likely a dry-run/real mismatch, e.g. it reads config the dry-run sandbox couldn't see)\n", f, suspects[0])
		default:
			_, _ = fmt.Fprintf(&b, "  %s (conflicting: %s)\n", f, strings.Join(suspects, ", "))
		}
	}
	return unstableFormatError(strings.TrimRight(b.String(), "\n"))
}

// unstableFormatError is the "did not converge" verdict: a result about the formatters, like
// `fmt --check` finding files to reformat, not a run that failed to run.
type unstableFormatError string

func (e unstableFormatError) Error() string { return string(e) }

// isUnstable reports whether err is the "did not converge" verdict.
func isUnstable(err error) bool {
	var u unstableFormatError
	return errors.As(err, &u)
}

// runFailedBy reports whether err means the run itself failed (for the run log's verdict): any
// non-nil error except an unstable-format verdict.
func runFailedBy(err error) bool {
	var u unstableFormatError
	return err != nil && !errors.As(err, &u)
}

// runStableFormat runs env's Formatter commands with a stability check, matching real trunk's own
// fmt safety net: write, then dry-run-check for a residual diff; if one exists, write once more
// and check again; if a diff still exists after that, the result is unstable -- return an error
// naming which linters are responsible, rather than silently leaving oscillating output on disk.
// A round whose real pass changes nothing skips its own dry-run check entirely (nothing was
// written, so nothing needs verifying). skipped accumulates across every round this function runs
// (real and dry-run alike), deduplicated, so an always-skipped linter is still reported even when
// the "nothing written" early return means no dry-run round ever runs. Callers (fmtCmd.Run's
// non-Check branch, checkRunCmd.Run's --fix branch) pass the SAME env they'd otherwise pass to a
// single engine.Run call.
func runStableFormat(ctx context.Context, env engine.Env, paths []string, onEvent func(engine.Event)) (changed []string, skipped []string, err error) {
	// runStableFormat always starts with a real (writing) round -- reset any DryRun the caller's
	// env might carry so this can never accidentally sandbox what's supposed to be a real write;
	// its own two dry-run rounds set DryRun on a local copy (checkEnv) instead.
	env.DryRun = false
	formatterPredicate := func(cmd config.Command) bool { return cmd.Formatter }

	round1Events, err := engine.Run(ctx, env, paths, formatterPredicate)
	if err != nil {
		return nil, nil, err
	}
	round1, skipped1, failed1 := collectChangedByLinter(onEvent, round1Events)
	skipped = skipped1
	changed = dedupeSortedFiles(round1)
	if failed1 != nil {
		return changed, skipped, failed1
	}
	if len(changed) == 0 {
		return changed, skipped, nil // nothing written, nothing to verify
	}

	checkEnv := env
	checkEnv.DryRun = true
	check1Events, err := engine.Run(ctx, checkEnv, paths, formatterPredicate)
	if err != nil {
		return changed, skipped, err
	}
	_, wouldChange1, checkSkipped1, checkFailed1 := drainRunEvents(onEvent, check1Events)
	_ = checkFailed1 // a dry-run check's own failure doesn't abort the loop (fail-open: the real
	// write from the preceding round already succeeded) -- the renderer above
	// already surfaced it to the user via the Failed event's own progress line.
	skipped = mergeSortedUnique(skipped, checkSkipped1)
	if len(wouldChange1) == 0 {
		return changed, skipped, nil // stable after 1 round
	}

	round2Events, err := engine.Run(ctx, env, paths, formatterPredicate)
	if err != nil {
		return changed, skipped, err
	}
	round2, skipped2, failed2 := collectChangedByLinter(onEvent, round2Events)
	skipped = mergeSortedUnique(skipped, skipped2)
	changed = dedupeSortedFiles(mergeChangedByLinter(round1, round2))
	if failed2 != nil {
		return changed, skipped, failed2
	}

	check2Events, err := engine.Run(ctx, checkEnv, paths, formatterPredicate)
	if err != nil {
		return changed, skipped, err
	}
	_, wouldChange2, checkSkipped2, checkFailed2 := drainRunEvents(onEvent, check2Events)
	_ = checkFailed2 // same fail-open rationale as checkFailed1 above.
	skipped = mergeSortedUnique(skipped, checkSkipped2)
	if len(wouldChange2) == 0 {
		return changed, skipped, nil // stable after 2 rounds
	}

	return changed, skipped, unstableError(wouldChange2, round1, round2)
}

// recentRunWindow bounds how long a previous run stays "recent" for runFormatOnce's own
// instability heuristic below.
const recentRunWindow = 30 * time.Second

// runFormatOnce is plain fmt/check --fix's default (no --verify-stable): a single real (writing)
// round, no dry-run re-check -- cheap, one pass, matching the command's pre-fmt-stability cost.
// In place of --verify-stable's own immediate double-check, it compares this round's own changed
// files against whatever the previous run for this repo (if any, and if recent) touched, and warns
// on stderr about any overlap -- the same "is this actually converging" question, answered cheaply
// across two separate invocations instead of rigorously within one.
func runFormatOnce(ctx context.Context, env engine.Env, paths []string, repoRoot string, stderr io.Writer, onEvent func(engine.Event)) (changed []string, skipped []string, err error) {
	env.DryRun = false
	formatterPredicate := func(cmd config.Command) bool { return cmd.Formatter }

	events, err := engine.Run(ctx, env, paths, formatterPredicate)
	if err != nil {
		return nil, nil, err
	}
	byLinter, skipped, failed := collectChangedByLinter(onEvent, events)
	changed = dedupeSortedFiles(byLinter)

	warnIfRecentOverlap(env.CacheDir, repoRoot, byLinter, stderr)
	if saveErr := saveRecentFmtRun(env.CacheDir, repoRoot, recentFmtRun{Timestamp: time.Now(), Changed: byLinter}); saveErr != nil {
		_, _ = fmt.Fprintf(stderr, "fmt: failed to record this run for future instability checks: %v\n", saveErr)
	}

	return changed, skipped, failed
}

// warnIfRecentOverlap loads repoRoot's last recorded run and, if it's within recentRunWindow,
// warns on stderr about every file byLinter (this run's own changes) shares with it -- the same
// file being reformatted again within seconds of the previous run suggests either that run's fix
// didn't actually stick, or two formatters are fighting over it. Best-effort: a load failure is
// silently ignored (the same fail-open reasoning runStableFormat's dry-run checks already use --
// this is a diagnostic, not part of fmt's own success/failure contract).
func warnIfRecentOverlap(cacheDir, repoRoot string, byLinter map[string][]string, stderr io.Writer) {
	prev, ok, err := loadRecentFmtRun(cacheDir, repoRoot)
	if err != nil || !ok {
		return
	}
	age := time.Since(prev.Timestamp)
	if age > recentRunWindow {
		return
	}

	for _, f := range dedupeSortedFiles(byLinter) {
		prevLinters := lintersFor(prev.Changed, f)
		if len(prevLinters) == 0 {
			continue
		}
		thisLinters := lintersFor(byLinter, f)
		_, _ = fmt.Fprintf(stderr, "warning: %s was reformatted again %s after a previous run (then: %s; now: %s) -- possible formatter instability, rerun with --verify-stable to check\n",
			f, age.Round(time.Second), strings.Join(prevLinters, ", "), strings.Join(thisLinters, ", "))
	}
}

// lintersFor returns the sorted names of every linter byLinter records as having changed file.
func lintersFor(byLinter map[string][]string, file string) []string {
	var out []string
	for linter, files := range byLinter {
		if slices.Contains(files, file) {
			out = append(out, linter)
		}
	}
	sort.Strings(out)
	return out
}
