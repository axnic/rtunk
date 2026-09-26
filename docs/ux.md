# Terminal UX design

This document specifies the terminal UX of `check` and `fmt`. Command semantics live in
[cli.md](./cli.md); staging lives in [ROADMAP.md](../ROADMAP.md) (`v0.9`).

**Implementation status.** `v0.9` item 1 (event stream + plain renderer) is implemented, in
`internal/cli/render`: `check`, `fmt` and `check --fix`'s formatter pass render through the plain
renderer (see "Non-TTY fallback" and "Issues output"). Engine terminal events carry the matched
`Files`, and `runlog.Writer.Name()` exposes the run uid used in the failures section. Still planned:
`--format json|sarif` (item 2), the TTY live view (item 3), the filtered `linters list` (item 4).
Color is not implemented (`NO_COLOR` is read for the later renderers). UI carries no compatibility
constraint with trunk: the former `file:line severity [rule] message` lines and the
`N issue(s) in M file(s)` summary are gone; stable machine output is planned as `--format json|sarif`.

## Architecture

`check` and `fmt` emit an event stream (linter started, files in progress, finished, finding,
failure). The `human`, `sarif` and `json` renderers consume it; the TTY live view is one consumer
among others and holds no business logic.

## Live view (TTY)

Final mockup:

```text
Checking  39% ⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣷⣀⣀⣀⣀⣀⣀⣀⣀⣀⣀⣀⣀⣀⣀ 12/31 · 3.2s
  markdownlint   ⣿⣿⣷⣀⣀ 7/47
    ⠋ docs/superpowers/plans/2026-09-26-run-logs.md
    ⠙ pkg/trunk/config/ARCHITECTURE.md
  golangci-lint  ⠹
    pkg/trunk/actions/run.go, pkg/trunk/actions/run_log.go (+151)
↓ shfmt          ⠛ 4.1/12 MB
↓ gitleaks       ⠛ waiting for runtime node
```

(`⠛` stands for whichever Sand frame is current.)

Rules:

- **Header.** Verb `Checking` or `Formatting` depending on the command, percentage, braille bar
  spanning the full terminal width minus fixed prefix/suffix, `done/total`, elapsed time. No worker
  count. The bar is recomputed every frame and on SIGWINCH; below about 40 columns the bar
  disappears and the counter stays.
- **Total.** Units of work known right after file selection: one per file for per-file linters,
  one per batch invocation, plus installs (so the bar does not sit at 0 during downloads).
- **Braille.** 8 dots per cell = 8 steps per character (100 columns = 800 steps; the 5-character
  mini-bar = 40 steps). ASCII fallback (`[=== ]`, `|/-\`) when the locale is not UTF-8,
  `TERM=dumb`, or `--ascii`.
- **Per-worker tree.** A linter processing file by file shows a 5-character braille mini-bar plus
  `done/total`, then one line per in-flight file with a spinner. A batch linter (a single
  invocation over many files) shows ONLY its spinner on the linter line (no "batch" label); the
  files are listed on a single line `a, b (+N)`. Long paths are shortened in the middle.
- **Install rows.** The `↓` glyph occupies the indentation slot (no padding before it) so linter
  names stay aligned with those of running linters; different color; dedicated Sand spinner; byte
  progress `4.1/12 MB` when Content-Length is known, spinner alone otherwise; states such as
  "waiting for runtime node". Glyph and label carry the information without color (`NO_COLOR`,
  CI). When the install finishes, the row switches to the normal style in place.
- **Height.** `--live-height <n>` and env var `RTUNK_LIVE_HEIGHT`; default is half the terminal
  height, minimum 3 lines (bar + 2); overflow is folded into `+N workers`. Deliberately absent
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
same kind stay in sync; about 70 ms per frame for Sand, to tune by eye.

**License note.** These frame sets come from the author's reference (spinner variants from Zed).
Licensing for rtunk is to be confirmed (Zed is GPL, but a list of braille characters is trivial).

## Non-TTY fallback

No redraw, no spinner: one stderr line per finished linter, then the report on stdout. Honors
`NO_COLOR` and `--no-progress` (a flag trunk also has; on `check` and `fmt`, it suppresses only the
per-linter progress lines, not warnings or errors). Implemented.

Progress line format: `✔|▲|-|✖ <linter>  done|skipped|failed  <detail>`.

## Issues output

Every issue line is printed: NO folding of repeated issues, NO per-rule summary.

```text
ISSUES   27 in 12 files

.agents/skills/git-commit/SKILL.md                                 (9)
  22:0   low   Fenced code blocks should have a language specified   markdownlint/MD040
  32:0   low   Fenced code blocks should have a language specified   markdownlint/MD040
  ...

pkg/trunk/download/shim.go                                         (2)
  57:3   high  QF1012: Use fmt.Fprintf(...) instead of WriteString(fmt.Sprintf(...))   golangci-lint2/staticcheck
  59:2   high  QF1012: Use fmt.Fprintf(...) instead of WriteString(fmt.Sprintf(...))   golangci-lint2/staticcheck

FAILURES
  ✖ gitleaks   failed to run   rtunk logs show 3f9a2c

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

Implemented by the plain renderer, without color: severity maps `error` to `high`, `warning` to
`medium`, anything else to `low`; issue lines are `line:col  high|medium|low  message  linter/rule`.
Skipped linters get one `Skipped  N linters: ...` line before the footer. The footer is
`Checked N files with M linters in Ts`, followed by a verdict: `✔ no issues`, or `✖ N issues (a high
· b medium · c low) · F failures`. `fmt` reports `REFORMATTED   N files` (`WOULD REFORMAT` with
`--check`) plus a verdict.

## Implementation (lazy)

Neither bubbletea nor a progress-bar library. A hand-written renderer (about 150 lines): a single
goroutine redrawing the live area at about 10 Hz with ANSI sequences (cursor up, erase),
`golang.org/x/term` for TTY detection and width. It must handle Ctrl+C (restore the cursor),
terminal resize, and truncation of lines to the terminal width. The current go.mod dependencies are
only kong, yaml.v3 and testify.

## `rtunk linters list` / `rtunk actions list`

Replaces trunk's flat 130-line listing (most of it empty) with a grouped output:

```text
Enabled
  ✔ gofmt            153 go files
  ✔ markdownlint      47 markdown files
Available for this repo (not enabled)
  ◯ golangci-lint    153 go files
  ◯ gitleaks         230 files
(97 other linters don't match any file here — rtunk linters list --all)
```

Enabled linters first (with pinned version), then non-enabled ones that match files in the repo,
the rest only with `--all`; footer with the hint `rtunk linters enable <id>`; `--format json`
supported; `actions list` follows the same layout.

## Implementation order

1. Event stream + plain renderer (needed for CI and tests). Implemented.
2. `--format json|sarif`.
3. TTY live view.
4. Filtered `linters list`.

Later: color themes, detailed byte-progress style.
