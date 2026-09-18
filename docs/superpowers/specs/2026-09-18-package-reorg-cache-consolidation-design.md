# Package Reorganization & Cache Consolidation: Design

**Status:** scope agreed item-by-item with the user in conversation (brainstorming skill,
2026-09-18) before this spec was written.

## Goal

Address structural debt found while doing a Go best-practices lint pass on rtunk: duplicated
cache-root resolution, duplicated action/engine helpers, an inconsistent trunk-vs-rtunk package
boundary, and two undocumented internal-only YAML shape types. This is a pure internal refactor —
no CLI-observable behavior change except where explicitly called out as a non-goal.

## Current state (found while investigating)

- `pkg/trunk/download/cache.go:Root()` resolves `os.UserCacheDir()/rtunk/downloads`;
  `pkg/trunk/config/git.go:fetchGitSource` independently resolves `os.UserCacheDir()/rtunk/plugins`
  — the same cache root, duplicated inline in two files, no shared function.
- `pkg/trunk/actions/run.go` reimplements `resolveRuntimeShimDir`, `quoteOne`/`quoteAll`, and its
  own variable-substitution pass (`substituteVars`/`findUnsupportedActionVar`) — all near-duplicates
  of `pkg/trunk/engine/engine.go`'s own `resolveRuntimeShimDir`, `quoteOne`/`quoteAll`,
  `findUnsupportedVar`.
- `pkg/trunk/engine`, `pkg/trunk/renovate`, `pkg/trunk/upgrade` don't represent trunk.yaml/
  plugin.yaml schema mechanics — they're rtunk's own execution/renovate-annotation/self-upgrade
  logic, independently designed (rtunk has no access to trunk's real internal engine to mirror).
  `pkg/trunk/config`, `download`, `output`, `actions`, `githooks` do faithfully implement
  trunk-schema-defined mechanics and stay under `pkg/trunk`.
- `trunkFile`/`pluginFile` (the raw YAML-decoded shapes for trunk.yaml/plugin.yaml, pre-merge) live
  unexported in `resolve.go`, with no home among the rest of the exported `config` shape types in
  `definitions.go`.
- `filterEnabled` (`pkg/trunk/config/filter.go:18-74`) computes a 4-stage transitive-closure filter
  with no comments marking which stage computes what.
- `rtunk cache clean`/`prune` (`internal/cli/cache.go`) only ever sweep `download.Root`'s
  installs/shims subtree — the plugin-source cache (checkouts + JSON) is never covered. Confirmed
  with the user: **not** extending coverage as part of this move — behavior stays exactly as today,
  only the code moves.

## Design

### 1. New `pkg/cache` package

Owns the on-disk cache: path layout, generic keyed JSON storage, clean/prune.

```go
// Root resolves the rtunk cache root: cacheDir if set, else os.UserCacheDir()/rtunk.
func Root(cacheDir string) (string, error)

// -- downloads subtree (moved verbatim from pkg/trunk/download/cache.go) --
func BlobPath(root, sum string) string
func InstallDir(root, category, id, version string) string
func InstallsBase(root, category, id string) string
func ShimPath(root, category, id, version, name string) string
func Platform() string

// -- plugins subtree (moved from pkg/trunk/config/git.go + config/cache.go, generalized) --
func PluginCheckoutDir(root, key string) string
func PluginCacheFile(root, key string) string

// LoadJSON/SaveJSON replace config/cache.go's sourceDefs-specific loadSourceCache/saveSourceCache
// with a generic, atomically-written (temp file + rename), schema-versioned envelope any caller
// can use -- keeps pkg/cache free of any pkg/trunk/config import (no cycle: config imports cache,
// never the reverse).
func LoadJSON[T any](path string, wantVersion int) (T, error)
func SaveJSON[T any](path string, version int, v T) error

// Clean/Prune: moved verbatim from internal/cli/cache.go (os.RemoveAll(root), and the existing
// installs/shims glob-and-keep sweep) -- same behavior, same scope (downloads subtree only).
func Clean(root string) error
func Prune(root string, keep map[string]bool) error
```

`pkg/trunk/download` keeps its domain logic (fetching blobs, extracting archives, runtime installs)
and calls into `pkg/cache` for path layout/storage instead of owning it. `pkg/trunk/config/git.go`
and `config/cache.go` do the same for the plugin-source cache, with `sourceDefs`/`cacheEnvelope`'s
JSON shape unchanged — just read/written via `cache.LoadJSON[sourceDefs]`/
`cache.SaveJSON[sourceDefs]` instead of the current hand-rolled pair. `internal/cli/cache.go`
imports `pkg/cache` instead of `pkg/trunk/download` for `Clean`/`Prune`/`InstallsBase`.

### 2. Package moves: engine, renovate, upgrade out of `pkg/trunk`

- `pkg/trunk/engine` → `pkg/engine` (`pkg/trunk/engine/security` → `pkg/engine/security`)
- `pkg/trunk/renovate` → `pkg/renovate`
- `pkg/trunk/upgrade` → `pkg/upgrade`

Every importer's path updates (`internal/cli/*`, `cmd/rtunk/main.go`, and cross-references between
these three and `pkg/trunk/config`/`actions`). Pure move: import paths and package clauses change,
no logic changes.

### 3. Shared actions/engine helpers

`resolveRuntimeShimDir`, `quoteOne`, `quoteAll`, and the variable-substitution core move to
`pkg/engine` (exported: `ResolveRuntimeShimDir`, `QuoteOne`, `QuoteAll`). `pkg/trunk/actions/run.go`
imports `pkg/engine` for these instead of keeping its own copies.

The two `Run` entry points stay separate: `engine.Run` matches files, groups them, and runs a
concurrent job queue across many linter commands, streaming `Event`s; `actions.Run` executes one
action and returns one `Result`. Only the genuinely-duplicated leaf helpers consolidate — actions
does not become "a job the engine runs," and `engine.Run`'s signature/event model doesn't change to
accommodate it.

Variable-substitution specifics: `engine.findUnsupportedVar`/`actions.findUnsupportedActionVar`
support different variable sets (engine: `${file}` etc.; actions: `${hook}`/`${cwd}`/`${plugin}`/
args). The shared piece is the substitution mechanism itself (look up `${name}` in a map, report
the first unknown one) parameterized by each caller's own variable table — not a single shared list
of variable names.

### 4. Export `trunkFile`/`pluginFile`

Move both struct definitions from `pkg/trunk/config/resolve.go` into `definitions.go` (alongside
the rest of the exported shape types), rename to `TrunkFile`/`PluginFile`. All internal references
(`resolve.go`, tests) update to the new names. This lets external consumers decode a raw
trunk.yaml/plugin.yaml into the same shape rtunk itself uses, before resolution.

### 5. `filterEnabled` comments

One comment per stage in `pkg/trunk/config/filter.go:18-74`, naming what each stage computes:
enabled lint/actions → tools+file-types they reference → runtimes referenced by those+linters+
actions → downloads referenced by runtimes+tools. No logic change.

## Testing strategy

- `pkg/cache`: unit tests for `Root`, the path-layout helpers (ported from
  `download/cache_test.go`), `LoadJSON`/`SaveJSON` (version-mismatch-is-a-miss behavior preserved,
  atomic-write-on-crash preserved), `Clean`/`Prune` (ported from the existing prune coverage in
  `internal/cli`).
- `pkg/trunk/download`, `pkg/trunk/config`: existing tests keep passing unchanged
  (behavior-preserving move) — this is the regression signal, not new test-writing.
- `pkg/engine`, `pkg/renovate`, `pkg/upgrade`: existing test suites move with their packages,
  unchanged.
- `pkg/trunk/actions`: existing `run_test.go` keeps passing; `ResolveRuntimeShimDir`/`QuoteOne`/
  `QuoteAll` gaining a second caller needs no new tests beyond what already covers each existing
  call site.
- Full-repo gate before this is done: `go build ./...`, `go vet ./...`, `go test ./...`,
  `golangci-lint run ./...` (against the repo's `.golangci.yml`), and
  `trunk check --filter=gofmt,golangci-lint2 --all`.

## Non-goals (ruled, disclosed)

- `rtunk cache clean`/`prune` coverage stays exactly as today (downloads subtree only) — not
  extended to sweep the plugin-source cache, per explicit user decision, even though `pkg/cache`
  now technically could.
- No change to `git` subprocess usage (`internal/cli`, `pkg/trunk/config/git.go`,
  `pkg/trunk/engine/match.go`'s `filterGitignored`) — evaluated switching to a Go git library
  (`go-git`), decided against it: no library replicates git's own `check-ignore` semantics exactly,
  and a silently-wrong gitignore filter is worse than the `git` binary dependency this CLI already
  assumes.
- No change to `actions.Run`'s or `engine.Run`'s own control flow/signature — only the leaf helpers
  they both duplicated consolidate; actions does not become "a job the engine runs."
