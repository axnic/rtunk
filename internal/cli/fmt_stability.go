package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/xunleii/rtunk/pkg/trunk/config"
	"github.com/xunleii/rtunk/pkg/trunk/engine"
)

// collectChangedByLinter drains events like drainRunEvents, but preserves per-linter attribution
// -- map[linterName][]repoRoot-relative changed file -- the information a flat, deduplicated
// aggregate (drainRunEvents' own ChangedFiles) discards, needed here to name which linters are
// responsible when a file doesn't stabilize. Used only for runStableFormat's two real (writing)
// rounds, where attribution is meaningful; its two dry-run rounds use drainRunEvents' own flat
// dedup instead, since they only need "is this file still unstable at all."
func collectChangedByLinter(printFn func(engine.Event), events <-chan engine.Event) (changed map[string][]string, failed error) {
	changed = map[string][]string{}
	for ev := range events {
		printFn(ev)
		switch ev.Phase {
		case engine.Done:
			if len(ev.ChangedFiles) > 0 {
				changed[ev.Linter] = ev.ChangedFiles
			}
		case engine.Failed:
			if failed == nil {
				failed = ev.Err
			}
		}
	}
	return changed, failed
}

// dedupeSortedFiles flattens a map[linter][]file's values into one deduplicated, sorted []string
// -- used to turn collectChangedByLinter's per-linter attribution into the same flat shape
// printFmtReport already expects.
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

// containsString reports whether ss contains s.
func containsString(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

// unstableError builds the final error when fmt fails to converge: for each file still unstable
// after both real rounds, names every linter whose own round-1 or round-2 ChangedFiles included
// it -- the "suspects," without attempting to determine which one is actually at fault (either
// could be the one undoing the other's fix; both are equally implicated).
func unstableError(stillUnstable []string, round1, round2 map[string][]string) error {
	var b strings.Builder
	fmt.Fprintln(&b, "fmt did not converge after 2 attempts. Still unstable:")
	for _, f := range stillUnstable {
		var suspects []string
		for linter, files := range round1 {
			if containsString(files, f) {
				suspects = append(suspects, linter)
			}
		}
		for linter, files := range round2 {
			if containsString(files, f) && !containsString(suspects, linter) {
				suspects = append(suspects, linter)
			}
		}
		sort.Strings(suspects)
		fmt.Fprintf(&b, "  %s (conflicting: %s)\n", f, strings.Join(suspects, ", "))
	}
	return errors.New(strings.TrimRight(b.String(), "\n"))
}

// runStableFormat runs env's Formatter commands with a stability check, matching real trunk's own
// fmt safety net: write, then dry-run-check for a residual diff; if one exists, write once more
// and check again; if a diff still exists after that, the result is unstable -- return an error
// naming which linters are responsible, rather than silently leaving oscillating output on disk.
// A round whose real pass changes nothing skips its own dry-run check entirely (nothing was
// written, so nothing needs verifying). env.DryRun is ignored on the way in -- this function
// always starts with a real (writing) round; it derives its own dry-run passes internally. Callers
// (fmtCmd.Run's non-Check branch, checkRunCmd.Run's --fix branch) pass the SAME env they'd
// otherwise pass to a single engine.Run call.
func runStableFormat(ctx context.Context, env engine.Env, paths []string, stderr io.Writer) (changed []string, skipped []string, err error) {
	formatterPredicate := func(cmd config.Command) bool { return cmd.Formatter }

	round1Events, err := engine.Run(ctx, env, paths, formatterPredicate)
	if err != nil {
		return nil, nil, err
	}
	round1, failed1 := collectChangedByLinter(func(ev engine.Event) { printFmtEvent(stderr, ev) }, round1Events)
	changed = dedupeSortedFiles(round1)
	if failed1 != nil {
		return changed, nil, failed1
	}
	if len(changed) == 0 {
		return changed, nil, nil // nothing written, nothing to verify
	}

	checkEnv := env
	checkEnv.DryRun = true
	check1Events, err := engine.Run(ctx, checkEnv, paths, formatterPredicate)
	if err != nil {
		return changed, nil, err
	}
	_, wouldChange1, skipped1, _ := drainRunEvents(func(ev engine.Event) {}, check1Events)
	if len(wouldChange1) == 0 {
		return changed, skipped1, nil // stable after 1 round
	}

	round2Events, err := engine.Run(ctx, env, paths, formatterPredicate)
	if err != nil {
		return changed, nil, err
	}
	round2, failed2 := collectChangedByLinter(func(ev engine.Event) { printFmtEvent(stderr, ev) }, round2Events)
	changed = dedupeSortedFiles(mergeChangedByLinter(round1, round2))
	if failed2 != nil {
		return changed, nil, failed2
	}

	check2Events, err := engine.Run(ctx, checkEnv, paths, formatterPredicate)
	if err != nil {
		return changed, nil, err
	}
	_, wouldChange2, skipped2, _ := drainRunEvents(func(ev engine.Event) {}, check2Events)
	if len(wouldChange2) == 0 {
		return changed, skipped2, nil // stable after 2 rounds
	}

	return changed, skipped2, unstableError(wouldChange2, round1, round2)
}
