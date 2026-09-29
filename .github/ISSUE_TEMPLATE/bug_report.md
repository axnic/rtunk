---
name: Bug report
about: Report unexpected or incorrect rtunk behavior
labels: bug
---

# Bug report

## rtunk version

Output of `rtunk --version`.

## Platform

OS and architecture (e.g. macOS arm64, Linux x86_64). Windows isn't a
supported host platform — see
[docs/migration-from-trunk.md](../../docs/migration-from-trunk.md).

## Configuration

Relevant excerpt of `.rtunk/rtunk.yaml` or `.trunk/trunk.yaml` — the
`lint`/`plugins` block involved, not necessarily the whole file.

## Linter or plugin involved

Name and version, if applicable (e.g. `golangci-lint2@2.13.2`). Leave blank
if this isn't linter-specific.

## Command run

Exact command, including flags (e.g. `rtunk check --from main`).

## Expected behavior

## Actual behavior

## Minimal reproduction

Does this reproduce against a minimal `.rtunk/rtunk.yaml` containing only the
linter/plugin involved, no other config? If not yet tried, say so — a
maintainer will likely ask for one before investigating further.
