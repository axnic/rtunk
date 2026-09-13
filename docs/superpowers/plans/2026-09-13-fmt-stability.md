# fmt Stability Verification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Match real trunk's own `fmt` safety net: `rtunk fmt --check` (a standalone dry-run mode,
never writes) plus an automatic write-check-retry-check stability loop for plain `rtunk fmt` and
`rtunk check --fix`'s own fix pass, reporting exactly which formatters conflict if the result never
stabilizes.

**Architecture:** `engine.Env` gains a `DryRun bool` that forces `InPlace` commands through a
throwaway sandbox copy (reusing `security.StageSandbox`'s existing `copy_targets` mechanism)
instead of the real files. A new `internal/cli/fmt_stability.go` orchestrates the write/check/
retry/check loop on top of `engine.Run`, using a new per-linter-attribution-preserving event
collector (`drainRunEvents`'s own flat dedup discards exactly what's needed to name conflicting
linters).

**Tech Stack:** Go, existing `pkg/trunk/engine`/`pkg/trunk/engine/security`/`internal/cli` packages,
no new dependencies.

**Spec:** `docs/superpowers/specs/2026-09-13-fmt-stability-design.md`

## Global Constraints

- No new `go.mod` dependencies.
- Every test asserts exact values, never just "no error."
- `go build ./... && go vet ./... && go test ./... -count=1 -race` clean before every task is done.
- Commits signed (`-S`), commitlint conventions per `.agents/skills/git-commit/SKILL.md` -- check
  its own `scopes` list before guessing a scope name (confirmed tonight more than once that a
  scope doesn't always match its package name, e.g. `pkg/trunk/download` commits use scope
  `cache`; commit message format is `<type>[<scope>]: <subject>`, e.g. `+[engine]: ...` or
  `![engine,check]: ...` for multiple scopes -- read a recent `git log --oneline` entry for the
  exact punctuation if unsure).
- Fixed at exactly 2 real (writing) rounds before giving up -- no configurable retry cap.
- The dry-run mechanism must never write to the real file, under any circumstance -- this is the
  single most important correctness property in this plan and must be independently verified by
  reproduction (read the real file's content after a `DryRun: true` run, not just check the
  reported `ChangedFiles`).

---

### Task 1: engine -- `Env.DryRun`, sandboxed dry-run invocation

**Files:**

- Modify: `pkg/trunk/engine/engine.go`
- Test: `pkg/trunk/engine/engine_test.go`

**Interfaces:**

- Produces: `engine.Env.DryRun bool` (new field; when true, every `InPlace` command in this run is
  staged into a throwaway sandbox copy and never touches the real file, while `Event.ChangedFiles`
  still reports what would have changed).
- Consumes: nothing new from elsewhere in this plan (Task 2 consumes this field from `internal/cli`
  by setting it on the `engine.Env` it constructs).

- [ ] **Step 1: Add `Env.DryRun`**

In `pkg/trunk/engine/engine.go`, replace:

```go
// Env is every piece of shared configuration a job needs to run.
type Env struct {
	Cfg         config.Config
	RepoRoot    string // always absolute, see Run
	CacheDir    string
	Concurrency int // workers, at least 1 -- Run clamps a lower value up to 1
}
```

with:

```go
// Env is every piece of shared configuration a job needs to run.
type Env struct {
	Cfg         config.Config
	RepoRoot    string // always absolute, see Run
	CacheDir    string
	Concurrency int // workers, at least 1 -- Run clamps a lower value up to 1
	// DryRun forces every InPlace command in this run to execute against a throwaway sandbox copy
	// of its own targets (reusing security.StageSandbox's "copy_targets" mechanism) instead of the
	// real files -- Event.ChangedFiles still reports what WOULD change, but nothing on disk is
	// ever modified. Independent of Command.SandboxType (which real catalog data never sets on an
	// InPlace command anyway, since a sandboxed write would otherwise be silently lost -- see the
	// InPlace+SandboxType skip in buildJobs).
	DryRun bool
}
```

- [ ] **Step 2: Thread `DryRun` from `Run` into `buildJobs` into `job`**

In `pkg/trunk/engine/engine.go`, replace `Run`'s call to `buildJobs`:

```go
			linterJobs := buildJobs(env.Cfg, root, env.CacheDir, repoRoot, name, env.Cfg.Lint.Definitions[name], paths, include, events)
```

with:

```go
			linterJobs := buildJobs(env.Cfg, root, env.CacheDir, repoRoot, name, env.Cfg.Lint.Definitions[name], paths, include, env.DryRun, events)
```

Replace `buildJobs`'s signature:

```go
func buildJobs(cfg config.Config, root, cacheDir, repoRoot, name string, linter config.Linter, paths []string, include func(config.Command) bool, events chan<- Event) []job {
```

with:

```go
func buildJobs(cfg config.Config, root, cacheDir, repoRoot, name string, linter config.Linter, paths []string, include func(config.Command) bool, dryRun bool, events chan<- Event) []job {
```

Inside `buildJobs`, replace the job-construction block:

```go
			for _, batch := range batches {
				jobs = append(jobs, job{
					linterName: name, linter: linter, cmd: cmd, batch: batch,
					pathEnv: pathEnv, parserPathEnv: parserPathEnv, resolvedDir: dir,
				})
			}
```

with:

```go
			for _, batch := range batches {
				jobs = append(jobs, job{
					linterName: name, linter: linter, cmd: cmd, batch: batch,
					pathEnv: pathEnv, parserPathEnv: parserPathEnv, resolvedDir: dir, dryRun: dryRun,
				})
			}
```

Add `dryRun bool` to the `job` struct -- replace:

```go
type job struct {
	linterName    string
	linter        config.Linter
	cmd           config.Command
	batch         []string
	pathEnv       string
	parserPathEnv string
	resolvedDir   string
}
```

with:

```go
type job struct {
	linterName    string
	linter        config.Linter
	cmd           config.Command
	batch         []string
	pathEnv       string
	parserPathEnv string
	resolvedDir   string
	dryRun        bool // set uniformly from Env.DryRun for every job in a run -- a run-level
	                    // setting, not a per-command one; see runBatch's own use of it.
}
```

- [ ] **Step 3: Force sandbox staging in `runBatch` when `dryRun && InPlace`**

In `pkg/trunk/engine/engine.go`, inside `runBatch`, replace:

```go
	workDir := j.resolvedDir
	if j.cmd.SandboxType != "" {
		sandboxDir, cleanup, err := security.StageSandbox(j.cmd.SandboxType, j.resolvedDir, j.batch)
		if cleanup != nil {
			defer cleanup()
		}
		if err != nil {
			return nil, nil, err
		}
		workDir = sandboxDir
	}
```

with:

```go
	workDir := j.resolvedDir
	// A dry run stages InPlace commands into a throwaway sandbox copy regardless of the command's
	// own SandboxType (always empty in practice for InPlace commands -- see the InPlace+SandboxType
	// skip in buildJobs) so the real file is never touched, while ChangedFiles (computed below via
	// the exact same before/after hash comparison a real run already uses) still reports what
	// would have changed.
	sandboxType := j.cmd.SandboxType
	if j.dryRun && j.cmd.InPlace {
		sandboxType = "copy_targets"
	}
	if sandboxType != "" {
		sandboxDir, cleanup, err := security.StageSandbox(sandboxType, j.resolvedDir, j.batch)
		if cleanup != nil {
			defer cleanup()
		}
		if err != nil {
			return nil, nil, err
		}
		workDir = sandboxDir
	}
```

Update `runBatch`'s own doc comment (the paragraph above its signature) -- find the sentence
`inPlaceMu serializes every InPlace invocation across the whole run (see the lock acquired below)
so two of them can never interleave their before-hash/invoke/after-hash cycle over the same file.`
and add, right after it, a new sentence: `A dry run (job.dryRun) never reaches the real file at
all -- see the sandboxType computation below.`

- [ ] **Step 4: Run the existing suite to confirm the signature changes compile cleanly everywhere**

Run: `go build ./... && go test ./pkg/trunk/engine/... -count=1 -v`
Expected: PASS. `buildJobs` has exactly one caller (`Run`, just updated); if anything else fails
to compile, it's calling `buildJobs` directly and was missed above.

- [ ] **Step 5: Write the dry-run correctness test**

This is the single most important test in this plan: it must prove the real file's content is
byte-identical before and after a `DryRun: true` run that itself reports a nonempty
`ChangedFiles`. Append to `pkg/trunk/engine/engine_test.go`:

```go
// TestRun_DryRunNeverWritesRealFile is the load-bearing test for this whole feature: a DryRun
// run of a real content-changing InPlace command must report ChangedFiles correctly (it WOULD
// change the file) while leaving the real file's content completely untouched -- proven by
// reading the real file's bytes after the run, not just trusting the reported Event.
func TestRun_DryRunNeverWritesRealFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("faketool invoked via sh -c")
	}

	binPath := buildFakeToolBinary(t)

	cacheDir := t.TempDir()
	root, err := download.Root(cacheDir)
	require.NoError(t, err)
	shimPath := download.ShimPath(root, "tools", "faketool", "1.0.0", "faketool")
	require.NoError(t, os.MkdirAll(filepath.Dir(shimPath), 0o755))
	require.NoError(t, download.WriteShim(shimPath, binPath))

	repoRoot := t.TempDir()
	target := filepath.Join(repoRoot, "messy.txt")
	require.NoError(t, os.WriteFile(target, []byte("messy\n"), 0o644))

	cfg := config.Config{
		Tools: map[string]config.Tool{
			"faketool": {Name: "faketool", KnownGoodVersion: "1.0.0"},
		},
		Lint: config.LintConfig{
			Files: map[string]config.FileType{},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"fakefmt": {
						Name: "fakefmt", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{{
							Name: "format", Run: "faketool rewrite ${target}", Output: "rewrite",
							SuccessCodes: []int{0}, Batch: true, InPlace: true, Formatter: true,
						}},
					},
				},
			},
		},
	}

	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: cacheDir, Concurrency: 1, DryRun: true}, nil, func(c config.Command) bool { return c.Formatter })
	require.NoError(t, err)

	var got Event
	for ev := range events {
		if ev.Linter == "fakefmt" && ev.Phase == Done {
			got = ev
		}
	}
	assert.Equal(t, []string{"messy.txt"}, got.ChangedFiles, "DryRun must still correctly report what WOULD change")

	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "messy\n", string(data), "DryRun must NEVER write to the real file, even though ChangedFiles reports a change")
}
```

- [ ] **Step 6: Run the new test and verify by mutation**

Run: `go test ./pkg/trunk/engine/... -run TestRun_DryRunNeverWritesRealFile -v -count=1`
Expected: PASS.

Then temporarily comment out the `if j.dryRun && j.cmd.InPlace { sandboxType = "copy_targets" }`
line from Step 3 and re-run the same test -- it must now FAIL on the `data, err :=
os.ReadFile(target)` assertion (the real file would show `"formatted\n"`, the fake tool's real
output, not `"messy\n"`). This proves the test is real, not a false positive. Restore the line
afterward and confirm the test passes again.

- [ ] **Step 7: Full build/vet/test, then commit**

Run: `go build ./... && go vet ./... && go test ./... -count=1 -race`
Expected: PASS across every package.

```bash
git add pkg/trunk/engine/engine.go pkg/trunk/engine/engine_test.go
git commit -S -m "$(cat <<'EOF'
+[engine]: Add DryRun for InPlace commands via sandbox staging

fmt --check (and the stability loop that will use it) needs a way to know
whether an InPlace command WOULD change a file, without ever writing to
the real one. Reuses security.StageSandbox's existing copy_targets
mechanism -- already exactly what a real "check" sandbox does for
non-formatter commands -- forcing it for InPlace commands whenever
Env.DryRun is set, regardless of the command's own (always-empty-in-
practice) SandboxType. Everything downstream (hashing, ChangedFiles,
absolute-path finding remap) is unchanged, since it already only cares
about workDir, not whether workDir happens to be a sandbox.
EOF
)"
```

---

### Task 2: internal/cli -- `fmt --check`, the stability loop, `check --fix` wiring

**Files:**

- Create: `internal/cli/fmt_stability.go`
- Test: `internal/cli/fmt_stability_test.go`
- Modify: `internal/cli/fmt.go`, `internal/cli/check.go`

**Interfaces:**

- Consumes: `engine.Env.DryRun` (Task 1), `engine.Event.ChangedFiles` (pre-existing), the existing
  `drainRunEvents(printFn func(engine.Event), events <-chan engine.Event) (findings
[]output.Finding, changed []string, skipped []string, failed error)` and `printFmtEvent(w
io.Writer, ev engine.Event)`/`printFmtReport(w io.Writer, changed, skipped []string)` (all
  pre-existing, in `internal/cli/check.go`, unmodified by this task).
- Produces: `runStableFormat(ctx context.Context, env engine.Env, paths []string, stderr io.Writer)
(changed []string, skipped []string, err error)` -- used by both `fmtCmd.Run`'s non-`--check`
  branch and `checkRunCmd.Run`'s `--fix` branch.

- [ ] **Step 1: Create `internal/cli/fmt_stability.go`**

```go
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
```

- [ ] **Step 2: Add `printFmtCheckReport` to `internal/cli/check.go`**

In `internal/cli/check.go`, right after the existing `printFmtReport` function's closing brace,
add:

```go

// printFmtCheckReport is printFmtReport's standalone-dry-run equivalent: same shape (one file per
// line, then a blank line, then a summary), but with truthful "would be reformatted" wording,
// since --check never actually writes anything.
func printFmtCheckReport(w io.Writer, wouldChange []string, skipped []string) {
	sorted := append([]string(nil), wouldChange...)
	sort.Strings(sorted)
	for _, f := range sorted {
		fmt.Fprintln(w, f)
	}

	summary := fmt.Sprintf("%d file(s) would be reformatted", len(sorted))
	if len(skipped) > 0 {
		sortedSkipped := append([]string(nil), skipped...)
		sort.Strings(sortedSkipped)
		summary += fmt.Sprintf(" (%d linter(s) skipped: %s)", len(sortedSkipped), strings.Join(sortedSkipped, ", "))
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, summary)
}
```

- [ ] **Step 3: Wire `--check` and the stability loop into `internal/cli/fmt.go`**

Replace the whole file:

```go
package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"runtime"

	"github.com/xunleii/rtunk/pkg/trunk/config"
	"github.com/xunleii/rtunk/pkg/trunk/engine"
)

// fmtCmd is `rtunk fmt [paths...]`: ROADMAP.md v0.4, running every enabled linter's Formatter
// command(s) -- gofmt, prettier, black, and the rest -- against the given paths, or the whole
// repository if none are given. Plain fmt automatically verifies its own result is stable (see
// runStableFormat) -- real trunk's own fmt safety net, no flag needed. --check instead runs a
// single, standalone dry-run pass that never writes to disk, matching real trunk's own `trunk fmt
// --check`: a CI gate asking "would anything change," not "make it change."
type fmtCmd struct {
	Paths []string `arg:"" optional:"" help:"Paths to format (default: whole repository)."`
	Jobs  int      `short:"j" help:"Number of parallel linter workers (default: number of CPUs)."`
	Check bool     `help:"Report files that would be reformatted, without writing them."`
}

func (c *fmtCmd) Run(cli *CLI, stdout io.Writer, stderr Stderr) error {
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
	repoRoot := filepath.Dir(filepath.Dir(configPath))

	jobs := c.Jobs
	if jobs <= 0 {
		jobs = runtime.NumCPU()
	}
	env := engine.Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: cli.CacheDir, Concurrency: jobs}

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

	changed, skipped, err := runStableFormat(context.Background(), env, c.Paths, stderr)
	printFmtReport(stdout, changed, skipped)
	return err
}
```

- [ ] **Step 4: Wire `runStableFormat` into `check --fix`**

In `internal/cli/check.go`, replace the existing `--fix` branch inside `checkRunCmd.Run`:

```go
	var fixFailed error
	if c.Fix {
		fixEvents, err := engine.Run(context.Background(), env, c.Paths, func(cmd config.Command) bool { return cmd.Formatter })
		if err != nil {
			return err
		}
		_, changed, fixSkipped, ffErr := drainRunEvents(func(ev engine.Event) { printFmtEvent(stderr, ev) }, fixEvents)
		fixFailed = ffErr
		printFmtReport(stdout, changed, fixSkipped)
	}
```

with:

```go
	var fixFailed error
	if c.Fix {
		changed, fixSkipped, ffErr := runStableFormat(context.Background(), env, c.Paths, stderr)
		fixFailed = ffErr
		printFmtReport(stdout, changed, fixSkipped)
	}
```

The comment block immediately above this branch (starting `// --fix runs every Formatter command
first...`) stays as-is -- its description of the fix-then-check ordering and the
Failed-doesn't-abort-checking principle is still accurate; `runStableFormat`'s own unstable-fmt
error is now one more thing that principle covers, alongside a plain `Failed` event.

- [ ] **Step 5: Run the existing test suites to confirm nothing broke**

Run: `go test ./internal/cli/... -count=1 -v`
Expected: PASS. In particular, `TestFmtCmd_ReportsOnlyChangedFiles`,
`TestFmtCmd_DedupesFilesChangedByMultipleLinters`,
`TestCheckRunCmd_Fix_AppliesFixesBeforeReporting`, and
`TestCheckRunCmd_DoesNotRunInPlaceCommands` (all pre-existing) must still pass unchanged --
`runStableFormat`'s "nothing written, nothing to verify" early return means a fixture with a
single idempotent formatter (the shape all of these use) behaves identically to the old
single-pass code, just routed through the new function.

- [ ] **Step 6: Write `internal/cli/fmt_stability_test.go`**

```go
package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFmtCmd_Check_NeverWritesAndReportsWouldChange proves `rtunk fmt --check` never writes to
// disk while still correctly reporting a file that would be reformatted, and exits non-zero.
func TestFmtCmd_Check_NeverWritesAndReportsWouldChange(t *testing.T) {
	cfgPath, repoRoot := writeLinterFixture(t, []string{"fakefmt"}, `    - name: fakefmt
      description: A fake in-place formatter
      files: [ALL]
      commands:
        - name: format
          run: printf 'formatted\n' > ${target}
          output: rewrite
          success_codes: [0]
          in_place: true
          formatter: true
`)
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "work"), 0o755))
	target := filepath.Join(repoRoot, "work", "messy.txt")
	require.NoError(t, os.WriteFile(target, []byte("messy\n"), 0o644))

	cacheDir := t.TempDir()
	stdout, stderr, err := run2(t, "--config", cfgPath, "--cache-dir", cacheDir, "fmt", "--check", filepath.Join(repoRoot, "work"))
	require.Error(t, err, "stderr: %s", stderr)

	want := "work/messy.txt\n\n1 file(s) would be reformatted\n"
	assert.Equal(t, want, stdout)

	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "messy\n", string(data), "--check must never write to the real file")
}

// TestFmtCmd_StableAfterOneRealRound proves plain `rtunk fmt` (no --check) reports success with
// no dry-run-check overhead visible in the outcome when a single idempotent formatter converges
// immediately -- the ordinary case, unaffected by the new stability machinery.
func TestFmtCmd_StableAfterOneRealRound(t *testing.T) {
	cfgPath, repoRoot := writeLinterFixture(t, []string{"fakefmt"}, `    - name: fakefmt
      description: A fake idempotent in-place formatter
      files: [ALL]
      commands:
        - name: format
          run: printf 'formatted\n' > ${target}
          output: rewrite
          success_codes: [0]
          in_place: true
          formatter: true
`)
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "work"), 0o755))
	target := filepath.Join(repoRoot, "work", "messy.txt")
	require.NoError(t, os.WriteFile(target, []byte("messy\n"), 0o644))

	cacheDir := t.TempDir()
	stdout, stderr, err := run2(t, "--config", cfgPath, "--cache-dir", cacheDir, "fmt", filepath.Join(repoRoot, "work"))
	require.NoError(t, err, "stderr: %s", stderr)

	want := "work/messy.txt\n\n1 file(s) reformatted\n"
	assert.Equal(t, want, stdout)

	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "formatted\n", string(data))
}

// TestFmtCmd_StableAfterSecondRound covers the middle case: round 1's dry-run check finds a
// residual diff (a second formatter's own first invocation still had something to do), but round
// 2's dry-run check finds nothing left -- stable after 2 rounds, success, no conflict error. Two
// formatters both write DIFFERENT content on their first pass (fmtA then fmtB, each genuinely
// changing the file once), but fmtB's own SECOND invocation (round 2) is a no-op on top of what it
// already wrote -- so after round 2, nothing would change anymore.
func TestFmtCmd_StableAfterSecondRound(t *testing.T) {
	cfgPath, repoRoot := writeLinterFixture(t, []string{"fmtA", "fmtB"}, `    - name: fmtA
      description: Writes AAA only if the file doesn't already start with AAA
      files: [ALL]
      commands:
        - name: format
          run: grep -qxF AAA ${target} || printf 'AAA\n' > ${target}
          output: rewrite
          success_codes: [0]
          in_place: true
          formatter: true
    - name: fmtB
      description: Appends BBB only if the file doesn't already contain it
      files: [ALL]
      commands:
        - name: format
          run: grep -qF BBB ${target} || printf 'BBB\n' >> ${target}
          output: rewrite
          success_codes: [0]
          in_place: true
          formatter: true
`)
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "work"), 0o755))
	target := filepath.Join(repoRoot, "work", "shared.txt")
	require.NoError(t, os.WriteFile(target, []byte("original\n"), 0o644))

	cacheDir := t.TempDir()
	// -j 1 forces deterministic linter ordering (fmtA before fmtB, matching buildJobs' own
	// name-sorted queue order under a single worker) -- with the default concurrency (NumCPU), two
	// different linters' InPlace jobs can be picked up by workers in either order (they're still
	// serialized against each other by the shared inPlaceMu, but WHICH one goes first is not
	// guaranteed), which would make this fixture's outcome non-deterministic.
	stdout, stderr, err := run2(t, "--config", cfgPath, "--cache-dir", cacheDir, "fmt", "-j", "1", filepath.Join(repoRoot, "work"))
	require.NoError(t, err, "stderr: %s", stderr)
	assert.Contains(t, stdout, "work/shared.txt")
	assert.Contains(t, stdout, "1 file(s) reformatted")

	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "AAA\nBBB\n", string(data))
}

// TestFmtCmd_UnstableReportsConflictingLinters is the core conflict-detection test: two formatters
// that keep rewriting the same file to different content forever, never converging. Must report
// the exact "did not converge" error naming both linters as suspects, and the command must exit
// non-zero.
func TestFmtCmd_UnstableReportsConflictingLinters(t *testing.T) {
	cfgPath, repoRoot := writeLinterFixture(t, []string{"fmtA", "fmtB"}, `    - name: fmtA
      description: Always rewrites to AAA, undoing fmtB's own change
      files: [ALL]
      commands:
        - name: format
          run: printf 'AAA\n' > ${target}
          output: rewrite
          success_codes: [0]
          in_place: true
          formatter: true
    - name: fmtB
      description: Always rewrites to BBB, undoing fmtA's own change
      files: [ALL]
      commands:
        - name: format
          run: printf 'BBB\n' > ${target}
          output: rewrite
          success_codes: [0]
          in_place: true
          formatter: true
`)
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "work"), 0o755))
	target := filepath.Join(repoRoot, "work", "oscillating.txt")
	require.NoError(t, os.WriteFile(target, []byte("original\n"), 0o644))

	cacheDir := t.TempDir()
	// -j 1 for the same determinism reason as TestFmtCmd_StableAfterSecondRound -- both linters
	// unconditionally overwrite here, so the outcome (never converges) doesn't actually depend on
	// which runs first, but forcing single-worker scheduling keeps this test's timing fully
	// reproducible rather than relying on that being true.
	stdout, stderr, err := run2(t, "--config", cfgPath, "--cache-dir", cacheDir, "fmt", "-j", "1", filepath.Join(repoRoot, "work"))
	require.Error(t, err)
	assert.Contains(t, stdout, "work/oscillating.txt")

	assert.Contains(t, err.Error(), "fmt did not converge after 2 attempts")
	assert.Contains(t, err.Error(), "work/oscillating.txt (conflicting: fmtA, fmtB)")
	_ = stderr
}
```

- [ ] **Step 7: Run the new tests**

Run: `go test ./internal/cli/... -run "TestFmtCmd_Check_NeverWritesAndReportsWouldChange|TestFmtCmd_StableAfterOneRealRound|TestFmtCmd_StableAfterSecondRound|TestFmtCmd_UnstableReportsConflictingLinters" -v -count=1`
Expected: PASS, all four.

- [ ] **Step 8: Full build/vet/test, then commit**

Run: `go build ./... && go vet ./... && go test ./... -count=1 -race`
Expected: PASS across every package.

```bash
git add internal/cli/fmt_stability.go internal/cli/fmt_stability_test.go internal/cli/fmt.go internal/cli/check.go
git commit -S -m "$(cat <<'EOF'
+[cli]: Add fmt --check and the fmt stability loop

Real trunk's own fmt safety net: write, dry-run-check for a residual diff,
retry once if unstable, and if it's still unstable after that, report
exactly which formatters are in conflict rather than silently leaving
oscillating output on disk. --check exposes the same dry-run mechanism
standalone as a CI gate (never writes, reports what would change). Both
plain fmt and check --fix's own fix pass now go through the same
runStableFormat loop -- a linter's own ChangedFiles across the two real
rounds is what identifies conflicting formatters, information the
existing drainRunEvents' flat, deduplicated aggregate discards by design,
hence the new collectChangedByLinter collector used only for this.
EOF
)"
```

---

## Self-Review Notes (for whoever runs this plan)

- **Spec coverage:** the dry-run mechanism (Task 1), the stability loop + conflict attribution +
  `--check` standalone (Task 2, Steps 1-4) all trace directly to the spec's own sections. The
  spec's Non-goals (no configurable retry cap, no culprit-picking between conflicting linters, no
  re-scoping to only-changed-files on retry) have no corresponding task -- nothing to do there, by
  design.
- **Task 1 -> Task 2 dependency:** Task 2's `runStableFormat` and `fmtCmd.Run`'s `--check` branch
  both set `Env.DryRun`, which only has any effect once Task 1 lands. Task 1 has no dependency on
  Task 2 and is independently testable (its own `TestRun_DryRunNeverWritesRealFile` needs nothing
  from `internal/cli`).
- **The `buildJobs` signature change is the one place a missed call site would silently break the
  build, not silently misbehave** -- Task 1 Step 4 exists specifically to catch that immediately,
  before any test-writing, the same discipline `fmt-autofix`'s own `runBatch` signature change
  used.
- **Type/signature consistency check:** `runStableFormat`'s return shape
  `(changed []string, skipped []string, err error)` matches exactly what both of Task 2's call
  sites (`fmtCmd.Run`, `checkRunCmd.Run`) destructure, and matches what `printFmtReport(w
io.Writer, changed, skipped []string)` (pre-existing, unmodified) already expects as its own two
  slice parameters.
