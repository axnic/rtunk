# Terminal UX design

This document specifies the terminal UX of `check` and `fmt`. Command semantics live in
[cli.md](./cli.md); staging lives in [ROADMAP.md](../ROADMAP.md).

**Implementation status.** `v0.9` item 1 (event stream + plain renderer) is implemented, in
`internal/cli/render`: `check`, `fmt` and `check --format-before-check`'s formatter pass render through the plain
renderer (see "Non-TTY fallback" and "Issues output"). Engine terminal events carry the matched
`Files`, and `runlog.Writer.Name()` exposes the run uid used in the failures section. Item 2
(`--format human|sarif|json` and color) is implemented: color applies to `human` only, when stdout
is a terminal and `NO_COLOR` is empty (see "Issues output"). Item 4 (filtered `linters list` /
`actions list`) is implemented, in internal/cli/list.go, with per-linter file counts from
`engine.Matches` (pkg/trunk/engine/match.go). Item 3 (TTY live view) is implemented, in
`internal/cli/render/live.go`, `screen.go` and `detect.go`: with it all four `v0.9` items are done.
UI carries no compatibility constraint with trunk: the former `file:line severity [rule] message` lines and the `N issue(s) in M file(s)` summary are
gone; stable machine output is `--format json|sarif`.

## Architecture

`check` and `fmt` emit an event stream (linter started, files in progress, finished, finding,
failure). The `human`, `sarif` and `json` renderers consume it; the TTY live view is one consumer
among others and holds no business logic: it is a decorator over the chosen renderer (`render.NewLive`),
not a fourth format. Besides `Running` and the terminal events, the engine emits additive
`Planned`, `JobDone` and `Install*` events that the live view needs (job totals, per-job completion,
downloads); the plain renderers ignore them.

## Live view (TTY)

Implemented. The live area is drawn on stderr when stderr is a terminal, `--no-progress` is not set
and `TERM` is not `dumb`; otherwise the plain progress lines of "Non-TTY fallback" are emitted
unchanged. It is erased before the stdout report is written, so the report is byte-identical to the
non-live one. Final mockup:

```text
Checking  39% ⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣷⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀ 12/31 · 3.2s
  markdownlint   ⣿⣿⣷⠀⠀ 7/47
    ⠋ docs/superpowers/plans/2026-09-26-run-logs.md
    ⠙ pkg/trunk/config/ARCHITECTURE.md
  golangci-lint  ⠹
    pkg/trunk/actions/run.go, pkg/trunk/actions/run_log.go (+151)
↓ shfmt          ⠛ 4.1/12 MB
```

(`⠛` stands for whichever Sand frame is current. The header verb is padded to 10 columns,
`Checking` or `Formatting`, `Installing` while planned installs are unfinished.)

Rules:

- **Header.** Verb `Checking` or `Formatting` depending on the command (`Installing` while tool downloads are pending), percentage, braille bar
  spanning the full terminal width minus fixed prefix/suffix, `done/total`, elapsed time. No worker
  count. The bar is recomputed every frame, and the terminal size is read every 100 ms tick, so a
  resize is picked up without a SIGWINCH handler; below 40 columns the bar disappears and the
  counter stays. Elapsed is `%.1fs` under a minute, `1m04s` after.
- **Total.** Units of work known once a linter is planned: one per file for per-file linters, one
  per batch invocation, plus the installs (so the bar does not sit at 0 during downloads). The
  total is fixed up front (the planned installs are announced before the first
  starts); a failed linter's unfinished jobs are credited so the bar still reaches 100%.
- **Braille.** 8 glyph states per cell (the blank braille `U+2800`, then `⣀ ⣄ ⣤ ⣦ ⣶ ⣷ ⣿`; an empty cell is
  truly empty) = 7 sub-steps per character (100 columns = 700 steps; the 5-character mini-bar = 35
  steps). ASCII
  fallback (`[=== ]`, `|/-\`, `v` for the install glyph, `...` for `…`, `-` for `·`, one step per
  cell) with `--ascii`, and automatically when the locale is not UTF-8: the first non-empty of
  `LC_ALL`, `LC_CTYPE`, `LANG` must contain `utf-8` or `utf8` (case-insensitive), no locale at all
  means ASCII. `TERM=dumb` does not draw the live view at all (plain lines instead).
- **Per-worker tree.** A linter processing file by file shows a 5-character braille mini-bar plus
  `done/total`, then one line per in-flight file with a spinner. A batch linter (a single
  invocation over many files) shows ONLY its spinner on the linter line (no "batch" label); the
  files are listed on a single line `a, b (+N)`. Long paths are shortened in the middle.
- **Install rows.** The `↓` glyph occupies the indentation slot (no padding before it) so linter
  names stay aligned with those of running linters; dedicated Sand spinner; byte progress
  `4.1/12 MB` when Content-Length is known, spinner alone otherwise. The row is removed when the
  install ends (the linter row appears with its first running file). No color in the live area
  yet: the glyph carries the information. Known gaps: no "waiting for runtime node" state (the
  download layer does not expose its runtime-before-tool ordering), and the different color for
  install rows is deferred.
- **Height.** `--live-height <n>` and env var `RTUNK_LIVE_HEIGHT`; default is half the terminal
  height, minimum 3 lines (bar + 2); overflow is folded into a last `+N workers` line (hidden linter
  blocks and install rows). Both flags exist on `check` and `fmt`. Deliberately absent
  from the config file in v1 (avoid mixing rtunk keys with trunk-compatible config).

## Spinners

| Set           | Usage                                                | Frames |
| ------------- | ---------------------------------------------------- | ------ |
| `Dots`        | linters                                              | 10     |
| `DotsVariant` | reserved for a possible third state (e.g. "waiting") | 8      |
| `Sand`        | installs                                             | 35     |

`Dots`:

```text
⠋ ⠙ ⠹ ⠸ ⠼ ⠴ ⠦ ⠧ ⠇ ⠏
```

`DotsVariant`:

```text
⣼ ⣹ ⢻ ⠿ ⡟ ⣏ ⣧ ⣶
```

`Sand`:

```text
⠁ ⠂ ⠄ ⡀ ⡈ ⡐ ⡠ ⣀ ⣁ ⣂ ⣄ ⣌ ⣔ ⣤ ⣥ ⣦ ⣮ ⣶ ⣷ ⣿ ⡿ ⠿ ⢟ ⠟ ⡛ ⠛ ⠫ ⢋ ⠋ ⠍ ⡉ ⠉ ⠑ ⠡ ⢁
```

The frame index is computed from the clock (`frames[t/interval % len(frames)]`) so all rows of the
same kind stay in sync; 80 ms per frame for `Dots`, 70 ms for `Sand`. `DotsVariant` is unused.

**License note.** These frame sets come from the author's reference (spinner variants from Zed).
Licensing for rtunk is to be confirmed (Zed is GPL, but a list of braille characters is trivial).

## Non-TTY fallback

No redraw, no spinner: one stderr line per finished linter, then the report on stdout. Honors
`NO_COLOR` and `--no-progress` (a flag trunk also has; on `check` and `fmt`, it suppresses only the
per-linter progress lines, not warnings or errors). Implemented.

Progress line format: `✔|▲|-|✖ <linter>  done|skipped|failed  <detail>`.

## Issues output

Every issue line is printed: NO folding of repeated issues, NO per-rule summary, NO leading total (the totals are in the closing verdict). Within a file the location, severity and message columns are aligned.

```text
.agents/skills/git-commit/SKILL.md                                 (9)
  22:0   low   Fenced code blocks should have a language specified   markdownlint/MD040
  32:0   low   Fenced code blocks should have a language specified   markdownlint/MD040
  ...

pkg/trunk/download/shim.go                                         (2)
  57:3   high  QF1012: Use fmt.Fprintf(...) instead of WriteString(fmt.Sprintf(...))   golangci-lint2/staticcheck
  59:2   high  QF1012: Use fmt.Fprintf(...) instead of WriteString(fmt.Sprintf(...))   golangci-lint2/staticcheck

FAILURES
  ✖ gitleaks   failed to run   rtunk logs show 3f9a2c
    download: https://example.com/gitleaks_darwin_arm64: unrecognized archive format

Checked 232 files with 6 linters in 14.2s
✖ 27 issues (5 high · 4 medium · 18 low) · 1 failure
```

Rules:

- File header = bold path plus issue count, no position.
- `linter/rule` in dim at the end of the line.
- Severity colored with a glyph (`✖` high red, `▲` medium yellow, `·` low dim) while keeping the
  words high/medium/low (trunk compatibility and no-color mode).
- Failures in their own section, with the log uid (link to `rtunk logs show`).
- Summary at the bottom.
- Files in alphabetical order, issues sorted by line.
- Deferred: clickable OSC 8 links, sort by severity.

Implemented by the plain renderer: severity maps `error` to `high`, `warning` to
`medium`, anything else to `low`; issue lines are `line:col  high|medium|low  message  linter/rule`.
Skipped linters get one `Skipped  N linters: ...` line before the footer. The footer is
`Checked N files with M linters in Ts`, followed by a verdict: `✔ no issues`, or `✖ N issues (a high
· b medium · c low) · F failures`. `fmt` reports `REFORMATTED   N files` (`WOULD REFORMAT` with
`--check`) plus a verdict.

Color (implemented, `human` only) is emitted only when stdout is a terminal (`ModeCharDevice`) and
`NO_COLOR` is empty; the severity glyphs are added only then. Output outside a terminal is
byte-identical to the uncolored report, and stderr progress lines are never colored.

| Element       | Style                             |
| ------------- | --------------------------------- |
| File header   | bold                              |
| `linter/rule` | dim                               |
| `high`        | red, glyph `✖`                    |
| `medium`      | yellow, glyph `▲`                 |
| `low`         | dim, glyph `·`                    |
| Verdict       | green when passing, red otherwise |

## Implementation (lazy)

Neither bubbletea nor a progress-bar library. A hand-written renderer: a single goroutine redrawing
the live area at about 10 Hz with ANSI sequences (cursor up, erase line), one `Write` per redraw. The
frame is a pure function of state, size, clock and glyph set (`live.go`); `screen.go` only moves the
cursor. The terminal size comes from a stdlib `syscall` ioctl on stderr (`termsize_unix.go`; 80x24
elsewhere) instead of `golang.org/x/term`, which is not a dependency. Lines are truncated to the
width minus one column (no auto-wrap). The cursor is never hidden, so Ctrl+C only erases the area
and re-raises SIGINT for the default termination. The current go.mod dependencies are only kong,
yaml.v3 and testify.

## `rtunk linters list` / `rtunk actions list`

Implemented (internal/cli/list.go). Replaces trunk's flat 130-line listing (most of it empty) and
the former `* name  description` format (gone, breaking) with a grouped output:

```text
Enabled
  ✔ gofmt@1.25.0            153 go files
  ✔ markdownlint@0.44.0      47 markdown files
Available for this repo (not enabled)
  ◯ golangci-lint           153 go files
  ◯ gitleaks                230 files
(97 other linters don't match any file here — rtunk linters list --all)

Enable one with: rtunk linters enable <id>
```

- **Groups.** `Enabled` (`✔`, `id@version` with the pinned version, shown even with 0 matching
  files), then `Available for this repo (not enabled)` (`◯`, linters matching at least one
  repository file). With `--all`, a third group `Other (no matching file)` replaces the
  parenthetical, which otherwise reads `(N other linters don't match any file here — rtunk
linters list --all)`.
- **Counts.** From every file of the repository (`git ls-files -co --exclude-standard` in git, a
  walk skipping `.git` otherwise), matched with the linter's `files:` criteria. The label is
  `N <type> files` for a linter with a single non-`ALL` file type (`2 go files`, `1 markdown
file`), else `N files`. Names are padded to align.
- **`actions list`** has two groups only, `Enabled` and `Available (not enabled)` (actions have no
  matching files and no `--all`), and the footer `Enable one with: rtunk actions enable <id>`.
- **`--format json`** prints `{"enabled": [...], "available": [...], "other": [...]}`, entries being
  `{id, version, files, description}`; `other` is only filled with `--all`, arrays are never
  `null`, and `files` is omitted for actions.
- No color and no ASCII fallback.

## Implementation order

1. Event stream + plain renderer (needed for CI and tests). Implemented.
2. `--format human|sarif|json` and color. Implemented.
3. TTY live view. Implemented.
4. Filtered `linters list` / `actions list`. Implemented.

All four items are implemented. Later: color themes (including a distinct install-row color),
detailed byte-progress style, a "waiting for runtime" install state.
