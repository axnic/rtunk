# fmt Stability Verification Design Spec

## Goal

Match real trunk's own `fmt` safety net: after writing formatter changes, verify the result is
actually stable (running the same formatters again wouldn't change anything further). Two
formatters that keep undoing each other's work (e.g. two competing style rules on the same file
type) currently leave `rtunk fmt` reporting silent success while the file oscillates forever on
every subsequent run. This adds:

- `rtunk fmt --check`: a standalone dry-run mode (never writes to disk) reporting which files
  would be reformatted -- a CI gate, matching real trunk's own `trunk fmt --check`.
- An automatic stability loop inside plain `rtunk fmt` (no flag needed) and `rtunk check --fix`'s
  own fix pass: write, dry-run-check for a residual diff, and if one exists, write once more and
  check again. If a diff still exists after that, report exactly which linters are in conflict
  over which files, as an error, rather than silently leaving unstable output on disk.

This was explicitly deferred as a Non-goal in the `fmt-autofix` design spec ("No `rtunk fmt
--check` (dry-run) in this feature... out of scope"); this spec is that deferred piece, now needed
as real infrastructure for the stability loop itself, not built as a separate later feature.

## The dry-run mechanism: reuse `security.StageSandbox`, don't build a new one

`security.StageSandbox` already copies a command's target files into a fresh temp directory,
before-and-after which `engine.go` already knows how to run a command and hash-compare files
against (this exact mechanism is what real check-time `SandboxType` sandboxing, and this
project's own `check-engine-refactor`/`fmt-autofix` branches, already built and tested). A dry run
is simply: stage a `copy_targets` sandbox for the `InPlace` command's own batch, run it against the
copy, hash-compare the copy before/after, discard the sandbox, and never touch the real file.

This is deliberately independent of `Command.SandboxType` (the field a real catalog command sets
for its own linting sandboxing needs) -- `fmt-autofix`'s own final review already established no
real catalog command combines `InPlace` with a declared `SandboxType` (a sandboxed write would be
silently lost), so `SandboxType` is always empty in practice for the `InPlace` commands this
feature drives through a dry run. The dry-run mechanism forces `copy_targets` staging itself,
engine-side, regardless of what `SandboxType` (if anything) the command happens to declare.

### `engine.Env.DryRun` and `job.dryRun`

```go
type Env struct {
	Cfg         config.Config
	RepoRoot    string
	CacheDir    string
	Concurrency int
	DryRun      bool // when true, InPlace commands run against a throwaway sandbox copy of their
	                 // own targets instead of the real files -- ChangedFiles still reports what
	                 // WOULD change, but nothing on disk is ever modified.
}
```

`job` gains a `dryRun bool` field, set uniformly for every job `buildJobs` constructs (from
`env.DryRun` -- a run-level setting, not a per-command one). In `runBatch`, the existing
sandbox-staging block:

```go
workDir := j.resolvedDir
if j.cmd.SandboxType != "" {
	sandboxDir, cleanup, err := security.StageSandbox(j.cmd.SandboxType, j.resolvedDir, j.batch)
	...
	workDir = sandboxDir
}
```

becomes:

```go
workDir := j.resolvedDir
sandboxType := j.cmd.SandboxType
if j.dryRun && j.cmd.InPlace {
	sandboxType = "copy_targets"
}
if sandboxType != "" {
	sandboxDir, cleanup, err := security.StageSandbox(sandboxType, j.resolvedDir, j.batch)
	...
	workDir = sandboxDir
}
```

Everything downstream (`beforeHashes`/`afterHashes`, `ChangedFiles`, the `workDir != j.resolvedDir`
absolute-path finding remap, `inPlaceMu`'s serialization) is already exactly the mechanism
`fmt-autofix` built and tested -- it works unmodified once `workDir` is a sandbox instead of the
real directory. `inPlaceMu` stays unconditional (locked whenever `cmd.InPlace`, dry run or not):
each dry run stages its own fresh temp directory via `os.MkdirTemp`, so concurrent dry runs can't
collide with each other the way concurrent real writes to the same real path could -- but keeping
the lock unconditional is simpler and costs nothing extra a dry run would otherwise need anyway.

`buildJobs`'s existing `InPlace`+`SandboxType` skip check is unaffected: it only ever fires on a
command's own declared `SandboxType`, which the real catalog never sets on an `InPlace` command
regardless of `DryRun`.

## The stability loop: a new `internal/cli/fmt_stability.go`

A new file, not folded into the already-substantial `check.go`, holding the orchestration logic
shared by plain `rtunk fmt` and `check --fix`'s own fix pass.

### Per-linter attribution: `collectChangedByLinter`

`drainRunEvents` (from `fmt-autofix`) flattens and deduplicates `ChangedFiles` across every
linter -- exactly right for reporting "what changed", but it discards _which_ linter changed _which_
file, which is precisely the information needed to name conflicting linters. A new, narrower
helper:

```go
// collectChangedByLinter drains events like drainRunEvents, but preserves per-linter attribution
// -- map[linterName][]repoRoot-relative changed file -- the information a flat, deduplicated
// aggregate (drainRunEvents' own ChangedFiles) discards, needed here to name which linters are
// responsible when a file doesn't stabilize.
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
```

Used only for the loop's two _real_ (writing) rounds, since attribution is only meaningful there.
The two _dry-run_ rounds only need "is this file still unstable at all" -- `drainRunEvents`'s
existing flat, deduplicated `changed []string` is exactly right for that, unchanged.

### `runStableFormat`

```go
// runStableFormat runs env's Formatter commands with a stability check, matching real trunk's own
// fmt safety net: write, then dry-run-check for a residual diff; if one exists, write once more
// and check again; if a diff still exists after that, the result is unstable -- return an error
// naming which linters are responsible, rather than silently leaving oscillating output on disk.
// A round whose real pass changes nothing skips its own dry-run check entirely (nothing was
// written, so nothing needs verifying). Env.DryRun on env itself is ignored; this function always
// starts with a real (writing) round -- pass DryRun: true env only to the standalone --check path
// (fmtCmd.Run's own single-pass branch), never to this function.
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
	_, wouldChange1, skipped1, _ := drainRunEvents(func(ev engine.Event) {}, check1Events) // silent: this is an internal verification pass, not user-visible progress
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
```

(`dedupeSortedFiles`/`mergeChangedByLinter`/`containsString` are small local helpers in the new
file -- flatten+dedupe+sort a `map[string][]string`'s values into one `[]string`, merge two such
maps' slices per key, and a plain linear string-slice membership check, respectively.)

### Conflict attribution: `unstableError`

For each file still in `wouldChange2` (still unstable after both real rounds), the conflicting
linters are every key in `round1` or `round2` whose own slice contains that file -- exactly the
"touched it on either real pass" rule already approved.

```go
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
```

## CLI wiring

### `rtunk fmt --check`

`fmtCmd` gains `Check bool` (`help:"Report files that would be reformatted, without writing them."`).
When set, `fmtCmd.Run` takes a single-pass branch instead of calling `runStableFormat`:

```go
if c.Check {
	env.DryRun = true
	events, err := engine.Run(context.Background(), env, c.Paths, func(cmd config.Command) bool { return cmd.Formatter })
	if err != nil {
		return err
	}
	_, wouldChange, skipped, failed := drainRunEvents(func(ev engine.Event) { printFmtEvent(stderr, ev) }, events)
	printFmtCheckReport(stdout, wouldChange, skipped)
	if failed != nil {
		return failed
	}
	if len(wouldChange) > 0 {
		return fmt.Errorf("rtunk: fmt --check found %d file(s) needing reformatting", len(wouldChange))
	}
	return nil
}
```

`printFmtCheckReport` mirrors `printFmtReport`'s shape exactly, but with truthful dry-run wording
("N file(s) would be reformatted", not "reformatted") -- copy-and-rename, not a shared function
with a mode flag, matching this project's own existing precedent (`printEvent`/`printFmtEvent` are
already two independent near-duplicates, deliberately, since the two report kinds are independent
contracts).

When `Check` is false (the default), `fmtCmd.Run` calls `runStableFormat` and reports via the
existing `printFmtReport`, exactly as `fmt-autofix` already built -- the only change to the
non-`--check` path is which function produces `changed`/`skipped`/`err`.

### `rtunk check --fix`

`checkRunCmd.Run`'s fix-pass branch (currently one `engine.Run(Formatter predicate)` call) is
replaced with a call to `runStableFormat`, same as `fmt.go`'s own non-`--check` branch -- both
consume the identical `(changed, skipped, err)` shape `printFmtReport` already expects. The
existing principle ("a Failed event in the fix pass does not abort the checking pass," from
`fmt-autofix`'s own design) extends naturally: an unstable-formatting error from `runStableFormat`
is treated exactly like the fix pass's pre-existing `Failed` case -- recorded, reported, but the
checking pass still runs afterward against whatever state the files ended up in.

## Testing approach

- `engine`: a new test proving `DryRun` genuinely never writes to the real file (real content
  unchanged on disk after a `DryRun: true` run reports a nonempty `ChangedFiles`) while still
  correctly detecting the would-be change -- the core claim this whole feature rests on.
- `internal/cli`: three fixtures exercising `runStableFormat`'s three real outcomes end-to-end:
  (1) a single genuinely-idempotent formatter -- stable after round 1, no dry-run check even
  needed (nothing changed); (2) two formatters where the second's rewrite happens to also be
  idempotent on top of the first's (stable only after the dry-run check, not trivially at round 1);
  (3) two formatters that genuinely keep rewriting the same file to different content forever --
  proving the exact conflict-error text and linter attribution. A separate `fmt --check` test
  proving it never writes and reports the correct dry-run wording.

## Non-goals

- No configurable retry cap -- fixed at exactly 2 real rounds (write, check, write, check),
  matching what was explicitly requested; a flag to tune this is unneeded speculative generality.
- No attempt to determine which of two conflicting linters is "actually wrong" -- both are named
  as suspects; picking a culprit would require semantic understanding of each tool's own rules,
  well outside this project's scope.
- No optimization to re-check only the specific files a previous round changed (rather than the
  full requested `paths` again) -- correctness first; a formatter could in principle touch sibling
  files during a batch invocation, so re-scoping only to "files known to have changed" risks
  missing a genuine instability. Full re-matching on every round, exactly like every other
  `engine.Run` call already does, is simpler and correct.

## Global constraints (carried into every task)

- No new `go.mod` dependencies.
- Every test asserts exact values, never just "no error."
- `go build ./... && go vet ./... && go test ./... -count=1 -race` clean before every task is done.
- Commits signed (`-S`), commitlint conventions per `.agents/skills/git-commit/SKILL.md` (check its
  own scope list before guessing a scope name -- confirmed tonight more than once that a scope name
  doesn't always match its package name, e.g. `pkg/trunk/download` commits use scope `cache`).
