# Run Logs: Design

**Status:** scope agreed item-by-item with the user in conversation (brainstorming skill,
2026-09-26) before this spec was written. This is spec A of two; spec B (controlled tool
environment) is deliberately separate, see Non-goals.

## Goal

Every rtunk run that executes tools (`check`, `fmt`, `actions run`) persists a structured log
detailed enough to (1) reproduce by hand exactly what happened and (2) understand how each raw
tool output became the reported findings. The terminal output is unchanged; the log is a tee.

## Current state (found while investigating)

- `pkg/trunk/engine/engine.go` streams `Event` values (Running / Done / Skipped / Failed) per
  linter. Raw stdout/stderr of each command is captured in `runOneInvocation` only to be parsed,
  then discarded; the parser stage (`runParser`) likewise.
- Both `runOneInvocation` and `runParser` run `sh -c <substituted run>` with
  `append(os.Environ(), "PATH="+shimDirs+":"+PATH)`, i.e. the full inherited environment.
- `pkg/trunk/actions/run.go` (~L269-280) executes actions with its own, separate `exec` code and
  the same inherited-env pattern; it does not go through the engine.
- Precedent for per-repo persisted state: `pkg/trunk/actions/history.go` writes
  `<cache root>/actions-history/<sha256(repoRoot)>.jsonl`, capped at 200 entries.
- `rtunk cache clean` (`internal/cli/cache.go`) does `os.RemoveAll(download.Root)`, i.e. the whole
  `<cache>/downloads` tree, `actions-history` included; `cache prune` only sweeps installs/shims.
  Run logs therefore live beside `downloads/`, not inside it.

## Design

### 1. New package `pkg/trunk/runlog`

Owns everything log-related; no dependency on the engine (the engine depends on it).

- `Writer`: one open run file. `Emit(event)` JSON-encodes one line under a mutex (engine workers
  write concurrently). Events carry an `id` (invocation number) so interleaved lines stay
  attributable. The file is written incrementally, never buffered to the end, so a crash leaves
  a valid prefix.
- `Start(StartOpts) *Writer`: creates
  `<cache>/logs/<sha256(repoRoot)>/<UTC yyyymmddThhmmss.nnnnnnnnnZ>-<cmd>.jsonl` and writes
  `run_start`, then prunes that repo's directory to the 50 newest runs. `<cache>` is the directory
  `download.Root(cacheDir)` sits in (`--cache-dir` / `RTUNK_CACHE_DIR`, else the OS user cache dir's
  `rtunk/`), so `logs/` is a sibling of `downloads/`. It never fails the run: on error it prints one
  warning and returns nil, a valid no-op writer. If the package-reorg spec moves the cache root,
  `logsRoot` is the single function to update. Files are `0600`, directories `0750`.
- Per-stream cap: 1 MiB for each `output` event, and cumulatively per stream for a `Tee`d live
  stream; beyond it the data is cut and `"truncated": true` is set.
- `RedactEnv(environ []string) map[string]string`: keeps every variable, replaces the value with
  `"<redacted>"` when the name matches `(?i)TOKEN|SECRET|PASSWORD|PASSWD|KEY|CREDENTIAL|AUTH`.
  Heuristic by decision: a secret held in an innocuously named variable is not caught.
- A nil `*Writer` is valid and every method on it is a no-op, so call sites need no `if`.
- Read side: `List(cacheDir, repoRoot)` (run summaries) and `Load(path)` (decoded events), used by
  the `logs` command.

### 2. Event schema (JSONL, one event per line)

```jsonl
{"t":"run_start","ts":"...","rtunk":"0.4.0","argv":["rtunk","check","--fix"],"cwd":"/repo","repo_root":"/repo","config":".trunk/trunk.yaml","concurrency":4,"dry_run":false,"env":{"HOME":"/Users/x","GITHUB_TOKEN":"<redacted>","PATH":"..."}}
{"t":"invocation","id":7,"linter":"shellcheck","tool_versions":{"shellcheck":"0.10.0"},"template":"shellcheck -f json ${target}","files":["a.sh","b.sh"],"sandbox":null,"cwd":"/repo","path_prefix":"/cache/shims/shellcheck-0.10.0","argv":["sh","-c","shellcheck -f json 'a.sh' 'b.sh'"]}
{"t":"output","id":7,"stream":"stdout","data":"...","truncated":false}
{"t":"output","id":7,"stream":"stderr","data":"...","truncated":false}
{"t":"exit","id":7,"code":1,"ms":142,"parsed_from":"stdout"}
{"t":"parser","id":7,"template":"...","argv":["sh","-c","..."],"stdin_from":"stdout","stdout":"...","exit":0}
{"t":"findings","id":7,"linter":"shellcheck","findings":[{"...":"..."}]}
{"t":"linter_end","linter":"shellcheck","phase":"Done","note":"","err":""}
{"t":"run_end","ts":"...","ms":3120,"status":"ok"}
```

Reproducibility decisions:

- **`argv` (substituted) plus `template` (original)**, with `cwd`, `files` and `sandbox`, so a
  command can be replayed by hand and its `${target}`/`${plugin}`/`${cwd}` origins are visible.
- **The environment is written once**, in `run_start.env` (identical for every job). Each
  `invocation` only carries `path_prefix`, the one thing that varies per job.
- **`parsed_from`** records which stream fed the parser (`read_output_from` may be stdout, stderr
  or tmp_file), and the **parser is its own step** (stdin source, stdout, exit code): that is where
  raw tool output becomes findings.
- **Only the main config file is recorded** (`config`); plugin sources are pinned inside it, which
  is what makes them reproducible.
- `status` in `run_end` is `ok` when every linter ended Done/Skipped, `failed` otherwise. A
  `--verify-stable` "did not converge" verdict is not a run failure and is logged as `ok`
  (`runFailedBy`, `internal/cli/fmt_stability.go`). A file with no `run_end` is an interrupted run.

### 3. Engine and command integration

- `engine.Env` gains `Log *runlog.Writer` (nil = off). `runOneInvocation`, `runParser`, and the
  place `Event`s are emitted call the writer where the data already exists. `runOneInvocation`
  and `runParser` must expose what they currently discard (argv, raw stdout/stderr, timing),
  either through extra return values or by taking the writer and emitting themselves; the engine
  passes each of them a pre-filled `runlog.Event` and a `*Writer`, which keeps their return values
  intact.
- `check` and `fmt` (`internal/cli`) open the writer at start, emit `run_start`/`run_end`, and
  pass it via `Env.Log`.
- `actions run` is a first-class consumer: it opens a writer the same way (`cmd` = `actions-run`,
  `run_start.argv` includes the action id) and emits `invocation` (`linter` = the action id; `env`
  = only the variables its own `environment:` adds, masked), `output`, `exit` and `run_end` from
  `actions/run.go`'s own exec site, with the same schema, caps and retention. It has no parser step
  and no findings. An action that fails before exec, or is skipped as non-interactive, emits
  `linter_end` (`Failed` with the error / `Skipped`), so the log explains why nothing ran. It does
  not share the engine's code path (deduplicating the two exec sites is out of scope); both call
  `runlog` directly. Its output is a live stream, so `Tee` copies it into the log, which makes the
  child see pipes rather than the terminal; therefore `actions run` attaches no log at all when
  stdin is a terminal (`actions.IsInteractive`), so a person running an action by hand keeps colors
  and TTY prompts. A hook run by git (stdin not a terminal) is logged.
- The log is keyed like check/fmt (the config file's grandparent directory), including for
  `actions run`, whose action history is keyed by git root, so `rtunk logs` sees all three.
- Always on, no flag. The log is written in addition to, never instead of, the existing terminal
  output.

### 4. `rtunk logs`

- `rtunk logs list`: recent runs of the current repo (date, command, status, duration; an
  unterminated file shows `interrupted`).
- `rtunk logs show [<run>|latest] [--json]`: `--json` prints the file as-is; the default is a text
  rendering (command line, cwd, exit code, stdout/stderr, parser step, findings) generated from
  the JSONL. There is no second on-disk format.
- `rtunk logs clean [--all]`: deletes the current repo's logs, or every repo's with `--all`
  (`runlog.Clean(cacheDir, repoRoot|"")`). Mirrors `cache clean`'s "remove everything" meaning.
  The automatic 50-run retention still applies between manual cleans.
- `rtunk cache clean` does not delete logs (consistent with `actions-history`); logs have their
  own `logs clean`.

### 5. Error handling

Logging never fails a run. On the first write error the writer prints one warning on stderr and
disables itself for the rest of the run. `Start` failing (unwritable cache) degrades to a nil
writer with the same single warning.

### 6. Testing

- `runlog`: redaction (matching and non-matching names), per-stream truncation flag, the `Tee`
  cumulative cap (`TestTeeForwardsEverythingButLogsOnlyUpToTheCap`), prune keeps the 50 newest, concurrent `Emit` from many goroutines yields only valid JSON lines, nil writer
  is a no-op.
- `engine`: a fake linter run produces the expected event sequence, including the parser step and
  a `Failed` linter.
- `internal/cli`: `check` creates a log file that `logs show` renders; `logs list` reports a
  truncated file as `interrupted`.
- `actions run`: emits `run_start`, `invocation`/`output`/`exit`, `run_end`, and the file shows up
  in `logs list` / renders in `logs show` alongside `check` runs.
- `logs clean`: removes the current repo's logs only; `--all` removes every repo's.
- `runlog.Clean` unit test: leaves other repos' directories untouched without `--all`.
- `check --fix` logs both engine passes into one file with distinct invocation ids; `fmt --check`
  records `dry_run: true`; `rtunk cache clean` leaves logs in place.

## Non-goals

- **Controlled tool environment (spec B).** The stated intent is that tools eventually run with a
  controlled environment instead of inheriting the shell's. That changes `runOneInvocation`,
  `runParser`, `actions/run.go`, `exec`, shim env, and the meaning of `${env.NAME}`
  (`download.BuildEnv`), and needs its own decisions (baseline variables, base `PATH`,
  passthrough for proxy/cert/`GIT_*` variables). It is a separate spec/plan. This spec only logs
  the environment actually used, so it stays correct once spec B lands.
- A `--no-log` flag, log export or upload (zero-telemetry rule: nothing leaves the machine).
- Deduplicating the engine's and actions' exec code.
