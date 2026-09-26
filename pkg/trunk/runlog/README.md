# pkg/trunk/runlog

Persists one JSONL log per rtunk run that executes tools (`check`, `fmt`, `actions run`), detailed
enough to replay by hand what happened and to see how raw tool output became findings. Design:
docs/superpowers/specs/2026-09-26-run-logs-design.md.

## Layout

Each run is one file, `<cache>/logs/<sha256(repoRoot)>/<UTC yyyymmddThhmmss.nnnnnnnnnZ>-<cmd>.jsonl`
(`logsRoot`, `Start`), where `<cache>` is the directory `download.Root` sits in
(`--cache-dir` / `RTUNK_CACHE_DIR`, else the OS user cache dir's `rtunk/`). `<cmd>` is `check`,
`fmt` or `actions-run`. `logs/` is a sibling of `downloads/`, so `rtunk cache destroy` (which removes
`downloads/`) never deletes logs; `rtunk logs clean [--all]` does (`Clean`). Files are `0600` and their
directories `0750`, because the first event records the environment. `Start` keeps the newest 50 runs per
repository and prunes the rest (`keepRuns`).

## Events

One JSON object per line, discriminated by `t` (`Event`, `Kind*`): `run_start` (rtunk version, argv,
cwd, config path, and the whole inherited environment with the value of any variable named like
`TOKEN|SECRET|PASSWORD|PASSWD|KEY|CREDENTIAL|AUTH` replaced by `<redacted>`, see `RedactEnv`),
then per command invocation `invocation` (substituted `argv`, the original `template`, `files`,
`cwd`, the `path_prefix` of tool shims put in front of `PATH`, tool versions, sandbox), `output`
(one per non-empty stream, `stdout`/`stderr`/`tmp_file`), `exit` (code, duration, which stream fed
the parser), `parser` (its own argv, stdout and exit) and `findings`; per linter `linter_end`
(`Done`/`Skipped`/`Failed`, changed files, error); finally `run_end` (`ok` or `failed`). All lines
of one invocation share its `id`, allocated by `Writer.NextID`, unique across the whole run. A file
with no `run_end` was interrupted; `Load` still reads its complete lines.

```jsonl
{"t":"invocation","id":1,"linter":"shellcheck","template":"shellcheck -f json ${target}","argv":["sh","-c","shellcheck -f json 'a.sh'"],"cwd":"/repo","path_prefix":"/cache/downloads/shims/tools/shellcheck/0.10.0","tool_versions":{"shellcheck":"0.10.0"},"files":["a.sh"]}
{"t":"exit","id":1,"code":1,"ms":142,"parsed_from":"stdout"}
```

## Behavior worth knowing

- A nil `*Writer` is valid; every method is a no-op, so callers never branch on it.
- Logging never fails a run: `Start` returns nil after one stderr warning if the file cannot be
  opened, and the first write error disables the writer the same way.
- Each `output` event, and each `Tee`d stream cumulatively, is cut at 1 MiB (`Cap`, `maxOutput`),
  flagged `truncated`.
- `Tee` (used by `actions run`, whose output is a live stream) makes the child's stdout/stderr
  pipes rather than the terminal, so `rtunk actions run` attaches no log when stdin is a terminal
  (`actions.IsInteractive`).
- The redaction is a name heuristic and covers variable names only: credentials embedded in the
  value of an innocuously named variable (a URL in `HTTP_PROXY` or `DATABASE_URL`, say) and anything
  a tool prints to stdout/stderr are logged verbatim. Treat a log file like the environment it
  came from.
