# Contributing to rtunk

The path from a fresh clone to an accepted change: setting up a development environment, the
tests and lint every change must pass locally, this project's commit conventions, and how to
submit a pull request. Audience: external contributors.

## Development environment

Use [docs/installation.md](docs/installation.md)'s "clone and build" path rather than
`go install` — it's the setup this file assumes:

```bash
git clone https://github.com/xunleii/rtunk.git
cd rtunk
mise trust    # if mise prompts about this repo's .mise.toml
mise install  # installs the go and trunk versions pinned in .mise.toml
go build -o rtunk ./cmd/rtunk
```

`mise install` resolves the toolchain declared in [`.mise.toml`](.mise.toml): the Go compiler
matching `go.mod`'s floor, and `trunk`, the metalinter used below. See
[docs/installation.md](docs/installation.md) for prerequisites and platform support (macOS and
Linux only) — this file doesn't repeat that.

## Tests and lint

Run all of the following before opening a pull request:

```bash
go test ./...
go vet ./...
gofmt -l .
trunk check   # or: ./rtunk check, once self-hosting is stable enough to require it
```

`go test ./...` and `go vet ./...` exit `0` with no output on success. `gofmt -l .` exits `0` and
prints nothing when the tree is already formatted; any path it lists needs `gofmt -w`. `trunk
check` runs the full lint stack declared in [`.trunk/trunk.yaml`](.trunk/trunk.yaml) — `gofmt`,
`golangci-lint2`, `markdownlint`, `prettier`, `yamllint`, `taplo`, plus the security scanners
(`grype`, `osv-scanner`, `checkov`, `trufflehog`); it's read-only and exits non-zero on any
finding. `./rtunk check` reads the same `.trunk/trunk.yaml` and already covers `gofmt` and
`golangci-lint2` findings the same way — e.g. `./rtunk check internal/cli/config.go` reports the
same `golangci-lint2/revive` findings `trunk check` would. Both commands accept path arguments to
scope a run to what you changed; with none, the default is changed files (see `--from`), not the
whole repository.

There is no CI workflow yet (no `.github/workflows/`) — these four commands are the actual gate
right now, enforced by review rather than automation.

For documentation-only changes, this repository's own convention is `./rtunk fmt <path>` then
`./rtunk check <path>` scoped to the files touched, in place of the full `trunk check` above.

## Commit conventions

Every commit follows `type[scope]: Subject` — a one-character type symbol (`+` Add, `-` Remove,
`~` Improve, `!` Fix, `=` Refactor, `^` Bump, `>` Move, `<` Revert, `@` Docs, `$` Security, `?`
Experiment, `*` Wildcard; `+!`/`~!`/`-!` for breaking changes), a mandatory bracketed scope, and
an imperative, sentence-case subject with no trailing period. An AI-assisted commit carries an
`Assisted-by: <provider>:<model-id>` trailer, never `Co-authored-by:` — a tool a human directs
isn't a co-author. Every commit is GPG-signed (`git commit -S`); never add `-s`/`--signoff`, since
DCO sign-off is the human committer's own attestation and an AI assistant must stay out of it.
Full type/scope tables, the commitlint rules commits are checked against, and the drafting
workflow: [`.agents/skills/git-commit/SKILL.md`](.agents/skills/git-commit/SKILL.md) — the
canonical reference; this section is a summary, not a substitute.

## Submitting a change

1. Fork the repository and branch off `main` — the project's only active integration branch;
   there is no packaged release yet, so nothing downstream of it to keep separate.
2. Make the change, running the commands under "Tests and lint" as you go, not only at the end.
3. Commit following "Commit conventions" above: GPG-signed, `Assisted-by:` if AI-assisted, never
   `--signoff`.
4. Open a pull request against `main` using the repository's pull request template
   (`.github/PULL_REQUEST_TEMPLATE.md`).
5. A maintainer re-runs "Tests and lint" during review — there's no CI to do it automatically yet.
   Passing all four locally before opening the PR is what keeps review fast.
