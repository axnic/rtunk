# Security Policy

## Reporting a vulnerability

Report suspected vulnerabilities privately through
[GitHub Security Advisories](https://github.com/xunleii/rtunk/security/advisories/new),
not a public issue. This channel is private between the reporter and the
maintainer, so nothing is disclosed publicly until a fix is available and
disclosure is coordinated.

Include what's needed to reproduce: the affected version or commit, the
command run, the plugin or linter involved if relevant, and the expected
versus actual behavior. There's no formal response-time SLA yet — rtunk has a
single maintainer — but reports are triaged as they arrive.

Do not report vulnerabilities through public GitHub issues, discussions, or
pull requests.

## Supported versions

rtunk has no packaged release yet: it is pre-`v1.0`, and `--version` reports
`dev`. Only the `main` branch is supported. There is no backport policy —
fixes land on `main`, and no older release branch exists to patch separately.

## Known limitation: download trust model

rtunk downloads and executes third-party binaries (linters and tools)
declared by trunk plugin `downloads:` recipes. Every download is fetched over
HTTPS only (plain `http`, including on redirect, is rejected) and streamed
through a SHA256 hasher before being moved into the content-addressed cache.
However, trunk plugin recipes carry no upstream checksum to verify against, so
the trust model is trust-on-first-use (TOFU): the first download of a given
artifact is accepted as-is, and a source compromised at that exact moment is
not detected.

This is a known, documented limitation — not a vulnerability to report. See
[AGENTS.md](AGENTS.md#non-negotiable-design-rules) ("Checksum-verified
downloads") and
`docs/superpowers/specs/2026-09-10-v0.2-download-design.md` ("Checksum
model") for the current design and its rationale. The planned hardening is
`rtunk.lock` (pinned, verifiable checksums per artifact), tracked on the
project roadmap and not yet implemented.
