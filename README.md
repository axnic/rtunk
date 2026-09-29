# rtunk

[![Go version](https://img.shields.io/github/go-mod/go-version/xunleii/rtunk)](go.mod)
[![License: MIT](https://img.shields.io/github/license/xunleii/rtunk)](LICENSE)

rtunk is a from-scratch, fully open-source rewrite of [trunk.io's Code Quality
CLI](https://docs.trunk.io/code-quality/overview) — `trunk`, the command that orchestrates dozens
of existing linters, formatters, and security scanners behind a single declarative config, with
hermetic per-project tool versions, git-aware "only check changed files" behavior, output
normalization, and caching. That orchestration model is genuinely good, which is why rtunk exists
at all: `trunk` itself ships as a closed-source binary, which is hard to audit or get approved in a
professional or regulated environment. rtunk offers the same orchestration experience as an
auditable, fully open-source, 100% local tool — no telemetry, no cloud account, no daemon, no
self-upgrade.

## Quickstart

Full installation options, including a contributor build from source, are in
[docs/installation.md](docs/installation.md). The fastest path, if you already have Go on `PATH`:

```bash
go install github.com/xunleii/rtunk/cmd/rtunk@latest
```

Then, in any git repository:

```console
$ rtunk init
initialized rtunk at .../.rtunk/rtunk.yaml
next: rtunk linters enable <linter>, rtunk actions enable <action>, rtunk git-hooks sync

$ rtunk linters enable yamllint

$ rtunk check
.rtunk/rtunk.yaml  (1)
  1:1  medium  missing document start "---"  yamllint/document-start

example.yaml  (2)
  1:1  medium  missing document start "---"  yamllint/document-start
  1:8  high    too many spaces after colon   yamllint/colons

Checked 2 files with 1 linter in 0.1s
✖ 3 issues (1 high · 2 medium · 0 low)
```

`rtunk init` scaffolds `.rtunk/rtunk.yaml` (or defers to an existing `.trunk/trunk.yaml` — see the
[migration guide](docs/migration-from-trunk.md) — if the repository already has one). `rtunk
linters enable <id>` turns a linter on. `rtunk check`, given no paths, checks whatever changed —
here, every file in the still-commit-less repository — and prints the report above. Full command
and flag reference: [docs/commands.md](docs/commands.md); full config key reference:
[docs/configuration.md](docs/configuration.md).

## How rtunk differs from trunk

A closed list of deliberate decisions, not open bugs. Full rationale, migration impact, and
"why" for each: [docs/migration-from-trunk.md](docs/migration-from-trunk.md).

- **Plain `check` never runs formatters.** trunk's `check` also runs every enabled formatter and
  reports an unformatted file as a finding; rtunk's `check` only ever runs genuine checking
  commands. Pass `--format-before-check` to reproduce trunk's default.
- **Windows isn't a supported host platform.** There is no rtunk build for Windows; a command
  variant declared Windows-only is skipped rather than attempted.
- **The run journal doesn't log file selection or provisioning.** `rtunk logs` records the commands
  actually run and what they produced, not what led up to running them.
- **Actions can't trigger on file changes or a schedule.** Both trigger kinds require a background
  daemon, and rtunk refuses to run as one — such an action's configuration is rejected outright.

## Documentation

- [docs/commands.md](docs/commands.md) — command and flag reference.
- [docs/configuration.md](docs/configuration.md) — config file schema, override precedence,
  `rtunk-ignore`/`trunk-ignore` syntax.
- [docs/installation.md](docs/installation.md) — full installation guide.
- [docs/migration-from-trunk.md](docs/migration-from-trunk.md) — moving an existing trunk
  repository to rtunk.
- [docs/architecture/README.md](docs/architecture/README.md) — internal architecture, for
  contributors making non-trivial changes.
- [CONTRIBUTING.md](CONTRIBUTING.md) — development environment, tests and lint, commit
  conventions.
- [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)
- [SECURITY.md](SECURITY.md) — reporting a vulnerability.
- [CHANGELOG.md](CHANGELOG.md) — release history.

## License

[MIT](LICENSE), copyright Alexandre NICOLAIE.
