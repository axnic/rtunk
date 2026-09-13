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

## How it relates to trunk

- **Config-compatible where practical.** rtunk aims to understand `.trunk/trunk.yaml` and honor
  `trunk-ignore` inline directives, so a repository already using trunk can adopt rtunk with
  minimal friction. rtunk's own native config lives at `.rtunk/rtunk.yaml` (with a git-ignored
  `.rtunk/user.yaml` local override), modeled closely on trunk's schema and semantics but not a
  literal copy of trunk's branding or documentation text.
- **Consumes the community plugin ecosystem.** The part of trunk that is genuinely open and
  well-maintained is [trunk-io/plugins](https://github.com/trunk-io/plugins) — the YAML
  definitions describing every linter's runtime, commands, and output format. rtunk consumes those
  same plugin definitions rather than reinventing linter metadata from scratch.
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
  npm, etc.) as declared in config; a manual `rtunk upgrade` checking the rtunk repo's own GitHub
  Releases; and resolving a remote plugin source (`plugins.sources`), which does an explicit `git
clone` of a repo URL pinned to a tag or SHA — never a branch.
- **Local, movable, controllable cache.** The cache lives in the OS-appropriate default location
  (XDG cache dir on Linux, `~/Library/Caches/rtunk` on macOS, `%LOCALAPPDATA%\rtunk\cache` on
  Windows), overridable via `--cache-dir` flag > `RTUNK_CACHE_DIR` env var > `cache.dir` config
  field > default, in that priority order. It is content-addressed, so it is safe to copy between
  machines, and nothing is ever uploaded from it.
- **`rtunk-ignore` with permanent trunk compatibility.** The native inline ignore directive is
  `rtunk-ignore(linter/rule): reason`. `trunk-ignore(...)` and its `-all`/`-begin`/`-end` variants
  are accepted forever as compatible aliases, so migrating from trunk never requires rewriting
  existing ignore comments.
- **Reproducibility.** Linters, runtimes, and tools are version-pinned in config and installed
  hermetically per that pin. There is no silent fallback to whatever happens to already be on
  `PATH`.
- **Checksum-verified downloads.** Every downloaded binary is fetched over HTTPS only and verified
  against a SHA256 checksum before use.

## Implementation

rtunk is implemented in Go: a single static binary, straightforward cross-compilation, and a good
fit for an orchestration-heavy workload built around process execution, pipes, timeouts, and
concurrency.

## Where to go next

See [ROADMAP.md](./ROADMAP.md) for the staged build-out of rtunk's functionality, from reading an
existing trunk configuration through CLI flag compatibility.
