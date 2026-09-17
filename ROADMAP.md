# ROADMAP.md

This is the authoritative, staged roadmap for rtunk, from `v0.1` through `v1.0`. Before reading
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
around. CLI flag compatibility (`v1.0`) comes last because it is a breadth pass over functionality
that already exists, not new capability.

## v0.1 — Read and query an existing trunk configuration

The first milestone makes rtunk able to parse and reason about a `.trunk/trunk.yaml` (or
`.rtunk/rtunk.yaml`) configuration without touching the network or the filesystem beyond reading
config. This validates the config model before anything downloads or executes.

- **`rtunk config {plugins,lint,actions,tools,runtimes} list [--enabled]`** — List the elements
  currently defined for that category. The `--enabled` flag restricts the list to only the
  elements that are actually turned on, as opposed to merely defined/available.
- **`rtunk config {plugins,lint,actions,tools,runtimes} show <id> [--output yaml|json]`** — Show
  detailed information about one specific element by its identifier, formatted as YAML or JSON.
- **`rtunk config print [--output yaml|json]`** — Print the fully compiled/merged configuration:
  the result of resolving all config sources (base config, local overrides, plugin definitions)
  into the single effective configuration rtunk would act on.

## v0.2 — Download linters, runtimes, and other tools

With configuration readable, the next step is fetching the actual tool binaries the config
references, hermetically and reproducibly, per the design rules in AGENTS.md.

- **`rtunk download {plugins,lint,actions,tools,runtimes} <id>[@<version>]`** — Download one
  specific item (optionally pinned to a version) into the local cache, and create its shim if one
  is needed.
- **`rtunk exec|x {tools,runtimes} <id>[@<version>] -- <args>`** — Run the given tool or runtime,
  downloading it first if it isn't already present locally.
- **`rtunk where {plugins,lint,actions,tools,runtimes} <id>[@<version>]`** — Print the filesystem
  path to an item's shim, for scripting or debugging what rtunk would actually invoke.
- **`rtunk cache clean`** — Remove all files from the rtunk cache directory.
- **`rtunk cache prune`** — Remove only the files in the cache that are no longer referenced by
  anything currently enabled, leaving what is still in use untouched.

## v0.3 — Run linters (read-only)

This is the first milestone where rtunk actually executes linters against source code, but strictly
in a read-only capacity: it reports findings without modifying files.

- **`rtunk check [paths...]`** — Run the enabled checks against the given paths, or against the
  whole repository if no paths are given.
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

- **`rtunk actions run`** — Run a specified action on demand.
- **`rtunk actions history`** — See recent runs of an action, for auditing or debugging.
- **`rtunk actions list`** — List all actions defined for the current configuration.
- **`rtunk actions enable`** — Enable one or more actions.
- **`rtunk actions disable`** — Disable one or more actions.
- **`rtunk git-hooks`** — Manage installation of git hooks that trigger actions automatically at
  the appropriate git lifecycle points.

## v0.6 — Manage upgrades

- **`rtunk upgrade`** — Check for and install a newer release of rtunk itself, fetched from rtunk's
  own GitHub Releases (the only network call this milestone introduces, and only ever triggered
  manually by the user, per the zero-telemetry rule).

## v0.7 — Manage init

- **`rtunk init`** — Initialize rtunk in a repository: scaffold the `.rtunk/` configuration
  directory and its base config.
- **`rtunk deinit`** — Remove rtunk's configuration and any installed artifacts (such as git
  hooks) from a repository, reversing `rtunk init`.

## v1.0 — CLI flag compatibility

Be compatible with most of trunk's CLI flags across the commands implemented in the milestones
above, so that scripts, CI pipelines, and muscle memory built around `trunk` carry over to `rtunk`
with minimal changes.

## v1.1 — Renovate integration

rtunk deliberately never checks or applies upstream version updates itself (see
docs/superpowers/specs/2026-09-17-renovate-annotations-design.md for why) — instead, it generates
the annotations [Renovate](https://docs.renovatebot.com/)'s regex manager needs to do that job on
its own.

- **`rtunk renovate annotate`** — Annotate `trunk.yaml`'s version-pinned entries with Renovate
  regex-manager comments, wherever a datasource can be confidently named.
- **`rtunk renovate config`** — Print the Renovate `regexManagers` config snippet to add.
