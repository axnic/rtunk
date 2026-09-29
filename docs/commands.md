# Command reference

The authoritative, public description of every command and flag `rtunk` ships: what each one does,
what it takes, and what it defaults to. Verified against the built binary (`rtunk help --all` and
`rtunk <command> --help`), not against any other document. For why the CLI is shaped this way, its
run semantics, and implementation status, see [cli.md](./cli.md) (contributor/maintainer audience).

Every command also accepts `-h`/`--help` for this same information at the terminal.

## Global flags

These apply to every command below; they are not repeated in each command's own table.

| Flag              | Argument | Default                                            | Meaning                                                                                 |
| ----------------- | -------- | -------------------------------------------------- | --------------------------------------------------------------------------------------- |
| `-h`, `--help`    | —        | —                                                  | Show context-sensitive help.                                                            |
| `--config`        | `STRING` | nearest `.rtunk/rtunk.yaml` or `.trunk/trunk.yaml` | Path to trunk.yaml.                                                                     |
| `--cache-dir`     | `STRING` | OS cache dir (`$RTUNK_CACHE_DIR`)                  | Plugin cache directory.                                                                 |
| `--version`       | —        | —                                                  | Print rtunk's own version and exit.                                                     |
| `--ci`            | —        | —                                                  | Accepted for trunk compatibility; rtunk is always CI-safe, this has no effect.          |
| `-v`, `--verbose` | —        | —                                                  | Accepted for trunk compatibility; rtunk already prints this detail, this has no effect. |

## Everyday commands

### `rtunk check`

Run enabled checks against source files (read-only).

```bash
rtunk check [<path>...] [flags]
```

Arguments:

- `<path>...`: paths to check (default: changed files, see `--from`).

Flags:

| Flag                    | Argument | Default                                                    | Meaning                                                                                                         |
| ----------------------- | -------- | ---------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------- |
| `--no-progress`         | —        | —                                                          | Do not print the per-linter progress lines on stderr.                                                           |
| `--ascii`               | —        | —                                                          | Use ASCII glyphs in the live view.                                                                              |
| `--live-height`         | `INT`    | half the terminal height, minimum 3 (`$RTUNK_LIVE_HEIGHT`) | Maximum height of the live view in lines.                                                                       |
| `--format`              | `STRING` | `human`                                                    | Output format: `human`, `sarif` (for CI) or `json`.                                                             |
| `--from`                | `STRING` | —                                                          | Diff base for the default file selection (e.g. `origin/main`, for CI).                                          |
| `-j`, `--jobs`          | `INT`    | number of CPUs                                             | Number of parallel linter workers.                                                                              |
| `--format-before-check` | —        | —                                                          | Run every formatter, then check the reformatted files.                                                          |
| `-y`, `--fix`           | —        | —                                                          | Apply linter fixes (fix commands and finding-level autofixes) to what checking found, then report what remains. |
| `--verify-stable`       | —        | —                                                          | With `--format-before-check`, verify the formatting result is stable instead of a single pass.                  |
| `--filter`              | `STRING` | —                                                          | Comma-separated linter id allow-list, or `--filter=-id,-id...` deny-list (trunk compatibility).                 |
| `--exclude`             | `STRING` | —                                                          | Comma-separated linter id deny-list; shorthand for an inverse `--filter` (trunk compatibility).                 |
| `--security-only`       | —        | —                                                          | Run only commands tagged `is_security: true`, skipping every other check.                                       |
| `-n`, `--no-fix`        | —        | —                                                          | Accepted for trunk compatibility; has no effect. `--fix` always wins if both are given.                         |
| `--print-failures`      | —        | —                                                          | Accepted for trunk compatibility; check already always prints failures, this has no effect.                     |

Example:

```bash
rtunk check --from origin/main
```

### `rtunk fmt`

Run configured formatters against source files.

```bash
rtunk fmt [<path>...] [flags]
```

Arguments:

- `<path>...`: paths to format (default: changed files, see `--from`).

Flags:

| Flag               | Argument | Default                                                    | Meaning                                                                                                   |
| ------------------ | -------- | ---------------------------------------------------------- | --------------------------------------------------------------------------------------------------------- |
| `--no-progress`    | —        | —                                                          | Do not print the per-linter progress lines on stderr.                                                     |
| `--ascii`          | —        | —                                                          | Use ASCII glyphs in the live view.                                                                        |
| `--live-height`    | `INT`    | half the terminal height, minimum 3 (`$RTUNK_LIVE_HEIGHT`) | Maximum height of the live view in lines.                                                                 |
| `--format`         | `STRING` | `human`                                                    | Output format: `human` or `json` (`sarif` is only supported by `check`).                                  |
| `--from`           | `STRING` | —                                                          | Diff base for the default file selection (e.g. `origin/main`, for CI).                                    |
| `--force`          | —        | —                                                          | Also format files with both staged and unstaged changes (skipped with a warning by default).              |
| `-j`, `--jobs`     | `INT`    | number of CPUs                                             | Number of parallel linter workers.                                                                        |
| `-n`, `--check`    | —        | —                                                          | Report files that would be reformatted, without writing them. (alias: `--no-fix`)                         |
| `--verify-stable`  | —        | —                                                          | Verify the result is stable (write, dry-run check, write+check again if needed) instead of a single pass. |
| `--filter`         | `STRING` | —                                                          | Comma-separated linter id allow-list, or `--filter=-id,-id...` deny-list (trunk compatibility).           |
| `--exclude`        | `STRING` | —                                                          | Comma-separated linter id deny-list; shorthand for an inverse `--filter` (trunk compatibility).           |
| `--print-failures` | —        | —                                                          | Accepted for trunk compatibility; fmt already always prints failures, this has no effect.                 |

Example:

```bash
rtunk fmt --check
```

### `rtunk run`

Run a specified action (shortcut for `actions run`; identical flags and behavior, see
[`rtunk actions run`](#rtunk-actions-run)).

```bash
rtunk run [<args>...] [flags]
```

Arguments:

- `<args>...`: `<action-id> [-- args...]` when `--hook` is not given; otherwise just the args to
  forward.

Flags:

| Flag     | Argument | Default | Meaning                                                                                |
| -------- | -------- | ------- | -------------------------------------------------------------------------------------- |
| `--hook` | `STRING` | —       | Run every enabled action triggered by this git hook, instead of a single action by id. |

Example:

```bash
rtunk run <action-id>
```

## Configuration inspection

### `rtunk config print`

Print the fully resolved configuration.

```bash
rtunk config print [flags]
```

Flags:

| Flag       | Argument | Default | Meaning                          |
| ---------- | -------- | ------- | -------------------------------- |
| `--output` | `STRING` | `yaml`  | Output format: `yaml` or `json`. |

Example:

```bash
rtunk config print --output json
```

### `rtunk plugins print`

Print all configuration available across all plugins, resolved. Can be very large; useful as a
registry dump. Replaces `config print --all`, which is removed.

```bash
rtunk plugins print [flags]
```

Flags:

| Flag       | Argument | Default | Meaning                          |
| ---------- | -------- | ------- | -------------------------------- |
| `--output` | `STRING` | `yaml`  | Output format: `yaml` or `json`. |

Example:

```bash
rtunk plugins print
```

## Linters and actions

### `rtunk linters list`

List all linters available for the current configuration.

```bash
rtunk linters list [flags]
```

Flags:

| Flag       | Argument | Default | Meaning                                                      |
| ---------- | -------- | ------- | ------------------------------------------------------------ |
| `--all`    | —        | —       | Also list the linters that match no file in this repository. |
| `--format` | `STRING` | `human` | Output format: `human` or `json`.                            |

Example:

```bash
rtunk linters list --all
```

### `rtunk linters enable`

Enable one or more linters.

```bash
rtunk linters enable <id>...
```

Arguments:

- `<id>...`: linter id(s) to enable, optionally `@version`.

Only the global flags apply.

Example:

```bash
rtunk linters enable shellcheck@0.10.0
```

### `rtunk linters disable`

Disable one or more linters.

```bash
rtunk linters disable <id>...
```

Arguments:

- `<id>...`: linter id(s) to disable.

Only the global flags apply.

Example:

```bash
rtunk linters disable shellcheck
```

### `rtunk actions list`

List actions available for the current configuration.

```bash
rtunk actions list [flags]
```

Flags:

| Flag       | Argument | Default | Meaning                           |
| ---------- | -------- | ------- | --------------------------------- |
| `--format` | `STRING` | `human` | Output format: `human` or `json`. |

Example:

```bash
rtunk actions list
```

### `rtunk actions enable`

Enable one or more actions.

```bash
rtunk actions enable <id>...
```

Arguments:

- `<id>...`: action id(s) to enable.

Only the global flags apply.

Example:

```bash
rtunk actions enable <action-id>
```

### `rtunk actions disable`

Disable one or more actions.

```bash
rtunk actions disable <id>...
```

Arguments:

- `<id>...`: action id(s) to disable.

Only the global flags apply.

Example:

```bash
rtunk actions disable <action-id>
```

### `rtunk actions run`

Run an action on demand, or every action a git hook triggers. `rtunk run` is a shortcut for this
command.

```bash
rtunk actions run [<args>...] [flags]
```

Arguments:

- `<args>...`: `<action-id> [-- args...]` when `--hook` is not given; otherwise just the args to
  forward.

Flags:

| Flag     | Argument | Default | Meaning                                                                                |
| -------- | -------- | ------- | -------------------------------------------------------------------------------------- |
| `--hook` | `STRING` | —       | Run every enabled action triggered by this git hook, instead of a single action by id. |

Example:

```bash
rtunk actions run <action-id>
```

### `rtunk actions history`

Show recent action runs.

```bash
rtunk actions history [flags]
```

Flags:

| Flag      | Argument | Default | Meaning                                     |
| --------- | -------- | ------- | ------------------------------------------- |
| `--id`    | `STRING` | —       | Restrict to one action id.                  |
| `--limit` | `INT`    | `20`    | Maximum entries to show. (alias: `--count`) |

Example:

```bash
rtunk actions history --id <action-id> --limit 5
```

## Administration

### `rtunk cache clean`

Remove the entire rtunk cache: downloads, plugin sources, and logs.

```bash
rtunk cache clean
```

Only the global flags apply.

Example:

```bash
rtunk cache clean
```

### `rtunk cache prune`

Remove cache entries no repository currently needs.

```bash
rtunk cache prune
```

Only the global flags apply.

Example:

```bash
rtunk cache prune
```

### `rtunk git-hooks sync`

Install git hooks for enabled actions. Idempotent: re-running it rewrites the same hook files.

```bash
rtunk git-hooks sync [flags]
```

Flags:

| Flag      | Argument | Default | Meaning                                     |
| --------- | -------- | ------- | ------------------------------------------- |
| `--force` | —        | —       | Overwrite an existing, non-rtunk hook file. |

Example:

```bash
rtunk git-hooks sync
```

### `rtunk git-hooks unsync`

Remove rtunk-installed git hooks (what `sync` installed).

```bash
rtunk git-hooks unsync
```

Only the global flags apply.

Example:

```bash
rtunk git-hooks unsync
```

### `rtunk init`

Initialize rtunk in this repository.

```bash
rtunk init [flags]
```

Flags:

| Flag      | Argument | Default | Meaning                                    |
| --------- | -------- | ------- | ------------------------------------------ |
| `--force` | —        | —       | Overwrite an existing `.rtunk/rtunk.yaml`. |

Example:

```bash
rtunk init
```

### `rtunk deinit`

Remove rtunk's configuration and installed artifacts.

```bash
rtunk deinit [flags]
```

Flags:

| Flag          | Argument | Default | Meaning                                                                     |
| ------------- | -------- | ------- | --------------------------------------------------------------------------- |
| `-y`, `--yes` | —        | —       | Accepted for trunk compatibility; deinit never prompts, this has no effect. |

Example:

```bash
rtunk deinit
```

### `rtunk logs list`

List this repository's recent runs.

```bash
rtunk logs list
```

Only the global flags apply.

Example:

```bash
rtunk logs list
```

### `rtunk logs show`

Show one run's log (default: the latest).

```bash
rtunk logs show [<run>] [flags]
```

Arguments:

- `<run>`: run name (or a unique prefix of it) as printed by `logs list`, or `latest`.

Flags:

| Flag     | Argument | Default | Meaning                                            |
| -------- | -------- | ------- | -------------------------------------------------- |
| `--json` | —        | —       | Print the raw JSONL instead of the text rendering. |

Example:

```bash
rtunk logs show latest
```

### `rtunk logs clean`

Delete this repository's run logs.

```bash
rtunk logs clean [flags]
```

Flags:

| Flag    | Argument | Default | Meaning                                              |
| ------- | -------- | ------- | ---------------------------------------------------- |
| `--all` | —        | —       | Delete every repository's logs, not just this one's. |

Example:

```bash
rtunk logs clean
```

## Renovate

Both `enable` and `disable` warn on stderr when no Renovate config file at the repository root
contains the regexManager `renovate config` prints.

### `rtunk renovate enable`

Annotate `trunk.yaml`'s version pins for Renovate.

```bash
rtunk renovate enable
```

Only the global flags apply.

Example:

```bash
rtunk renovate enable
```

### `rtunk renovate disable`

Remove the Renovate annotations from `trunk.yaml`.

```bash
rtunk renovate disable
```

Only the global flags apply.

Example:

```bash
rtunk renovate disable
```

### `rtunk renovate config`

Print the Renovate `regexManagers` config to add.

```bash
rtunk renovate config
```

Only the global flags apply.

Example:

```bash
rtunk renovate config
```

## Internal commands (hidden)

`toolbox` is callable but absent from the default `rtunk --help`; `rtunk help --all` lists it.

### `rtunk toolbox download`

Download one runtime or tool into the local cache.

```bash
rtunk toolbox download <category> <id>
```

Arguments:

- `<category>`: resource category, one of `runtime` or `tools`.
- `<id>`: resource id, optionally `@version`.

Only the global flags apply.

Example:

```bash
rtunk toolbox download tools shellcheck@0.10.0
```

### `rtunk toolbox where`

Print a cached item's install directory (not its shim).

```bash
rtunk toolbox where <category> <id>
```

Arguments:

- `<category>`: resource category, one of `runtime` or `tools`.
- `<id>`: resource id, optionally `@version`.

Only the global flags apply.

Example:

```bash
rtunk toolbox where tools shellcheck@0.10.0
```

### `rtunk toolbox exec` (alias `x`)

Run a command from a runtime or tool, downloading it first if missing. `<cmd>` is the item's shim
when equal to `<id>`, else an executable in its install directory.

```bash
rtunk toolbox exec <category> <id> <args>... [flags]
```

Arguments:

- `<category>`: resource category, one of `runtime` or `tools`.
- `<id>`: resource id, optionally `@version`.
- `<args>...`: command to run, then its arguments.

Flags:

| Flag            | Argument | Default | Meaning                                |
| --------------- | -------- | ------- | -------------------------------------- |
| `--interactive` | —        | —       | Bind stdin and stdout to the terminal. |

Example:

```bash
rtunk toolbox x runtime python@3.14 -- python3 --version
```
