# Package Reorganization & Cache Consolidation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Consolidate rtunk's cache-root logic into one `pkg/cache` package, move `pkg/trunk/{engine,renovate,upgrade}` to top-level `pkg/{engine,renovate,upgrade}`, deduplicate the helpers `pkg/trunk/actions` and the engine both reimplement, export two internal-only YAML shape types, and document `filterEnabled`'s stages — all behavior-preserving.

**Architecture:** Seven sequential tasks, each independently buildable and testable: (1) create `pkg/cache`, (2)/(3) repoint `download`/`config` at it and delete the code it replaces, (4) move `engine`/`renovate`/`upgrade` out of `pkg/trunk`, (5) extract the actions/engine duplicate helpers into `pkg/engine`, (6) export `trunkFile`/`pluginFile`, (7) comment `filterEnabled`. Later tasks depend on earlier ones (4 must land before 5; 1 before 2 and 3), so execute in order.

**Tech Stack:** Go 1.27, testify (`assert`/`require`), no new dependencies.

**Spec:** `docs/superpowers/specs/2026-09-18-package-reorg-cache-consolidation-design.md`

## Global Constraints

- Every task is behavior-preserving except the two explicit non-goals already ruled in the spec (cache clean/prune scope stays exactly as today; `actions.Run`/`engine.Run` signatures don't change). Do not fix unrelated things noticed along the way — note them, don't touch them.
- After every task: `go build ./...`, `go vet ./...`, `go test ./...` must all pass before moving on. If `golangci-lint` is available (`go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest run ./...`, using this repo's `.golangci.yml`), run it too — it must report 0 issues.
- Commit after every task via the repo's own convention: read `.agents/skills/git-commit/SKILL.md` (type/scope/subject/body, `Assisted-by: anthropic:claude-sonnet-5` trailer, GPG `-S`, never `-s`). This repo's CLAUDE.md makes the `git-commit-assistant` agent mandatory for commit tasks — route each task's commit through it rather than committing directly.
- Never `git push`.

---

## Task 1: Create `pkg/cache`

**Files:**

- Create: `pkg/cache/cache.go`
- Create: `pkg/cache/cache_test.go`
- Create: `pkg/cache/json.go`
- Create: `pkg/cache/json_test.go`
- Create: `pkg/cache/prune.go`
- Create: `pkg/cache/prune_test.go`

**Interfaces:**

- Produces: `cache.Root(cacheDir string) (string, error)`, `cache.PluginsRoot(cacheDir string) (string, error)`, `cache.BlobPath(root, sum string) string`, `cache.InstallDir(root, category, id, version string) string`, `cache.InstallsBase(root, category, id string) string`, `cache.ShimPath(root, category, id, version, name string) string`, `cache.Platform() string`, `cache.PluginCacheFile(root, key string) string`, `cache.PluginCheckoutDir(root, key string) string`, `cache.LoadJSON[T any](path string, wantVersion int) (T, error)`, `cache.SaveJSON[T any](path string, version int, v T) error`, `cache.Clean(root string) error`, `cache.Prune(root string, keep map[string]bool) error` — all consumed by Tasks 2 and 3.

- [ ] **Step 1: Write `pkg/cache/cache.go`**

Ports `pkg/trunk/download/cache.go` verbatim for the downloads side, and generalizes
`pkg/trunk/config/git.go`'s inline `os.UserCacheDir()/rtunk/plugins` resolution (currently
duplicated logic — see spec "Current state") into `PluginsRoot`, sharing one private
`userCacheRoot` helper with `Root` for the real dedup. **`Root`'s behavior is an exact copy of
today's `download.Root`** (always downloads-suffixed); **`PluginsRoot`'s behavior is an exact copy
of today's `fetchGitSource`'s inline block** (plugins-suffixed only on the empty-cacheDir default
path, `filepath.Abs`'d unconditionally either way) — this asymmetry is pre-existing and must be
preserved, not "fixed."

```go
// Package cache implements rtunk's on-disk cache: path layout, generic keyed JSON storage, and
// clean/prune, shared by pkg/trunk/download's content-addressed download cache and
// pkg/trunk/config's plugin-source cache.
package cache

import (
	"os"
	"path/filepath"
	goruntime "runtime"
)

// userCacheRoot returns os.UserCacheDir()/rtunk -- the shared prefix both Root's and PluginsRoot's
// own default-cacheDir fallback build on.
func userCacheRoot() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "rtunk"), nil
}

// Root resolves the downloads cache root: cacheDir/downloads if cacheDir is set, or the OS-default
// userCacheRoot()/downloads otherwise. It does not create the directory -- callers that need it to
// exist call os.MkdirAll themselves.
func Root(cacheDir string) (string, error) {
	if cacheDir != "" {
		return filepath.Join(cacheDir, "downloads"), nil
	}
	base, err := userCacheRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "downloads"), nil
}

// PluginsRoot resolves the plugin-source cache root: cacheDir itself (made absolute) if set --
// unlike Root, an explicit cacheDir is used as-is, with no "/plugins" suffix appended -- or
// userCacheRoot()/plugins otherwise.
func PluginsRoot(cacheDir string) (string, error) {
	if cacheDir == "" {
		base, err := userCacheRoot()
		if err != nil {
			return "", err
		}
		cacheDir = filepath.Join(base, "plugins")
	}
	return filepath.Abs(cacheDir)
}

// BlobPath is where a fetched artifact's raw bytes live, content-addressed by sum (its own SHA256
// hex digest).
func BlobPath(root, sum string) string {
	return filepath.Join(root, "blobs", "sha256", sum)
}

// InstallDir is where one item's extracted/installed tree lives, keyed by category/id/version/
// platform since a hermetic install is platform-specific.
func InstallDir(root, category, id, version string) string {
	return filepath.Join(root, "installs", category, id, version, Platform())
}

// InstallsBase is category/id's installs directory with no version/platform suffix -- unlike
// InstallDir, which is keyed per-version, this is a stable prefix Prune uses to decide whether an
// on-disk installed version (any version, any platform) is still referenced by the resolved
// config, without needing to know which version is current.
func InstallsBase(root, category, id string) string {
	return filepath.Join(root, "installs", category, id)
}

// ShimPath is the filesystem path `rtunk where`/`rtunk exec` resolve to for one item's named shim.
func ShimPath(root, category, id, version, name string) string {
	return filepath.Join(root, "shims", category, id, version, name)
}

// Platform is the GOOS-GOARCH pair InstallDir keys installs by.
func Platform() string {
	return goruntime.GOOS + "-" + goruntime.GOARCH
}

// PluginCacheFile returns where one plugin source's parsed-definitions cache lives under root
// (from PluginsRoot), keyed by key (the source's own stable identity hash).
func PluginCacheFile(root, key string) string {
	return filepath.Join(root, key+".json")
}

// PluginCheckoutDir returns where one plugin source's full git checkout is persisted under root,
// keyed the same way as PluginCacheFile.
func PluginCheckoutDir(root, key string) string {
	return filepath.Join(root, "checkouts", key)
}
```

- [ ] **Step 2: Write `pkg/cache/cache_test.go`**

Ported from `pkg/trunk/download/cache_test.go` (which Task 2 deletes), `download.` renamed to
`cache.`, plus two new cases for `PluginsRoot`'s distinct behavior.

```go
package cache_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/cache"
)

func TestRoot_ExplicitCacheDir(t *testing.T) {
	root, err := cache.Root("/tmp/somewhere")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("/tmp/somewhere", "downloads"), root)
}

func TestRoot_DefaultCacheDir(t *testing.T) {
	root, err := cache.Root("")
	require.NoError(t, err)
	userCache, err := os.UserCacheDir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(userCache, "rtunk", "downloads"), root)
}

func TestPluginsRoot_ExplicitCacheDir(t *testing.T) {
	root, err := cache.PluginsRoot("/tmp/somewhere")
	require.NoError(t, err)
	assert.Equal(t, "/tmp/somewhere", root, "an explicit cacheDir is used as-is, no /plugins suffix")
}

func TestPluginsRoot_DefaultCacheDir(t *testing.T) {
	root, err := cache.PluginsRoot("")
	require.NoError(t, err)
	userCache, err := os.UserCacheDir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(userCache, "rtunk", "plugins"), root)
}

func TestBlobPath(t *testing.T) {
	assert.Equal(t, filepath.Join("root", "blobs", "sha256", "abc123"), cache.BlobPath("root", "abc123"))
}

func TestInstallDir(t *testing.T) {
	got := cache.InstallDir("root", "tools", "shellcheck", "0.11.0")
	want := filepath.Join("root", "installs", "tools", "shellcheck", "0.11.0", cache.Platform())
	assert.Equal(t, want, got)
}

func TestShimPath(t *testing.T) {
	got := cache.ShimPath("root", "tools", "shellcheck", "0.11.0", "shellcheck")
	want := filepath.Join("root", "shims", "tools", "shellcheck", "0.11.0", "shellcheck")
	assert.Equal(t, want, got)
}

func TestPlatform(t *testing.T) {
	assert.Equal(t, runtime.GOOS+"-"+runtime.GOARCH, cache.Platform())
}

func TestInstallsBase(t *testing.T) {
	got := cache.InstallsBase("root", "tools", "shellcheck")
	want := filepath.Join("root", "installs", "tools", "shellcheck")
	assert.Equal(t, want, got)
}

func TestPluginCacheFile(t *testing.T) {
	assert.Equal(t, filepath.Join("root", "abc123.json"), cache.PluginCacheFile("root", "abc123"))
}

func TestPluginCheckoutDir(t *testing.T) {
	assert.Equal(t, filepath.Join("root", "checkouts", "abc123"), cache.PluginCheckoutDir("root", "abc123"))
}
```

- [ ] **Step 3: Write `pkg/cache/json.go`**

Generalizes `pkg/trunk/config/cache.go`'s `cacheEnvelope`/`loadSourceCache`/`saveSourceCache`
(which Task 3 removes) into a type-parameterized version any caller can use.

```go
package cache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// jsonEnvelope is what actually lives on disk: v plus the schema version it was written under.
type jsonEnvelope[T any] struct {
	Version int
	Value   T
}

// LoadJSON reads path's cached value, rejecting (as a decode failure, same as malformed JSON)
// anything not written under wantVersion -- callers bump their own version constant whenever T's
// shape gains a field an older cache file wouldn't populate, so a stale cache never looks like a
// silently under-populated hit.
func LoadJSON[T any](path string, wantVersion int) (T, error) {
	var zero T
	data, err := os.ReadFile(path)
	if err != nil {
		return zero, err
	}
	var env jsonEnvelope[T]
	if err := json.Unmarshal(data, &env); err != nil {
		return zero, err
	}
	if env.Version != wantVersion {
		return zero, fmt.Errorf("cache: %s: schema version %d, want %d", path, env.Version, wantVersion)
	}
	return env.Value, nil
}

// SaveJSON writes v atomically (temp file + rename, in path's own directory so the rename is
// same-filesystem) under path, versioned as version, so a crash mid-write never leaves a
// half-written file behind to be mistaken for a valid cache hit.
func SaveJSON[T any](path string, version int, v T) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}

	data, err := json.Marshal(jsonEnvelope[T]{Version: version, Value: v})
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // no-op once renamed below

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
```

- [ ] **Step 4: Write `pkg/cache/json_test.go`**

Ported/genericized from `pkg/trunk/config/cache_test.go`'s round-trip and version-mismatch cases
(which Task 3 trims down to just the schema-version regression pin, staying in `config`), using a
local test struct instead of `config.sourceDefs` since `LoadJSON`/`SaveJSON` are generic.

```go
package cache_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/cache"
)

type testValue struct {
	Name string
}

func TestSaveLoadJSON_RoundTrips(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")

	want := testValue{Name: "foo"}
	require.NoError(t, cache.SaveJSON(path, 1, want))

	got, err := cache.LoadJSON[testValue](path, 1)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

// TestLoadJSON_RejectsWrongVersion covers the real regression this exists to prevent: a cache file
// written under an older schema version decodes without error into a JSON envelope missing a field
// added since -- json.Unmarshal leaves it zero-valued rather than failing -- so version-checking is
// the only thing that turns that silent under-population into a cache miss instead of a corrupted
// hit.
func TestLoadJSON_RejectsWrongVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")

	data, err := json.Marshal(struct {
		Version int
		Value   testValue
	}{Version: 1, Value: testValue{Name: "foo"}})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o644))

	_, err = cache.LoadJSON[testValue](path, 2)
	assert.Error(t, err, "a cache written under an older schema version must be treated as a miss")
}

// TestLoadJSON_RejectsPreVersioningFile covers the exact real-world shape: a cache file written
// before any envelope existed -- a flat value JSON object, no "Version"/"Value" wrapping at all.
func TestLoadJSON_RejectsPreVersioningFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")

	data, err := json.Marshal(testValue{Name: "foo"})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o644))

	_, err = cache.LoadJSON[testValue](path, 1)
	assert.Error(t, err, "a pre-versioning flat file must decode to Version 0, which never matches a real wantVersion")
}
```

- [ ] **Step 5: Write `pkg/cache/prune.go`**

Ports `internal/cli/cache.go`'s `cacheCleanCmd.Run` body and `pruneUnused` verbatim, exported.

```go
package cache

import (
	"os"
	"path/filepath"
)

// Clean removes every file under root (the downloads cache root from Root).
func Clean(root string) error {
	return os.RemoveAll(root)
}

// Prune removes every installs/<category>/<id> and shims/<category>/<id> directory under root
// whose path isn't one of keep -- callers key keep with both an InstallsBase path and its
// shims/<category>/<id> counterpart for each id to keep, since a surviving install's shim would
// otherwise never match either glob's keep-check.
func Prune(root string, keep map[string]bool) error {
	for _, pattern := range []string{
		filepath.Join(root, "installs", "*", "*"),
		filepath.Join(root, "shims", "*", "*"),
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return err
		}
		for _, match := range matches {
			if keep[match] {
				continue
			}
			if err := os.RemoveAll(match); err != nil {
				return err
			}
		}
	}
	return nil
}
```

- [ ] **Step 6: Write `pkg/cache/prune_test.go`**

Ported from `internal/cli/cache_test.go`'s `TestPruneUnused` (which Task 2 removes from
`internal/cli/cache_test.go`, since `pruneUnused` moves here as `Prune`).

```go
package cache_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/cache"
)

func TestPrune(t *testing.T) {
	root := t.TempDir()
	keep := cache.InstallDir(root, "tools", "actionlint", "1.0.0")
	stale := cache.InstallDir(root, "tools", "eslint", "1.0.0")
	keepShim := cache.ShimPath(root, "tools", "actionlint", "1.0.0", "actionlint")
	staleShim := cache.ShimPath(root, "tools", "eslint", "1.0.0", "eslint")
	require.NoError(t, os.MkdirAll(keep, 0o755))
	require.NoError(t, os.MkdirAll(stale, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(keepShim), 0o755))
	require.NoError(t, os.WriteFile(keepShim, nil, 0o644))
	require.NoError(t, os.MkdirAll(filepath.Dir(staleShim), 0o755))
	require.NoError(t, os.WriteFile(staleShim, nil, 0o644))

	keepPrefixes := map[string]bool{
		cache.InstallsBase(root, "tools", "actionlint"):     true,
		filepath.Join(root, "shims", "tools", "actionlint"): true,
	}
	require.NoError(t, cache.Prune(root, keepPrefixes))

	assert.DirExists(t, keep)
	assert.NoDirExists(t, filepath.Dir(filepath.Dir(stale))) // installs/tools/eslint gone entirely
	assert.DirExists(t, filepath.Dir(filepath.Dir(keepShim)))
	assert.NoDirExists(t, filepath.Dir(filepath.Dir(staleShim)))
}
```

- [ ] **Step 7: Build and test**

Run: `go build ./... && go vet ./... && go test ./pkg/cache/...`
Expected: builds clean, all new tests pass. (The rest of the repo still uses the old
`download`/`config` cache code at this point — that's Tasks 2-3 — so don't run the full `go test
./...` yet, only the new package.)

- [ ] **Step 8: Commit**

Route through `git-commit-assistant` per the Global Constraints. Suggested header:
`+[cache]: Add pkg/cache — shared cache-root layout, generic JSON storage, clean/prune`.

---

## Task 2: Point `pkg/trunk/download` at `pkg/cache`

**Files:**

- Delete: `pkg/trunk/download/cache.go` (moved to `pkg/cache/cache.go` in Task 1)
- Delete: `pkg/trunk/download/cache_test.go` (moved to `pkg/cache/cache_test.go` in Task 1)
- Modify: `pkg/trunk/download/blob.go` (1 call site)
- Modify: `pkg/trunk/download/download.go` (6 call sites)
- Modify: `pkg/trunk/download/download_test.go` (5 call sites, keeps its `download` import)
- Modify: `internal/cli/cache.go` (4 call sites, drops `download` import)
- Modify: `internal/cli/cache_test.go` (9 call sites, drops `download` import)
- Modify: `internal/cli/exec.go` (2 call sites, keeps `download` import)
- Modify: `internal/cli/exec_test.go` (6 call sites, drops `download` import)
- Modify: `internal/cli/fmt_recent_run.go` (1 call site, drops `download` import)
- Modify: `internal/cli/where.go` (2 call sites, drops `download` import)
- Modify: `internal/cli/where_test.go` (2 call sites, drops `download` import)
- Modify: `pkg/trunk/actions/history.go` (1 call site, drops `download` import)
- Modify: `pkg/trunk/actions/run.go` (4 call sites, keeps `download` import)
- Modify: `pkg/trunk/engine/engine.go` (3 call sites, keeps `download` import)
- Modify: `pkg/trunk/engine/engine_test.go` (28 call sites, keeps `download` import)
- Modify: `pkg/trunk/upgrade/upgrade.go` (1 call site, keeps `download` import)

**Interfaces:**

- Consumes: every `cache.*` function from Task 1.
- Produces: nothing new — `pkg/trunk/download`'s own exported API (`Download`, `FetchBlob`,
  `InstallDownload`, `BuildEnv`, `ResolveVersion`, `InstallPackagesFile`, `WriteShim`, etc.) is
  unchanged; only where it gets its cache paths from changes.

- [ ] **Step 1: Delete the two files moved in Task 1**

```bash
git rm pkg/trunk/download/cache.go pkg/trunk/download/cache_test.go
```

- [ ] **Step 2: Fix the two internal (unqualified) call sites**

`pkg/trunk/download/blob.go:100` and `pkg/trunk/download/download.go:53,138,159,182,203,219,246`
call `Root`/`BlobPath`/`InstallDir`/`ShimPath` unqualified (same package as the old `cache.go`).
Add the import and qualify each call:

```bash
sed -i '' -E 's/\b(Root|BlobPath|InstallDir|InstallsBase|ShimPath|Platform)\(/cache.\1(/g' \
  pkg/trunk/download/blob.go pkg/trunk/download/download.go
```

Then add `"github.com/xunleii/rtunk/pkg/cache"` to each file's import block (both already import
other `github.com/xunleii/rtunk/...` packages — add it alongside them, gofmt-grouped).

- [ ] **Step 3: Fix every external (qualified `download.X`) call site**

For all 13 files listed above except `blob.go`/`download.go` (already done in Step 2), rename the
six moved functions from `download.` to `cache.`:

```bash
sed -i '' -E 's/\bdownload\.(Root|BlobPath|InstallDir|InstallsBase|ShimPath|Platform)\(/cache.\1(/g' \
  pkg/trunk/download/download_test.go \
  internal/cli/cache.go internal/cli/cache_test.go \
  internal/cli/exec.go internal/cli/exec_test.go \
  internal/cli/fmt_recent_run.go \
  internal/cli/where.go internal/cli/where_test.go \
  pkg/trunk/actions/history.go pkg/trunk/actions/run.go \
  pkg/trunk/engine/engine.go pkg/trunk/engine/engine_test.go \
  pkg/trunk/upgrade/upgrade.go
```

- [ ] **Step 4: Fix imports in every file touched by Step 3**

Add `"github.com/xunleii/rtunk/pkg/cache"` to all 13 files. Then, in the seven files that used
`download.` **only** for the six moved functions (per the Files list above: `internal/cli/cache.go`,
`internal/cli/cache_test.go`, `internal/cli/exec_test.go`, `internal/cli/fmt_recent_run.go`,
`internal/cli/where.go`, `internal/cli/where_test.go`, `pkg/trunk/actions/history.go`), remove the
now-unused `"github.com/xunleii/rtunk/pkg/trunk/download"` import line entirely. The other six
files (`download_test.go`, `exec.go`, `actions/run.go`, `engine.go`, `engine_test.go`,
`upgrade.go`) still call other `download.*` symbols (confirmed while researching this plan — see
spec) and keep the import.

- [ ] **Step 5: Also fix `internal/cli/cache_test.go`'s `pruneUnused` call**

`internal/cli/cache_test.go`'s `TestPruneUnused` (line ~14-37 as of this plan) called the local
unexported `pruneUnused` — that function no longer exists in `internal/cli` (it moved to
`cache.Prune` in Task 1, and its own test moved to `pkg/cache/prune_test.go`'s `TestPrune`). Delete
`TestPruneUnused` from `internal/cli/cache_test.go` entirely (it's now redundant with
`pkg/cache/prune_test.go:TestPrune`). Keep `TestCacheClean` and `TestCachePrune_KeepsEnabledUsed`
(the CLI-level integration tests) — just make sure their `download.Root`/`InstallDir`/`ShimPath`
calls became `cache.Root`/`InstallDir`/`ShimPath` per Step 3.

- [ ] **Step 6: Update `internal/cli/cache.go`'s command bodies to call `cache.Clean`/`cache.Prune`**

`cacheCleanCmd.Run` currently does `return os.RemoveAll(root)` directly — replace with
`return cache.Clean(root)`. `cachePruneCmd.Run` currently ends with `return pruneUnused(root, keep)`
— replace with `return cache.Prune(root, keep)`, and delete the now-unused local `pruneUnused`
function (and its doc comment) from `internal/cli/cache.go` entirely — it's `cache.Prune` now.

- [ ] **Step 7: Build and test**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: builds clean, all tests pass (this is the regression signal for a behavior-preserving
move — no new test assertions needed here beyond what Task 1 already added).

- [ ] **Step 8: golangci-lint check**

Run: `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest run ./...`
Expected: 0 issues (this repo's `.golangci.yml` is already in place from the prior lint pass).

- [ ] **Step 9: Commit**

Route through `git-commit-assistant`. This span touches `cache`, `cli`, `actions`, `engine`,
`upgrade` scopes — split into per-scope commits per `.agents/skills/git-commit/SKILL.md`'s decision
tree (cap 3 scopes/commit), same approach used for the prior lint-cleanup commits in this repo's
history.

---

## Task 3: Point `pkg/trunk/config` at `pkg/cache`

**Files:**

- Modify: `pkg/trunk/config/git.go` (`fetchGitSource`)
- Modify: `pkg/trunk/config/cache.go` (shrinks significantly)
- Modify: `pkg/trunk/config/cache_test.go` (shrinks to one test)

**Interfaces:**

- Consumes: `cache.PluginsRoot`, `cache.PluginCacheFile`, `cache.PluginCheckoutDir`,
  `cache.LoadJSON[config.sourceDefs]`, `cache.SaveJSON` from Task 1.
- Produces: nothing new — `config.Resolve`/`config.ResolveAll`'s own signatures are unchanged.

- [ ] **Step 1: Shrink `pkg/trunk/config/cache.go`**

Keep `sourceDefs` (the type being cached) and `sourceHash` (the key-derivation function, needs
`PluginSource` which is config-specific) and `cacheSchemaVersion`. Remove `cacheEnvelope`,
`cacheFilePath`, `checkoutDirPath`, `loadSourceCache`, `saveSourceCache` entirely — their job now
lives in `pkg/cache` (envelope+atomic write) and directly inline in `fetchGitSource` (path
construction, via `cache.PluginCacheFile`/`cache.PluginCheckoutDir`). The file becomes:

```go
package config

import (
	"crypto/sha256"
	"encoding/hex"
)

// sourceDefs holds every definition contributed by a single plugin source, unmerged -- the shape
// both the on-disk cache stores (via pkg/cache.LoadJSON/SaveJSON) and mergeSourceInto folds into a
// Config.
type sourceDefs struct {
	// Environments and CommentFormats are global config, not per-id definitions: no map, just
	// concatenated across every plugin.yaml that contributes them (ARCHITECTURE.md "Built-in /
	// global config").
	Environments   []NamedEnvironment
	CommentFormats []CommentFormat

	Downloads map[string]Download
	Tools     map[string]Tool
	Lint      map[string]Linter
	Files     map[string]FileType
	Actions   map[string]Action
	Runtimes  map[string]Runtime
}

// cacheSchemaVersion must be bumped whenever sourceDefs' shape gains a field an older cache file
// wouldn't populate -- see pkg/cache.LoadJSON's own doc comment for why. Bumped four times already
// (ParseRegex, Linter.SourceDir, Download.Args, Action.Environment/SourceDir/SourceRoot/
// NotifyOnError) -- see git history for the real production bugs each of those closed.
const cacheSchemaVersion = 4

// sourceHash is the stable identity of a git plugin source, shared by pkg/cache.PluginCacheFile
// (the parsed-definitions cache) and pkg/cache.PluginCheckoutDir (the persisted checkout) so both
// live under the same key for the same uri+ref.
func sourceHash(src PluginSource) string {
	sum := sha256.Sum256([]byte(src.URI + "@" + src.Ref))
	return hex.EncodeToString(sum[:])
}
```

- [ ] **Step 2: Update `pkg/trunk/config/git.go`'s `fetchGitSource`**

Replace the cache-root resolution block (the `if cacheDir == "" { ... }` + `filepath.Abs` pair) and
the `cacheFilePath`/`checkoutDirPath`/`loadSourceCache`/`saveSourceCache` calls with their `pkg/cache`
equivalents. The function's own signature, error wrapping, and control flow (cache hit/miss, clone,
parse, persist) are unchanged -- only how paths are computed and how the cache is read/written.

```go
func fetchGitSource(cacheDir string, src PluginSource) (defs sourceDefs, dupErrs []error, err error) {
	cacheDir, err = cache.PluginsRoot(cacheDir)
	if err != nil {
		return sourceDefs{}, nil, &FetchError{SourceID: src.ID, URI: src.URI, Ref: src.Ref, Err: err}
	}

	key := sourceHash(src)
	cacheFile := cache.PluginCacheFile(cacheDir, key)
	checkoutDir := cache.PluginCheckoutDir(cacheDir, key)

	if defs, err := cache.LoadJSON[sourceDefs](cacheFile, cacheSchemaVersion); err == nil {
		if info, statErr := os.Stat(checkoutDir); statErr == nil && info.IsDir() {
			setSourceRoot(defs, checkoutDir)
			return defs, nil, nil
		}
		// The parsed-definitions cache survived but its paired checkout didn't -- treat exactly
		// like a decode failure and fall through to regenerate both together, below.
	}
	_ = os.Remove(cacheFile) // missing is fine; corrupt/stale/orphaned is dropped so it regenerates below

	if err := os.MkdirAll(cacheDir, 0o750); err != nil {
		return sourceDefs{}, nil, &FetchError{SourceID: src.ID, URI: src.URI, Ref: src.Ref, Err: err}
	}
	// MkdirTemp'd inside cacheDir (not the OS temp dir) so the persist step below is a same-
	// filesystem os.Rename -- atomic, no cross-device copy fallback needed.
	tmpDir, err := os.MkdirTemp(cacheDir, "checkout-tmp-*")
	if err != nil {
		return sourceDefs{}, nil, &FetchError{SourceID: src.ID, URI: src.URI, Ref: src.Ref, Err: err}
	}
	persisted := false
	defer func() {
		if !persisted {
			_ = os.RemoveAll(tmpDir)
		}
	}()

	for _, args := range [][]string{
		{"init"},
		{"remote", "add", "origin", src.URI},
		{"fetch", "--depth", "1", "origin", src.Ref},
		{"checkout", "FETCH_HEAD"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = tmpDir
		if out, err := cmd.CombinedOutput(); err != nil {
			return sourceDefs{}, nil, &FetchError{SourceID: src.ID, URI: src.URI, Ref: src.Ref, Err: fmt.Errorf("%v: %s", err, out)}
		}
	}

	defs, dupErrs, err = parseSourceDir(tmpDir)
	if err != nil {
		return sourceDefs{}, nil, err
	}

	checkoutParentDir := filepath.Dir(checkoutDir)
	if err := os.MkdirAll(checkoutParentDir, 0o750); err != nil {
		return sourceDefs{}, nil, &FetchError{SourceID: src.ID, URI: src.URI, Ref: src.Ref, Err: err}
	}
	if err := os.Rename(tmpDir, checkoutDir); err != nil {
		if info, statErr := os.Stat(checkoutDir); statErr != nil || !info.IsDir() {
			return sourceDefs{}, nil, &FetchError{SourceID: src.ID, URI: src.URI, Ref: src.Ref, Err: err}
		}
	} else {
		persisted = true
	}

	setSourceRoot(defs, checkoutDir)

	if err := cache.SaveJSON(cacheFile, cacheSchemaVersion, defs); err != nil {
		return sourceDefs{}, nil, &FetchError{SourceID: src.ID, URI: src.URI, Ref: src.Ref, Err: err}
	}

	return defs, dupErrs, nil
}
```

Add `"github.com/xunleii/rtunk/pkg/cache"` to `git.go`'s import block.

- [ ] **Step 3: Shrink `pkg/trunk/config/cache_test.go`**

Delete `TestSaveLoadSourceCache_RoundTrips`, `TestLoadSourceCache_RejectsWrongSchemaVersion`,
`TestLoadSourceCache_RejectsPreVersioningCacheFile` (their behavior is now covered generically by
`pkg/cache/json_test.go` from Task 1). Keep only the regression pin, unchanged:

```go
package config

import "testing"

import "github.com/stretchr/testify/assert"

func TestCacheSchemaVersion_Is4(t *testing.T) {
	// Regression pin: Action.Environment/SourceDir/SourceRoot/NotifyOnError are new fields an
	// already-cached git plugin source's JSON would silently decode as their zero values without
	// this bump -- see cache.go's own comment for the three prior times this exact bug was hit.
	assert.Equal(t, 4, cacheSchemaVersion)
}
```

- [ ] **Step 4: Build and test**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all pass, including `pkg/trunk/config/git_test.go`'s existing `fetchGitSource` coverage
(unchanged — this is the regression signal that the cache-path rewiring didn't break the real
fetch/cache-hit/cache-miss flow).

- [ ] **Step 5: golangci-lint check**

Run: `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest run ./...`
Expected: 0 issues.

- [ ] **Step 6: Commit**

Route through `git-commit-assistant`. Scope: `config`.

---

## Task 4: Move `engine`, `renovate`, `upgrade` out of `pkg/trunk`

**Files:**

- Move: `pkg/trunk/engine/**` → `pkg/engine/**` (includes `pkg/trunk/engine/security/**` →
  `pkg/engine/security/**`)
- Move: `pkg/trunk/renovate/**` → `pkg/renovate/**`
- Move: `pkg/trunk/upgrade/**` → `pkg/upgrade/**`
- Modify (import path only): `internal/cli/check.go`, `internal/cli/fmt_stability.go`,
  `internal/cli/fmt.go`, `internal/cli/renovate.go`, `internal/cli/upgrade.go`,
  `pkg/engine/engine.go` (post-move path), `pkg/upgrade/upgrade_test.go` (post-move path)

**Interfaces:**

- Produces: nothing new — every moved package's exported API is byte-for-byte unchanged, only its
  import path changes (`github.com/xunleii/rtunk/pkg/trunk/engine` →
  `github.com/xunleii/rtunk/pkg/engine`, same for `renovate`/`upgrade`).

- [ ] **Step 1: Move the three package trees**

```bash
git mv pkg/trunk/engine pkg/engine
git mv pkg/trunk/renovate pkg/renovate
git mv pkg/trunk/upgrade pkg/upgrade
```

(`git mv` on a directory moves every file under it, including `pkg/trunk/engine/security` →
`pkg/engine/security` as part of the first command — no separate step needed.)

- [ ] **Step 2: Fix the two self-referencing imports inside the moved trees**

Only two files import one of these three packages from _within_ one of them (confirmed while
researching this plan — every other file inside `engine`/`renovate`/`upgrade` only imports
`pkg/trunk/config`/`pkg/trunk/download`/`pkg/trunk/output`, which aren't moving):

```bash
sed -i '' 's#github.com/xunleii/rtunk/pkg/trunk/engine/security#github.com/xunleii/rtunk/pkg/engine/security#' \
  pkg/engine/engine.go
sed -i '' 's#github.com/xunleii/rtunk/pkg/trunk/upgrade#github.com/xunleii/rtunk/pkg/upgrade#' \
  pkg/upgrade/upgrade_test.go
```

- [ ] **Step 3: Fix every external importer**

```bash
sed -i '' 's#github.com/xunleii/rtunk/pkg/trunk/engine#github.com/xunleii/rtunk/pkg/engine#' \
  internal/cli/check.go internal/cli/fmt_stability.go internal/cli/fmt.go
sed -i '' 's#github.com/xunleii/rtunk/pkg/trunk/renovate#github.com/xunleii/rtunk/pkg/renovate#' \
  internal/cli/check.go internal/cli/renovate.go
sed -i '' 's#github.com/xunleii/rtunk/pkg/trunk/upgrade#github.com/xunleii/rtunk/pkg/upgrade#' \
  internal/cli/upgrade.go
```

(`internal/cli/check.go` imports both `engine` and `renovate` — both `sed` commands touch it, which
is correct: each only rewrites its own package's import line.)

- [ ] **Step 4: Build and test**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all pass. `gofmt -l .` should report nothing (the `sed` edits are single-line import path
swaps that don't disturb import-block grouping/ordering, but check anyway — if `gofmt -l` lists a
file, run `gofmt -w` on it).

- [ ] **Step 5: golangci-lint check**

Run: `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest run ./...`
Expected: 0 issues.

- [ ] **Step 6: Commit**

Route through `git-commit-assistant`. This is a pure move (`type` should reflect "no behavior
change" per SKILL.md's `=` vs `~` rule) touching `engine`+`renovate`+`upgrade`+`cli` — split by
scope per the SKILL.md decision tree, same as prior multi-scope commits in this repo's history.

---

## Task 5: Extract actions/engine shared helpers into `pkg/engine`

**Files:**

- Create: `pkg/engine/vars.go`
- Create: `pkg/engine/vars_test.go`
- Modify: `pkg/engine/engine.go` (remove the four functions moving to `vars.go`, update their call
  sites to the new exported names)
- Modify: `pkg/trunk/actions/run.go` (remove its own copies, call `pkg/engine` instead)

**Interfaces:**

- Produces: `engine.FindUnsupportedVar(run string, allowed func(token string) bool) (string, bool)`,
  `engine.ResolveRuntimeShimDir(cfg config.Config, root, cacheDir, runtimeID string) (string, error)`,
  `engine.QuoteOne(s string) string`, `engine.QuoteAll(ss []string) []string` — consumed by both
  `pkg/engine/engine.go` (internally) and `pkg/trunk/actions/run.go`.

- [ ] **Step 1: Write `pkg/engine/vars.go`**

`resolveRuntimeShimDir` and `quoteOne`/`quoteAll` are already byte-identical in logic between
`pkg/engine/engine.go` and `pkg/trunk/actions/run.go` (confirmed while researching this plan — only
doc comments and an error-message prefix differ). `findUnsupportedVar` (engine) and
`findUnsupportedActionVar` (actions) support different variable sets, so only the scanning
mechanism generalizes, parameterized by an `allowed` predicate each caller supplies.

```go
package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/xunleii/rtunk/pkg/trunk/config"
	"github.com/xunleii/rtunk/pkg/trunk/download"
)

var templateVarRE = regexp.MustCompile(`\$\{[^}]+\}`)

// FindUnsupportedVar returns the first ${...} placeholder in run for which allowed returns false,
// scanning left to right -- shared by engine's own Command.Run/Parser.Run checks and
// pkg/trunk/actions' Action.Run check, each supplying its own variable allow-list since the two
// packages substitute different variable sets.
func FindUnsupportedVar(run string, allowed func(token string) bool) (string, bool) {
	for _, v := range templateVarRE.FindAllString(run, -1) {
		if allowed(v) {
			continue
		}
		return v, true
	}
	return "", false
}

// ResolveRuntimeShimDir resolves (downloading first if not already cached) runtimeID's own shim
// directory under root, from cfg's resolved runtime definitions.
func ResolveRuntimeShimDir(cfg config.Config, root, cacheDir, runtimeID string) (string, error) {
	rt, ok := cfg.Runtimes.Definitions[runtimeID]
	if !ok {
		return "", fmt.Errorf("engine: runtime %q referenced but not found in resolved config", runtimeID)
	}
	if len(rt.Shims) == 0 {
		return "", fmt.Errorf("engine: runtime %q has no shims declared", runtimeID)
	}
	version := download.ResolveVersion(cfg.Runtimes.Enabled, runtimeID, rt.KnownGoodVersion)
	shimPath := download.ShimPath(root, "runtimes", runtimeID, version, rt.Shims[0])
	if _, statErr := os.Stat(shimPath); statErr != nil {
		evs, err := download.Download(cfg, cacheDir, download.Ref{Category: "runtimes", ID: runtimeID, Version: version})
		if err != nil {
			return "", err
		}
		for ev := range evs {
			if ev.Phase == download.Failed {
				return "", ev.Err
			}
		}
	}
	return filepath.Dir(shimPath), nil
}

// QuoteOne single-quotes s for safe interpolation into a shell command line (POSIX sh -c),
// escaping embedded single quotes.
func QuoteOne(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// QuoteAll is QuoteOne applied to every element of ss.
func QuoteAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = QuoteOne(s)
	}
	return out
}
```

Note: `download.ShimPath`/`download.Download`/`download.ResolveVersion` here already reflect Tasks
2-4's changes (`download.ShimPath` still exists — Task 2 only moved the cache _layout_ helpers to
`pkg/cache`, `download.ResolveVersion`/`download.Download` are unaffected download-domain logic
that never moved).

- [ ] **Step 2: Remove the four originals from `pkg/engine/engine.go`, call the new names**

Delete `engine.go`'s own `templateVarRE` var declaration, `findUnsupportedVar` function (the
`${target}`/`${tmpfile}`/`${plugin}`/`${cwd}` one — NOT `findUnsupportedParserVar`, which stays,
it's genuinely engine-specific and only has one caller), `resolveRuntimeShimDir`, `quoteAll`,
`quoteOne`.

Update call sites within `engine.go`:

- The line calling `findUnsupportedVar(cmd.Run)` becomes:
  ```go
  if v, ok := FindUnsupportedVar(cmd.Run, func(v string) bool {
  	switch v {
  	case "${target}", "${tmpfile}", "${plugin}", "${cwd}":
  		return true
  	}
  	return false
  }); ok {
  ```
- The line calling `resolveRuntimeShimDir(cfg, root, cacheDir, cmd.Parser.Runtime)` becomes
  `ResolveRuntimeShimDir(cfg, root, cacheDir, cmd.Parser.Runtime)`.
- Every `quoteAll(...)`/`quoteOne(...)` call site becomes `QuoteAll(...)`/`QuoteOne(...)`.

- [ ] **Step 3: Write `pkg/engine/vars_test.go`**

Covers `FindUnsupportedVar` directly (the other three are already covered by existing
`engine_test.go`/`run_test.go` call sites through their exported names now).

```go
package engine_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/xunleii/rtunk/pkg/engine"
)

func allowTargetOnly(v string) bool {
	return v == "${target}"
}

func TestFindUnsupportedVar_AllSupported(t *testing.T) {
	_, ok := engine.FindUnsupportedVar("cmd ${target}", allowTargetOnly)
	assert.False(t, ok)
}

func TestFindUnsupportedVar_ReturnsFirstUnsupported(t *testing.T) {
	v, ok := engine.FindUnsupportedVar("cmd ${target} ${bogus}", allowTargetOnly)
	assert.True(t, ok)
	assert.Equal(t, "${bogus}", v)
}
```

- [ ] **Step 4: Update `pkg/trunk/actions/run.go`**

Delete its own `templateVarRE`, `quoteOne`, `quoteAll`, `resolveRuntimeShimDir` (keep
`envVarRE`, `findUnsupportedActionVar`, and `substituteVars` — those are actions-specific, not
duplicated). Add `"github.com/xunleii/rtunk/pkg/engine"` to the import block.

`findUnsupportedActionVar` becomes a thin wrapper around the shared scanner, keeping its own exact
allow-list logic (env var regex + positional arg pattern) as the `allowed` predicate:

```go
func findUnsupportedActionVar(run string) (string, bool) {
	return engine.FindUnsupportedVar(run, func(v string) bool {
		switch v {
		case "${cwd}", "${plugin}", "${hook}", "${hook_stdin_path}", "${@}":
			return true
		}
		if envVarRE.MatchString(v) {
			return true
		}
		if len(v) == 4 && v[1] == '{' && v[2] >= '1' && v[2] <= '9' && v[3] == '}' {
			return true
		}
		return false
	})
}
```

Every `quoteOne(...)`/`quoteAll(...)` call in `run.go` (including inside `substituteVars`) becomes
`engine.QuoteOne(...)`/`engine.QuoteAll(...)`. The one `resolveRuntimeShimDir(cfg, root, opts.CacheDir,
action.Runtime)` call becomes `engine.ResolveRuntimeShimDir(cfg, root, opts.CacheDir, action.Runtime)`.

- [ ] **Step 5: Build and test**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all pass — `pkg/trunk/actions/run_test.go` and `pkg/engine`'s own existing test suite are
the regression signal (no logic changed, only where each function lives).

- [ ] **Step 6: golangci-lint check**

Run: `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest run ./...`
Expected: 0 issues.

- [ ] **Step 7: Commit**

Route through `git-commit-assistant`. Scopes: `engine`, `actions` — likely two commits per
SKILL.md's decision tree (the new shared code lands with `engine`; `actions/run.go`'s own
call-site switch is its own commit).

---

## Task 6: Export `trunkFile`/`pluginFile`

**Files:**

- Modify: `pkg/trunk/config/resolve.go` (remove both type definitions, update all references)
- Modify: `pkg/trunk/config/definitions.go` (add both type definitions)
- Modify: `pkg/trunk/config/config.go` (one comment references `trunkFile.Actions` by name)

**Interfaces:**

- Produces: `config.TrunkFile` (was `trunkFile`), `config.PluginFile` (was `pluginFile`) — same
  fields, same YAML tags, now exported for external consumers to decode a raw trunk.yaml/plugin.yaml
  into.

- [ ] **Step 1: Add both types to `pkg/trunk/config/definitions.go`**

Append after the existing `CommentFormat` type (the file's last type as of this plan):

```go
// TrunkFile is the raw shape of a trunk.yaml file, as YAML naturally decodes it (lists, not
// maps) -- the lecture phase's own output, before Resolve merges it into a map-keyed Config.
type TrunkFile struct {
	Version string `yaml:"version"`
	CLI     struct {
		Version string `yaml:"version"`
	} `yaml:"cli"`
	Plugins struct {
		Sources []PluginSource `yaml:"sources"`
	} `yaml:"plugins"`
	Runtimes struct {
		Enabled []string `yaml:"enabled"`
	} `yaml:"runtimes"`
	Lint struct {
		Enabled []string `yaml:"enabled"`
	} `yaml:"lint"`
	Actions struct {
		Enabled  []string `yaml:"enabled"`
		Disabled []string `yaml:"disabled"`
	} `yaml:"actions"`
}

// PluginFile is the raw shape of one plugin.yaml: any mix of the section kinds below
// (ARCHITECTURE.md "a single file commonly mixes sections"). The same shape covers both a
// resource file (linters/<name>/plugin.yaml, contributing Lint.Definitions etc.) and a global
// config file (the repo-root plugin.yaml, contributing Environments; a category-root file like
// linters/plugin.yaml, contributing Lint.CommentFormats) -- whichever fields a given file sets.
type PluginFile struct {
	Environments []NamedEnvironment `yaml:"environments"`
	Downloads    []Download         `yaml:"downloads"`
	Tools        struct {
		Definitions []Tool `yaml:"definitions"`
	} `yaml:"tools"`
	Lint struct {
		Definitions    []Linter        `yaml:"definitions"`
		CommentFormats []CommentFormat `yaml:"comment_formats"`
		Files          []FileType      `yaml:"files"`
	} `yaml:"lint"`
	Actions struct {
		Definitions []Action `yaml:"definitions"`
	} `yaml:"actions"`
	Runtimes struct {
		Definitions []Runtime `yaml:"definitions"`
	} `yaml:"runtimes"`
}
```

- [ ] **Step 2: Remove both type definitions from `pkg/trunk/config/resolve.go`**

Delete the `trunkFile` struct (with its doc comment) and `pluginFile` struct (with its doc comment)
from `resolve.go` — they now live in `definitions.go` as `TrunkFile`/`PluginFile`.

- [ ] **Step 3: Update every reference in `resolve.go`**

```bash
sed -i '' -E 's/\btrunkFile\b/TrunkFile/g; s/\bpluginFile\b/PluginFile/g' pkg/trunk/config/resolve.go
```

This renames the type name everywhere it's used as a type (`readTrunkFile(path string) (trunkFile,
error)` → `(TrunkFile, error)`, `var tf trunkFile` → `var tf TrunkFile`, `readPluginFile`'s
`pluginFile{}`/`var pf pluginFile` likewise) without touching the function names `readTrunkFile`/
`readPluginFile` themselves (they don't contain the bare word `trunkFile`/`pluginFile` as a whole
token — `readTrunkFile` won't match `\btrunkFile\b` since `Trunk` there is capitalized and preceded
by `read`, so the regex's word boundary keeps it untouched; verify this with Step 5's build).

- [ ] **Step 4: Update the one comment reference in `config.go`**

`pkg/trunk/config/config.go`'s `CategoryConfig.Disabled` field comment says `"only trunkFile.Actions
parses this today"` — update to `"only TrunkFile.Actions parses this today"`.

- [ ] **Step 5: Build and test**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all pass. If Step 3's `sed` missed an occurrence, this fails with `undefined: trunkFile`
or `undefined: pluginFile` — fix any stragglers by hand and re-run.

- [ ] **Step 6: golangci-lint check**

Run: `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest run ./...`
Expected: 0 issues. (Exporting a previously-unexported type with no doc comment would trigger
`revive`'s `exported` check — both types already carry doc comments in Step 1, so this should be
clean; if not, add one.)

- [ ] **Step 7: Commit**

Route through `git-commit-assistant`. Scope: `config`.

---

## Task 7: Comment `filterEnabled`'s stages

**Files:**

- Modify: `pkg/trunk/config/filter.go:18-74`

**Interfaces:**

- Produces: nothing — comments only, zero logic change.

- [ ] **Step 1: Add one comment per stage**

`filterEnabled` (full current body, for reference — do not change any line other than adding the
four comments below):

```go
func filterEnabled(cfg *Config) {
	// Stage 1: start from what trunk.yaml's own enabled: lists turned on.
	keepLint := filterMap(cfg.Lint.Definitions, enabledIDs(cfg.Lint.Enabled))
	keepActions := filterMap(cfg.Actions.Definitions, enabledIDs(cfg.Actions.Enabled))

	// Stage 2: keep every tool and file type those enabled linters reference.
	toolIDs := map[string]struct{}{}
	fileIDs := map[string]struct{}{}
	for _, l := range keepLint {
		for _, t := range l.Tools {
			toolIDs[t] = struct{}{}
		}
		for _, f := range l.Files {
			fileIDs[f] = struct{}{}
		}
	}
	keepTools := filterMap(cfg.Tools, toolIDs)
	keepFiles := filterMapTransitive(cfg.Lint.Files, fileIDs, func(f FileType) []string { return f.Inherit })

	// Stage 3: keep every runtime that trunk.yaml's own enabled: list turned on, plus every
	// runtime referenced by an enabled linter's parser, an enabled tool, or an enabled action.
	runtimeIDs := enabledIDs(cfg.Runtimes.Enabled)
	for _, l := range keepLint {
		for _, cmd := range l.Commands {
			if cmd.Parser != nil && cmd.Parser.Runtime != "" {
				runtimeIDs[cmd.Parser.Runtime] = struct{}{}
			}
		}
	}
	for _, t := range keepTools {
		if t.Runtime != "" {
			runtimeIDs[t.Runtime] = struct{}{}
		}
	}
	for _, a := range keepActions {
		if a.Runtime != "" {
			runtimeIDs[a.Runtime] = struct{}{}
		}
	}
	keepRuntimes := filterMap(cfg.Runtimes.Definitions, runtimeIDs)

	// Stage 4: keep every download recipe a kept tool or runtime references.
	downloadIDs := map[string]struct{}{}
	for _, t := range keepTools {
		if t.Download != "" {
			downloadIDs[t.Download] = struct{}{}
		}
	}
	for _, r := range keepRuntimes {
		if r.Download != "" {
			downloadIDs[r.Download] = struct{}{}
		}
	}
	keepDownloads := filterMap(cfg.Downloads, downloadIDs)

	cfg.Lint.Definitions = keepLint
	cfg.Actions.Definitions = keepActions
	cfg.Tools = keepTools
	cfg.Lint.Files = keepFiles
	cfg.Runtimes.Definitions = keepRuntimes
	cfg.Downloads = keepDownloads
}
```

- [ ] **Step 2: Build and test**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all pass (comments only — this step exists purely to confirm nothing else in this final
task drifted).

- [ ] **Step 3: golangci-lint check**

Run: `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest run ./...`
Expected: 0 issues.

- [ ] **Step 4: Full-repo final gate**

Run: `trunk check --filter=gofmt,golangci-lint2 --no-progress --all`
Expected: 0 issues, matching the state at the end of the prior lint-cleanup session — this confirms
the whole reorg is clean end to end, not just per-task.

- [ ] **Step 5: Commit**

Route through `git-commit-assistant`. Scope: `config`.
