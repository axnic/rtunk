# Changelog

All notable changes to `rtunk` are documented in this file. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

rtunk has not tagged a release yet — `--version` reports `dev`. `[Unreleased]` below collects every
user-visible change shipped by the milestones [ROADMAP.md](./ROADMAP.md) already records as done
(`v0.10` through `v0.12`), grouped by milestone. The first tagged release will be `v1.0`; at that
point this heading is renamed to `[1.0.0]` and dated, and a fresh `[Unreleased]` is started above it.

## [Unreleased]

### v0.12 — Renovate integration, promoted to a public command

#### Changed (v0.12)

- The `renovate` command group (annotate a configuration for Renovate and print its regex-manager
  snippet) is now listed in `rtunk help` by default, without needing `--all`.

### v0.11 — Cache, provisioning, and catalog fidelity

#### Added (v0.11)

- A linter whose catalog entry declares a "suggest when" condition is now offered as a suggestion
  once that condition holds for the current repository.
- A command with a declared run timeout is now stopped and reported as failed if it exceeds that
  timeout, instead of being able to run indefinitely.
- Findings from a command flagged security-related in the catalog are now tagged, so they can be
  filtered or displayed separately from ordinary findings.
- A tool that declares companion packages now gets them installed alongside it automatically.
- Concurrent installs of the same item now fail fast, by name: a second process that finds an
  install already in progress either proceeds immediately (if the recorded process is no longer
  running) or stops immediately with an error naming the repository and item, instead of waiting.

#### Changed (v0.11)

- `rtunk cache clean` is now a full, unconditional wipe of the entire cache root — downloads,
  plugin sources, and logs — replacing the previous separate "destroy everything" and age-based
  "prune" commands.
- `rtunk cache prune` now keeps exactly what a currently-existing, currently-configured repository
  still needs, instead of pruning by age; a repository that no longer exists is dropped from the
  record.
- The archive used to install a tool or runtime is no longer kept on disk once the install
  completes.

#### Fixed (v0.11)

- A command with a declared concurrency limit is now honored and never runs more instances in
  parallel than that limit, regardless of the run's overall worker count.
- A tool with declared health checks is now verified right after install, instead of a broken
  install only surfacing later when a linter using it fails.
- When both a specific command and the more generic linter it supersedes are enabled, the
  superseded one's duplicate findings on the same issue are no longer reported twice.
- A command that declares a one-time setup step now runs that step before its first invocation.

### v0.10 — Unify and correct check, fmt, and fix

#### Added (v0.10)

- `--format-before-check` flag runs every formatter first, then checks the reformatted files — the
  "format, then check" behavior that used to be `check --fix`'s only behavior is now explicit and
  opt-in instead of implicit and default.
- Enabling a linter or command flagged deprecated in the catalog now produces a warning naming its
  replacement.

#### Changed (v0.10)

- `check --fix` now applies linter fix commands and finding-level autofixes only, then reports
  whatever remains unfixed; it no longer runs formatters as a side effect.
- Inside a git repository with no upstream branch, a path-less `check`/`fmt` now picks up every
  staged, unstaged, and new untracked file, instead of staged changes only.
- Enabling a linter that still uses the old, single-command declaration shape is now rejected at
  configuration time, naming its replacement, instead of running and silently finding nothing.
- Outside a git repository, a path-less `check`/`fmt` now fails with an explicit error requiring
  paths, instead of exiting successfully having checked nothing.

#### Fixed (v0.10)

- A fix-only linter (one whose autofix is an in-place fix without also being a formatter) is now
  selectable and actually runs under `check --fix`, where it previously never ran under any
  command.
- A finding that already carries a computer-generated replacement from its own linter is now
  applied by `check --fix`, instead of only being reported as text.
- A formatter that rewrites content through its own standard input/output, rather than editing the
  file in place, now runs correctly under `fmt`, instead of being permanently inert.
- A linter command declared for a specific tool-version range now only runs when the resolved tool
  version falls in that range, instead of every declared variant running unconditionally.
- A command variant declared for a platform rtunk does not support (Windows) is no longer attempted
  on a supported platform.
