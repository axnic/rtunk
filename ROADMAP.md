# ROADMAP.md

This is the authoritative, staged roadmap for rtunk, from `v0.1` through `v1.2`. Before reading
further, see [AGENTS.md](./AGENTS.md) for the project's non-negotiable design rules and permanent
boundaries — in particular: **the trunk daemon, Merge Queue, Flaky Tests, any hosted/SaaS
dashboard, and `login`/`logout`/`whoami`/any notion of a cloud account are permanently out of
scope and will never appear on this roadmap**, at any version.

## Sequencing logic

The stages build on each other deliberately. You cannot run a linter you cannot yet locate, so
reading and understanding an existing trunk configuration (`v0.1`) comes before downloading the
tools it references (`v0.2`), which in turn must exist locally before anything can be executed
against them (`v0.3`). Read-only checking is proven out before anything is allowed to rewrite
files on disk (`v0.4`). Actions, upgrades, and project init/deinit (`v0.5`–`v0.7`) are layered on
top once the core check/fmt loop is solid, since they depend on it rather than the other way
around. Once those exist, `v0.8` reshapes the command surface and fixes behavioral decisions
(root rule, file selection, exit codes) before `v0.9` builds the output layer on top of them. CLI
flag compatibility (`v1.0`) comes last because it is a breadth pass over functionality that
already exists, not new capability.

## v0.1 — Read and query an existing trunk configuration

The first milestone makes rtunk able to parse and reason about a `.trunk/trunk.yaml` (or
`.rtunk/rtunk.yaml`) configuration without touching the network or the filesystem beyond reading
config. This validates the config model before anything downloads or executes.

- **`rtunk config print [--output yaml|json]`** — Print the fully compiled/merged
  configuration: the result of resolving all config sources (base config, local overrides, plugin
  definitions) into the single effective configuration rtunk would act on. The full merged plugin
  catalog moved to `rtunk plugins print` in `v0.8` (`--all` removed).

The originally planned `rtunk config {plugins,lint,actions,tools,runtimes} list|show` were never
implemented and are dropped: `linters`/`actions` listing (`v0.9`) and `plugins print` (`v0.8`)
cover the need.

## v0.2 — Download linters, runtimes, and other tools

With configuration readable, the next step is fetching the actual tool binaries the config
references, hermetically and reproducibly, per the design rules in AGENTS.md.

- **`rtunk download {plugins,lint,actions,tools,runtimes} <id>[@<version>]`** — Download one
  specific item (optionally pinned to a version) into the local cache, and create its shim if one
  is needed.
- **`rtunk exec|x {tools,runtimes,actions} <id>[@<version>] -- <args>`** — Run the given tool or
  runtime, downloading it first if it isn't already present locally.
- **`rtunk where {plugins,lint,actions,tools,runtimes} <id>[@<version>]`** — Print the filesystem
  path to an item's shim, for scripting or debugging what rtunk would actually invoke.
- **`rtunk cache clean`** — Remove all files from the rtunk cache directory.
- **`rtunk cache prune`** — Remove only the files in the cache that are no longer referenced by
  anything currently enabled, leaving what is still in use untouched.

## v0.3 — Run linters (read-only)

This is the first milestone where rtunk actually executes linters against source code, but strictly
in a read-only capacity: it reports findings without modifying files.

- **`rtunk check [paths...]`** — Run the enabled checks against the given paths, or against the
  whole repository if no paths are given. Per-file running/done progress streams to stderr.
- **`rtunk check enable`** — Enable one or more linters.
- **`rtunk check disable`** — Disable one or more linters.
- **`rtunk check list`** — List all linters available for the current configuration.

## v0.4 — Run linters & formatters (read-write)

Once read-only checking is trustworthy, rtunk is extended to modify files: applying automatic
fixes from linters, and running dedicated formatters.

- **`rtunk check --fix [paths...]`** — Run checks as in `v0.3`, but apply automatic fixes wherever
  a linter supports them.
- **`rtunk fmt [paths...]`** — Run configured formatters against the given paths, or the whole
  repository if none are given.

## v0.5 — Manage actions

Actions are the automation hooks trunk-style tooling runs around git operations and other
lifecycle events (for example, on commit or push). This milestone brings that concept to rtunk.

- **`rtunk [actions] run <id>`** — Run a specified action on demand, in the current directory
  (`rtunk run` is a top-level shortcut for `rtunk actions run`).
- **`rtunk actions history`** — See recent runs of actions, for auditing or debugging.
- **`rtunk actions list`** — List all actions defined for the current configuration.
- **`rtunk actions enable`** — Enable one or more actions.
- **`rtunk actions disable`** — Disable one or more actions.
- **`rtunk git-hooks install|uninstall`** — Install or remove the git hooks that trigger actions
  automatically at the appropriate git lifecycle points (`sync` is accepted as an alias of
  `install`).

## v0.6 — Manage upgrades

- **`rtunk upgrade`** — Check for and install a newer release of rtunk itself, fetched from rtunk's
  own GitHub Releases (the only network call this milestone introduces, and only ever triggered
  manually by the user, per the zero-telemetry rule).

## v0.7 — Manage init

- **`rtunk init`** — Initialize rtunk in a repository: scaffold the `.rtunk/` configuration
  directory and its base config.
- **`rtunk deinit`** — Remove rtunk's configuration and any installed artifacts (such as git
  hooks) from a repository, reversing `rtunk init`.

## v0.8 — CLI reshape and behavioral decisions

Nothing here adds a linter capability; it aligns the command surface and run semantics with the
decisions recorded in AGENTS.md ("Behavioral decisions") and detailed in docs/cli.md, before the output layer (`v0.9`) is built
on them. Implemented: `.rtunk` wins over `.trunk` with no merge, the project root rule, file
selection, exit codes, `fmt` working-tree-only, and the whole command reshape below: all `v0.8` items are implemented, under internal/cli. Full rules: [docs/cli.md](./docs/cli.md).

- **Project root rule (implemented).** `check`, `fmt` and `run` refuse to run unless an ancestor (itself
  included) contains `.trunk` or `.rtunk`, with an explicit message; this covers `rtunk check .` in
  `$HOME` (`findTrunkYAML`, internal/cli/findtrunk.go).
- **File selection (implemented, internal/cli/selection.go).** `check` and `fmt` without paths process changed files only: in git with an
  upstream, the diff from `merge-base(upstream, HEAD)` to the working tree; in git without an
  upstream, staged files only; outside git, nothing runs (no timestamp fallback). `--from <ref>`
  forces the diff base (for CI). With explicit paths, every file under them is processed
  (`git ls-files -co --exclude-standard` in git, everything otherwise). When nothing runs, rtunk
  prints `rtunk: no files to check|format` and exits `0`.
- **Exit codes (implemented).** `0` on success, `1` on findings, nonexistent path, invalid config, or a tool
  failing to run; no distinction between findings and errors. `fmt` exits `0` whether or not it
  fixed files. Verified against `cmd/rtunk/main.go`: any returned error exits `1`,
  otherwise `0`.
- **`fmt` writes to the working tree only (implemented)** and never touches the index (no `git add`); partially
  staged files are skipped with a warning unless `--force`.
- **`linters` / `actions` symmetry (implemented).** Replace `check enable|disable|list` with
  `rtunk linters {list,enable,disable} <id>[@version]`, mirroring `actions {list,enable,disable}`;
  `check` and `fmt` take paths only. The filtered layout of `list` lands in `v0.9`.
- **`rtunk git-hooks sync|unsync` (implemented)** — Renames `install`/`uninstall`; the old names and
  the alias are removed, with no compat aliases.
- **`rtunk plugins print` (implemented)** — Print all configuration available across all plugins,
  resolved (a registry dump, can be very large). Replaces `config print --all`; `--all` is removed
  from `config print`.
- **`toolbox` group (hidden, implemented).** Moves and narrows the former top-level commands:
  `download` -> `rtunk toolbox download {runtime,tools} <id>[@<version>]` (item required, no bare
  download-everything); `exec|x` -> `rtunk toolbox exec {runtime,tools} <id>[@<version>] -- <cmd>
[<args>...]` (`<cmd>` is the item's shim if equal to `<id>`, else an executable in its install
  dir; `--interactive` binds stdin/stdout, otherwise stdin is unbound); `where` ->
  `rtunk toolbox where {runtime,tools} <id>[@<version>]` (absolute install directory, not the
  shim). `renovate annotate|config` (shipped in `v1.1`) become `toolbox renovate
enable|disable|config`: `enable` is the former `annotate`, `disable` strips the annotations, and
  both warn on stderr when no Renovate config file at the repo root contains the regexManager.
- **`rtunk logs list|show|clean` (implemented)** — Inspect and clean the per-run logs written by `check`, `fmt`
  and `actions run` (`logs show <run>|latest`; see
  docs/superpowers/specs/2026-09-26-run-logs-design.md). The code already exists
  (internal/cli/logs.go), unchanged by the reshape.
- **Hidden commands (implemented).** `toolbox` and other internal commands stay callable but absent
  from default help; `rtunk help [--all]` lists everything with `--all`.
- **Cache administration (implemented).** `rtunk cache clean` -> `rtunk cache destroy`, and
  `rtunk cache prune --older-than <duration>` (required; Go durations plus a `d` suffix, e.g.
  `30d`) removes `installs/<cat>/<id>/<version>` and `shims/<cat>/<id>/<version>` directories whose
  mtime is older. Every use (check/fmt/actions runtime and tool resolution, `toolbox exec`) touches
  those mtimes via `download.Touch`; caches created before this change look old until their next
  use. Known gap: `--older-than` replaces the former prune of what the config no longer
  references, and there is no project registry, so detecting truly unreferenced entries is
  deliberately out of v1.

## v0.9 — Output and UX

`check` and `fmt` emit an event stream; renderers consume it and hold no business logic. Item 1 is
implemented (`internal/cli/render`), as are items 2 and 4; the TTY live view does not exist yet. Design: [docs/ux.md](./docs/ux.md). Implemented in this order:

1. **Event stream + plain renderer** (implemented) — needed for CI and tests. Out of TTY: one line per finished
   linter, then findings; honors `NO_COLOR` and `--no-progress`. Every issue line is printed, files
   sorted alphabetically and issues by line; failures get their own section carrying the run log
   uid (linking to `rtunk logs show`, see `v0.8`). Breaking: the old `file:line severity [rule]
message` lines and `N issue(s) in M file(s)` summary are gone; stable machine output comes with
   item 2. `--no-progress` (on `check` and `fmt`) suppresses only the per-linter stderr lines. No
   color (added by item 2).
2. **`--format human|sarif|json`** (implemented) on `check` and `fmt`: `human` is the default, `sarif`
   is for CI (`check` only), `json` for other machine consumers. Each is a renderer over the event
   stream. `human` adds ANSI color only on a terminal with `NO_COLOR` empty; output elsewhere is
   uncolored. Progress stays on stderr for every format; stdout carries only the report or document.
3. **TTY live view** — hand-written renderer (no bubbletea): header bar, per-linter tree, install
   lines with byte progress, `--live-height` / `RTUNK_LIVE_HEIGHT`, ASCII fallback (`--ascii`,
   `TERM=dumb`, non-UTF-8 locale), Ctrl+C and SIGWINCH handling.
4. **Filtered `linters list` / `actions list`** (implemented, internal/cli/list.go; file matching
   via `engine.Matches` in pkg/trunk/engine/match.go) — `rtunk linters list [--all] [--format
human|json]` groups linters as enabled (with pinned version, even with 0 matching files),
   available for this repo (matching at least one repository file, not enabled) and, with `--all`,
   the rest; the footer hints `rtunk linters enable <id>`. `rtunk actions list [--format
human|json]` has two groups only, `Enabled` and `Available (not enabled)`. Breaking: the old
   `* name  description` format is gone. No color or ASCII fallback yet (item 3).

Later: color themes, detailed byte-progress style, clickable OSC 8 links, sort by severity.

## v1.0 — CLI flag compatibility

Be compatible with most of trunk's CLI flags across the commands implemented in the milestones
above, so that scripts, CI pipelines, and muscle memory built around `trunk` carry over to `rtunk`
with minimal changes.

## v1.1 — Renovate integration

rtunk deliberately never checks or applies upstream version updates itself (see
docs/superpowers/specs/2026-09-17-renovate-annotations-design.md for why) — instead, it generates
the annotations [Renovate](https://docs.renovatebot.com/)'s regex manager needs to do that job on
its own.

- **`rtunk toolbox renovate enable`** (formerly `renovate annotate`) — Annotate `trunk.yaml`'s version-pinned entries with Renovate
  regex-manager comments, wherever a datasource can be confidently named.
- **`rtunk toolbox renovate config`** (formerly `renovate config`) — Print the Renovate `regexManagers` config snippet to add.

Renamed in `v0.8` (implemented) to the hidden `rtunk toolbox renovate enable|disable|config`
(enable/disable turn the annotations on or off and warn when the regexManager configuration is
missing; `config` will include `postUpgradeTasks: rtunk lock` once `rtunk.lock` exists, `v1.2`).

## v1.2 — Download integrity (`rtunk.lock`)

Post-v1 hardening; not blocking for `check`/`fmt`/`run`. The current model is SHA256 content
addressing with trust-on-first-use (see AGENTS.md and docs/cli.md, "Download integrity"); a source compromised at first download is not
detected.

- **`rtunk.lock`** — Versioned in the repo, `id@version@platform -> sha256` (go.sum style). A
  present entry that mismatches is a blocking error (temp file deleted, no fallback). An entry
  absent locally is downloaded and recorded; absent with `--locked` or `CI=true` it is an error.
- **`rtunk lock`** — Hidden command precomputing hashes for the other platforms.
- **Later (v1+):** reuse checksums and signatures from the aqua-registry.
