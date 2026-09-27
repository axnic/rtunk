---
current_version: 0.10.x
last_updated: 2026-09-27
---

# ROADMAP.md

This is the authoritative, staged roadmap for rtunk.
Before reading further, see [AGENTS.md](./AGENTS.md) for the project's non-negotiable
design rules and permanent boundaries — in particular: **the trunk daemon, Merge Queue, Flaky
Tests, any hosted/SaaS dashboard, and `login`/`logout`/`whoami`/any notion of a cloud account are
permanently out of scope and will never appear on this roadmap**, at any version.

## How to read this roadmap

- **`current_version`** (front matter, above) is the latest milestone that has fully shipped. Every
  version listed below this line is still to be built, in the order listed.
- **A milestone is a version** (`v0.10`, `v0.11`, …). Each has one goal paragraph, a list of items,
  and an explicit **Done when** section. A milestone only advances `current_version` once every one
  of its items is delivered and its **Done when** criteria hold — a partially delivered milestone is
  not "shipped," it is "in progress," and only its remaining items would stay on the roadmap.
- **Items describe an observable outcome** — what a user or maintainer can see or do once the item
  ships — never an implementation detail. User-facing command names, flags, and configuration keys
  are fine to name; file paths and internal identifiers are not.
- **Ordering is a dependency, not a suggestion**, except where a milestone says otherwise: later
  milestones are written assuming earlier ones already shipped.
- **"Deliberate divergences from trunk"** (near the end) is not a work list. It records decisions
  already made and closed, so they are not mistaken for open bugs later.
- **"Later / not scheduled"** (at the end) lists ideas already acknowledged elsewhere in the
  project's docs that are in scope for rtunk eventually, but are not assigned to a milestone and
  carry no ordering guarantee.

### What's ahead

The remaining work before `v1.0` falls into three phases, in order: first, a correctness and
consistency pass over what already exists (`v0.10`–`v0.11`), driven by the architecture review in
[docs/architecture/inconsistencies.md](./docs/architecture/inconsistencies.md); then Renovate
integration is promoted from an internal command to a supported, public one (`v0.12`); and finally,
documentation is written to the standard a public release requires (`v0.13`), immediately before
`v1.0` itself. `v1.0` is the public release — CLI flag compatibility with trunk is explicitly not a
goal, at `v1.0` or any other version. `v1.1`, after the public release, hardens download integrity.

## v0.10 — Unify and correct check, fmt, and fix

Checking, formatting, and fixing are put on one consistent execution model, and the command
selection and validation bugs recorded in
[docs/architecture/inconsistencies.md](./docs/architecture/inconsistencies.md) are fixed, so every
real plugin behavior rtunk's own catalog already declares runs the way it's declared, and a
misconfigured or deprecated setup is reported instead of silently doing nothing.

- **One execution model for check, fmt, and fix.** Checking, formatting, and fixing are three
  configurations of the same underlying run, not three code paths that can drift apart from one
  another: what differs between them is only which commands are selected and what happens to each
  one's output.
- **Fix-only linters actually fix.** A linter whose autofix is an in-place fix without also being a
  formatter is now selectable and runnable: enabling it and running `check --fix` applies its fix,
  where today it silently never runs under any command.
- **Findings apply their own attached fix.** A finding that already carries a computer-generated
  replacement from its own linter is applied by `check --fix`, not just reported as text.
- **`check --fix` means linter fixes only.** `check --fix` applies fix commands and finding-level
  autofixes, then reports whatever remains unfixed — it no longer runs formatters as a side effect.
- **`--format-before-check` recreates the previous combined behavior.** A new, opt-in flag runs
  every formatter first, then checks the reformatted files — the "format, then check" sequence that
  used to be `check --fix`'s only behavior is now explicit and optional, not implicit and default.
- **Stdin/stdout formatters run.** A formatter that rewrites content through its own standard
  input/output, rather than editing the file in place, now runs under `fmt` like any other
  formatter, instead of being permanently inert.
- **Version-scoped commands run only for their version.** A linter command declared for a specific
  tool-version range only runs when the resolved tool version falls in that range, instead of every
  declared variant running unconditionally and producing duplicate or broken results.
- **Unsupported-platform commands are skipped.** A command variant declared for a platform rtunk
  does not support (Windows, today) is never attempted on a supported platform, removing a
  guaranteed failure for every linter that declares one.
- **Legacy linter configurations are refused.** Enabling a linter that still uses the old,
  single-command declaration shape is rejected at configuration time, naming its replacement,
  instead of silently running and finding nothing.
- **Deprecated linters and commands warn.** Enabling a linter or command flagged deprecated in the
  catalog produces a warning naming its replacement.
- **Default file selection matches "everything since the last commit."** Inside a git repository
  with no upstream branch, a path-less `check`/`fmt` picks up every staged, unstaged, and new
  untracked file, not staged changes only.
- **Running outside git without paths is a hard error.** Outside a git repository, a path-less
  `check`/`fmt` fails with an explicit message requiring paths, instead of exiting successfully
  having checked nothing.

**Done when**: every fix-only linter and every finding-level autofix in the real plugin catalog is
applied by `check --fix` when enabled; `--format-before-check` exists and plain `check`/`check
--fix` no longer run formatters; every stdin/stdout-only formatter in the catalog runs successfully
under `fmt`; no enabled linter runs a command outside its declared version range or a Windows-only
variant on a supported platform; enabling a legacy-shaped or deprecated linter produces the
documented refusal or warning; file selection matches the target behavior in git with an upstream,
git without one, and outside git.

## v0.11 — Cache, provisioning, and catalog fidelity

The cache is consolidated into one coherent design, and rtunk acts on every plugin-catalog field it
already parses but currently ignores, removing duplicated internal logic along the way. None of
this changes what a user configures; it changes whether rtunk's behavior matches what plugins
already declare and what a custom cache location already implies.

- **One cache root, one on-disk layout.** A custom cache directory produces exactly the same
  subtree structure as the default location, instead of a silently different one.
- **Plugin sources have their own area of the cache.** Plugin-source definitions and checkouts are
  kept separately from downloaded tool/runtime installs, under the same root, and reached by the
  same custom-root override as everything else.
- **`rtunk cache clean` is a full, unconditional wipe.** One command empties the entire cache root —
  downloads, plugin sources, and logs alike — replacing today's separate "destroy everything" and
  age-based "prune" commands.
- **`rtunk cache prune` keeps only what's still in use, not what's merely old.** rtunk keeps track
  of which repositories have used the cache; `prune` keeps exactly what those repositories (the ones
  that still exist, with their current configuration) still need, and removes everything else — a
  repository that no longer exists is dropped from that record. There is no age or duration to pick.
- **Downloaded archives aren't kept after install.** Once a tool or runtime is installed, the
  archive used to install it is not retained — the cache does not keep growing from one-time
  downloads that are never read again.
- **Concurrent installs fail fast, by name.** While an item is being downloaded and installed, it is
  marked as in progress, naming the repository and process doing the work. A second process that
  finds that mark either proceeds immediately, if the recorded process is no longer running, or
  stops immediately with an error naming which repository is currently installing which item and
  suggesting a retry later — it never waits.
- **One shared implementation for variable substitution and runtime resolution.** Checking/
  formatting and actions resolve invocation variables and locate runtime shims through the same
  logic, so a fix or a new variable is never applied in only one of the two places.
- **Actions provision through the runtime path only.** Provisioning an action never performs a
  separate fetch step of its own; it reuses runtime provisioning directly.
- **Suggested linters.** A linter whose catalog entry declares a "suggest when" condition is
  offered as a suggestion once that condition holds for the current repository.
- **Per-command run timeouts.** A command with a declared run timeout is stopped and reported as
  failed if it exceeds it, instead of being able to run indefinitely.
- **Security findings are tagged.** Findings from a command flagged as security-related in the
  catalog are marked as such, so they can be filtered or displayed separately from ordinary findings.
- **Overlapping linters don't double-report.** When both a specific command and the more generic
  linter it supersedes are enabled, the superseded one's duplicate findings on the same issue are
  suppressed.
- **Per-command setup runs before first use.** A command that declares a one-time setup step runs
  that step before its first invocation.
- **Per-command concurrency caps are honored.** A command with a declared concurrency limit never
  runs more instances in parallel than that limit, regardless of the run's overall worker count.
- **Tool installs are verified.** A tool with declared health checks is confirmed to actually work
  right after being installed, instead of a broken install only surfacing when a linter using it
  later fails.
- **Companion packages install automatically.** A tool that declares companion packages gets them
  installed alongside it.

**Done when**: a custom cache directory and the default location produce identical subtree layouts;
`cache clean` leaves nothing behind under the cache root, plugin sources included; `cache prune`
keeps exactly the installs, registry entries, and logs that a currently-existing, currently-
configured repository still needs, drops a repository that no longer exists from its own record,
and removes everything else; no archive remains on disk once its tool or runtime has finished
installing; two processes racing to install the same item never corrupt it or duplicate the work —
the second either proceeds after a stale in-progress mark clears, or stops immediately with an error
naming the repository and item, never waiting; every catalog field listed above is verified against
at least one real plugin in the catalog that declares it.

## v0.12 — Renovate integration, promoted to a public command

Renovate annotation support already exists and works. This milestone graduates it from an internal,
hidden command surface to a fully documented, first-class part of rtunk's everyday command set,
since generating the annotations Renovate needs is a real, user-facing capability, not an internal
or debugging tool.

- **Renovate commands are visible by default.** The commands that annotate a configuration for
  Renovate and print its regex-manager snippet appear in `rtunk help` without needing `--all`.
- **The generated Renovate snippet is verified end to end.** The printed regex-manager
  configuration is validated against a real Renovate run, so copying it produces correctly matched
  dependency updates, not just a plausible-looking snippet that was never actually exercised.

**Done when**: `rtunk help` lists the Renovate commands without `--all`; a Renovate run using the
printed configuration correctly proposes an update for at least one version-pinned entry in a
sample project.

## v0.13 — Documentation

The last milestone before the public release (`v1.0`), and treated as a first-class deliverable,
not an afterthought: the precise, exhaustive set of documents a public open-source Go CLI needs,
covering the project itself, its internals, and its users. Every document below is written or
brought up to date; none is optional.

**Standard project documents**

- **README** — Purpose: the single entry point explaining what rtunk is, why it exists, how to
  install and run it, and a dedicated section on every way rtunk's behavior intentionally differs
  from trunk's, with the reasoning for each. Audience: anyone landing on the repository for the
  first time. Done when: someone with no prior context can install rtunk, run their first check,
  and understand every place its behavior differs from trunk and why, using only this document.
- **CONTRIBUTING** — Purpose: how to set up a development environment, run the test suite, and
  submit a change, including the project's commit conventions. Audience: external contributors.
  Done when: a first-time contributor can go from a fresh clone to an accepted change using only
  this document.
- **LICENSE** — Purpose: the open-source license rtunk is distributed under. Audience: adopters and
  legal/compliance reviewers. Done when: a license file is present at the repository root, is a
  recognized open-source license, and is referenced from the README.
- **CODE_OF_CONDUCT** — Purpose: the standard of behavior expected in the project's spaces.
  Audience: contributors and community members. Done when: a code of conduct is published, linked
  from the README and CONTRIBUTING, and names a contact for reports.
- **SECURITY policy** — Purpose: how to responsibly report a vulnerability, which matters in
  particular because rtunk downloads and executes third-party binaries. Audience: security
  researchers and users. Done when: a working, private reporting channel is documented and linked
  from the README.
- **CHANGELOG** — Purpose: a human-readable record of notable changes per release, so someone
  upgrading knows what changed. Audience: users upgrading between releases. Done when: the
  changelog exists, follows a recognizable convention, and has a real first entry for the `v1.0`
  release.
- **Issue and pull request templates** — Purpose: guide a bug report or feature request to include
  what maintainers need to act on it (reproduction, configuration, plugin involved), and a pull
  request to state its rationale. Audience: contributors and issue reporters. Done when: opening a
  new issue or pull request shows the template, and a submission that follows it contains the
  requested information.

**Technical documents**

- **Internal architecture** — Purpose: explain how rtunk works internally end to end —
  configuration resolution, provisioning and the cache, the execution engine, actions, and output
  rendering — building on the existing architecture notes. Audience: contributors and maintainers
  making non-trivial changes. Done when: a contributor can explain how a `check` invocation flows
  from the command line to a printed report after reading only this document.
- **Plugin model** — Purpose: explain how trunk's plugin catalog works: what a plugin declares
  (linters, tools, runtimes, actions, and their fields) and how rtunk resolves and consumes those
  declarations. Audience: contributors extending plugin support, and advanced users adapting or
  debugging a plugin. Done when: someone unfamiliar with trunk's plugin format can explain, using
  only this document, what a given declared field does and whether rtunk currently acts on it.

**User documents**

- **Installation guide** — Purpose: every supported way to obtain a working `rtunk` binary and
  confirm it works. Audience: new users. Done when: a user on each officially supported platform
  can go from nothing to a working install using only this document.
- **Configuration reference** — Purpose: every configuration key rtunk reads (native and
  trunk-compatible), every override and its precedence, and the ignore-comment syntax. Audience:
  anyone configuring rtunk for a repository. Done when: every configuration key and override rtunk
  actually reads is documented, with no gap against the code's behavior.
- **Command reference** — Purpose: one authoritative, public description of every user-facing
  command and flag. Audience: day-to-day users and CI authors. Done when: every command and flag
  `rtunk help --all` lists appears here with a description matching its actual behavior.
- **Migration guide from trunk** — Purpose: help an existing trunk user adopt rtunk — what carries
  over unchanged (configuration, plugin ecosystem, ignore comments) and an itemized list of every
  behavioral difference to expect, cross-referencing the README's rationale for each. Audience:
  teams currently using trunk. Done when: a trunk user can follow this guide to switch a real
  repository to rtunk knowing, in advance, every place its behavior will differ.

**Done when**: every document above exists, is accurate against rtunk's actual behavior at the time
of the `v1.0` release, and is reachable from the README; no shipped command, flag, configuration
key, or intentional divergence from trunk is undocumented.

## v1.0 — Public release

`v1.0` marks rtunk's first public release. CLI flag compatibility with trunk is explicitly not a
goal for this or any milestone; instead, `v1.0` means a complete tool backed by precise, exhaustive
documentation (`v0.13`) that lets someone outside the project discover, install, and adopt rtunk
without help from its author.

- **The project is public.** The repository and its issue tracker are open, with the full
  documentation set from `v0.13` in place.
- **A real, versioned release exists.** A tagged release is published with installable binaries for
  every officially supported platform, and the changelog carries its first real entry.

**Done when**: someone with no prior contact with the project can discover rtunk, install it,
migrate an existing trunk-based repository to it, and find an answer to any question about its
behavior — entirely from the published documentation and release artifacts.

## v1.1 — Download integrity (`rtunk.lock`)

Post-`v1.0` hardening; not blocking for `check`/`fmt`/`run`. Today, a downloaded tool is trusted the
first time it's fetched, with no independent check afterward (see [AGENTS.md](./AGENTS.md) and
[docs/cli.md](./docs/cli.md), "Download integrity"); a source compromised at that exact moment would
not be detected.

- **A committed lock file records what every download should be.** `rtunk.lock`, checked into the
  repository, records the expected fingerprint of every tool and runtime rtunk can install, per
  version and per platform.
- **A mismatch blocks the run.** If a downloaded tool's fingerprint doesn't match what `rtunk.lock`
  records for it, rtunk refuses to use it and the run fails, with no silent fallback.
- **A missing entry is recorded, or refused, depending on context.** A download not yet in
  `rtunk.lock` is fetched normally and its fingerprint recorded; in a locked or CI context (
  `--locked`, `CI=true`), the same missing entry is an error instead, so CI never silently trusts a
  new download.
- **`rtunk lock` precomputes entries for other platforms.** Fingerprints for a platform other than
  the one rtunk is currently running on can be added to `rtunk.lock` ahead of time.
- **The Renovate snippet keeps the lock file current.** Once `rtunk.lock` exists, the printed
  Renovate configuration includes a step that refreshes it automatically after a version bump.

**Done when**: `rtunk.lock` exists and is versioned in the repository; a downloaded tool whose
fingerprint mismatches `rtunk.lock` blocks the run with an error and no fallback; a download missing
from `rtunk.lock` is fetched and recorded normally, or is an error under `--locked`/`CI=true`;
`rtunk lock` can precompute fingerprints for platforms other than the current one; the Renovate
configuration snippet keeps `rtunk.lock` current after a version bump.

## Deliberate divergences from trunk

Not work items — decisions already made and closed, recorded here so they are not mistaken for
open bugs later. Full rationale for each is in
[docs/architecture/inconsistencies.md](./docs/architecture/inconsistencies.md).

- **Plain `check` does not run formatters.** Unlike trunk, a checking run never reports unformatted
  files as findings by default; `--format-before-check` (`v0.10`) reproduces that behavior on
  request.
- **Windows is not a supported host platform.** A command variant declared Windows-only is skipped
  rather than attempted; there is no rtunk build for Windows today.
- **The run journal doesn't log file selection or provisioning.** It records the commands actually
  run and what they produced, not what led up to running them.
- **Actions can't trigger on file changes or a schedule.** Both trigger kinds require a background
  daemon, and rtunk refuses to run as one; such an action's configuration is rejected outright
  rather than silently never firing.

## Later / not scheduled

Ideas already acknowledged in the project's own docs that are in scope for rtunk eventually, but
not assigned to a milestone and not ordered relative to one another:

- Color themes for the live view, including a distinct color for install rows.
- A more detailed byte-progress style for install rows.
- A "waiting for runtime" state in the live view, shown while a runtime download blocks a tool's
  own install.
- Clickable terminal links (OSC 8) and sorting issues by severity in the `human` report.
- Reusing checksums and signatures from the aqua-registry for download integrity, instead of
  rtunk's own first-fetch trust model.
