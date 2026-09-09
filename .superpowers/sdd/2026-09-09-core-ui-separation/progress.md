# SDD ledger — plan: /Volumes/Spaces/Lab/rtunk/docs/superpowers/plans/2026-09-09-core-ui-separation.md

## Setup

- Branch: `refactor/core-ui-separation` (orphan branch, per user instruction — no shared history with `main`; baseline capture of pre-existing out-of-band work landed as 17 dependency-ordered commits, HEAD `7ed0573`, `go build ./... && go vet ./... && go test ./...` all green at that point).
- Spec: `docs/superpowers/specs/2026-09-09-core-ui-separation-design.md` — reachable, read in full.
- `core.hooksPath` was unset repo-locally (was pointing at a real trunk.io CLI pre-commit hook that crashed on this orphan branch's zero-commit/no-upstream state) — per explicit user instruction ("disable trunk"). Reversible: `git config core.hooksPath /Users/nicolaie/.cache/trunk/repos/315446a3e4e9001edff1e8b1e3ddbc77/git-hooks` restores it.

## Pre-flight conflict scan

Pairwise table — every pair of tasks sharing a file or interface:

| Task A | Task B | Shared surface | A produces | B consumes | Finding |
|---|---|---|---|---|---|
| 1 (pkg/diagnostic) | 2–6, 9 | `pkg/diagnostic.Diagnostic`/`Fix`/`Severity` import path | promotes `internal/diagnostic`→`pkg/diagnostic`, rewrites 12 files' imports | every later task's new/moved code imports `pkg/diagnostic` | Clean — Task 1 must land first; plan already orders it first. |
| 2 (output parsers) | 6 (exec.go) | `parseGitleaksJSON`/`parseSarif`/`parseMarkdownlint`/`parseTaplo`/`parseRegex` names | unexports these 5 names in `pkg/worker/output_*.go` | Task 6's `parseOutput` (moved verbatim from `pkg/linter/linter.go`) calls them by these exact names | Clean — names match exactly (verified against real `pkg/linter/linter.go:274-317` before writing the plan). |
| 3 (config_ignore.go) | 8 (worker.go) | `filterPaths` | unexports `FilterPaths`→`filterPaths` | `Runner.CountJobs`/`Run` call `filterPaths` | Clean — signature matches. |
| 4 (directive.go) | 5 (filter.go) | `directive`/`kind*`/`parseDirectives` | unexports `Directive`→`directive`, `ParseDirectives`→`parseDirectives`, `Kind*`→`kind*` | `filter.go`'s `apply`/`suppressed`/`resolveBlocks` consume these types | Clean, but **sequencing note**: Task 4 alone leaves the package non-compiling (`internal/ignore/filter.go` and `ignore_test.go` still reference the old exported names) until Task 5 lands. Plan already says explicitly "Tasks 4 and 5 land as one commit" (plan §Task 4 Step 4, §Task 5). Ruling: not a defect, this is intentional and documented — dispatch Tasks 4+5 to the SAME implementer as one unit, not as two separate subagent dispatches. |
| 5 (FilterDirectives) | 9 (cmd/rtunk) | `worker.FilterDirectives(root, targets, diags)` | exports this one renamed function | `checkCmd` calls it once, post-fan-in, before `report.Print` | Clean — signature and call order match spec §3/§6 exactly. |
| 6 (exec.go) | 8 (worker.go) | `group`, `filterMatches`, `groupTargets`, `resolveConfigArgs`, `runInPlace`, `runRewrite`, `runParser`, `runCommand`, `severityOf`, `containsInt`, `parseOutput` | moves these unexported from `pkg/linter/linter.go` verbatim | `Runner.dispatch`/`runGroup`/`runLint` call every one of these by these exact names | Clean — cross-checked every name against the real `pkg/linter/linter.go` function list (grepped before writing the plan); all 11 names match. |
| 7 (event.go) | 8 (worker.go) | `Event`, `JobStarted`, `JobDone`, `DiagnosticFound`, `FixProposed`, `FileChanged`, `RunError` | defines the 6 concrete types + sum-type interface | `Runner.Run`/`dispatch`/`runGroup`/`runLint` construct and send every one of these | Clean — field names (`Target`, `Path`, `Err`) match between Task 7's definitions and Task 8's construction sites. |
| 8 (Runner) | 9 (cmd/rtunk) | `Definition{Linter,LintersDir,Runtimes,ProjectCacheDir}`, `RunScope{Root,Targets,IgnoreRules,Formatter}`, `NewRunner`, `CountJobs(scope) (int,error)`, `Run(scope) (<-chan Event,error)` | defines this API | `newRunners` builds `Definition`s from `*workspace.Workspace`'s real fields; `checkCmd`/`fmtCmd` call `CountJobs`/`Run` | Clean — verified `workspace.Workspace`'s actual fields (`LintersDir`, `Runtimes`, `ProjectCacheDir`, `Linters map[string]config.LinterDefinition`) match what Task 9's `newRunners` assumes, by reading `pkg/workspace/workspace.go` directly (not just the plan's prose). |
| 9 (cmd/rtunk) | 9 (cmd/rtunk, internal self-consistency) | `checkCmd`/`fmtCmd` bodies vs. untouched `loadDefinitions`/`setup`/`applyFixes`/`resolvePath` | rewrites only `checkCmd`/`fmtCmd`, deletes `linterWork`/`resolveWork`/`countJobs`/`newLinter` | — | Clean — verified via `grep -n "^func "` against the real `cmd/rtunk/main.go` that the untouched functions' signatures match exactly what the plan's new `checkCmd`/`fmtCmd` call (`setup(args, all)`, `cfg.ResolveCache`, `loadDefinitions(cmd, cfg, c, root)`, `diagnostic.ParseSeverity(failOn)`, `applyFixes(cmd, root, fixes, autoYes, autoNo)`). Also verified `progress.New(w, label, total)`/`.Start(name)`/`.Done(name)`/`.Finish()` against the real `internal/progress/progress.go` signatures. |
| 9 | 9 (workspace leak fix) | `Workspace.NewLinter` deletion | Task 9 Step 1 deletes `(ws *Workspace) NewLinter(...)`, drops its `pkg/linter`/`internal/progress` imports | nothing else in the plan calls `Workspace.NewLinter` (confirmed: only `cmd/rtunk/main.go`'s old `newLinter` helper did, and that helper is itself deleted in the same task) | Clean. |

Self-consistency check (does each task's own text agree with itself — tests specified against code specified):

- Task 7's test (`TestEvent_TypeSwitch`) exercises exactly the 6 types Task 7's `event.go` defines. Clean.
- Task 8's tests (`TestRunner_CountJobs`, `TestRunner_Run_PreflightFailureReturnsSyncError`, `TestRunner_Run_NoMatchingTargetsSkipsPreflight`, `TestRunner_Dispatch_JobFailureDoesNotStopOthers`) call `NewRunner`, `CountJobs`, `Run`, `dispatch` with the exact signatures Task 8's own `worker.go` code block defines. Clean.
- Task 3's Step 3 test file content is explicitly flagged in the plan itself as needing verification against the real `internal/ignore/ignore_test.go` content before finalizing literals ("read the file first") — this is a legitimate implementer instruction, not a plan defect (the plan cannot inline content it would have to re-derive from a file the implementer will read anyway).

**Scan verdict: clean.** No contradictions found against the plan's own Global Constraints or between task pairs. No rulings needed before Task 1 dispatch.

## Task log
