# Configuration reference

Every key `rtunk` reads from its own config file — native and trunk-compatible — every override and
its precedence, and the inline ignore-comment syntax. Audience: anyone configuring `rtunk` for a
repository. Verified against `pkg/trunk/config`'s actual struct/tag definitions and `pkg/ignore`'s
actual matching logic, not transcribed from prose.

This document covers the config file itself: the keys a repository writes into
`.rtunk/rtunk.yaml`/`.trunk/trunk.yaml`. For the conceptual model of what a plugin repository
contributes (linters, tools, runtimes, actions, and how they reference each other), see
[architecture/plugin-model.md](./architecture/plugin-model.md). For command/flag syntax, see
[commands.md](./commands.md).

## Config file discovery

`rtunk` looks for a config file starting at the current directory and walking upward to the git
repository root (or the filesystem root if not in a git repository). At each directory it checks,
in order, `.rtunk/rtunk.yaml` then `.trunk/trunk.yaml`; the first match found stops the search
(`internal/cli/shared.go:37-66`, `findConfig`). `--config <path>` bypasses discovery entirely and
reads that file instead — there is no environment variable for the config path.

When both `.rtunk/rtunk.yaml` and `.trunk/trunk.yaml` exist in the same directory, `.rtunk` wins
and `.trunk` is not read at all — the two are never merged (`internal/cli/shared.go:37-66`; also
stated as a non-negotiable rule at `AGENTS.md:48`). `rtunk init` warns to stderr, but still
succeeds, if it would write `.rtunk/rtunk.yaml` into a repository that already has a
`.trunk/trunk.yaml`, since from that point on the new file shadows it (`internal/cli/init.go:52-58`).

**`.rtunk/user.yaml` does not exist in the current implementation.** `AGENTS.md:35-36` describes a
"git-ignored `.rtunk/user.yaml` local override" as part of rtunk's design intent, but `findConfig`
only ever checks the two paths above — nothing in `pkg/trunk/config` or `internal/cli` reads a
`user.yaml` file, and no merge step for one exists. Treat that line as an unimplemented design goal,
not current behavior, until it ships.

## Schema overview

A config file has six top-level sections. All of them are optional in the sense that a missing
section decodes to its zero value (`gopkg.in/yaml.v3` unmarshalling into `trunkFile`,
`pkg/trunk/config/resolve.go:18-36`) — but a `version` line is what every real-world config and
`rtunk init`'s own scaffold write in practice.

| Key                | Type           | Default | Meaning                                                                                                         |
| ------------------ | -------------- | ------- | --------------------------------------------------------------------------------------------------------------- |
| `version`          | string         | `""`    | Schema version marker. Parsed and stored, not otherwise validated or acted on.                                  |
| `cli.version`      | string         | `""`    | Trunk CLI version marker, kept for trunk-compatibility. Parsed and stored, not otherwise validated or acted on. |
| `plugins.sources`  | list of source | `[]`    | Plugin repositories to merge linter/tool/runtime/action definitions from.                                       |
| `runtimes.enabled` | list of string | `[]`    | Runtime ids (optionally `id@version`) to activate.                                                              |
| `lint.enabled`     | list of string | `[]`    | Linter ids (optionally `id@version`) to activate.                                                               |
| `actions.enabled`  | list of string | `[]`    | Action ids (optionally `id@version`) to activate.                                                               |
| `actions.disabled` | list of string | `[]`    | Action ids `rtunk actions disable` records as turned off. See note below.                                       |

(`pkg/trunk/config/resolve.go:18-36`, the raw `trunkFile` shape a config file decodes into before
`Resolve` merges in plugin definitions.)

An `enabled:` entry is either a bare id (`golangci-lint2`) or `id@version` (`golangci-lint2@2.13.2`)
to pin which version of that id's downloads get resolved; `Resolve`/`Validate` strip the `@version`
suffix before checking the id exists (`pkg/trunk/config/filter.go:113-122`,
`pkg/trunk/config/validate.go:21-32`). Version pinning does not choose among a linter's several
`commands:` variants — nothing in the config file does that.

### `plugins.sources`

Each entry is either a **git source** (`id`, `uri`, `ref`) or a **local source** (`id`, `local`) —
mutually exclusive; an entry with neither `local:` nor `uri:` set fails to resolve with `plugin
source "<id>": neither local nor uri is set` (`pkg/trunk/config/config.go:32-37`,
`pkg/trunk/config/errors.go:39-48`, `pkg/trunk/config/resolve.go:115-134`).

| Field   | Applies to   | Required        | Meaning                                                                                                                                                                                                                                                                                        |
| ------- | ------------ | --------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `id`    | both         | yes             | Source identifier, referenced nowhere else in the config file but used to key caching and error messages.                                                                                                                                                                                      |
| `uri`   | git source   | yes             | Git remote URL, cloned via the system `git` binary (`init`/`fetch --depth 1`/`checkout`).                                                                                                                                                                                                      |
| `ref`   | git source   | yes in practice | Tag or commit SHA to fetch — passed directly to `git fetch origin <ref>`; an empty value fails the fetch. Never a branch (`AGENTS.md:72-73`).                                                                                                                                                  |
| `local` | local source | yes             | Filesystem path to the plugin repository, resolved relative to the config file's own directory. Must already exist — unlike a git source, a missing local path is a hard error (`plugin source "<id>": local path <path> does not exist`), never fetched (`pkg/trunk/config/errors.go:26-37`). |

A git source's parsed definitions (not its raw checkout) are cached under the resolved cache
directory, keyed by `uri`+`ref`, so a pinned ref is fetched from the network only once
(`pkg/trunk/config/docs.go:22-27`, `pkg/trunk/config/git.go`) — see "Cache directory" below for
where that cache lives.

### `actions.disabled`

`actions.disabled` is parsed and stored (`cfg.Actions.Disabled`,
`pkg/trunk/config/resolve.go:109`), but nothing else in the codebase reads it back — it has no
effect on which actions run. The actual gate is `actions.enabled`: `Resolve`'s `filterEnabled` trims
the action catalog down to exactly what `actions.enabled` lists
(`pkg/trunk/config/filter.go:18-20`, `pkg/trunk/config/config.go:44`). `rtunk actions disable <id>`
removes `<id>` from `enabled:` (which is what actually turns it off) and separately adds it to
`disabled:` for record-keeping — mirrored by `rtunk actions enable` doing the reverse
(`internal/cli/actions_edit.go:10-12`). Treat `disabled:` as a log of what was turned off, not a
config key that itself suppresses anything.

### Minimal example

The exact scaffold `rtunk init` writes to a fresh `.rtunk/rtunk.yaml` (`internal/cli/init.go:19-25`):

```yaml
version: "0.1"
plugins:
  sources:
    - id: trunk
      uri: https://github.com/trunk-io/plugins
      ref: v1.11.0
```

Enabled lists are intentionally omitted from the scaffold — an absent `enabled:` decodes to the
same empty list as `enabled: []` — and grown from there with `rtunk linters enable`/`rtunk actions
enable`.

### Fuller example

This repository's own `.trunk/trunk.yaml` (paraphrased, same shape) shows every section populated,
including `id@version` pinning and `actions.disabled`:

```yaml
version: "0.1"
cli:
  version: 1.25.0
plugins:
  sources:
    - id: trunk
      ref: v1.11.0
      uri: https://github.com/trunk-io/plugins
runtimes:
  enabled:
    - go@1.27.0
    - node@22.16.0
lint:
  enabled:
    - gofmt@1.20.4
    - golangci-lint2@2.13.2
    - markdownlint@0.49.1
    - prettier@3.9.6
actions:
  disabled:
    - trunk-announce
  enabled:
    - commitlint
    - trunk-check-pre-push
```

## Override precedence

### Config file path

1. `--config <path>` — read exactly that file, no discovery.
2. Automatic discovery — nearest `.rtunk/rtunk.yaml`, else nearest `.trunk/trunk.yaml`, walking
   from the current directory up to the git root (see "Config file discovery" above).

There is no environment variable for the config path (`internal/cli/cli.go:31`, the `CLI.Config`
field carries no `env:` tag, unlike `CacheDir` below).

### Cache directory

1. `--cache-dir <path>` — explicit flag value.
2. `RTUNK_CACHE_DIR` environment variable — same underlying field as `--cache-dir`
   (`internal/cli/cli.go:32`, `CacheDir string ... env:"RTUNK_CACHE_DIR"`); this is [kong](https://github.com/alecthomas/kong)'s
   standard flag-or-env binding, not two independently-read sources, so the flag wins only because
   an explicit `--cache-dir` overrides the value kong would otherwise take from the environment.
3. OS-default cache directory — `os.UserCacheDir()/rtunk/downloads` for the downloads cache
   (`pkg/cache/download/cache.go:14-23`) and `os.UserCacheDir()/rtunk/plugins` for the plugin
   source cache (`pkg/trunk/config/git.go:33-42`); XDG cache dir on Linux, `~/Library/Caches` on
   macOS, `%LOCALAPPDATA%` on Windows, per Go's `os.UserCacheDir()`.

**There is no `cache.dir` config-file key.** `AGENTS.md:79-83` describes the precedence as
`--cache-dir` flag > `RTUNK_CACHE_DIR` env var > `cache.dir` config field > default — the config
field does not exist in `pkg/trunk/config` (no `yaml:"cache"` or `yaml:"dir"` tag anywhere in the
package; `grep -rn 'yaml:"' pkg/trunk/config/*.go` finds none). Every cache-consuming code path
takes a single `cacheDir string` parameter sourced only from `cli.CacheDir`, never from the resolved
`config.Config` (`internal/cli/cache_clean.go`, `internal/cli/cache_prune.go`,
`internal/cli/check_run.go:57,85`, `internal/cli/fmt.go:55,87`, and every other
`resolveConfig(..., cli.CacheDir, ...)` call site). Treat the AGENTS.md line as describing the
design intent, not the current schema.

When `--cache-dir`/`RTUNK_CACHE_DIR` is set to `<dir>`, downloads live under `<dir>/downloads` and
the plugin source cache under `<dir>/plugins` — the same two-subdirectory split as the OS default,
just rooted differently.

### Environment

Beyond `RTUNK_CACHE_DIR` above, `rtunk` reads a handful of other environment variables that affect
output rendering rather than configuration resolution (`internal/cli/shared.go:339-364`):

| Variable                     | Effect                                                                                                                                                                                          |
| ---------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `RTUNK_LIVE_HEIGHT`          | Maximum height of the live view, in lines. Same field as `--live-height`; see [commands.md](./commands.md#rtunk-check).                                                                         |
| `NO_COLOR`                   | Any value disables ANSI color in output, when stdout isn't otherwise forcing color. See `--ascii`/`--no-progress` in [commands.md](./commands.md#rtunk-check) for related output-shaping flags. |
| `TERM`                       | `TERM=dumb` disables the live terminal view; output falls back to plain progress lines.                                                                                                         |
| `LC_ALL`, `LC_CTYPE`, `LANG` | Checked in that order; the first non-empty one that doesn't contain `utf-8`/`utf8` triggers the ASCII glyph fallback in the live view — the same effect as passing `--ascii`.                   |

None of these have a config-file equivalent — they are read directly from the process environment,
not from `.rtunk/rtunk.yaml`/`.trunk/trunk.yaml`.

## Ignore-comment syntax

Inline directives suppress specific findings in the file they appear in. `pkg/ignore` is the sole
consumer: the execution engine calls `Filter` once per job's findings
(`pkg/ignore/docs.go:1-5`, `pkg/ignore/ignore.go:220-246`).

### Forms

| Form                                                                        | Scope                                                                                                                 |
| --------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------- |
| `rtunk-ignore(<targets>): <reason>`                                         | The line the directive is on, or — if the directive is the only non-whitespace content on its line — the _next_ line. |
| `rtunk-ignore-all(<targets>): <reason>`                                     | The whole file, including findings with no line number (e.g. pass/fail-style linters).                                |
| `rtunk-ignore-begin(<targets>): <reason>` ... `rtunk-ignore-end(<targets>)` | Every line from `-begin` through `-end`, inclusive.                                                                   |

`trunk-ignore(...)` and all three variants (`-all`/`-begin`/`-end`) are accepted as permanent,
fully equivalent aliases of their `rtunk-ignore` counterparts — the same regex matches either
prefix, so migrating from trunk never requires rewriting existing ignore comments
(`AGENTS.md:84-87`, `pkg/ignore/ignore.go:13-19`, `directiveRE`).

`<targets>` is a comma-separated list. Each entry is either a bare linter id (suppresses every
rule of that linter) or `linter/rule` (suppresses one rule). A bare entry immediately following a
`linter/rule` entry attaches to _that_ entry's linter — `eslint/no-console,no-unused-vars`
suppresses two rules of `eslint`; without a preceding slash, a bare entry is its own whole-linter
target — `eslint,prettier` suppresses both linters entirely (`pkg/ignore/ignore.go:61-90`,
`parseTargets`). A rule id may itself contain `/` (e.g. `@typescript-eslint/no-unused-vars`,
markdownlint's `MD013/line-length`) — only the _first_ `/` in an entry splits linter from rule.

### Comment-leader gating

A directive only counts if the text on its line _before_ the match contains one of the comment-
opening delimiters the enabled linters' plugin definitions declare (`comment_formats:`, each
entry's `LeadingDelimiter` — e.g. `#`, `//`, `<!--`). This is what stops a directive-shaped string
literal or documentation example from being treated as a real suppression. The check is global
across every enabled linter's comment formats, not resolved per file type
(`pkg/ignore/ignore.go:21-42`, `hasCommentLeader`). `comment_formats:` itself is contributed by
plugin definitions, not something a config file sets directly — see
[architecture/plugin-model.md](./architecture/plugin-model.md).

Whether a same-line/next-line directive targets its own line or the next one is decided by the
same prefix text: if everything before the match is whitespace and/or the comment opener (no
letter or digit), the directive is standalone and applies to the _next_ line; otherwise it's a
trailing comment on code and applies to its _own_ line (`pkg/ignore/ignore.go:44-56,197-201`,
`isCommentLeaderOnly`).

### `-begin`/`-end` block ranges

Each target's `-begin` pushes the current line onto a per-`(linter, rule)` stack; the next matching
`-end` for that same target pops it and records an inclusive `[start, end]` range. An unmatched
`-begin` (no `-end` before end of file) suppresses nothing — the findings it would have hidden stay
visible rather than silently disappearing past a typo'd or forgotten `-end`. A stray `-end` with no
open `-begin` is ignored (`pkg/ignore/ignore.go:148-153,181-196`, `buildIndex`).

### Examples

```text
# rtunk-ignore(gofmt): generated file, formatting intentionally left as-is
badly formatted line here

x = 1  # trunk-ignore(eslint/no-console,no-unused-vars): debug scaffolding
```

```html
<!-- rtunk-ignore-all(markdownlint/MD013): long reference table, wrapping hurts readability -->
```

```python
# rtunk-ignore-begin(checkov/CKV_AWS_1): legacy bucket, ticket JIRA-1234 tracks remediation
resource_with_finding_a()
resource_with_finding_b()
# rtunk-ignore-end(checkov/CKV_AWS_1)
```

## Inspecting the resolved configuration

`rtunk config print` prints the fully resolved, enabled configuration; `rtunk plugins print`
prints every definition available across all configured plugin sources, enabled or not. Neither
command runs the `check`/`fmt` deprecation validation, so a broken configuration can still be
inspected in order to fix it. See [commands.md](./commands.md) for their full flag reference.
