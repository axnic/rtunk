# Inconsistencies to resolve

This is the refactor's decision log, not a freestanding bug tracker: every entry has been reviewed
and given a **Status**, so this document drives what the redesign actually does, not just what it
should reconsider. No file paths, package names, or code identifiers — the fix is expected to change
exactly those.

- **`accepted — implement`**: the direction below is the decided target; build it.
- **`decision — intentional divergence`**: rtunk deliberately does not (or will not) match upstream
  here; the entry records the decision and its rationale so it isn't re-litigated as a bug later.
- **`roadmap`**: already planned, tracked under a named milestone elsewhere; not re-scoped here.
- **`open question`**: a real trade-off with no decided answer yet; options are stated, not chosen.

Severity is one of **correctness** (wrong or missing behavior for a real, enabled configuration),
**compatibility** (a real divergence from the upstream tool this project tracks), or
**maintainability** (accepted complexity or dead surface with no behavioral bug today). An entry
marked **(inferred)** is a plausible, evidence-consistent consequence this review did not directly
execute/observe; everything else was verified against this project's own behavior and/or the real
plugin catalog.

## TODO summary

| # | Entry | Status | Severity |
| - | --- | --- | --- |
| 1 | Fix vs. formatter conflation | accepted — implement | correctness + compatibility |
| 2 | Finding-level autofixes and fix presentation text | accepted — implement | compatibility |
| 3 | Plain check does not surface formatting issues | decision — intentional divergence | compatibility |
| 4 | `check --fix` scope and the `--format-before-check` flag | accepted — implement | compatibility |
| 5 | Stdin/stdout-only formatters never run | accepted — implement | compatibility |
| 6 | Declared version range selects a command variant against the fully pinned tool version | accepted — implement | correctness |
| 7 | Declared platform restriction: Windows unsupported for now | decision — intentional divergence | correctness |
| 8 | Legacy single-command linter shape | accepted — implement (hard refusal) | compatibility |
| 9 | `deprecated` migration message never surfaced | accepted — implement (warn) | maintainability |
| 10 | Cache-root derivation inconsistency | accepted — implement (single root) | maintainability |
| 11 | Plugin-source registry never swept | accepted — implement (superseded by unified cache design) | maintainability |
| 12 | Trust-on-first-use downloads | roadmap (`rtunk.lock`) | correctness |
| 13 | One-process-only locking on a shared cache | accepted — implement (lock files) | maintainability |
| 14 | Duplicated variable substitution / runtime-shim resolution | accepted — implement | maintainability |
| 15 | Declared-but-inert catalog fields | accepted — implement | mixed (per field) |
| 16 | Action fetch category is redundant with runtime provisioning | accepted — implement | maintainability |
| 17 | Install events and file selection are deliberately not logged | decision — intentional | maintainability |
| 18 | File-change and schedule action triggers are refused, not run | decision — intentional divergence | compatibility |
| 19 | No-upstream file selection is narrower than "changes since the last commit" | accepted — implement | correctness |
| 20 | Checking outside a git repository with no paths should be a hard error | accepted — implement | correctness |

## Fix, formatter, and autofix semantics

### 1. Fix vs. formatter conflation

**Status**: accepted — implement.

**What**: "formatter" and "fix" are independent declared properties of a command, not one concept.
Confirmed as intentional: checking and formatting run through the **same execution pipeline** —
resolve, select commands, plan work, execute, normalize output — and this does not change. What
distinguishes a checking run from a formatting run, or a plain fix from a formatting pass, is purely
(a) which declared commands get selected into that one pipeline and (b) what happens with each
selected command's output (a finding to report, vs. a file to rewrite). A command that is a linter's
own autofix — in-place, rewriting, but *not* a formatter — needs a selection path of its own,
alongside "formatter" and "checking," within that same shared pipeline; see entries 2-4 below for
what that path is.

**Why it's a problem today**: users enabling a fix-only linter get no autofix at all, under any
command or flag combination, with no error or note explaining why — the command is simply never
selected by anything.

**Evidence**: in the real catalog, commands declared rewrite-output + in-place mostly also declare
themselves a formatter, but at least two real, currently-enableable linters declare an in-place fix
**without** the formatter property — one of them additionally carrying a confirmation-prompt string
and a named action verb for an interactive apply flow, properties that only make sense for a
non-formatter fix. Both linters' fix commands are excluded from every rtunk selection today.

**Direction**: model "fix" as its own selectable role within the shared pipeline, independent of
"formatter" — see entries 2 and 4 for the concrete selection and application design.

**Severity**: correctness + compatibility.

### 2. Finding-level autofixes and fix presentation text

**Status**: accepted — implement.

**What**: two related, currently-unused declared capabilities, both feeding into `check --fix` (entry
4): a **finding-level autofix** is a computer-applicable replacement a tool attaches directly to one
of its own findings, rather than through a separate fix command — the tool already computed exactly
what the fixed content should be and reports it alongside the finding itself. This is a real, common
shape: several linters (including eslint/ruff-style tools, for their auto-fixable rules) report
findings this way in their structured output. Separately, a fix **command** (the non-formatter kind
from entry 1) can declare a short human-readable prompt and an action verb meant for an interactive
"apply this fix?" flow (e.g. presenting "Quick fix available" with a `fix` verb next to the finding it
resolves).

**Why it's a problem**: findings that already carry a computer-applicable fix are reported as plain
findings, with rtunk unable to apply what it already received; and once fix commands are selectable
(entry 1), there is no declared UX text to present them with beyond a generic label.

**Evidence**: verified — the normalizer's decoding shape for the relevant structured output format has
no field for an inline fix/replacement; neither the prompt nor the verb field appears in any parsed
configuration shape, though both are real, present fields in the real catalog.

**Direction**: extend the normalized finding shape with an optional inline fix payload, decoded
wherever the underlying output format carries one; carry the prompt/verb fields through the resolved
command shape. `check --fix` (entry 4) applies both: a finding's own inline fix, and a fix command's
in-place rewrite, presenting the declared prompt/verb text where an interactive or reported apply
flow is in scope.

**Severity**: compatibility.

### 3. Plain check does not surface formatting issues

**Status**: decision — intentional divergence.

**What**: in the upstream tool, a checking run also runs every formatter and reports an unformatted
file as a normal, autofixable issue. rtunk's plain `check` deliberately does not: running every
formatter as part of every checking invocation was judged an annoying default (a formatting-only
concern surfacing as a "failure" on every check, for a repository that may not want formatting
enforced as a gating check at all). Plain `check` therefore only ever runs genuine checking commands.

**Why this is a decision, not a bug**: formatting-before-checking is still fully available — see
entry 4's `--format-before-check` flag, which reproduces exactly the upstream-equivalent "format,
then check" sequence, opt-in rather than default.

**Direction**: none needed beyond documenting the decision here, so it is not later "fixed" back into
matching upstream's default by mistake.

**Severity**: compatibility (a deliberate one).

### 4. `check --fix` scope and the `--format-before-check` flag

**Status**: accepted — implement.

**What**: `check --fix` is redefined to mean **linter fixes only**: every non-formatter fix command
(entry 1) plus every finding-level autofix (entry 2), applied to what the checking pass found, with a
second checking pass afterward reporting whatever remains unfixed. It never runs a formatter. A new
`--format-before-check` flag covers the previously-conflated case: run every formatter first, then
run the checking pass against the now-reformatted files — the "format, then check" sequence entry 3
opts out of by default.

**Today**: rtunk has only one such flag, named `--fix`, and it runs the *formatter* pass (today's
equivalent of `--format-before-check`) before checking — the inverse of the target's naming, and with
no linter-fix behavior (entries 1-2) available under any name.

**Direction**: implement linter-fix application (fix commands + finding-level autofixes) as
`check --fix`'s scope; move today's formatter-then-check behavior to `--format-before-check`.

**Severity**: compatibility.

### 5. Stdin/stdout-only formatters never run under any command

**Status**: accepted — implement.

**What**: a formatter command that is not in-place — it reads a file's content and writes the
reformatted result back through the process's own input/output streams, rather than rewriting the
file directly — has no execution path in rtunk today. This is a deliberate, already-recognized skip
in the current implementation, not a silent gap, but it is a real compatibility hole to close.

**Why it's a problem**: several real, currently-enableable formatters use exactly this shape and are
therefore permanently inert under rtunk today, with no message beyond a generic "unsupported" skip
note.

**Evidence**: at least eight real catalog linters declare a formatter command with no in-place flag
and a rewrite-class output — none of them can run today.

**Direction**: implement the stdin/stdout formatting shape (pipe the target's content in, write the
process's output back to the file) as a first-class execution mode alongside in-place rewriting.

**Severity**: compatibility.

## Command selection and gating

### 6. Declared version range selects a command variant against the fully pinned tool version

**Status**: accepted — implement.

**What**: rtunk resolves exactly one, fully pinned version for every enabled tool/runtime — never a
range — so that two people (or two machines) running the same configuration always run the identical
version and get identical results. This pinning model itself is not in question and is kept exactly
as-is: **a range is refused on the tool pin itself**, full pins only.

Separately, a command can declare the tool-version range *that command variant* applies to — a
different, existing field, evaluated against the tool version once it's already pinned, not a way to
loosen the pin. In the real catalog, this is used to give the same linter genuinely different
invocations for different tool major versions (a real, common shape: more than twenty
currently-enableable linters declare two or more same-named commands distinguished only by such a
range — well-known examples include two major-version-specific invocations of a widely used JS
linter, a widely used Python type checker, and a widely used Python linter/formatter). **Today, rtunk
parses this field into the resolved configuration and never reads it again — every declared variant
runs unconditionally**, which for a version range covering incompatible invocation syntax means at
least one variant fails outright on every run, and for two variants that both nominally succeed means
duplicate, conflicting findings.

**Direction**: evaluate each command's declared version range against the already-resolved, fully
pinned tool version at command-selection time, and run only the variant(s) whose range contains it.
This never touches the tool pin itself (which stays exact, per above) — it only narrows which
already-declared command variant applies to that pinned version.

**Severity**: correctness (high — affects a large, common fraction of the real catalog outright).

### 7. Declared platform restriction: Windows unsupported for now

**Status**: decision — intentional divergence.

**What**: rtunk does not support Windows as a host platform for now (untested, no Windows build). A
command declared restricted to a specific platform — in the real catalog, this is overwhelmingly a
Windows-only variant of a command, alongside a separate, unrestricted variant of the same name for
every other platform — should therefore be **discarded during configuration resolution** whenever it
does not match a supported host, not attempted.

**Today**: this field has no place in the resolved command shape at all — it is silently dropped
while parsing, not merely unread. Both the platform-restricted variant and its unrestricted
counterpart therefore run unconditionally, under the same command name, on every host. On the only
host platforms rtunk actually supports, that means invoking a Windows-specific command line (naming a
binary extension or invocation shape that only exists on Windows) alongside the correct one — a
guaranteed failure on every run for that command, for every affected linter.

**Evidence**: verified on both sides — no field exists on the resolved command shape to carry the
restriction, and the real catalog confirms the consequence: well over a dozen currently-enableable
linters declare a Windows-restricted command variant alongside an unrestricted one of the same name.

**Direction**: add the platform restriction to the resolved command shape and discard, at resolution,
any command variant whose restriction does not match a supported host — today that means discarding
every Windows-only variant unconditionally, since no host platform rtunk runs on is Windows.

**Severity**: correctness (high — a guaranteed per-run failure for every affected linter today).

### 8. Legacy single-command linter shape

**Status**: accepted — implement (hard refusal at validation).

**What**: a small number of real, currently-enableable linter ids — each explicitly marked deprecated
in favor of a replacement id — use an older declaration shape with a single type/command pair
directly on the linter, instead of the modern list of command definitions. Configuration validation
should **refuse** a project's configuration outright if it enables one of these, before any execution
starts, rather than silently resolving it to nothing.

**Today**: the resolved linter shape only has a place for the modern list form, so a project that
still enables one of these deprecated ids resolves to a linter with **zero** runnable commands. It
runs, matches files, reports success, and does nothing — no error, no skip note, no indication that
the linter never actually checked anything.

**Evidence**: verified against the real catalog — five currently-declared, deprecated linter ids use
exactly this legacy shape with no modern command list present at all.

**Direction**: detect the legacy shape during configuration validation and hard-refuse (configuration
error, not a warning) any enabled id that uses it, naming the replacement id from its own `deprecated`
message (entry 9) in the refusal.

**Severity**: compatibility.

### 9. `deprecated` migration message never surfaced

**Status**: accepted — implement (warning at validation).

**What**: a linter or command can carry a human-readable message explaining that it is deprecated and
naming its replacement. Configuration validation should surface this as a warning whenever an
enabled id carries it (and, per entry 8, as part of a hard refusal for the legacy-shape case
specifically).

**Today**: nothing reads this field; a user enabling a deprecated id gets no hint from rtunk that they
should migrate.

**Evidence**: the field is present in the real catalog on several ids; no parsed configuration shape
carries it through to anywhere a user-facing surface could read it.

**Direction**: carry the field through resolution and emit a warning during configuration validation
for every enabled id that carries it.

**Severity**: maintainability.

## Cache

### 10. Cache-root derivation inconsistency

**Status**: accepted — implement (single root).

**What**: the download cache and the plugin-source cache each independently derive "the cache root"
from the same override value, with different join conventions, so a custom cache directory produces
a different, undocumented on-disk shape than the default location. Full detail, the target design
(one shared root-resolution, every subtree derived from it), and the on-disk layout are in
[cache.md](./cache.md#single-cache-root).

**Severity**: maintainability (correctness-adjacent: no data loss, but a real, silent behavioral
difference based on a single flag).

### 11. Plugin-source registry never swept

**Status**: accepted — implement — superseded by the unified cache design, not fixed as a standalone
patch.

**What**: today, neither cache-cleanup command's scope reaches the plugin-source cache at all — it
survives a full wipe silently. The target's unified `cache clean` (full wipe of the whole root) and
`cache prune` (index-driven garbage collection across every subtree) both close this by construction,
since neither is scoped to "downloads only." See
[cache.md](./cache.md#repository-index-and-cache-prune).

**Severity**: maintainability.

### 12. Trust-on-first-use downloads

**Status**: roadmap — already planned as the `rtunk.lock` milestone (ROADMAP.md's "Download
integrity"); not re-scoped here.

**What**: every artifact fetch is verified against a content hash computed from the download itself,
but that hash is never checked against any independently-supplied value — the first fetch of a given
artifact is unconditionally trusted as authoritative. A source compromised at exactly the moment of a
user's first fetch of a given version would not be detected, and every subsequent fetch of that same
version then trusts the compromised content indefinitely.

**Direction**: a version-pinned checksum ledger, checked in alongside the project's own configuration,
so a mismatch on fetch becomes a hard, blocking error instead of a silent first-trust — already the
scoped design of the named roadmap milestone.

**Severity**: correctness (security-adjacent; no known exploit, but the trust model has no
independent check by design today).

### 13. One-process-only locking on a shared, multi-process cache

**Status**: accepted — implement (lock files).

**What**: today, every coordination mechanism protecting the cache from concurrent writers is scoped
to one running process's own in-memory state; two entirely separate processes racing to populate the
same cache entry are not coordinated at all beyond the atomic-publish guarantee each write already
has on its own (which prevents corruption, not duplicated work). The target's per-target,
exclusive-create lock file, recording the owning process id and triggering repository and fail-fast
on contention, is a real cross-process fix — a lock whose recorded process id is no longer alive is
detected as stale and cleared automatically, rather than blocking forever. See
[cache.md](./cache.md#locking), including the accepted pid-liveness limitation (a reused pid, or a
lock seen from a different host over a shared network filesystem, can't be checked for liveness).

**Severity**: maintainability (no correctness bug given atomic publish today, but a real scaling limit
on concurrent-process usage that the lock-file design removes).

## Duplicated logic

### 14. Duplicated variable substitution and runtime-shim resolution

**Status**: partially implemented (v0.11 shared-provisioning plan) — runtime-shim resolution and
quote helpers unified; the two callers' variable-substitution tables remain separate on purpose
(they support different, non-overlapping variable sets).

**What**: the execution engine and the action runner each independently implement invocation-template
variable substitution (including basic shell-quoting helpers) and runtime-shim-directory resolution,
rather than sharing one implementation of either.

**Why it's a problem**: the two implementations can (and do) support different variable vocabularies
and different edge-case handling by accident of separate authorship rather than deliberate design,
and every future variable or quoting fix has to be applied twice, with no structural guarantee both
copies stay in sync.

**Evidence**: verified — both variable substitution and runtime-shim resolution exist as separate,
independently-implemented functions in the execution engine and in the action runner, each with its
own shell-quoting helpers.

**Direction**: one shared implementation of invocation-template substitution and one of runtime-shim
resolution, used by both the execution engine and the action runner.

**Severity**: maintainability.

## Declared-but-inert catalog fields

**Status**: accepted — implement (each field below, to its stated intended behavior). `run_timeout`,
`disable_upstream`, `prepare_run`, `max_concurrency` (v0.11 catalog-fidelity-execution plan), and
`suggest_if`, `is_security`, `health_checks`, `extra_packages` (v0.11 catalog-fidelity —
installs/suggestions plan) are all done. `output_type` (action-level, the one remaining field from
the original survey) is not part of either plan or of ROADMAP v0.11's own checklist — left as its
own future follow-up.

Beyond the fields already covered above (version range, platform restriction, fix prompt/verb, and
the two caching opt-outs), the real catalog declares a further set of fields on commands, tools, and
actions that the resolved configuration shape either drops entirely while parsing or parses and then
never reads again. See
[plugin-model.md](./plugin-model.md#declared-fields-the-current-execution-engine-does-not-act-on) for
the full field-by-field index with catalog occurrence counts.

- **`suggest_if` (linter-level, the single most common of these fields in the real catalog)**:
  intended behavior — drive a "suggested linters" surface (e.g. onboarding/listing output) that
  recommends enabling a matching linter when its declared condition holds. **Implemented (v0.11
  catalog-fidelity — installs/suggestions plan)** — `rtunk linters list`'s Available bucket now
  honors all 3 real values (`files_present`, `config_present` via `DirectConfigs` presence, `never`);
  unset keeps the pre-existing files-matched default for backward compatibility.
- **`run_timeout` (linter-level)**: intended behavior — enforce a maximum run time for that linter's
  commands, failing the invocation if exceeded, instead of relying only on whatever general execution
  timeout (if any) applies uniformly. **Implemented (v0.11 catalog-fidelity-execution plan)** — wraps
  `runBatch`'s context with `context.WithTimeout` when set; a killed/timed-out invocation now also
  correctly surfaces as a `Failed` event on every command shape, not only the stdin-formatter path.
- **`is_security` (command-level, common across the real catalog's security/vulnerability scanners)**:
  intended behavior — tag that command's findings as security-category, so filtering/display can
  distinguish them from an ordinary linter's findings. **Implemented (v0.11 catalog-fidelity —
  installs/suggestions plan)** — `output.ApplyIsSecurity` (mirrors `ApplyIssueURL`'s post-parse
  shape) tags every finding; surfaced as a `[security]` marker in human output, a `security` field
  in JSON, and a `properties.tags: ["security"]` on the matching rule in SARIF.
- **`disable_upstream` (command-level)**: intended behavior — when both an overlap-marked command and
  the generic linter it supersedes are enabled, suppress the superseded one's duplicate findings on
  the same signal. **Implemented (v0.11 catalog-fidelity-execution plan)** — modeled as `[]string` on
  `Command`; whole-linter suppression in `drainEvents`, gated on both linters being enabled and the
  superseding linter having actually produced ≥1 finding of its own (not suppressed merely by being
  enabled). Real catalog shape still unconfirmed — this is the plan's own best-effort interpretation,
  documented as such at the time.
- **`prepare_run` (command-level)**: intended behavior — run a declared one-time/per-run setup
  invocation before the command itself, for tools that need initialization (e.g. a plugin-download
  step) before they can run correctly. **Implemented (v0.11 catalog-fidelity-execution plan)** — runs
  once per (linter,command) pair per `engine.Run` call via the existing invocation machinery; a
  failing setup fails every job for that command, including ones blocked concurrently on the same
  setup call. Real catalog shape still unconfirmed, same caveat as above.
- **`stdin` (command-level)**: intended behavior — feed a command's target content via standard input
  rather than a path argument; the main real-world consequence today is entry 5's stdin/stdout
  formatters, already covered there. **(covered by entry 5)**
- **`max_concurrency` (command-level)**: intended behavior — cap concurrent invocations of that
  specific command, independent of the run's overall worker count, for tools that cannot safely run
  many instances in parallel (e.g. sharing a lock file or a local daemon). **Implemented (v0.11
  catalog-fidelity-execution plan)** — a per-(linter,command) buffered-channel semaphore, acquired via
  `select`/`ctx.Done()` so run-level cancellation is still honored while queued; different commands on
  the same linter are capped fully independently of each other.
- **`health_checks` (tool-level)**: intended behavior — proactively verify an installed tool actually
  works (run a declared version-check invocation) as part of provisioning it, instead of only
  discovering a broken install when a linter command using it fails at run time. **Implemented
  (v0.11 catalog-fidelity — installs/suggestions plan)** — runs once per tool per `engine.Run` call,
  right after its shim is resolved and before any linter referencing it can use it; a non-zero exit
  fails resolution for every linter sharing that tool, not just whichever one triggered the check.
- **`extra_packages` (tool-level)**: intended behavior — install declared companion packages alongside
  a tool's main package as part of provisioning it. **Implemented (v0.11 catalog-fidelity —
  installs/suggestions plan)** — every one of the 6 runtime package installers now installs the main
  package and every extra (each independently version-pinned, `name@version`) into one shared
  install tree via a single atomic `Finalize`, avoiding a real landmine (`install.Finalize`'s
  rename-based caching would otherwise silently no-op a second install into an already-finalized
  directory).
- **`output_type` (action-level)**: present on a small number of actions in the real catalog; not
  investigated further — flagged for completeness, intended behavior unconfirmed. **(inferred,
  unconfirmed)**

## Miscellaneous

### 16. Action fetch category is redundant with runtime provisioning

**Status**: accepted — implemented (v0.11 shared-provisioning plan).

**What**: fetching "for an action" is modeled today as its own fetch-target category, alongside tool,
runtime, and linter — but an action has no download of its own; provisioning it is entirely a matter
of provisioning whatever runtime it declares (or nothing, if it declares none). A separate action
category adds a fetch-target kind that does no fetching of its own and only ever delegates to the
runtime category.

**Direction**: drop the action fetch-target category; provisioning an action is exactly "provision
its declared runtime, if any" using the runtime category directly.

**Severity**: maintainability.

### 17. Install events and file selection are deliberately not logged

**Status**: decision — intentional.

**What**: the run journal records, per command invocation, its argv/cwd/PATH prefix, its raw output,
its exit code, its output-parser sub-invocation (if any), the findings that resulted, and each
linter's own end state — plus a run-level start/end marker. It deliberately does **not** record which
files were selected for the run, or anything about provisioning (a tool/runtime already cached vs.
freshly fetched, fetch progress, a fetch failure): the journal's scope is the checking/formatting
commands actually run and what they produced, not everything that led up to running them.

**Direction**: none — this is the recorded scope decision, not a gap to close. If provisioning or
file-selection visibility is ever needed, it belongs in a separate, purpose-built mechanism rather
than an expansion of this journal's own scope.

**Severity**: maintainability (recorded as a decision so it is not mistaken for an oversight later).

### 18. File-change and schedule action triggers are refused, not run

**Status**: decision — intentional divergence.

**What**: an action can declare a file-change trigger or a periodic-schedule trigger as an
alternative to a git-hook trigger. Both are refused by design: either one would require rtunk to run
as a background daemon watching for file changes or waiting out an interval, and rtunk refuses a
daemon mode entirely — every invocation is a single, foreground, exit-when-done run. This is a
deliberate scope boundary, not a missing feature for either trigger kind, and applies to both
identically (there is no design rationale that treats one differently from the other).

**Target**: configuration validation refuses an action whose triggers rely on a file-change or
schedule trigger. The simplest, decided rule: **any trigger kind rtunk does not support makes that
action's configuration invalid**, refused at validation — it does not matter whether the action
declares only unsupported trigger kinds or mixes a supported git-hook trigger alongside an
unsupported one; declaring an unsupported trigger kind at all is refused.

**Today**: neither trigger kind is refused — both are silently parsed and then never consulted by
anything (verified directly: the query that resolves "which actions fire on a given trigger" only
ever matches the git-hook trigger kind), so an action relying on either quietly never fires, with no
indication why.

**Severity**: compatibility.

## File selection

### 19. No-upstream file selection is narrower than "changes since the last commit"

**Status**: accepted — implement.

**What**: inside a git repository with no default file selection input, and no upstream configured
for the current branch, the target selection is precisely: every file with a staged change, every
file with an unstaged change, and every untracked, non-ignored file — i.e. everything that differs
from the last commit, tracked or not, staged or not.

**Today**: this fallback selects **staged changes only**, verified directly — narrower than the
target, since it misses unstaged changes to already-tracked files and any new, untracked file.

**Direction**: widen the no-upstream fallback to the precise definition above, consistent with the
upstream-present case's own shape (a diff plus untracked files), just measured against the last
commit instead of a merge-base.

**Severity**: correctness (a real file silently excluded from the default selection is a real
checking/formatting gap, not merely a documentation mismatch).

### 20. Checking outside a git repository with no paths should be a hard error, not a silent no-op

**Status**: accepted — implement.

**What**: outside a git repository, with no explicit paths given, there is no default file selection
— by design (see flows.md's "File selection"). The target makes this an explicit, actionable
validation error: exit non-zero with a message explaining that explicit paths are required outside a
git repository.

**Today**: this case exits **0** with a quiet "no files to process" message — indistinguishable, from
an exit code alone, from "ran successfully and found nothing to do."

**Direction**: detect "outside a git repository, no paths given" specifically and fail with a message
naming the requirement, rather than falling through to the same empty-selection path a legitimately
empty git-based selection also takes.

**Severity**: correctness (a scriptable/CI caller cannot currently distinguish "nothing to check" from
"this invocation was missing required arguments").
