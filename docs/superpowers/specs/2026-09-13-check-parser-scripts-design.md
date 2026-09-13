# `Command.Parser` Support — Design Spec

> **Autonomous session note:** written and approved by the operator before going to bed
> ("implémente ça ce soir... ne fais rien de bloquant"), then designed, planned, and implemented
> without further live confirmation. Every judgment call below is marked **Ruling:** with its
> reasoning and cost-if-wrong, per this project's standing "rulings, not stalls" practice for
> unattended work. Nothing here is provisional pending morning approval — it is what shipped,
> recorded so the reasoning is auditable.

## Goal

`check`'s engine has always treated any command with `Command.Parser != nil` as unsupported
(`Skipped`, "native output requires a converter script") -- v0.3's original scope decision, never
revisited. This plan implements it: a `Parser` is a converter script (Python or Node) that trunk's
own real plugin catalog ships alongside `plugin.yaml`, reading the linter's raw native output on
stdin and writing a normalized format (almost always SARIF) on stdout. Real trigger: the operator
noticed `trufflehog`'s real `plugin.yaml` uses exactly this mechanism and asked for it to be
implemented tonight, alongside two implementation-approach options -- copy only the referenced
script files into the persistent cache, or keep the whole checkout. The research below settled on
the latter, with reasoning.

## Real catalog research

Surveyed every command with a `parser:` field across the real trunk-io plugin catalog
(`~/.cache/trunk/plugins/https---github-com-trunk-io-plugins/v1.11.0-.../linters/*/plugin.yaml`):
**27 commands across 19 linters** (`codespell`, `graphql-schema-linter`, `ls-lint`,
`markdown-link-check`, `nancy`, `osv-scanner`, `phpstan`, `prettier`, `pyright`, `remark-lint`,
`renovate`, `rubocop`, `ruff`, `sqlfluff`, `terrascan`, `tfsec`, `trivy`, `trufflehog`,
`trufflehog-git`), all `runtime: python` except `ls-lint` (`runtime: node`).

### The stdin/stdout contract

Read `trufflehog_to_sarif.py`, `tfsec/parse.py`, and `ruff_to_sarif.py` in full (not just their
signatures): all three read the raw tool output from **stdin** (`sys.stdin.readlines()` /
`sys.stdin.read()` / `json.load(sys.stdin)`) and print the converted result to **stdout**. This is
the universal contract, not linter-specific: run the real command first, capture its raw output,
pipe that into the parser script's stdin, capture the script's stdout, and parse _that_ per
`cmd.Output` (almost always `sarif` -- confirmed by reading every one of the 27 commands' `output:`
field). `ruff_to_sarif.py` additionally takes a literal positional arg (`0` or `1`, a column-index
offset baked directly into `parser.run` per command variant) -- an ordinary extra argument, not a
second template var, substituted the same way `${target}`/`${tmpfile}` already are today.

### `${plugin}` and `${cwd}`: two forms of the same idea

Every `parser.run` references its script via one of two forms:

- `${plugin}/linters/<name>/<script>` -- the plugin source's own root, plus the path down to this
  linter's own subdirectory (12 linters: `phpstan`, `osv-scanner`, `sqlfluff`, `prettier`,
  `terrascan`, `nancy`, `codespell`, `trufflehog`/`trufflehog-git`, `rubocop`, `pyright`,
  `markdown-link-check`, `trivy`).
- `${cwd}/<script>` -- 7 linters (`tfsec`, `ruff`, `graphql-schema-linter`, `ls-lint`, `renovate`,
  `remark-lint`). Verified directly (not assumed) by listing each of these linters' real
  directories: `tfsec/parse.py`, `ruff/ruff_to_sarif.py`, and `graphql-schema-linter/parse.py` all
  sit in the exact same directory as their own `plugin.yaml`. `${cwd}` is `${plugin}` plus this
  linter's own subdirectory, pre-joined -- a shorthand, not a different concept. Both resolve
  relative to the _plugin source_, never to `repoRoot` or a sandbox -- confirmed there is no case
  where `${cwd}` could plausibly mean the command's own working directory, since every real use is
  immediately followed by a script filename, never a data file.

**`nancy`'s two scripts are not a "multi-hop" case.** The operator specifically asked whether any
tool chains an entrypoint script into a second script. `nancy`'s own `commands[].run` is itself
`sh ${plugin}/linters/nancy/run.sh` (a wrapper shell script, not the `nancy` binary directly), and
its `parser.run` is a _separate_ `python3 ${plugin}/linters/nancy/parse.py` for output conversion.
Read `run.sh` in full: it calls `go list -json -deps ./... | nancy sleuth "$@"` directly -- no
further script indirection. So `nancy` is two _independent_ script references (one for `Run`, one
for `Parser`), both resolved by the exact same `${plugin}` mechanism, not a chain requiring special
handling. This also means `${plugin}` must be supported in `Command.Run` itself, not only
`Command.Parser.Run` -- it already appears there in real data.

## Architecture

### Where the script files live: keep the whole checkout, don't cherry-pick

**Ruling:** persist a git plugin source's entire checkout (not just the specific files a
`${plugin}`/`${cwd}` reference resolves to), keyed the same way the existing JSON cache already is
(`sha256(uri+ref)`). **Why:** cherry-picking requires parsing every `Run`/`Parser.Run` string at
resolution time to extract which file(s) it references -- fragile (a new template-var shape, a
script that itself dynamically imports a sibling module, a future catalog change) for a real save
of, at most, a few tens of MB (trunk-io/plugins is almost entirely small YAML and script files, no
built artifacts) on a machine that already has `go`/`node`/downloaded tool binaries far larger than
that. The operator's own later observation -- "the repo already ends up cloned in the cache
anyway, leave it as is" -- lines up with this choice. **Cost if wrong:** a config change (a
smaller cherry-pick pass) confined entirely to `fetchGitSource`; no schema or caller changes needed
elsewhere, since `SourceRoot` is already an opaque directory to every consumer.

Mechanically: `fetchGitSource`'s throwaway `tmpDir` (currently `os.MkdirTemp("", ...)`, always
`os.RemoveAll`'d) is created _inside_ `cacheDir` instead (`os.MkdirTemp(cacheDir, "checkout-tmp-*")`
-- guarantees the later persist-rename is same-filesystem, no cross-device fallback needed), and
instead of being deleted after `parseSourceDir` succeeds, it's renamed into
`cacheDir/checkouts/<hash>/` (removing any stale prior checkout at that path first). A **local**
plugin source (`src.Local != ""`) needs none of this: it already lives at a real, permanent,
user-controlled path, so `SourceRoot` for it is just that path directly.

### `${plugin}`/`${cwd}` resolution: two `Linter` fields, only one of them cached

```go
// In config.Linter:
SourceDir  string // relative to the plugin source root, e.g. "linters/trufflehog" -- stable,
                   // safe to cache in sourceDefs' JSON.
SourceRoot string // absolute local directory ${plugin} resolves to for this linter's commands --
                   // a local source's own dir, or a git source's persisted checkout. Recomputed
                   // fresh on every resolution from the *current* cacheDir; never itself
                   // serialized to the cache (a cacheDir override must never leave a stale
                   // absolute path baked into cached JSON).
```

`SourceDir` is tagged in `parseSourceDir` (which already iterates each linter's own `plugin.yaml`
path via `filepath.Glob(filepath.Join(dir, category, "*", "plugin.yaml"))` -- `filepath.Rel(dir,
filepath.Dir(path))` is available for free at exactly the point each `pf.Lint.Definitions` batch is
read, before merging). `SourceRoot` is filled in by the two callers of `parseSourceDir`
(`mergePluginRepo` for local sources, `fetchGitSource` for git ones) _after_ it returns, since only
they know whether `dir` is a permanent local path or this call's persisted-checkout path -- a
simple "loop over the returned map, set the field, write back" (Go map values aren't addressable
in place).

`${cwd}` is never stored -- it's computed on demand as `filepath.Join(linter.SourceRoot,
linter.SourceDir)`.

### Reusing the existing cache-schema-version mechanism

**Ruling:** bump `cacheSchemaVersion` (added earlier tonight for the `Command.ParseRegex`
regression) again for this change. **Why:** a JSON cache written before `Linter.SourceDir` existed
would decode it as `""`, and -- separately -- even a cache written _by this exact feature_ has no
guarantee its paired `checkouts/<hash>/` directory still exists (a user could `rm -rf` it, or an
older `rtunk` could have written the JSON without ever creating one). `loadSourceCache`'s existing
version check already makes any such mismatch a clean "miss, regenerate" rather than a silent
under-population -- the exact class of bug the version field exists to prevent, now protecting a
second real feature instead of a hypothetical one. **Cost if wrong:** none observed -- this is the
same mechanism the previous fix already shipped and tested, applied a second time.

Additionally: `fetchGitSource`'s cache-hit path must independently verify
`cacheDir/checkouts/<hash>/` still exists on disk before trusting the JSON hit (a user could
delete just the checkout directory, leaving the JSON cache file behind) -- if missing, treat as a
miss and re-fetch, exactly like a decode failure.

### Runtime resolution: reuse the existing tool-shim mechanism, no new download-package code

**Ruling:** resolve a parser script's `runtime` (`python`/`node`) the same way `resolveShimDirs`
already resolves a linter's `Tools` -- look it up in `cfg.Runtimes.Definitions[id]`, resolve its
version via the existing `download.ResolveVersion`, download via the existing
`download.Download(cfg, cacheDir, download.Ref{Category: "runtimes", ID: id, Version: version})` if
its install dir is empty, and prefix `download.ShimPath(...)`'s directory onto `PATH` -- identical
shape to the tools case, verified by reading `fetchRuntimeRef` in `pkg/trunk/download/download.go`
in full: a runtime's `Shims []string` (from its own real catalog entry, e.g. `python`'s presumably
declaring `python3` among its shims) is what actually gets written under `ShimPath`, so `python3`
being directly invocable on `PATH` is a property of the catalog data, not something this project
needs to hardcode. **Why:** this is exactly the kind of thing the `download` package already
solves generically; no new capability needed, just a second caller. **Cost if wrong:** isolated to
one new small function in `engine`, easy to correct without touching `download` itself.

**Real, necessary side effect:** `config.filterEnabled` currently pulls a runtime into the
"enabled+used" set only via an enabled Tool's or Action's own `Runtime` field (`filter.go`, the
`runtimeIDs` loop) -- never via a `Command.Parser.Runtime`. Without a fix, `Resolve()`'s default
trim would silently drop the very runtime a parser script needs, for any linter whose Parser is its
_only_ reason to need that runtime. One more contribution to the same `runtimeIDs` map, in the same
loop shape as the existing Tool/Action contributions (iterate `keepLint`'s `Commands`, add
`cmd.Parser.Runtime` when `cmd.Parser != nil`).

### Engine changes

- `findUnsupportedVar`'s allowlist grows from `${target}`/`${tmpfile}` to also allow
  `${plugin}`/`${cwd}` -- both now substituted, both resolved from `job.linter.SourceRoot`/
  `SourceDir`, computed once per job (not per invocation) alongside the existing `pathEnv`.
- `buildJobs`'s blanket `if cmd.Parser != nil { Skip }` is replaced with: resolve the parser's
  runtime shim (skip with a clear note if the runtime isn't in `cfg.Runtimes.Definitions` at all --
  a real config gap, same treatment as an unresolvable tool); otherwise proceed.
- `runBatch` gains a second stage when `job.cmd.Parser != nil`: after the real command's invocation
  produces `out` (raw native output, exactly as today), substitute `${plugin}`/`${cwd}`/`${target}`
  into `cmd.Parser.Run`, execute it via the same `sh -c` mechanism as the main command (same
  `workDir`, same `ctx` for cancellation) but with `out` piped to **its stdin** and its **stdout**
  captured as the new `out` -- then proceed to the existing `cmd.Output`-driven parse dispatch
  exactly as if the real tool had produced that output directly (almost always `sarif`, so
  `output.ParseSARIF` unchanged). The parser script's own stderr is captured and folded into the
  error message the same way the main invocation's already is, on a non-zero exit.

## Testing approach

- `config`: `parseSourceDir` tags `SourceDir` correctly for a multi-linter fixture tree (unit
  test, real temp-dir fixture, exact relative-path assertions). `fetchGitSource`'s persisted
  checkout: a fixture git repo, fetched once, confirm `checkouts/<hash>/` exists and contains the
  fixture's real files after the call returns (not just that the JSON cache file exists) and that a
  second call (cache hit) does not re-clone (assert via a marker file's mtime, or a call counter on
  a stubbed clone path). Deleting the checkout but leaving the JSON cache: confirm the next
  `Resolve` call re-fetches rather than silently using a nonexistent `SourceRoot`.
- `filter.go`: an enabled linter whose only Command sets `Parser.Runtime` pulls that runtime into
  the filtered config even with no Tool/Action needing it.
- `engine`: extend the existing fake-tool-binary test harness with a "raw" case (emits a
  non-SARIF, tool-specific-shaped string) and a fake Python-standing-in shim that reads stdin,
  transforms it, writes SARIF to stdout -- an end-to-end job with `Command.Parser` set, asserting
  the final `Finding` matches what the fake parser script produced, not the raw tool output.
  Real-shape regression test: reproduce `trufflehog_to_sarif.py`'s actual NDJSON-in/SARIF-out
  contract with a literal Python fixture script (not a Go stand-in) if a Python interpreter is
  available in CI/dev; skip gracefully (not fail) if not, same pattern this project already uses
  for `runtime.GOOS == "windows"` skips.

## Non-goals

- No change to any of the 27 real commands' _other_ blockers -- several combine `Parser` with an
  unsupported `RunFrom`/`SandboxType`/template var already documented as out of scope elsewhere
  (e.g. `ruff`'s `${cachedir}`, `renovate.cmd`'s Windows-only variant); this plan only removes the
  `Parser != nil` blanket skip, it does not audit every combination for a second blocker.
- No garbage collection for `checkouts/<hash>/` beyond what the JSON cache already lacks (a pinned
  `ref` never changes content, so nothing there goes stale on its own) -- `rtunk cache clean`/
  `prune` do not touch the plugin-source cache today at all (a pre-existing, separate scope), so
  this doesn't newly regress anything they already handled.
- `ruff_to_sarif.py`'s literal `0`/`1` positional argument is passed through unchanged as ordinary
  `parser.run` text (already substitutable, no new template mechanism); this plan does not attempt
  to understand or validate what that argument means.
- **Known gap, found by the final whole-branch review, deliberately left unfixed:** `${target}` is
  substituted through `quoteAll` (single-quoted), matching this project's existing convention for
  `Command.Run`. Two of the 19 real linters this feature newly unblocks pre-quote it themselves in
  their real catalog `run:`/`parser.run:` text -- `markdown-link-check` (`"${target}"` in both) and
  `phpstan` (`"${target}"` in `run:`) -- which double-quotes under this convention and silently
  produces a non-existent path (no error, just a script that can't find its input). This is
  pre-existing quoting behavior, not something this feature's own code introduced; it was simply
  unreachable before, since both commands were blanket-skipped for having a `Parser` at all. A
  general fix (detecting and stripping an author's own enclosing quotes) is out of proportion to
  this feature's scope; documented here as a known limitation for these two specific linters rather
  than fixed.

## Global constraints (carried into every task)

- No new `go.mod` dependencies.
- Every test asserts exact values, never just "no error."
- `go build ./... && go vet ./... && go test ./... -count=1 -race` clean before every task is done.
- Commits signed (`-S`), commitlint conventions per `.agents/skills/git-commit/SKILL.md`.
- Do not touch `output`'s existing parsers or `engine/security` -- this plan's surface is
  `config` (schema + resolution + cache), `engine` (dispatch + new template vars), and the runtime
  side of `download`'s existing call sites (no new code inside `pkg/trunk/download` itself).
