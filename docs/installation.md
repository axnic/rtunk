# Installation

`rtunk` ships no prebuilt binary yet — there is no `.goreleaser.yml`, no `Makefile`, and no GitHub
Releases artifact to download. Every build reports `dev` for `--version` regardless of how it was
built. The two paths below build from source; there is no third, "just download it" option today.

## Supported platforms

macOS and Linux only. Windows is not a supported host platform — a deliberate divergence recorded
in [ROADMAP.md](../ROADMAP.md#deliberate-divergences-from-trunk) — so neither path below is
verified there. Both paths work on any macOS/Linux architecture Go itself supports (e.g.
`darwin/arm64`, `darwin/amd64`, `linux/amd64`, `linux/arm64`), since Go cross-compiles.

## Option 1: `go install`

The quickest path if you already have Go on `PATH`.

Prerequisites: Go ≥ 1.27.0 (the floor declared in [`go.mod`](../go.mod)).

```bash
go install github.com/xunleii/rtunk/cmd/rtunk@latest
```

This installs `rtunk` to `$(go env GOBIN)`, or `$(go env GOPATH)/bin` if `GOBIN` is unset — make
sure that directory is on `PATH`.

Verify:

```bash
rtunk --version
rtunk help
```

`--version` prints `dev` — expected, not an error; rtunk has no version-stamped release build yet.
`help` prints the command list (`check`, `fmt`, `actions`, `linters`, `plugins`, `git-hooks`,
`init`, `deinit`, `run`, `renovate`, `logs`, and the extended `config`/`cache` commands).

## Option 2: clone and build (contributor path)

Use this if you're contributing, or want the exact pinned toolchain the project develops against
(this is the setup [CONTRIBUTING.md](../CONTRIBUTING.md) assumes).

Prerequisites: [mise](https://mise.jdx.dev/) installed.

```bash
git clone https://github.com/xunleii/rtunk.git
cd rtunk
mise trust    # if mise prompts about this repo's .mise.toml
mise install  # installs the go and trunk versions pinned in .mise.toml
go build -o rtunk ./cmd/rtunk
```

`mise install` resolves the toolchain declared in [`.mise.toml`](../.mise.toml): the Go compiler
(matching `go.mod`'s `go 1.27.0` floor) and `trunk`, the metalinter rtunk dogfoods on its own
source and docs. Only Go is required to build `rtunk` itself; `trunk` is needed for the lint stack
(`./rtunk fmt`, `./rtunk check`) contributors run before committing.

Verify:

```bash
./rtunk --version
./rtunk help
```

Same expected output as Option 1: `--version` prints `dev`, `help` prints the command list.

## Upgrading

There is no `rtunk upgrade`/self-upgrade command, by design — rtunk stays a local tool with no
network calls it didn't explicitly ask you to trigger. To upgrade, repeat whichever install option
you used: re-run `go install .../rtunk@latest`, or `git pull` and rebuild.
