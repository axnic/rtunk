# AGENTS.md

This file orients anyone — human or AI coding agent — landing in this repository for the first
time. It explains what rtunk is, how it relates to the project it is modeled on, and the
boundaries that are not up for debate. For what is actually being built and in what order, see
[ROADMAP.md](./ROADMAP.md).

## What rtunk is

rtunk is a from-scratch, fully open-source rewrite of [trunk.io's Code Quality
CLI](https://docs.trunk.io/code-quality/overview) — `trunk`, the command that orchestrates dozens
of existing linters, formatters, and security scanners behind a single declarative config, with
hermetic per-project tool versions, git-aware "only check changed files" behavior, output
normalization, and caching. That orchestration model is genuinely good, which is why rtunk exists
at all. The problem is that `trunk` itself ships as a closed-source binary you download and run on
your machine, which is hard to audit or get approved in a professional or regulated environment.
rtunk exists to offer the same orchestration experience as an auditable, fully open-source tool.

## Vision

- **A meta-linter powered by the trunk ecosystem.** rtunk works through trunk's strength: its
  plugins.
- **Open to other uses within the meta-linter scope.** MR checks and CI are welcome; trunk is a
  company's product, and rtunk targets no other part of its offering and does not encroach on it.
- **Partially compatible with trunk.** 100% compatible with trunk configuration; no compatibility
  constraint for the rest (cache, other) nor for the UI.
- **Fast.** Checks are optimized: by default only changed files are verified (see
  [docs/cli.md](./docs/cli.md), "File selection").
- **Pleasant, simple to understand and use.** A simple CLI and a simple UX.

## How it relates to trunk

- **Config-compatible where practical.** rtunk aims to understand `.trunk/trunk.yaml` and honor
  `trunk-ignore` inline directives, so a repository already using trunk can adopt rtunk with
  minimal friction. rtunk's own native config lives at `.rtunk/rtunk.yaml` (a git-ignored
  `.rtunk/user.yaml` local override is design intent, not yet implemented — see
  [docs/configuration.md](./docs/configuration.md#config-file-discovery)), modeled closely on
  trunk's schema and semantics but not a literal copy of trunk's branding or documentation text.
- **Consumes the community plugin ecosystem.** The part of trunk that is genuinely open and
  well-maintained is [trunk-io/plugins](https://github.com/trunk-io/plugins) — the YAML
  definitions describing every linter's runtime, commands, and output format. rtunk consumes those
  same plugin definitions rather than reinventing linter metadata from scratch.
- **Scope: the meta-linter part of trunk only.** rtunk is open to other uses (MR checks, CI), but
  only within the meta-linter surface. trunk is a company's product; rtunk targets no other part
  of its offering and does not encroach on it.
- **Compatibility is limited to configuration.** Compatibility with trunk configuration is
  100% by goal. Everything else (cache layout, CLI surface beyond the commands trunk shares, UI
  and output rendering) carries no compatibility constraint.
- **`.rtunk` wins over `.trunk`, no merge.** When both `.rtunk` and `.trunk` exist, `.rtunk` takes
  precedence and exactly one of the two is read. They are never merged.
- **Independent implementation.** rtunk is not a fork of trunk and does not vendor any of trunk's
  proprietary code. It is a new codebase, written from scratch, that happens to speak a compatible
  config dialect and read the same community plugin definitions.

## Permanently out of scope

The following are not deferred, not "maybe later" — they will never be part of rtunk, and no
proposal to add them should be entertained:

- The trunk daemon
- Merge Queue
- Flaky Tests
- Any hosted or SaaS dashboard
- `login` / `logout` / `whoami` or any notion of a cloud account/organization

rtunk is, and will remain, a 100% local tool.

## Non-negotiable design rules

- **Zero telemetry.** No network call happens unless the user explicitly triggers it. No
  phone-home, no crash reporting, no install ID. The only network calls rtunk ever makes are:
  downloading linter/runtime/tool binaries from their official sources (GitHub Releases, PyPI,
  npm, etc.) as declared in config; and resolving a remote plugin source (`plugins.sources`),
  which does an explicit `git clone` of a repo URL pinned to a tag or SHA — never a branch.
- **rtunk does not manage its own binary.** There is no `rtunk upgrade` command, and there never
  will be: how the rtunk binary itself gets installed or updated (a package manager, mise, a CI
  pipeline, a manual download) is entirely up to whoever deploys it. rtunk won't check GitHub
  Releases, won't nag about a newer version, and won't touch its own binary under any
  circumstances.
- **Local, movable, controllable cache.** The cache lives in the OS-appropriate default location
  (XDG cache dir on Linux, `~/Library/Caches/rtunk` on macOS, `%LOCALAPPDATA%\rtunk\cache` on
  Windows), overridable via `--cache-dir` flag / `RTUNK_CACHE_DIR` env var (the same underlying
  flag, kong-bound — not two independently-read sources) falling back to that default; there is no
  config-file key for it (see
  [docs/configuration.md](./docs/configuration.md#cache-directory)). It is content-addressed, so
  it is safe to copy between machines, and nothing is ever uploaded from it.
- **`rtunk-ignore` with permanent trunk compatibility.** The native inline ignore directive is
  `rtunk-ignore(linter/rule): reason`. `trunk-ignore(...)` and its `-all`/`-begin`/`-end` variants
  are accepted forever as compatible aliases, so migrating from trunk never requires rewriting
  existing ignore comments.
- **Reproducibility.** Linters, runtimes, and tools are version-pinned in config and installed
  hermetically per that pin. There is no silent fallback to whatever happens to already be on
  `PATH`.
- **Checksum-verified downloads.** Every downloaded binary is fetched over HTTPS only (`http` is
  rejected, redirects included), streamed through a SHA256 hasher into a temporary file, and moved
  to `blobs/sha256/<hex>` only once the hash is known. trunk plugin `downloads:` recipes carry no
  upstream checksum, so the model is trust-on-first-use (TOFU): the first download of an artifact
  is accepted as is, and a source compromised at that moment is not detected. See
  docs/superpowers/specs/2026-09-10-v0.2-download-design.md ("Checksum model"). The planned
  hardening is `rtunk.lock` (see "Download integrity roadmap" below).

## Behavioral decisions

Detail lives in [docs/cli.md](./docs/cli.md) (commands and run semantics) and
[docs/ux.md](./docs/ux.md) (terminal UX). They are the target design, authoritative over older
specs under `docs/superpowers/`. Everything through `v0.9` is implemented today; `v1.1`
(`rtunk.lock`) remains planned.

- **Project root.** `check`, `fmt` and `run` refuse to run without a `.trunk`/`.rtunk` ancestor.
- **Changed files by default.** No-path `check`/`fmt` process only changed files (merge-base diff,
  else diff from `HEAD`, both plus untracked; an error outside git); `--from <ref>` for CI; explicit
  paths process everything under them.
- **Exit codes.** Identical to trunk's: only `0` and `1`, measured on trunk 1.25.0.
- **`fmt`** writes to the working tree only, never the index.
- **Command surface.** Symmetric `linters`/`actions` groups, hidden `toolbox`, `cache
clean|prune` (`clean` wipes the whole cache, `prune` is usage-based, no flag), `plugins print`.
- **UX.** `check`/`fmt` emit an event stream consumed by `human`, `sarif` and `json` renderers;
  every issue line is printed, no folding.
- **Download integrity.** `rtunk.lock` (`id@version@platform -> sha256`) is post-v1 hardening,
  roadmap `v1.1`; until then the TOFU limit above stands.

Staging: `v0.8` CLI reshape and behavioral decisions, `v0.9` output and UX, `v1.1` `rtunk.lock`
(see [ROADMAP.md](./ROADMAP.md)).

## Implementation

rtunk is implemented in Go: a single static binary, straightforward cross-compilation, and a good
fit for an orchestration-heavy workload built around process execution, pipes, timeouts, and
concurrency.

## Where to go next

See [docs/cli.md](./docs/cli.md) and [docs/ux.md](./docs/ux.md) for the command and UX design, and
[ROADMAP.md](./ROADMAP.md) for the staged build-out of rtunk's functionality, from reading an
existing trunk configuration through its public release and beyond.
