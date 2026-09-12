# Check Engine Refactor — `engine`/`output` Extraction + `parse_regex` — Design Spec

## Goal

Before continuing feature work (`fmt`, v0.4), extract the parts of `pkg/trunk/check` that are not
actually check-specific into packages `fmt` can reuse without duplication, and fix a real
path-traversal gap found while doing so. Four changes, one cohesive refactor:

1. Move the job-queue/worker-pool execution engine (currently `check/run.go`) and file matching
   (`check/match.go`) into a new `pkg/trunk/engine` package, generic over "check" vs. a future
   "fmt" by taking a command-filter predicate rather than hardcoding `Formatter: false`.
2. Move `RunFrom`/`SandboxType` resolution (`check/runfrom.go`, `check/sandbox.go`) into
   `pkg/trunk/engine/security`, and hardened with adversarial tests — this is where a real
   path-traversal gap was found (below).
3. Move output-format parsing (`check/parse.go`, one 664-line file) into `pkg/trunk/output`, one
   file per parser, and replace `ParsePerlCritic` plus v0.3.1's best-effort generic regex parser
   with a single declarative parser driven by a `Command.ParseRegex` field the real trunk-io
   catalog has always carried and rtunk has always silently discarded.
4. Replace the growing parameter lists (`cfg`, `repoRoot`, `cacheDir`, ...) threaded through every
   function with a small `engine.Env` struct, and thread a real `context.Context` through for
   cancellation — `exec.CommandContext` kills an in-flight linter subprocess the moment the
   context is canceled, so this is "quick stop mid-work" via the stdlib mechanism built for it,
   not a bespoke one.

`pkg/trunk/check` becomes a thin adapter: `check.Run` calls `engine.Run` with a predicate that
selects non-formatter commands, plus whatever check-specific glue (config editing for
`enable`/`disable`, already in `internal/cli/check.go`) stays where it is. A future `pkg/trunk/fmt`
becomes an equally thin sibling with a `Formatter: true` predicate.

## Why now, not incrementally

v0.3/v0.3.1/v0.3.2 all landed inside `pkg/trunk/check` because there was only one consumer. Now
that `fmt` (ROADMAP v0.4) is next and needs the identical job-queue/RunFrom/SandboxType machinery,
extracting it before adding a second consumer avoids copy-pasting ~1,600 lines and then having to
reconcile two copies' bugfixes later.

## Package layout

```
pkg/trunk/engine/
    engine.go          -- Env, Phase, Event, job, linterState-equivalent, Run(ctx, env, filter, ...)
    engine_test.go
    match.go            -- Files() and its helpers (moved verbatim from check/match.go)
    match_test.go
    security/
        runfrom.go       -- resolveRunFrom (moved from check/runfrom.go)
        runfrom_test.go
        sandbox.go       -- stageSandbox, remapFindings (moved from check/sandbox.go)
        sandbox_test.go

pkg/trunk/output/
    finding.go          -- Finding, ApplyIssueURL
    sarif.go            -- ParseSARIF (sarif + sarif_uri)
    pass_fail.go         -- ParsePassFail
    regex.go             -- NEW: ParseFromRegex(pattern, data, linter) -- replaces ParsePerlCritic
                            and v0.3.1's ParseGenericRegex entirely
    taplo.go             -- ParseTaplo (still bespoke -- Output: "taplo", not "regex", no parse_regex)
    actionlint.go, bandit.go, buildifier.go, cfnlint.go, eslint.go, hadolint.go,
    haml_lint.go, markdownlint.go, pylint.go, rubocop.go, stylelint.go
    (each with its _test.go alongside, same fixtures as today -- pure move + split, no behavior
    change for these 11)

pkg/trunk/check/
    check.go            -- Run(ctx, env) (<-chan engine.Event, error): calls
                            engine.Run(ctx, env, func(c config.Command) bool { return !c.Formatter })
                            -- the entire "check-specific" surface left in this package
```

`pkg/trunk/config` gains one field: `Command.ParseRegex string \`yaml:"parse_regex,omitempty"\`` .

## The `parse_regex` discovery

Every real trunk-io `Output: "regex"` command's `plugin.yaml` carries a `parse_regex` field: a
single regular expression with **named capture groups** (`path`, `line`, `col`, `code`, `message`,
sometimes `severity`) that is the tool's actual output shape. `config.Command` never had a field to
receive it, so `yaml.v3` silently dropped it on every unmarshal since v0.1 -- confirmed two ways:

- Grepping the real cached plugin repo clone
  (`~/.cache/trunk/plugins/https---github-com-trunk-io-plugins/v1.11.0-.../linters/*/plugin.yaml`)
  for `parse_regex` under a command (not `version_command`, which is unrelated -- that one extracts
  a tool's own version string) finds it on **every single** real `Output: "regex"` command: 15
  commands across 14 linters -- `perlcritic`, `markdownlint-cli2`, `vale`, `rumdl`, `yamllint`, `git-diff-check`,
  `swiftlint`, `squawk` (both commands), `biome`, `rome`, `djlint`, `dustilock`, `stringslint`,
  `ty` -- exactly the full set v0.3.1's own scan already enumerated as "regex"-output linters.
- `go run ./cmd/rtunk/main.go config print --all --output json | jq -r '[.lint.definitions[].commands[]? | keys[]] | unique | .[]'`
  against this repo's own real resolved config confirms `parse_regex` is absent from the current
  schema's captured keys entirely.

This means v0.3.1's whole "best-effort generic regex parser, with documented false-negative
limitations for biome/rome/djlint/ty/dustilock/stringslint" was working around self-inflicted data
loss. `ParsePerlCritic` existing as bespoke Go code was likewise never necessary: its hand-written
regex is a byte-for-byte transcription of perlcritic's own `parse_regex` value, not a
reverse-engineered native format like `ParseSARIF`/`ParseESLint` genuinely are.

**Fix:** add `Command.ParseRegex` to the config schema. Replace `ParsePerlCritic` and
`ParseGenericRegex` with one function:

```go
// ParseFromRegex parses data using pattern's named capture groups, straight from the real
// trunk-io catalog's own Command.ParseRegex field -- the same mechanism trunk's own closed-source
// engine uses for every "regex"-output linter. A fixed name->Finding-field table does the mapping
// (path->File, line->Line, col->Column, code->RuleID, message->Message, severity->Severity) --
// nothing else, no linter-specific logic anywhere in this function. A name the table has no entry
// for is simply not looked up: it isn't mapped to anything, on principle, not detected and
// aliased to its closest match. A table entry whose group is absent from a given pattern (e.g. no
// severity, no col) leaves that Finding field zero-valued, the same convention every other parser
// in this project already uses.
func ParseFromRegex(pattern string, data []byte, linter string) ([]Finding, error)
```

All 14 distinct `parse_regex` values (15 commands -- squawk's two commands share one identical
pattern) use Go-compatible RE2 named-group syntax (`(?P<name>...)`) already -- verified by
test-compiling every one of them directly against Go's `regexp` package during this spec's
research (all 14 compile cleanly). Several (`biome`, `rome`) embed a literal `\n` inside the
pattern to span the multi-line "code frame" shape those tools print -- this works directly with
Go's `regexp.FindAllStringSubmatch` without any special-casing, closing v0.3.1's documented false
negatives for those tools as a side effect, not a separate fix. One thing this deliberately does
NOT special-case: `ty`'s pattern names its column group `column`, not `col` like every other
pattern -- `column` has no entry in the mapping table, so it stays unmapped and `ty`'s findings
have no `Column`. This is not treated as a bug to fix with a `col`-or-`column` alias: guessing that
`column` "must mean" the same thing as `col` is exactly the kind of per-linter judgment call this
parser exists to avoid making. If `ty`'s own `parse_regex` doesn't match this project's table,
that's a fact about `ty`'s catalog entry, not something this parser corrects for.

`taplo` keeps its own bespoke parser: its real `Output` value is `"taplo"`, not `"regex"`, and it
carries no `parse_regex` field at all in the real catalog -- it is a genuine one-off, unlike
perlcritic.

### Corrections from trunk's own official `regex` documentation

The user supplied trunk.io's real docs for the `regex` output type (`docs.trunk.io`) during this
spec's review. Three corrections against them:

- **`path` is documented as required**; `line`/`col`/`severity`/`code`/`message` are all optional
  -- matches this spec's existing "absent group leaves that Finding field zero-valued" rule with no
  change needed, but confirms `path`'s absence from a pattern is a config error, not a normal case.
- **`severity`'s real value set is 8 values**: `note`, `notice`, `allow`, `deny`, `disabled`,
  `error`, `info`, `warning` -- not the 3 (`error`/`warning`/`info`) `Finding.Severity`'s existing
  doc comment claims. This project's existing convention (every current parser normalizes its
  tool's own severity vocabulary down to those 3 via a dedicated `xSeverity()` helper --
  `sarifSeverity`, `banditSeverity`, etc.) is a prior, deliberate design decision this refactor
  doesn't revisit. `ParseFromRegex` gets its own `regexSeverity()` helper, same shape as the
  others, mapping trunk's 8 real values down to the existing 3 -- exact mapping is an
  implementation detail for the plan, not this spec.
- **Trunk's own regex engine allows a pattern to repeat the same named group** (picking whichever
  branch's capture is non-empty; two non-empty captures is a trunk-side error). Go's `regexp`
  (RE2) does not support duplicate named capture groups in one pattern at all --
  `regexp.Compile` fails outright. None of the 14 real `parse_regex` values in the catalog today
  use this (confirmed: all 14 have exactly one capture per name), so this is a known, narrow,
  currently-unreachable limitation, not something to build support for now: a hypothetical future
  catalog entry using it would fail to compile with a clear Go error, surfacing as that command
  failing outright rather than silently misparsing -- an acceptable failure mode to leave
  undocumented-away rather than engineer around speculatively.
- Trunk documents two more output types this project has never seen mentioned in its own
  catalog research: `lsp_json` and `arcanist`. Checked: zero real linters in the current catalog
  use either (`grep -rl "output: lsp_json\|output: arcanist"` across every real `plugin.yaml`
  finds nothing) -- no follow-up plan needed for either, unlike the actual "second wave" gaps
  (`ansible_lint`, `brakeman`, `buf`, etc.) that already have real linters waiting on them.

## `engine.Env` and `context.Context`

```go
// Env is every piece of shared configuration a job needs to run -- the thing every function in
// this package used to receive as 3-5 separate parameters.
type Env struct {
    Cfg         config.Config
    RepoRoot    string // always absolute -- see "Path validation" below
    CacheDir    string
    Concurrency int // workers; callers should default this to runtime.NumCPU() before passing it in
}
```

`context.Context` is **not** used to carry `Env` (via `context.WithValue`) -- the stdlib's own
guidance is explicit that context values are for request-scoped data transiting API boundaries
(tracing IDs, deadlines), not for a function's actual required dependencies, which belong in an
explicit parameter so the signature says what the function needs. `Env` is a plain parameter;
`ctx context.Context` is a separate, first parameter, used only for cancellation:

```go
func Run(ctx context.Context, env Env, paths []string, include func(config.Command) bool) (<-chan Event, error)
```

`runOneInvocation`'s `exec.Command` becomes `exec.CommandContext(ctx, ...)`: canceling `ctx` kills
an in-flight linter subprocess immediately, for free -- no bespoke stop channel or polling loop.
Each worker goroutine also checks `ctx.Err()` before picking up its next queued job, so cancellation
also stops new work from starting, not just kills whatever's already running.

## The check/fmt filter predicate

`buildJobs`'s current `if cmd.Formatter { continue }` becomes a parameter:

```go
func Run(ctx context.Context, env Env, paths []string, include func(config.Command) bool) (<-chan Event, error)
```

`pkg/trunk/check`'s `Run` calls `engine.Run(ctx, env, paths, func(c config.Command) bool { return !c.Formatter })`.
A future `pkg/trunk/fmt` calls the same `engine.Run` with `func(c config.Command) bool { return c.Formatter }`.
Every other check-specific behavior (Output-format dispatch, Finding-based reporting) is
unaffected by this predicate -- it only decides which commands are even considered.

## Security: path-traversal gap in sandbox staging

**The gap (found during this spec's research, not introduced by it -- lives in the already-shipped
v0.3.2 code):** `stageSandbox`/`copySandboxFile` computes `dst := filepath.Join(sandboxDir, rel)`
where `rel` comes from `filepath.Rel(resolvedDir, target)`. Nothing today guarantees `target` is
actually a descendant of `repoRoot` -- `rtunk check /etc` (or any out-of-repo path a user or wrapper
script passes as a CLI argument) produces a `target` outside `repoRoot`, so `resolveRunFrom`'s
empty-`RunFrom` case (`return repoRoot, true`) combined with `filepath.Rel(repoRoot, target)`
yields a `rel` starting with `../`. `filepath.Join(sandboxDir, "../../etc/passwd")` collapses that
`..` and can write **outside** the sandbox's own temp directory entirely, on any enabled linter
with `SandboxType: copy_targets` or `expanded`.

**Fix, defense in depth (both, not either/or):**

1. **Reject at the boundary, in `Files()`.** `Files()` already receives every path argument before
   walking it; it validates each one is inside `repoRoot` (after `filepath.Abs`, matching how
   `engine.Run` already absolutizes both) before matching against it, returning a clear error for
   one that isn't -- not silently accepted and only failing later, deep inside sandbox staging.
2. **Guard `copySandboxFile` independently.** Even with (1) in place, `copySandboxFile` verifies
   the computed `dst` is genuinely still inside `sandboxDir` (e.g. `filepath.Rel(sandboxDir, dst)`
   must not start with `..` and must not be absolute) before ever opening it for writing -- so a
   future bug anywhere upstream of this function (a new `RunFrom` form added later, a config-parsing
   edge case) can't turn into an arbitrary-file-write by itself. This is the security package's
   own responsibility, not something it should have to trust every caller to have gotten right.

## Testing approach: hardening `engine/security`

Beyond the functional tests `runfrom_test.go`/`sandbox_test.go` already have (moved verbatim), add
adversarial coverage specifically for the security package:

- **Path traversal:** a target file outside `repoRoot` must be rejected before it ever reaches
  `stageSandbox` (boundary test), AND `copySandboxFile` must independently refuse to write outside
  `sandboxDir` even when handed a `rel` that maliciously contains `..` components (unit test on
  `copySandboxFile` directly, bypassing the boundary check to prove the inner guard holds alone).
- **Symlink handling:** a symlinked directory inside the walked tree must not let `Files()` or
  `resolveRunFrom`'s directory walk escape `repoRoot` -- `filepath.WalkDir` (used by `Files()`)
  already does not follow symlinks into directories by default; add a regression test proving this
  explicitly (a symlink pointing outside repoRoot, present in the walked tree, must not be
  descended into or matched).
- **Resource bounds on `expanded` staging:** `stageSandbox`'s `"expanded"` mode copies every
  regular file directly in a resolved directory -- add a test with an unusually large number of
  files (e.g. 500) in one directory to confirm no pathological behavior (no quadratic blowup, no
  silent truncation) rather than leaving this untested at today's small scale.
- **Symlinked files as targets:** a matched target that is itself a symlink (not a directory
  symlink) -- confirm `copySandboxFile` copies the symlink's _content_ (via `os.Open`, which
  follows the link) rather than the tool ending up executing against a dangling or
  attacker-controlled link target outside the repo.

## Non-goals

- No behavior change for the 172 already-working real trunk-io commands, or the 9/10 that v0.3.2
  unlocked -- this is a structural refactor plus one security fix and one data-loss fix, not new
  linter coverage.
- `${compile_command}` and the literal `RunFrom: "apps"` remain unsupported, unchanged from v0.3.2.
- No change to `internal/cli/check.go`'s CLI surface (flags, output format) beyond updated import
  paths for the moved/renamed types.
- `pkg/trunk/fmt` itself is not built in this refactor -- only the shared foundation it will sit on.

## Global constraints (carried into every task)

- No new `go.mod` dependencies.
- Every test asserts exact values, never just "no error."
- `go build ./... && go vet ./... && go test ./... -count=1 -race` clean before every task is done.
- Every moved file keeps its existing tests passing verbatim (import-path updates aside) -- this is
  a refactor, and a test that needs behavioral changes to keep passing is a signal something moved
  wrong, not just relocated.
- Commits signed (`-S`), commitlint conventions per `.agents/skills/git-commit/SKILL.md`.
