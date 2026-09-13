# `rtunk fmt` and `check --fix` Design Spec

> **Note on process:** the user brainstormed this live, answered every open design
> question (scope, `check --fix` ordering, hashing, `Command.Enabled`, sandbox+formatter,
> `fmt --check`, failure handling), then explicitly said "I'm going to be unavailable,
> ask me everything now, activate caffeinate, deactivate it when you're done" before
> the design was fully written up. Everything from this point (the write-up itself,
> the plan, and its execution) proceeds without further live confirmation, per that
> instruction — this spec is the faithful record of what was actually agreed, not a
> retroactive justification for design choices made alone.

## Goal

ROADMAP.md's v0.4 milestone: `rtunk fmt [paths...]` (run configured formatters) and
`rtunk check --fix [paths...]` (run checks, but auto-fix first). Both build on the exact
gap `engine.Run`'s own doc comment named when `check-engine-refactor` extracted it
tonight: "a future fmt package ... genuinely gets the job queue, RunFrom/SandboxType
resolution, and file matching; the output/reporting half remains fmt's own work."

## Real catalog grounding

Surveyed the real trunk-io catalog's formatter commands (`gofmt`, `shfmt`, `black`,
`rustfmt`, `isort`, `autopep8`, `prettier`, `rubocop`'s `fix-layout`, `stylelint`'s
`fix`, `ruff`'s `format`) in full. Findings that shaped this design:

- **`output: rewrite` and `output: shfmt` need zero parsing.** Every plain formatter
  (gofmt, black, rustfmt, isort, autopep8, rubocop's fix-layout, stylelint's fix) uses
  one of these two `Output` values. Neither has any structured result to parse --
  success is purely "the exit code is in `success_codes`" (already the existing
  `ErrorCodes` mechanism `engine.go` has for every command). `engine.go` currently
  treats both as unsupported (`Skipped`) purely because they're absent from
  `supportedOutputFormats` -- adding them is the entire "output" half of the gap the
  refactor's doc comment named.
- **`check --fix` is not a runtime flag -- it's a separate `Command` in the same
  `Linter`.** `rubocop` has a `lint` command AND a `fix-layout` command; `stylelint`
  has `lint` AND `fix`. Both are marked `formatter: true, in_place: true`, distinct
  from their linter's own checking command(s). This means `fmt` and `check --fix` can
  share one selection mechanism: "does this command have `Formatter: true`" -- `fmt`
  selects all of them, `check --fix` runs them first, then runs the normal
  `!Formatter` set.
- **`Command.Formatter`/`InPlace` always travel together in every real example found** --
  no catalog command sets one without the other. This project's design conditions the
  new file-hashing mechanism (below) on `InPlace` specifically (the semantically
  precise flag: "this command may rewrite its own input files"), not `Formatter`
  (which only means "belongs to the fmt/fix selection set") -- `prettier`'s own command
  is both `Formatter: true` and `Output: sarif` (it reports SARIF via its own
  `parser`, in addition to rewriting files), so gating hashing on `InPlace` rather than
  a specific `Output` value correctly covers it too.
- **No real catalog command combines a formatter with `sandbox_type`.** This makes
  sense: a sandboxed invocation writes into a throwaway staged copy, and
  `security.RemapFindings` only ever remaps _reported_ paths back to the real repo --
  there is no mechanism to copy a sandboxed formatter's _file writes_ back out. Rather
  than build one (unused by any real catalog data), this design explicitly rejects the
  combination.
- **`ruff`'s real `format` command sets `enabled: false`** (ruff-format competes with
  black; catalog data defaults it off even though `ruff`'s own linting stays on).
  `config.Command` has no `Enabled` field today -- without one, `rtunk fmt` would run
  every `Formatter` command belonging to an enabled linter unconditionally, silently
  diverging from real trunk's own default for this exact real case.

## `config.Command.Enabled`

New field:

```go
// Enabled defaults a command on (nil) or explicitly off (real catalog example: ruff's
// own "format" command sets false, since ruff-format competes with black) --
// distinct from Linter-level enable/disable (trunk.yaml's lint.enabled: list), which
// this field does not touch. rtunk has no trunk.yaml-level override for a single
// command's own Enabled today (a real gap, deliberately out of this feature's
// scope -- see Non-goals); this field only ever reflects what the plugin source's own
// catalog data says.
Enabled *bool `yaml:"enabled,omitempty"`
```

`buildJobs` gains, at the very top of its per-command loop (before every other check):

```go
if cmd.Enabled != nil && !*cmd.Enabled {
    events <- Event{Linter: name, Phase: Skipped, Note: "disabled by its own plugin source"}
    continue
}
```

Generic (not `Formatter`-gated): the real catalog schema has no rule limiting `enabled:
false` to formatter commands, so neither does this check, even though every real
example found today happens to be one.

## `engine.go` changes

### `supportedOutputFormats`

Add `"rewrite": true, "shfmt": true`. Both dispatch to a no-op case in `runBatch`'s
switch (findings stay `nil` -- there is nothing to parse; success is already decided by
the pre-existing `ErrorCodes` check that runs before the switch).

### Formatter + sandbox: explicit skip

In `buildJobs`, alongside the existing `SandboxType` validity check:

```go
if cmd.InPlace && cmd.SandboxType != "" {
    events <- Event{Linter: name, Phase: Skipped, Note: "in_place command combined with sandbox_type is unsupported (writes would be lost)"}
    continue
}
```

### `Event.ChangedFiles`

```go
type Event struct {
    Linter       string
    Phase        Phase
    Findings     []output.Finding // Done only
    ChangedFiles []string         // Done only, InPlace commands only -- repoRoot-relative
    Note         string
    Err          error
    File         string
}
```

Accumulated across a linter's jobs exactly like `Findings` already is (`linterState`
gains a `changedFiles []string` field, appended to in `runJob`, placed on the terminal
`Done` event).

### Hashing

`runBatch` gains, gated on `j.cmd.InPlace` (not `Formatter` -- see prettier's own case
above), a before/after SHA-256 comparison per file in `j.batch`:

```go
func hashFiles(dir string, files []string) map[string][32]byte {
    hashes := make(map[string][32]byte, len(files))
    for _, f := range files {
        data, err := os.ReadFile(filepath.Join(dir, f))
        if err != nil {
            continue // missing (deleted, or never existed) -- zero value, differs from any real hash
        }
        hashes[f] = sha256.Sum256(data)
    }
    return hashes
}
```

Called once before `runOneInvocation` and once after (only when `j.cmd.InPlace`); a
file whose before/after hash differs (including "existed before, missing after" and
vice versa, both naturally producing a hash mismatch against the zero value) is
"changed." Changed paths -- still relative to `workDir` at this point -- are remapped
to repoRoot-relative the same way `security.RemapFindings` already remaps
`Finding.File` (a small `remapPaths` helper mirroring that exact logic, since these are
plain strings, not `output.Finding`).

Since `InPlace`+`SandboxType` is now an explicit `buildJobs`-time skip, `workDir ==
j.resolvedDir` is guaranteed whenever hashing runs -- no sandbox-remap complexity to
consider here.

## `pkg/trunk/check` is deleted

Confirmed via `grep`: after this feature, its only real (non-doc-comment) consumer is
`internal/cli/check.go`'s one-line predicate wrapper. Once `internal/cli`'s `fmt.go`
needs to call `engine.Run` directly with its own predicate anyway, keeping a whole
package around for a second one-line predicate is unjustified indirection. `check.go`
imports `pkg/trunk/engine` directly and inlines
`func(c config.Command) bool { return !c.Formatter }` where `check.Run(...)` used to be
called.

## `internal/cli` changes

### `rtunk fmt [paths...]`

New `internal/cli/fmt.go`, structured like `checkRunCmd`: resolves config the same way,
calls `engine.Run` with predicate `func(c config.Command) bool { return c.Formatter }`,
drains events, prints progress (`running`/`skipped`/`failed` lines identical in shape to
`check`'s), and a final report counting `ChangedFiles` per linter instead of
`Findings` -- e.g. `"gofmt: reformatted 2 file(s)"` per linter with at least one
changed file, plus a trailing summary line. Exit code: non-zero only on a real `Failed`
event (mirrors `check`'s own convention) -- a formatter that ran clean with zero
changed files is success, not a "finding."

### `rtunk check --fix [paths...]`

`checkRunCmd` gains a `Fix bool` field (`help:"Apply automatic fixes before reporting."`).
When set, `checkRunCmd.Run` calls `engine.Run` TWICE, sequentially, against the same
resolved `cfg`/`repoRoot`/`paths`:

1. First pass, predicate `c.Formatter` (identical selection to `fmt`) -- drained fully
   before the second pass starts (a formatter command must finish writing before the
   checking commands read the same files). Progress lines print as they arrive; a
   `Failed` event here is recorded but does NOT abort -- the second pass runs
   regardless (confirmed: "un linter Failed n'empêche pas les autres de tourner"
   already, matching `check`'s own existing per-linter isolation).
2. Second pass, predicate `!c.Formatter` (the existing `check` behavior, unchanged) --
   its own findings are what gets reported; anything the first pass fixed is, by
   definition, no longer wrong by the time the second pass reads the files, so it never
   appears as a finding needing attention.

The final report shows both: the fix pass's own changed-file counts (reusing `fmt`'s
report shape) printed first, then `check`'s own existing `printReport` findings
summary. Exit code: non-zero if either pass had a real `Failed` event, OR the second
pass found any remaining findings -- unchanged logic from today's `check`, plus the
fix pass's own `Failed` check.

## Testing approach

- `engine`: extend the existing fake-tool-binary harness (`buildFakeToolBinary`) with an
  `InPlace`-rewriting case (a fake tool that overwrites its target file's content when
  invoked, standing in for `gofmt -w`) to test the new `rewrite`/`shfmt` dispatch
  end-to-end, plus a case that leaves a file byte-identical (proving `ChangedFiles`
  correctly excludes an unchanged file, not just "ran successfully"). A dedicated test
  for the new `InPlace`+`SandboxType` skip and the new `Enabled: false` skip, each
  asserting the exact `Note` text. A `hashFiles`/`remapPaths` unit test with a
  `RunFrom`-relocated directory (mirrors how `security_test.go` already tests
  `RemapFindings` with a non-trivial `base`).
- `config`: a definitions-level test confirming `Command.Enabled` round-trips through
  YAML both when present (`false`) and absent (nil, not `false` -- a hand-authored
  command with no `enabled:` key at all must default on).
- `internal/cli`: an end-to-end `fmt` test (real fake-tool fixture, confirms the exit
  code and report line shape) and a `check --fix` test proving the two-pass ordering
  for real -- a linter whose formatter command fixes an issue its OWN checking command
  would otherwise report, confirming the finding is genuinely absent from `--fix`'s
  final report (not just "the fix ran"), matching the real trunk UX this design targets.

## Non-goals

- **No `rtunk fmt --check` (dry-run) in this feature.** Not in ROADMAP.md's current
  v0.4 wording; the hashing mechanism this feature adds makes a future dry-run cheaper
  to build later (diff instead of write), but building it now is out of scope.
- **No trunk.yaml-level override for a single `Command.Enabled` value.** `rtunk fmt`
  respects whatever the plugin source's own catalog says (e.g. `ruff format` stays off
  by default); there is no mechanism today, and this feature does not add one, for a
  user to flip a single disabled command back on without editing the plugin source
  itself. A real, deliberately-deferred gap.
- **No diff display, no interactive confirm-per-file, no undo/backup mechanism.**
  `fmt`/`check --fix` write directly, same as real trunk and every real formatter
  invoked directly; relying on the user's own version control for review, same as
  today's `check` relies on the user to act on findings.
- **No change to `pkg/trunk/output`, `pkg/trunk/engine/security`, or the existing
  `check`-only `Output` dispatch cases (`sarif`, `eslint`, etc.)** -- this feature only
  adds two new `Output` values and one new `Event` field, both additive.

## Global constraints (carried into every task)

- No new `go.mod` dependencies (SHA-256 is stdlib `crypto/sha256`).
- Every test asserts exact values, never just "no error."
- `go build ./... && go vet ./... && go test ./... -count=1 -race` clean before every
  task is done.
- Commits signed (`-S`), commitlint conventions per `.agents/skills/git-commit/SKILL.md`
  (scope for `pkg/trunk/download` commits is `cache`, not `download` -- confirmed
  tonight; check `.commitlintrc.js`'s `scopes` list for any other package before
  guessing a scope name).
