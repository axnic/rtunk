# CLI reference and behavioral rules

This document is the authoritative target design for rtunk's command surface and the run
semantics of `check`, `fmt` and `run`. It is authoritative over older specs under
`docs/superpowers/`. Terminal rendering lives in [ux.md](./ux.md); staging lives in
[ROADMAP.md](../ROADMAP.md); project principles live in [AGENTS.md](../AGENTS.md).

**Implementation status.** `v0.8` (CLI reshape) is implemented, under internal/cli: `.rtunk` over
`.trunk` precedence and the project root rule (`findTrunkYAML` in internal/cli/findtrunk.go), file
selection and `--from` (internal/cli/selection.go), exit codes (`cmd/rtunk/main.go`), `fmt`
working-tree-only behavior with `--force`, `linters {list,enable,disable}`, `git-hooks sync|unsync`,
`plugins print`, the hidden `toolbox` group, `logs list|show|clean`, `help [--all]`, and
`cache destroy|prune --older-than`. `v0.9` item 1 is implemented: `check` and `fmt` render through
the plain renderer in `internal/cli/render` (see [ux.md](./ux.md)), with `--no-progress`; this is a
breaking change, the former `file:line severity [rule] message` lines and `N issue(s) in M file(s)`
summary are gone. `v0.9` item 2 is implemented: `--format human|sarif|json` and ANSI color. `v0.9` item 4
is implemented: the filtered `linters list` / `actions list` (internal/cli/list.go), a breaking
change, the former `* name  description` format is gone. `v0.9` item 3 is implemented: the TTY live
view on stderr, with `--ascii` and `--live-height` (`RTUNK_LIVE_HEIGHT`); all four `v0.9` items are
done. Still planned: `v1.1` (`rtunk.lock`).

## Cross-cutting rules

### Project root

`check`, `fmt` and `run` only execute in a directory that has an ancestor (itself included)
containing a config (`.trunk` or `.rtunk`). If no root is found, rtunk refuses with an explicit
message. This guards against running in `$HOME`, including via `rtunk check .`. (Implemented.)

Precedence when `.trunk` and `.rtunk` coexist: `.rtunk` wins over `.trunk`. Exactly one of the two
is read, with no merge. (Implemented.)

### File selection

Without a path (`rtunk check`, `rtunk fmt`):

| Situation                      | Files processed                                                                                      |
| ------------------------------ | ---------------------------------------------------------------------------------------------------- |
| In git, branch has an upstream | diff from `merge-base(upstream, HEAD)` to the working tree (staged, unstaged, untracked-not-ignored) |
| In git, no upstream            | diff from `HEAD` to the working tree (staged, unstaged, untracked-not-ignored)                       |
| Not in git                     | nothing runs (no timestamp fallback)                                                                 |

`--from <ref>` forces the diff base. It exists for CI (detached HEAD, no upstream). When nothing
runs, rtunk prints `rtunk: no files to check` (`check`) or `rtunk: no files to format` (`fmt`) and
exits `0`. (Implemented, internal/cli/selection.go.)

With explicit path(s) (`rtunk check .`), every file under the path is processed: `git ls-files -co
--exclude-standard` in git, everything otherwise.

Default-to-changed-files is rtunk's performance lever: the common case checks a handful of files,
not the whole repository.

### Output

`--no-progress` (`check` and `fmt`, implemented) suppresses the per-linter progress lines on stderr;
warnings and errors are still printed. It also disables the live view. The default output is the
plain renderer described in [ux.md](./ux.md).

Live view (`check` and `fmt`, implemented): when stderr is a terminal, `--no-progress` is not set and
`TERM` is not `dumb`, a live area is drawn on stderr while the run progresses and erased before the
stdout report; otherwise the per-linter progress lines are emitted unchanged. stdout, exit codes and
run logs are not affected.

- `--ascii`: ASCII glyphs in the live view. Also chosen automatically for a non-UTF-8 locale (first
  non-empty of `LC_ALL`, `LC_CTYPE`, `LANG` must contain `utf-8` or `utf8`).
- `--live-height <n>` (env `RTUNK_LIVE_HEIGHT`): maximum height of the live area in lines; default
  half the terminal height, minimum 3. Not a config file key.
- Ctrl+C erases the area and re-raises SIGINT.

`--format human|sarif|json` (`check` and `fmt`, implemented; default `human`):

- `human`: the report described in [ux.md](./ux.md). ANSI color is added only when stdout is a
  terminal and `NO_COLOR` is empty; stderr progress lines are never colored. Output outside a
  terminal is uncolored.
- `json`: one document on stdout: `{version: 1, command: "check"|"fmt", elapsed_ms, run_log,
files_checked, linters, issues: [{file, line, column, severity, message, linter, rule, url}],
failures: [{linter, error}], skipped: [], changed: []}`. All keys are always present, empty arrays
  are `[]` (never `null`), `line`/`column` are `0` when unknown.
- `sarif`: SARIF 2.1.0 for CI, `check` only: one run, `tool.driver` `rtunk`, deduplicated rules, one
  result per issue, failures as `toolExecutionNotifications` with `executionSuccessful: false`.
  `fmt --format sarif` is refused with `--format sarif is only supported by check` (exit `1`)
  before anything runs.

Progress lines stay on stderr for every format (`--no-progress` silences them); stdout carries only
the report or document. `check --fix` under `json`/`sarif` emits a single document for the check
pass (`json` puts the formatter pass's files under `changed`). When no file is selected no document
is written (empty stdout, exit `0`). Exit codes are unchanged for every format.

### Exit codes

Identical to trunk's for `check`, `fmt` and `run`. trunk's public documentation publishes no table;
measured on trunk 1.25.0 (throwaway repo, shfmt linter):

| Case                                                        | Code |
| ----------------------------------------------------------- | ---- |
| `check` without finding                                     | `0`  |
| `check` with findings                                       | `1`  |
| `check` on a nonexistent path                               | `1`  |
| `check` with an invalid config                              | `1`  |
| `check` where a tool fails to run (e.g. install impossible) | `1`  |
| `fmt` without change                                        | `0`  |
| `fmt` that fixes files                                      | `0`  |

Only `0` and `1` exist, with no distinction between "findings" and "error" (consistent with
`docs/superpowers/specs/2026-09-12-check-v0.3-design.md`). A `fmt` that fixes files does not return
an error. For `run` and `fmt` with a failing tool (not measured on trunk): `0` on success, non-zero
on error. Verified: `cmd/rtunk/main.go` exits `1` on any returned error and `0` otherwise; a `fmt`
that fixes files exits `0`. (Implemented.)

### fmt

`fmt` writes to the working tree only and never touches the index (no `git add`). Partially staged
files are skipped with a warning unless `--force` is given. (Implemented.)

### Hidden commands

`toolbox` and other internal commands are callable but absent from the default help. `rtunk help
--all` lists everything. (Implemented.)

## Everyday commands

```text
rtunk check [--from <ref>] [--no-progress] [--ascii] [--live-height <n>] [--format human|sarif|json] [<path>...]
  -> read the config
  => [in parallel]
    -> download runtimes if needed
    -> determine the files to check (see "File selection")
  -> download the required tools
  -> run the check
  -> output the result
```

`rtunk fmt [--no-progress] [--ascii] [--live-height <n>] [--format human|json] [<path>...]` follows the same flow, with the `fmt` rules above.

`rtunk [actions] run <id>` runs an action in the current directory.

`check` and `fmt` take paths only. There are no trunk-style aliases. (Implemented.)

## Configuration inspection

- **`rtunk config print`**: print the current, fully resolved configuration.
- **`rtunk plugins print`**: print all configuration available across all plugins, resolved. Can
  be very large; useful as a registry dump. Replaces `config print --all`, which is removed.
  (Implemented.)

The originally planned `config {plugins,lint,actions,tools,runtimes} list|show` were never shipped
and stay dropped: `linters`/`actions` listing and `plugins print` cover the need.

## Internal commands (advanced, hidden)

- **`rtunk toolbox download {runtime,tools} <id>[@version]`**: download one specific runtime or
  tool. The item is required; there is no bare download-everything.
- **`rtunk toolbox where {runtime,tools} <id>[@version]`**: absolute path of the item's install
  directory (not its shim).
- **`rtunk toolbox exec|x {runtime,tools} <id>[@version] -- <cmd> [<args>...]`**: run `<cmd>`, which
  is the item's shim when equal to `<id>`, else an executable in its install directory.
  `--interactive` binds stdin/stdout, for instance to open a python or node shell from a runtime;
  otherwise stdin is unbound.
- **`rtunk lock`**: see "Download integrity" below (planned, `v1.1`).

`toolbox download`, `exec` and `where` are implemented and replace the former top-level `download`,
`exec|x` and `where` (`v0.2`), narrowed to `{runtime,tools}`; the old names are removed, with no
compat aliases.

## Administration

### Cache

- **`rtunk cache destroy`**: remove the entire cache (replaces `cache clean`). (Implemented.)
- **`rtunk cache prune --older-than <duration>`** (required flag): remove entries unused for the
  given duration. The duration accepts Go durations plus a `d` days suffix (`30d`). Removes the
  `installs/<cat>/<id>/<version>` and `shims/<cat>/<id>/<version>` directories whose mtime is older.
  Every use (check/fmt/actions runtime and tool resolution, and `toolbox exec`) touches those mtimes
  via `download.Touch`; caches created before this change look old until their next use. Known gap:
  `--older-than` replaces the former behavior of pruning what the config no longer references, and
  there is deliberately no project registry, so detecting truly unreferenced entries is out of v1.
  (Implemented.)

### Linters and actions

The two groups are symmetric:

- **`rtunk linters {list,enable,disable} <id>[@version]`**: list linters grouped as enabled
  (`id@version`), available for this repo (matching at least one repository file, not enabled) and,
  with `--all`, the rest; add or remove a linter in the config. `list [--all] [--format
human|json]`. (`list` implemented, internal/cli/list.go.)
- **`rtunk actions {list,enable,disable,history}`**: list actions (`list [--format human|json]`,
  groups `Enabled` and `Available (not enabled)`, no `--all`); enable or disable them; show
  the history of actions in this repo (`history <id>`).

`linters list` and `actions list` share one layout (see [ux.md](./ux.md)). `linters ...` replaces
`check enable|disable|list` (implemented; `check` takes paths only). File counts come from every
repository file matched against the linter's `files:` criteria.

### Miscellaneous

- **`rtunk git-hooks sync|unsync`**: enable or disable the git hooks defined by actions (implemented;
  `install`, `uninstall` and the alias are removed, with no compat aliases).
- **`rtunk init`**: initialize a repository that has neither `.trunk` nor `.rtunk`.
- **`rtunk logs list [<file>...]`**, **`rtunk logs show <uid>|latest [<file>...]`**,
  **`rtunk logs clean`**: inspect and clean per-run logs (see
  `docs/superpowers/specs/2026-09-26-run-logs-design.md`). Implemented in internal/cli/logs.go.

## Renovate

- **`rtunk toolbox renovate enable|disable`**: turn Renovate annotations in the configuration on or
  off (`enable` is the former `annotate`; `disable` strips the annotations). Both warn on stderr when
  no Renovate config file at the repo root contains the regexManager. (Implemented.)
- **`rtunk toolbox renovate config`**: print the Renovate configuration with the custom
  regexManager used for version management. Eventually includes `postUpgradeTasks: rtunk lock`.
  (Implemented, without the `postUpgradeTasks` entry.)

These replace the former `rtunk renovate annotate|config` (`v1.1`).

## Download integrity

### Current model

Specified in `docs/superpowers/specs/2026-09-10-v0.2-download-design.md` ("Checksum model"):

- HTTPS-only fetch (`http` rejected, redirects included);
- stream passed through a SHA256 hasher into a temporary file;
- renamed to `blobs/sha256/<hex>` only once the hash is known (content-addressing, TOFU).

trunk plugin `downloads:` recipes carry no upstream checksum.

Known limit: TOFU. The first download is accepted as is; a source compromised at that moment is
not detected.

### Next step (after check/fmt/run, non-blocking for v1; roadmap `v1.1`)

A versioned `rtunk.lock` in the repo, `id@version@platform -> sha256` (go.sum style):

- entry present: mismatch is a blocking error, temp file deleted, no fallback;
- entry absent locally: download and record;
- entry absent with `--locked` or `CI=true`: error;
- hidden `rtunk lock` precomputes hashes for the other platforms;
- Renovate: `postUpgradeTasks: rtunk lock`, to be added to `toolbox renovate config`.

### Later (v1+)

Reuse checksums and signatures from the aqua-registry.
