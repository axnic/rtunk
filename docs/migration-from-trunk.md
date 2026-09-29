# Migration guide from trunk

For a team already running trunk's Code Quality CLI, this document lists exactly what stays the
same and what changes when switching a repository to `rtunk`, so nothing about the switch is a
surprise discovered in CI. It does not explain how to use `rtunk` itself — see
[commands.md](./commands.md) and [configuration.md](./configuration.md) for that.

## What carries over unchanged

Three things travel from trunk to `rtunk` without translation (`AGENTS.md`'s "How it relates to
trunk" section):

- **Configuration.** `rtunk` understands `.trunk/trunk.yaml` as-is — no rewrite is required to
  start. `rtunk`'s own native config file, `.rtunk/rtunk.yaml`, is modeled closely on trunk's
  schema and semantics, so moving to it later is a rename plus whatever new, rtunk-only keys you
  choose to add, not a rewrite. See [configuration.md](./configuration.md#config-file-discovery)
  for the exact discovery and precedence rules.
- **Ignore comments.** `trunk-ignore(...)`, `trunk-ignore-all(...)`, and the `-begin`/`-end` block
  form are permanent, fully equivalent aliases of `rtunk`'s own `rtunk-ignore` directives — the
  same matching regex accepts either prefix. There is no deadline to rewrite existing
  `trunk-ignore` comments. See
  [configuration.md](./configuration.md#ignore-comment-syntax) for every form and its matching
  rules.
- **The plugin ecosystem.** `rtunk` consumes [trunk-io/plugins](https://github.com/trunk-io/plugins)
  directly — the same linter/tool/runtime/action definitions trunk itself reads, not a
  reimplementation of linter metadata. A `plugins.sources` entry pointing at that repository (or a
  fork of it) works unchanged.

Compatibility is scoped to configuration only: cache layout, the CLI surface beyond the commands
trunk and `rtunk` both have, and output rendering carry no compatibility constraint and are not
covered by the guarantee above.

## Behavioral differences to expect

Items 1 and 3–5 are [ROADMAP.md](../ROADMAP.md#deliberate-divergences-from-trunk)'s own closed
list of deliberate divergences — recorded there so none of them is ever mistaken for an open bug.
Item 2 is not a separate ROADMAP entry; it is the current, already-implemented flag-level
consequence of item 1's decision, surfaced here because it changes how an existing `trunk check
--fix` invocation needs to be rewritten. None of the five is something `rtunk` is still working
toward matching trunk on — all are shipped, current behavior. Full rationale for each is in
[docs/architecture/inconsistencies.md](./architecture/inconsistencies.md).

### 1. Plain `check` does not run formatters

- **trunk**: a `check` run also runs every enabled formatter and reports an unformatted file as a
  normal, autofixable finding.
- **rtunk**: plain `check` only ever runs genuine checking commands; formatting is never a side
  effect of `check` by default.
- **Why**: running every formatter on every checking invocation was judged an annoying default — a
  formatting-only concern surfacing as a failure on every check, for a repository that may not
  want formatting enforced as a gate at all
  ([inconsistencies.md](./architecture/inconsistencies.md#3-plain-check-does-not-surface-formatting-issues)).
- **To reproduce trunk's default behavior**: pass `--format-before-check`, which runs every
  enabled formatter first, then checks the reformatted files — see item 2 below.

### 2. `check --fix`'s scope is narrower than trunk's, under a different flag split

- **trunk**: `check --fix` runs the "format, then check" sequence — every formatter, then the
  checking pass — and applies linter fixes.
- **rtunk**: `check --fix` (`-y`) applies linter-level fixes only — every non-formatter fix
  command plus every finding's own attached autofix — then reports whatever remains; it never
  runs a formatter. The combined "format, then check" sequence trunk's `--fix` performs is a
  separate, opt-in flag: `--format-before-check`. Given together, `--format-before-check` and
  `--fix` compose in order: format pass, checking pass, fix application, final checking pass.
- **Why**: this is the direct consequence of item 1's decision — since plain `check` no longer
  formats by default, `--fix`'s formatting half moved to its own explicit flag rather than staying
  bundled under one name with two different meanings
  ([inconsistencies.md](./architecture/inconsistencies.md#4-check---fix-scope-and-the---format-before-check-flag)).
- **Migration impact**: a CI script or pre-commit hook that relies on `trunk check --fix`
  reformatting files needs `rtunk check --fix --format-before-check` instead, or two separate
  invocations (`rtunk fmt` then `rtunk check --fix`) — see [commands.md](./commands.md#rtunk-check)
  for the full flag reference.

### 3. Windows is not a supported host platform

- **trunk**: ships and supports Windows as a host platform.
- **rtunk**: does not — there is no Windows build. A command variant declared Windows-only in the
  plugin catalog is skipped during configuration resolution rather than attempted, so an
  otherwise-working config does not fail on a Windows-restricted command; it simply never runs
  that variant. macOS and Linux are the two supported host platforms
  (see [installation.md](./installation.md#supported-platforms)).
- **Why**: no rtunk build for Windows exists today
  ([inconsistencies.md](./architecture/inconsistencies.md#7-declared-platform-restriction-windows-unsupported-for-now)).
- **Migration impact**: a team running trunk on Windows hosts (developer machines or Windows CI
  runners) cannot switch those hosts to `rtunk` yet.

### 4. The run journal doesn't log file selection or provisioning

- **trunk**: its equivalent run history/journal records more context around each run, including
  what led up to running the recorded commands.
- **rtunk**: the run journal (`rtunk logs`) records, per command invocation, its argv/cwd/PATH
  prefix, raw output, exit code, output-parser sub-invocation, and resulting findings, plus a
  run-level start/end marker. It deliberately does **not** record which files were selected for
  the run, or anything about provisioning (a tool/runtime already cached versus freshly fetched,
  fetch progress, a fetch failure).
- **Why**: the journal's scope is the checking/formatting commands actually run and what they
  produced, not everything that led up to running them; if file-selection or provisioning
  visibility is ever needed, it belongs in a separate, purpose-built mechanism rather than an
  expansion of this journal
  ([inconsistencies.md](./architecture/inconsistencies.md#17-install-events-and-file-selection-are-deliberately-not-logged)).
- **Migration impact**: a workflow that inspects trunk's run history to see which files a given
  run considered, or to debug a provisioning/download failure, cannot do the same through
  `rtunk logs`.

### 5. Actions can't trigger on file changes or a schedule

- **trunk**: an action can declare a file-change trigger or a periodic-schedule trigger, alongside
  git-hook triggers.
- **rtunk**: refuses both. An action whose configuration relies on a file-change or schedule
  trigger — even mixed alongside a supported git-hook trigger — is rejected outright at
  configuration validation, not silently accepted and never fired.
- **Why**: both trigger kinds require a background process watching for changes or waiting out an
  interval, and `rtunk` refuses to run as a daemon under any circumstance — every invocation is a
  single, foreground, exit-when-done run
  ([inconsistencies.md](./architecture/inconsistencies.md#18-file-change-and-schedule-action-triggers-are-refused-not-run)).
- **Migration impact**: an action configuration that relies on either trigger kind needs an
  external scheduler (cron, a CI schedule trigger, a file-watcher wrapper) invoking `rtunk actions
run`/`rtunk check` instead — `rtunk` will not do the watching or waiting itself.

## Not a difference: exit codes

`rtunk`'s exit codes for `check`, `fmt`, and `run` are identical to trunk's: `0` on success or on a
run that found nothing to report, `1` on any finding, error, invalid config, nonexistent path, or
failed tool invocation. Only `0` and `1` exist — there is no separate code distinguishing
"findings" from "error." A `fmt` that fixes files exits `0`, not an error. A CI pipeline or
pre-commit hook gating on trunk's exit code needs no change here; this is called out explicitly so
a migrating team does not spend time re-verifying something that already matches.

## Switching a repository

1. **Point CI and pre-commit hooks at the `rtunk` binary instead of `trunk`.** Same invocation
   shape for the commands both tools share (`check`, `fmt`); adjust any script that depended on
   item 2's flag split above.
2. **Leave `.trunk/trunk.yaml` in place, or move it to `.rtunk/rtunk.yaml`.** Both work: `rtunk`
   reads `.trunk/trunk.yaml` when no `.rtunk/rtunk.yaml` exists. Renaming is optional and can
   happen later, at your own pace — see
   [configuration.md](./configuration.md#config-file-discovery) for the exact discovery order.
3. **Run `rtunk check` once and diff its findings against the last `trunk check` run.** This is
   the practical way to catch any of the five itemized divergences above in your actual
   configuration — a missing formatter finding under plain `check` (item 1), a Windows-only
   command that no longer runs (item 3), or an action that now fails validation (item 5) will show
   up as a concrete difference in this diff rather than as a surprise later.
