# Command.Parser (native converter script) support Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Support `Command.Parser` (a real trunk-io catalog field naming a converter script that
turns a tool's native output into a supported shape, almost always SARIF) instead of unconditionally
skipping every command that sets it.

**Architecture:** Two layers. `pkg/trunk/config` gains a persisted git-checkout (so a linter's own
plugin-source scripts have real files to point at on a warm run), two new `Linter` fields
(`SourceDir`/`SourceRoot`) that resolve the `${plugin}`/`${cwd}` template vars, and a
`filterEnabled` fix so a parser's own runtime survives the enabled+used trim. `pkg/trunk/engine`
substitutes the two new template vars, resolves the parser's runtime shim (reusing the existing
tool-shim download machinery), and runs the parser script as a second stage: the real command's
raw stdout piped into the parser's stdin, its stdout captured as the new "real" output before the
existing `cmd.Output` dispatch runs.

**Tech Stack:** Go, existing `pkg/trunk/{config,engine,download}` packages, no new dependencies.

**Spec:** `docs/superpowers/specs/2026-09-13-check-parser-scripts-design.md`

## Global Constraints

- No new `go.mod` dependencies.
- Every test asserts exact values, never just "no error."
- `go build ./... && go vet ./... && go test ./... -count=1 -race` clean before every task is done.
- Commits signed (`-S`), commitlint conventions per `.agents/skills/git-commit/SKILL.md`.
- Do not touch `pkg/trunk/output`'s existing parsers or `pkg/trunk/engine/security` -- this plan's
  surface is `pkg/trunk/config` (schema + resolution + cache), `pkg/trunk/engine` (dispatch + new
  template vars), and existing call sites into `pkg/trunk/download` (no new code inside that
  package itself).
- `${plugin}`/`${cwd}` are literal string substitutions, exactly like `${target}`/`${tmpfile}`
  already are -- no validation that the resulting path exists, no special-casing per linter.

---

### Task 1: config -- persisted git checkout, SourceDir/SourceRoot, filterEnabled fix

**Files:**

- Modify: `pkg/trunk/config/definitions.go` (add `Linter.SourceDir`/`SourceRoot`)
- Modify: `pkg/trunk/config/resolve.go` (`parseSourceDir` tags `SourceDir`; new `setSourceRoot`
  helper; `mergePluginRepo` calls it)
- Modify: `pkg/trunk/config/git.go` (persist the checkout instead of discarding it; stamp
  `SourceRoot`; treat a cache hit whose checkout went missing as a miss)
- Modify: `pkg/trunk/config/cache.go` (`cacheSchemaVersion` 1 -> 2; factor `sourceHash`; add
  `checkoutDirPath`)
- Modify: `pkg/trunk/config/filter.go` (pull `Command.Parser.Runtime` into `runtimeIDs`)
- Test: `pkg/trunk/config/resolve_test.go`, `pkg/trunk/config/git_test.go`,
  `pkg/trunk/config/filter_test.go`

**Interfaces:**

- Produces: `config.Linter.SourceDir string` (relative, e.g. `"linters/trufflehog"`, stable/cached)
  and `config.Linter.SourceRoot string` (absolute, never cached, recomputed every `Resolve` call).
  Task 2's engine package reads both off `job.linter` (already a `config.Linter`) to resolve
  `${plugin}`/`${cwd}`.
- Consumes: nothing new from elsewhere in this plan.

- [ ] **Step 1: Add the two new `Linter` fields**

In `pkg/trunk/config/definitions.go`, in the `Linter` struct (after `VersionCommand`):

```go
	VersionCommand     *VersionCommand `yaml:"version_command,omitempty"`

	// SourceDir is this linter's own directory, relative to its plugin source's root (e.g.
	// "linters/trufflehog") -- how ${cwd} resolves relative to ${plugin} in a Command.Run or
	// Command.Parser.Run. Stable across machines/cacheDir, so safe to cache; set by
	// parseSourceDir. yaml:"-" blocks it from ever being read out of an actual plugin.yaml file --
	// it is derived from the file's own path, never authored.
	SourceDir string `yaml:"-"`
	// SourceRoot is the absolute local directory ${plugin} resolves to for this linter's
	// Run/Parser.Run strings: a local plugin source's own directory, or a git source's persisted
	// checkout (see fetchGitSource). Recomputed fresh from the *current* cacheDir on every Resolve
	// call -- json:"-" keeps it out of the on-disk cache, so a cacheDir override (or a checkout
	// later rebuilt at a new path) never leaves a stale absolute path baked into cached JSON.
	SourceRoot string `yaml:"-" json:"-"`
}
```

- [ ] **Step 2: `parseSourceDir` tags `SourceDir`**

In `pkg/trunk/config/resolve.go`, inside `parseSourceDir`'s category loop, replace:

```go
		for _, path := range matches {
			pf, err := readPluginFile(path)
			if err != nil {
				return sourceDefs{}, nil, err
			}

			mergeKeyed(defs.Downloads, pf.Downloads, func(d Download) string { return d.Name }, "download", &dupErrs)
			mergeKeyed(defs.Tools, pf.Tools.Definitions, func(t Tool) string { return t.Name }, "tool", &dupErrs)
			mergeKeyed(defs.Lint, pf.Lint.Definitions, func(l Linter) string { return l.Name }, "lint", &dupErrs)
			mergeKeyed(defs.Actions, pf.Actions.Definitions, func(a Action) string { return a.ID }, "action", &dupErrs)
			mergeKeyed(defs.Runtimes, pf.Runtimes.Definitions, func(r Runtime) string { return r.Type }, "runtime", &dupErrs)
		}
```

with:

```go
		for _, path := range matches {
			pf, err := readPluginFile(path)
			if err != nil {
				return sourceDefs{}, nil, err
			}

			// path is always dir/category/<name>/plugin.yaml (built from the Glob pattern just
			// above), so filepath.Dir(path) is always under dir -- Rel cannot fail here.
			sourceDir, relErr := filepath.Rel(dir, filepath.Dir(path))
			if relErr != nil {
				return sourceDefs{}, nil, relErr
			}
			for i := range pf.Lint.Definitions {
				pf.Lint.Definitions[i].SourceDir = sourceDir
			}

			mergeKeyed(defs.Downloads, pf.Downloads, func(d Download) string { return d.Name }, "download", &dupErrs)
			mergeKeyed(defs.Tools, pf.Tools.Definitions, func(t Tool) string { return t.Name }, "tool", &dupErrs)
			mergeKeyed(defs.Lint, pf.Lint.Definitions, func(l Linter) string { return l.Name }, "lint", &dupErrs)
			mergeKeyed(defs.Actions, pf.Actions.Definitions, func(a Action) string { return a.ID }, "action", &dupErrs)
			mergeKeyed(defs.Runtimes, pf.Runtimes.Definitions, func(r Runtime) string { return r.Type }, "runtime", &dupErrs)
		}
```

Then add this helper right after `parseSourceDir` (still in `resolve.go`):

```go
// setSourceRoot stamps root onto every Linter defs.Lint holds -- root is where ${plugin} should
// resolve to for this one source: a local source's own directory (mergePluginRepo), or a git
// source's persisted checkout (fetchGitSource). Map values aren't addressable in Go, so this reads
// each entry, sets the field, and writes it back.
func setSourceRoot(defs sourceDefs, root string) {
	for name, l := range defs.Lint {
		l.SourceRoot = root
		defs.Lint[name] = l
	}
}
```

And update `mergePluginRepo` to call it:

```go
func mergePluginRepo(cfg *Config, dir string, errs *[]error) error {
	defs, dupErrs, err := parseSourceDir(dir)
	if err != nil {
		return err
	}
	setSourceRoot(defs, dir)
	*errs = append(*errs, dupErrs...)
	mergeSourceInto(cfg, defs, errs)
	return nil
}
```

- [ ] **Step 3: Run the existing tests to confirm the tagging compiles and doesn't break anything yet**

Run: `go test ./pkg/trunk/config/... -run TestResolve -v`
Expected: PASS (no assertions reference the new fields yet, but nothing should break).

- [ ] **Step 4: Bump `cacheSchemaVersion` and factor the hash**

In `pkg/trunk/config/cache.go`, replace the existing `cacheSchemaVersion` const and its doc comment:

```go
// cacheSchemaVersion must be bumped whenever sourceDefs' shape gains a field an older cache file
// wouldn't populate -- json.Unmarshal silently leaves a new field zero-valued instead of erroring,
// so a stale cache written before the field existed looks like a normal cache hit, forever, unless
// something notices the version disagrees. Bumped once already for check-engine-refactor's
// Command.ParseRegex (see git history); bumped again here for Linter.SourceDir: a cache written
// before that field existed would decode every Linter's SourceDir as "", silently breaking ${cwd}
// substitution for every command that references it, with no error at all. loadSourceCache rejects
// a version mismatch as a decode failure; fetchGitSource already treats any decode failure as
// "drop and regenerate" (see git.go), so this one check is the whole fix -- no new code path.
const cacheSchemaVersion = 2
```

Replace the existing `cacheFilePath` function with:

```go
// sourceHash is the stable identity of a git plugin source, shared by cacheFilePath (the parsed-
// definitions cache) and checkoutDirPath (the persisted checkout) so both live under the same key
// for the same uri+ref.
func sourceHash(src PluginSource) string {
	sum := sha256.Sum256([]byte(src.URI + "@" + src.Ref))
	return hex.EncodeToString(sum[:])
}

// cacheFilePath returns where src's parsed-definitions cache lives: keyed by uri+ref, since a
// pinned ref never changes content.
func cacheFilePath(cacheDir string, src PluginSource) string {
	return filepath.Join(cacheDir, sourceHash(src)+".json")
}

// checkoutDirPath is where a git source's full checkout is persisted (see fetchGitSource) --
// keyed the same way as cacheFilePath, since a pinned ref never changes content.
func checkoutDirPath(cacheDir string, src PluginSource) string {
	return filepath.Join(cacheDir, "checkouts", sourceHash(src))
}
```

- [ ] **Step 5: Run the cache tests**

Run: `go test ./pkg/trunk/config/... -run TestSaveLoadSourceCache -run TestLoadSourceCache -v`
Expected: PASS (these tests reference `cacheSchemaVersion` symbolically, not its literal value, so
the bump to 2 doesn't require touching them).

- [ ] **Step 6: Persist the git checkout in `fetchGitSource`**

Replace all of `pkg/trunk/config/git.go` with:

```go
package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// fetchGitSource returns everything a git plugin source (uri/ref) contributes, preferring a cache
// of the already-parsed definitions at cacheFilePath(cacheDir, src) over touching the network at
// all. Caching is keyed by uri+ref (never mutates once fetched, since ref is always a tag or SHA
// per ARCHITECTURE.md), so a cache hit is safe to reuse indefinitely. The cache file is versioned
// (cacheSchemaVersion in cache.go), so a schema change never decodes into a silently
// under-populated result.
//
// Unlike the parsed-definitions cache, the git checkout itself is also persisted, at
// checkoutDirPath(cacheDir, src) -- not just cloned and discarded -- so ${plugin}/${cwd} (a
// Command.Run or Command.Parser.Run template var resolving into this source's own directory tree,
// e.g. a converter script real trunk-io linters like trufflehog ship next to their plugin.yaml)
// has real files to point at even on a warm run that never touches git again. Every Linter this
// source contributes gets its SourceRoot stamped to this checkout's directory (see setSourceRoot);
// a cache hit re-stamps it fresh every time, since SourceRoot is deliberately excluded from the
// cached JSON (see Linter.SourceRoot's own doc comment).
//
// On a cache miss -- a decode failure, a schema mismatch, or a cache hit whose paired checkout
// directory has gone missing (e.g. someone rm -rf'd just that one directory) -- both the parsed
// cache and the checkout are dropped and regenerated together: src's ref is cloned into a
// throwaway same-filesystem temp dir under cacheDir (so the later persist is an atomic rename,
// never a cross-device copy), parsed, then renamed into place at checkoutDirPath. dupErrs carries
// any *DuplicateError found while parsing (only possible on a cold fetch: a cache hit returns the
// already-deduplicated result, so there's nothing left to report).
func fetchGitSource(cacheDir string, src PluginSource) (defs sourceDefs, dupErrs []error, err error) {
	if cacheDir == "" {
		dir, err := os.UserCacheDir()
		if err != nil {
			return sourceDefs{}, nil, &FetchError{SourceID: src.ID, URI: src.URI, Ref: src.Ref, Err: err}
		}
		cacheDir = filepath.Join(dir, "rtunk", "plugins")
	}

	cacheFile := cacheFilePath(cacheDir, src)
	checkoutDir := checkoutDirPath(cacheDir, src)

	if defs, err := loadSourceCache(cacheFile); err == nil {
		if info, statErr := os.Stat(checkoutDir); statErr == nil && info.IsDir() {
			setSourceRoot(defs, checkoutDir)
			return defs, nil, nil
		}
		// The parsed-definitions cache survived but its paired checkout didn't -- treat exactly
		// like a decode failure and fall through to regenerate both together, below.
	}
	_ = os.Remove(cacheFile) // missing is fine; corrupt/stale/orphaned is dropped so it regenerates below

	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
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
			os.RemoveAll(tmpDir)
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

	if err := os.RemoveAll(checkoutDir); err != nil {
		return sourceDefs{}, nil, &FetchError{SourceID: src.ID, URI: src.URI, Ref: src.Ref, Err: err}
	}
	if err := os.Rename(tmpDir, checkoutDir); err != nil {
		return sourceDefs{}, nil, &FetchError{SourceID: src.ID, URI: src.URI, Ref: src.Ref, Err: err}
	}
	persisted = true // now living at checkoutDir; nothing left at tmpDir for the deferred cleanup

	setSourceRoot(defs, checkoutDir)

	if err := saveSourceCache(cacheFile, defs); err != nil {
		return sourceDefs{}, nil, &FetchError{SourceID: src.ID, URI: src.URI, Ref: src.Ref, Err: err}
	}

	return defs, dupErrs, nil
}
```

- [ ] **Step 7: Pull `Command.Parser.Runtime` into `filterEnabled`'s runtime set**

In `pkg/trunk/config/filter.go`, update the doc comment's second paragraph and the body. Replace:

```go
// "Used by the enabled ones" follows the reference graph one hop at a time: an enabled linter's
// tools:/files:, then a kept tool's runtime:/download:, an enabled action's runtime:, and a kept
// runtime's download:. A kept FileType's own inherit: chain is followed to a fixed point (unlike
// the rest of the graph, it can be more than one hop deep — e.g. bazel -> bazel-build -> ...).
// There's no cycle to worry about elsewhere (a Tool/Runtime never references a Linter/Action
// back), so a single pass is enough there.
```

with:

```go
// "Used by the enabled ones" follows the reference graph one hop at a time: an enabled linter's
// tools:/files:/commands[].parser.runtime, then a kept tool's runtime:/download:, an enabled
// action's runtime:, and a kept runtime's download:. A kept FileType's own inherit: chain is
// followed to a fixed point (unlike the rest of the graph, it can be more than one hop deep —
// e.g. bazel -> bazel-build -> ...). There's no cycle to worry about elsewhere (a Tool/Runtime
// never references a Linter/Action back), so a single pass is enough there.
```

Then, in the function body, replace:

```go
	runtimeIDs := enabledIDs(cfg.Runtimes.Enabled)
	for _, t := range keepTools {
		if t.Runtime != "" {
			runtimeIDs[t.Runtime] = struct{}{}
		}
	}
```

with:

```go
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
```

- [ ] **Step 8: Add the failing/new tests**

In `pkg/trunk/config/filter_test.go`, append:

```go
// TestFilterEnabled_ParserRuntimeTransitivelyKept: a Command.Parser.Runtime must be pulled in the
// same way a Tool's or Action's Runtime already is -- an enabled linter whose only command sets
// Parser.Runtime (no Tool/Action needs that runtime at all) must still keep it, not have Resolve's
// trim silently drop the very runtime its parser script needs.
func TestFilterEnabled_ParserRuntimeTransitivelyKept(t *testing.T) {
	cfg := Config{
		Lint: LintConfig{
			CategoryConfig: CategoryConfig[Linter]{
				Enabled: []string{"trufflehog"},
				Definitions: map[string]Linter{
					"trufflehog": {
						Name: "trufflehog",
						Commands: []Command{
							{Name: "lint", Run: "trufflehog ${target}", Parser: &Parser{Runtime: "python", Run: "convert.py"}},
						},
					},
				},
			},
		},
		Runtimes: CategoryConfig[Runtime]{
			Definitions: map[string]Runtime{
				"python": {Type: "python", Download: "python"},
			},
		},
		Downloads: map[string]Download{
			"python": {Name: "python"},
		},
	}

	filterEnabled(&cfg)

	assert.Contains(t, cfg.Runtimes.Definitions, "python")
	assert.Contains(t, cfg.Downloads, "python")
}
```

In `pkg/trunk/config/resolve_test.go`, in `TestResolve_WithPluginRepo`, add at the end (before the
closing `}`):

```go
	// SourceDir/SourceRoot let ${cwd}/${plugin} resolve into this local source's own directory
	// tree (pkg/trunk/engine's job): SourceDir is derived from the plugin.yaml's own path within
	// the source; SourceRoot is that source's own directory as mergePluginRepo resolved it.
	assert.Equal(t, filepath.Join("linters", "actionlint"), cfg.Lint.Definitions["actionlint"].SourceDir)
	assert.Equal(t, filepath.Join("testdata", "pluginrepo"), cfg.Lint.Definitions["actionlint"].SourceRoot)
```

(`resolve_test.go` is `package config_test`; if `"path/filepath"` isn't already imported there, add
it.)

In `pkg/trunk/config/git_test.go`, replace `TestResolve_GitSource` entirely with:

```go
// TestResolve_GitSource clones a local git fixture (built from the same plugin repo excerpts as
// TestResolve_WithPluginRepo) and resolves against it, and confirms the fetch leaves a cache file
// AND a persisted checkout behind under cacheDir, with the Linter's own SourceRoot/SourceDir
// pointing at real files inside that checkout.
func TestResolve_GitSource(t *testing.T) {
	src := gitFixture(t, "testdata/pluginrepo")
	cacheDir := t.TempDir()
	trunkYAML := trunkYAMLFor(t, src, []string{"actionlint"}, []string{"commitlint"}, []string{"node"})

	cfg, err := config.Resolve(trunkYAML, cacheDir)
	require.NoError(t, err)

	assert.Contains(t, cfg.Tools, "actionlint")
	assert.Contains(t, cfg.Lint.Definitions, "actionlint")
	assert.Contains(t, cfg.Actions.Definitions, "commitlint")
	assert.Contains(t, cfg.Runtimes.Definitions, "node")

	cacheFiles, err := filepath.Glob(filepath.Join(cacheDir, "*.json"))
	require.NoError(t, err)
	assert.Len(t, cacheFiles, 1, "fetch must leave exactly one parsed-definitions cache file behind")

	// The full checkout, not just the parsed-definitions cache, must be persisted -- this is what
	// lets ${plugin}/${cwd} resolve to real files on a later warm run.
	checkouts, err := filepath.Glob(filepath.Join(cacheDir, "checkouts", "*"))
	require.NoError(t, err)
	require.Len(t, checkouts, 1, "fetch must persist exactly one checkout directory")
	assert.Equal(t, checkouts[0], cfg.Lint.Definitions["actionlint"].SourceRoot,
		"a git-sourced Linter's SourceRoot must point at its persisted checkout")
	assert.Equal(t, filepath.Join("linters", "actionlint"), cfg.Lint.Definitions["actionlint"].SourceDir)
	assert.FileExists(t, filepath.Join(checkouts[0], "linters", "actionlint", "plugin.yaml"),
		"the persisted checkout must contain real files, not an empty directory")
}
```

Replace `TestResolve_GitSource_CorruptCache_Regenerates` entirely with:

```go
// TestResolve_GitSource_CorruptCache_Regenerates: a cache file that fails to decode must be
// dropped and rebuilt from a real fetch, not trusted or treated as fatal.
func TestResolve_GitSource_CorruptCache_Regenerates(t *testing.T) {
	src := gitFixture(t, "testdata/pluginrepo")
	cacheDir := t.TempDir()
	trunkYAML := trunkYAMLFor(t, src, nil, nil, nil)

	cfg1, err := config.Resolve(trunkYAML, cacheDir)
	require.NoError(t, err)

	cacheFiles, err := filepath.Glob(filepath.Join(cacheDir, "*.json"))
	require.NoError(t, err)
	require.Len(t, cacheFiles, 1)
	require.NoError(t, os.WriteFile(cacheFiles[0], []byte("not valid json"), 0o644))

	cfg2, err := config.Resolve(trunkYAML, cacheDir)
	require.NoError(t, err)
	assert.Equal(t, cfg1, cfg2)
}
```

Replace `TestResolve_GitSource_CorruptCache_FetchFails` entirely with:

```go
// TestResolve_GitSource_CorruptCache_FetchFails: same as above, but the fixture repo is also gone
// by the second Resolve — the dropped cache must surface as a real *FetchError, not a silent
// empty result.
func TestResolve_GitSource_CorruptCache_FetchFails(t *testing.T) {
	src := gitFixture(t, "testdata/pluginrepo")
	cacheDir := t.TempDir()
	trunkYAML := trunkYAMLFor(t, src, nil, nil, nil)

	_, err := config.Resolve(trunkYAML, cacheDir)
	require.NoError(t, err)

	cacheFiles, err := filepath.Glob(filepath.Join(cacheDir, "*.json"))
	require.NoError(t, err)
	require.Len(t, cacheFiles, 1)
	cacheFile := cacheFiles[0]
	require.NoError(t, os.WriteFile(cacheFile, []byte("not valid json"), 0o644))
	require.NoError(t, os.RemoveAll(src.URI))

	_, err = config.Resolve(trunkYAML, cacheDir)

	var fetchErr *config.FetchError
	require.ErrorAs(t, err, &fetchErr)
	assert.Equal(t, "fixture", fetchErr.SourceID)
	_, statErr := os.Stat(cacheFile)
	assert.True(t, os.IsNotExist(statErr), "corrupt cache file must be removed, not left behind")
}
```

Leave `TestResolve_GitSource_CacheHit` and `TestResolve_GitSource_DuplicateResource` untouched --
neither reads `cacheDir`'s entries directly, so neither breaks. Then append two new tests to
`git_test.go`:

```go
// TestResolve_GitSource_MissingCheckoutTriggersRefetch: a valid, decodable parsed-definitions
// cache whose paired checkout directory has been deleted (e.g. an operator manually cleaned it up)
// must not be trusted as a hit -- SourceRoot pointing at a directory that no longer exists would
// make ${plugin}/${cwd} resolve to nothing. Deleting the fixture repo between calls would make a
// real re-fetch fail outright, but here it's left alone: the point is only to prove the cache
// entry gets rebuilt, so the assertions below check the checkout is a NEW directory with the
// linter's fields pointing at it.
func TestResolve_GitSource_MissingCheckoutTriggersRefetch(t *testing.T) {
	src := gitFixture(t, "testdata/pluginrepo")
	cacheDir := t.TempDir()
	trunkYAML := trunkYAMLFor(t, src, []string{"actionlint"}, nil, nil)

	_, err := config.Resolve(trunkYAML, cacheDir)
	require.NoError(t, err)

	require.NoError(t, os.RemoveAll(filepath.Join(cacheDir, "checkouts")))

	cfg2, err := config.Resolve(trunkYAML, cacheDir)
	require.NoError(t, err)

	checkouts, err := filepath.Glob(filepath.Join(cacheDir, "checkouts", "*"))
	require.NoError(t, err)
	require.Len(t, checkouts, 1, "the missing checkout must be regenerated")
	assert.Equal(t, checkouts[0], cfg2.Lint.Definitions["actionlint"].SourceRoot)
	assert.Equal(t, filepath.Join("linters", "actionlint"), cfg2.Lint.Definitions["actionlint"].SourceDir)
}

// TestResolve_GitSource_CacheHitDoesNotReclone proves a genuine cache hit (parsed-definitions
// cache AND its checkout both present) never touches git again -- not just that it doesn't error
// (TestResolve_GitSource_CacheHit already proves that), but that the checkout directory itself is
// left untouched, via its mtime.
func TestResolve_GitSource_CacheHitDoesNotReclone(t *testing.T) {
	src := gitFixture(t, "testdata/pluginrepo")
	cacheDir := t.TempDir()
	trunkYAML := trunkYAMLFor(t, src, []string{"actionlint"}, nil, nil)

	_, err := config.Resolve(trunkYAML, cacheDir)
	require.NoError(t, err)

	checkouts, err := filepath.Glob(filepath.Join(cacheDir, "checkouts", "*"))
	require.NoError(t, err)
	require.Len(t, checkouts, 1)
	before, err := os.Stat(checkouts[0])
	require.NoError(t, err)

	require.NoError(t, os.RemoveAll(src.URI)) // a real second clone would now fail outright

	_, err = config.Resolve(trunkYAML, cacheDir)
	require.NoError(t, err)

	after, err := os.Stat(checkouts[0])
	require.NoError(t, err)
	assert.Equal(t, before.ModTime(), after.ModTime(), "a genuine cache hit must not touch the persisted checkout")
}
```

- [ ] **Step 9: Run the whole config package's tests**

Run: `go test ./pkg/trunk/config/... -v -count=1`
Expected: PASS, all tests including the new/updated ones above.

- [ ] **Step 10: Full build/vet/test, then commit**

Run: `go build ./... && go vet ./... && go test ./... -count=1 -race`
Expected: PASS across every package (only `pkg/trunk/config` changed in this task, so nothing else
should even recompile differently, but the plan's Global Constraints require this full check every
task).

```bash
git add pkg/trunk/config/definitions.go pkg/trunk/config/resolve.go pkg/trunk/config/git.go \
        pkg/trunk/config/cache.go pkg/trunk/config/filter.go pkg/trunk/config/resolve_test.go \
        pkg/trunk/config/git_test.go pkg/trunk/config/filter_test.go
git commit -S -m "$(cat <<'EOF'
+[config]: Persist plugin-source checkouts, resolve ${plugin}/${cwd}

Command.Parser scripts (and some Command.Run strings, e.g. nancy's real
run.sh) reference their own plugin source's files via ${plugin}/${cwd}.
Persist a git source's full checkout (cacheDir/checkouts/<hash>/, atomic
rename from a same-filesystem temp dir) instead of discarding it, and stamp
every Linter with SourceDir (relative, cached) and SourceRoot (absolute,
never cached, recomputed per Resolve call) so pkg/trunk/engine can resolve
both vars. Bumps cacheSchemaVersion to 2 (SourceDir is a new sourceDefs
field an older cache wouldn't populate) and treats a cache hit whose paired
checkout has gone missing as a miss, same as a decode failure. Also pulls
Command.Parser.Runtime into filterEnabled's transitive runtime set, so a
parser's own runtime survives the enabled+used trim even when no Tool/
Action needs it.
EOF
)"
```

---

### Task 2: engine -- ${plugin}/${cwd} substitution, parser runtime resolution, second-stage dispatch

**Files:**

- Modify: `pkg/trunk/engine/engine.go`
- Test: `pkg/trunk/engine/engine_test.go`

**Interfaces:**

- Consumes: `config.Linter.SourceDir`/`SourceRoot` (Task 1), `config.Command.Parser` (`*config.Parser{Runtime, Run string}`, pre-existing field), `config.Runtime.Shims`/`KnownGoodVersion` (pre-existing), `download.ResolveVersion`, `download.ShimPath`, `download.Download`, `download.Ref` (pre-existing, unchanged).
- Produces: nothing new consumed outside this package -- `Run`'s public signature is unchanged.

- [ ] **Step 1: Extend `findUnsupportedVar`'s allowlist**

In `pkg/trunk/engine/engine.go`, replace:

```go
// findUnsupportedVar reports the first ${...} placeholder in run that isn't ${target} or
// ${tmpfile} -- the only two this plan substitutes. Everything else (e.g. ${target,},
// ${upstream-ref}) is unsupported: left unsubstituted, it either breaks the shell (bad
// substitution) or gets silently reinterpreted by sh itself (${upstream-ref} -> ${upstream:-ref}).
func findUnsupportedVar(run string) (string, bool) {
	for _, v := range templateVarRE.FindAllString(run, -1) {
		if v != "${target}" && v != "${tmpfile}" {
			return v, true
		}
	}
	return "", false
}
```

with:

```go
// findUnsupportedVar reports the first ${...} placeholder in run that isn't one of the four this
// package substitutes: ${target}, ${tmpfile}, ${plugin} (a Linter's own plugin source's root
// directory, config.Linter.SourceRoot), or ${cwd} (that plugin source's own linter subdirectory,
// SourceRoot joined with SourceDir). Everything else (e.g. ${target,}, ${upstream-ref}) is
// unsupported: left unsubstituted, it either breaks the shell (bad substitution) or gets silently
// reinterpreted by sh itself (${upstream-ref} -> ${upstream:-ref}).
func findUnsupportedVar(run string) (string, bool) {
	for _, v := range templateVarRE.FindAllString(run, -1) {
		switch v {
		case "${target}", "${tmpfile}", "${plugin}", "${cwd}":
			continue
		}
		return v, true
	}
	return "", false
}
```

- [ ] **Step 2: Add `parserPathEnv` to `job`, and resolve it in `buildJobs`**

Replace the `job` struct's doc comment and body:

```go
// job is one command invocation queued for a worker: one batch (all matched files, for a Batch
// command; one file otherwise) of one linter's one command, with that linter's tool shims already
// resolved onto pathEnv. resolvedDir is the directory Command.RunFrom resolved to for every file
// in batch (repoRoot when RunFrom is empty) -- batch's entries are paths relative to resolvedDir,
// not repoRoot, ready for ${target} substitution once the invocation's cwd becomes resolvedDir (or
// a sandbox mirroring it). Resolving shims (which may download a tool) happens once per linter
// before any worker starts -- never inside a worker -- so two jobs never race downloading the
// same tool.
type job struct {
	linterName  string
	linter      config.Linter
	cmd         config.Command
	batch       []string
	pathEnv     string
	resolvedDir string
}
```

with:

```go
// job is one command invocation queued for a worker: one batch (all matched files, for a Batch
// command; one file otherwise) of one linter's one command, with that linter's tool shims already
// resolved onto pathEnv, and -- when cmd.Parser is set -- that parser's own runtime shim directory
// resolved onto parserPathEnv (a separate PATH prefix used only for the parser-stage invocation: a
// parser's runtime need not be any tool the linter itself uses). resolvedDir is the directory
// Command.RunFrom resolved to for every file in batch (repoRoot when RunFrom is empty) -- batch's
// entries are paths relative to resolvedDir, not repoRoot, ready for ${target} substitution once
// the invocation's cwd becomes resolvedDir (or a sandbox mirroring it). Resolving shims (which may
// download a tool or runtime) happens once per linter before any worker starts -- never inside a
// worker -- so two jobs never race downloading the same thing.
type job struct {
	linterName    string
	linter        config.Linter
	cmd           config.Command
	batch         []string
	pathEnv       string
	parserPathEnv string
	resolvedDir   string
}
```

Then, in `buildJobs`, replace the whole function body with:

```go
func buildJobs(cfg config.Config, root, cacheDir, repoRoot, name string, linter config.Linter, paths []string, include func(config.Command) bool, events chan<- Event) []job {
	files, err := Files(cfg, linter, repoRoot, paths)
	if err != nil {
		events <- Event{Linter: name, Phase: Failed, Note: "matching files", Err: err}
		return nil
	}
	if len(files) == 0 {
		return nil
	}

	var jobs []job
	var pathEnv string
	pathEnvResolved := false
	parserPathEnvByRuntime := map[string]string{}

	for _, cmd := range linter.Commands {
		if !include(cmd) {
			continue
		}
		if v, ok := findUnsupportedVar(cmd.Run); ok {
			events <- Event{Linter: name, Phase: Skipped, Note: fmt.Sprintf("unsupported template var %q", v)}
			continue
		}
		if !supportedOutputFormats[cmd.Output] {
			events <- Event{Linter: name, Phase: Skipped, Note: fmt.Sprintf("unsupported output format %q", cmd.Output)}
			continue
		}

		var parserPathEnv string
		if cmd.Parser != nil {
			dir, cached := parserPathEnvByRuntime[cmd.Parser.Runtime]
			if !cached {
				resolved, err := resolveRuntimeShimDir(cfg, root, cacheDir, cmd.Parser.Runtime)
				if err != nil {
					events <- Event{Linter: name, Phase: Skipped, Note: fmt.Sprintf("parser runtime %q unavailable: %v", cmd.Parser.Runtime, err)}
					continue
				}
				dir = resolved
				parserPathEnvByRuntime[cmd.Parser.Runtime] = dir
			}
			parserPathEnv = dir
		}

		if cmd.SandboxType != "" && cmd.SandboxType != "copy_targets" && cmd.SandboxType != "expanded" {
			events <- Event{Linter: name, Phase: Skipped, Note: fmt.Sprintf("unsupported sandbox_type %q", cmd.SandboxType)}
			continue
		}

		// "expanded" without an explicit RunFrom needs to default to the target's own directory,
		// not repoRoot: it exists to give a tool sibling-file context (a whole Go package, a
		// whole Terraform module), and real catalog data confirms this matters -- gokart's real
		// command is exactly SandboxType: expanded with RunFrom left empty, and it needs its
		// target's own directory, not repoRoot, to expand meaningfully. tflint's own "expanded"
		// command instead sets RunFrom: ${target_directory} explicitly, so this default only ever
		// fires when RunFrom really is empty -- an explicit RunFrom (of any form) is untouched.
		effectiveRunFrom := cmd.RunFrom
		if cmd.SandboxType == "expanded" && effectiveRunFrom == "" {
			effectiveRunFrom = "${target_directory}"
		}

		groups, ok := groupByRunFrom(effectiveRunFrom, files, repoRoot, linter.DirectConfigs)
		if !ok {
			events <- Event{Linter: name, Phase: Skipped, Note: fmt.Sprintf("unsupported run_from %q", cmd.RunFrom)}
			continue
		}

		if !pathEnvResolved {
			shimDirs, err := resolveShimDirs(cfg, root, cacheDir, linter.Tools)
			if err != nil {
				events <- Event{Linter: name, Phase: Failed, Note: "resolving tools", Err: err}
				return nil
			}
			pathEnv = strings.Join(shimDirs, string(os.PathListSeparator))
			pathEnvResolved = true
		}

		for _, dir := range sortedKeys(groups) {
			relFiles := groups[dir]
			var batches [][]string
			if cmd.Batch || !strings.Contains(cmd.Run, "${target}") {
				// A Run string with no ${target} placeholder can't distinguish between files --
				// running it once per matched file (Batch: false's default) would just repeat
				// the exact same invocation N times, reporting the exact same findings N times
				// (real catalog examples: tflint's and brakeman's first commands). One invocation
				// per resolved directory is what such a command can actually tell apart.
				batches = [][]string{relFiles}
			} else {
				for _, f := range relFiles {
					batches = append(batches, []string{f})
				}
			}
			for _, batch := range batches {
				jobs = append(jobs, job{
					linterName: name, linter: linter, cmd: cmd, batch: batch,
					pathEnv: pathEnv, parserPathEnv: parserPathEnv, resolvedDir: dir,
				})
			}
		}
	}
	return jobs
}
```

Also update `buildJobs`'s own doc comment (the paragraph right above the function) from:

```go
// buildJobs resolves name's matched files and queues one job per runnable command invocation
// (include selects which commands are runnable), emitting a Skipped event immediately for every
// command an unsupported feature rules out (var, output format, parser -- unchanged from v0.3.1;
// SandboxType/RunFrom attempt real resolution instead of a blanket skip, per v0.3.2) and a Failed
// event (returning no jobs) if matching files or resolving tools errors outright. Shim resolution
// -- which may download a tool -- runs at most once per linter, lazily, on the first command that
// needs it.
```

to:

```go
// buildJobs resolves name's matched files and queues one job per runnable command invocation
// (include selects which commands are runnable), emitting a Skipped event immediately for every
// command an unsupported feature rules out (var, output format, an unresolvable Parser.Runtime,
// SandboxType/RunFrom attempt real resolution instead of a blanket skip too, per v0.3.2) and a
// Failed event (returning no jobs) if matching files or resolving tools errors outright. Shim
// resolution -- which may download a tool, or (separately) a Command.Parser's own runtime -- runs
// at most once per linter (per distinct Parser.Runtime, for the parser case), lazily, on the first
// command that needs it.
```

- [ ] **Step 3: Add `resolveRuntimeShimDir`**

Add this function right after `resolveShimDirs` in `engine.go`:

```go
// resolveRuntimeShimDir resolves (downloading first if not already cached) runtimeID's own shim
// directory -- mirrors resolveShimDirs, but for a single Command.Parser.Runtime rather than a
// linter's own Tools list: a parser script is invoked through its runtime's own interpreter shim
// (e.g. python3), not a tool binary, and that runtime need not be one any Tool in cfg references.
//
// Every real trunk-io Command.Parser this feature was designed against uses runtime: python or
// runtime: node, and both real Runtime definitions fetch via a download: recipe (confirmed by
// reading their real plugin.yaml files) -- so this always has a real shim to resolve in practice.
// A runtime whose SystemVersion is set instead (the "already installed on this machine" case)
// never gets a shim written for it at all (see fetchRuntimeRef), so resolving one here would
// return a directory that was never created; this is a known, real gap for that specific
// combination, left unhandled since no real catalog Command.Parser reaches it today.
func resolveRuntimeShimDir(cfg config.Config, root, cacheDir, runtimeID string) (string, error) {
	rt, ok := cfg.Runtimes.Definitions[runtimeID]
	if !ok {
		return "", fmt.Errorf("engine: parser runtime %q referenced but not found in resolved config", runtimeID)
	}
	if len(rt.Shims) == 0 {
		return "", fmt.Errorf("engine: parser runtime %q has no shims declared", runtimeID)
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
```

- [ ] **Step 4: Rename `runOneInvocation`'s shadowing named return, add `pluginDir`/`cwdDir` params**

Replace `runOneInvocation` entirely:

```go
// runOneInvocation substitutes ${target}/${tmpfile}/${plugin}/${cwd} into cmd.Run and executes it
// through a shell (a Command.Run string is a shell command line referencing its tool(s) by bare
// name, not a path), with pathEnv prefixed onto PATH (verbatim PATH when pathEnv is empty -- a
// leading empty PATH component means "current directory" on POSIX, which would let workDir's own
// files shadow real binaries) and workDir as the working directory. ctx cancellation kills the
// subprocess immediately via exec.CommandContext. Returns the output named by cmd.ReadOutputFrom
// (default stdout), the process's raw stderr (always captured, regardless of ReadOutputFrom, so
// callers can surface it on a crash), and the exit code; err is only ever a launch failure (e.g.
// "sh" missing), never a non-zero exit -- callers read exitCode for that.
func runOneInvocation(ctx context.Context, cmd config.Command, workDir, pathEnv string, files []string, pluginDir, cwdDir string) (out, stderrOut string, exitCode int, err error) {
	target := strings.Join(quoteAll(files), " ")

	var tmpfile string
	if strings.Contains(cmd.Run, "${tmpfile}") {
		f, err := os.CreateTemp("", "rtunk-check-*")
		if err != nil {
			return "", "", 0, err
		}
		tmpfile = f.Name()
		f.Close()
		defer os.Remove(tmpfile)
	}

	run := strings.NewReplacer(
		"${target}", target, "${tmpfile}", tmpfile,
		"${plugin}", pluginDir, "${cwd}", cwdDir,
	).Replace(cmd.Run)

	c := exec.CommandContext(ctx, "sh", "-c", run)
	c.Dir = workDir
	path := os.Getenv("PATH")
	if pathEnv != "" {
		path = pathEnv + string(os.PathListSeparator) + path
	}
	c.Env = append(os.Environ(), "PATH="+path)

	var stdout, stderr strings.Builder
	c.Stdout = &stdout
	c.Stderr = &stderr

	runErr := c.Run()
	code := 0
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			code = exitErr.ExitCode()
		} else {
			return "", "", 0, runErr
		}
	}

	switch cmd.ReadOutputFrom {
	case "stderr":
		out = stderr.String()
	case "tmp_file":
		data, readErr := os.ReadFile(tmpfile)
		if readErr != nil {
			return "", stderr.String(), code, readErr
		}
		out = string(data)
	default: // "" or "stdout"
		out = stdout.String()
	}
	return out, stderr.String(), code, nil
}
```

(This closes a Minor the check-engine-refactor branch's own final review deferred: the named
return used to be called `output`, shadowing the imported `output` package for this function's
whole body -- harmless today only because the body never needed `output.Finding`, but a footgun
for a future edit. Renamed to `out` while this function's signature is being touched anyway.)

- [ ] **Step 5: Add `runParser`, wire it into `runBatch`**

Add this function right after `runOneInvocation`:

```go
// runParser converts a real command's raw native output into the shape cmd.Output expects, by
// piping it through parser.Run: stdin is stdin (the real command's own raw output, exactly what
// runOneInvocation returned), and the script's own stdout is the result -- the universal contract
// every real trunk-io Command.Parser script uses (confirmed by reading trufflehog_to_sarif.py,
// tfsec/parse.py, and ruff_to_sarif.py in full during this feature's design). ${target}/${plugin}/
// ${cwd} substitute into parser.Run exactly as they do into cmd.Run; workDir is the same directory
// (or sandbox) the real command itself just ran in. parserPathEnv is the parser's own runtime's
// shim directory (e.g. wherever python3 lives), entirely separate from the linter's own pathEnv --
// a parser's runtime need not be any tool the linter itself uses.
func runParser(ctx context.Context, parser *config.Parser, workDir, parserPathEnv, stdin string, batch []string, pluginDir, cwdDir string) (string, error) {
	target := strings.Join(quoteAll(batch), " ")
	run := strings.NewReplacer(
		"${target}", target, "${plugin}", pluginDir, "${cwd}", cwdDir,
	).Replace(parser.Run)

	c := exec.CommandContext(ctx, "sh", "-c", run)
	c.Dir = workDir
	c.Stdin = strings.NewReader(stdin)
	path := os.Getenv("PATH")
	if parserPathEnv != "" {
		path = parserPathEnv + string(os.PathListSeparator) + path
	}
	c.Env = append(os.Environ(), "PATH="+path)

	var stdout, stderr strings.Builder
	c.Stdout = &stdout
	c.Stderr = &stderr

	if err := c.Run(); err != nil {
		errText := strings.TrimSpace(stderr.String())
		if errText == "" {
			errText = err.Error()
		}
		return "", fmt.Errorf("engine: parser: %s", errText)
	}
	return stdout.String(), nil
}
```

Then, in `runBatch`, replace:

```go
	out, stderr, exitCode, err := runOneInvocation(ctx, j.cmd, workDir, j.pathEnv, j.batch)
	if err != nil {
		return nil, err
	}
	if containsInt(j.cmd.ErrorCodes, exitCode) {
		msg := strings.TrimSpace(out)
		if errText := strings.TrimSpace(stderr); errText != "" {
			if msg == "" {
				msg = errText
			} else {
				msg += "\n" + errText
			}
		}
		return nil, fmt.Errorf("engine: %s: %s exited %d: %s", j.linterName, j.cmd.Name, exitCode, msg)
	}
```

with:

```go
	pluginDir := j.linter.SourceRoot
	cwdDir := filepath.Join(j.linter.SourceRoot, j.linter.SourceDir)

	out, stderr, exitCode, err := runOneInvocation(ctx, j.cmd, workDir, j.pathEnv, j.batch, pluginDir, cwdDir)
	if err != nil {
		return nil, err
	}
	if containsInt(j.cmd.ErrorCodes, exitCode) {
		msg := strings.TrimSpace(out)
		if errText := strings.TrimSpace(stderr); errText != "" {
			if msg == "" {
				msg = errText
			} else {
				msg += "\n" + errText
			}
		}
		return nil, fmt.Errorf("engine: %s: %s exited %d: %s", j.linterName, j.cmd.Name, exitCode, msg)
	}

	// A Parser converts the real command's raw output into cmd.Output's expected shape (almost
	// always SARIF) before any of the dispatch below runs -- everything from here on parses out
	// exactly as if the real tool had produced it directly, whether or not a parser was involved.
	if j.cmd.Parser != nil {
		converted, err := runParser(ctx, j.cmd.Parser, workDir, j.parserPathEnv, out, j.batch, pluginDir, cwdDir)
		if err != nil {
			return nil, err
		}
		out = converted
	}
```

- [ ] **Step 6: Update the one existing direct caller of `runOneInvocation` in tests**

In `pkg/trunk/engine/engine_test.go`, in `TestRunOneInvocation_EmptyPathEnvHasNoCwdComponent`,
replace:

```go
	out, stderr, exitCode, err := runOneInvocation(context.Background(), cmd, repoRoot, "", nil)
```

with:

```go
	out, stderr, exitCode, err := runOneInvocation(context.Background(), cmd, repoRoot, "", nil, "", "")
```

- [ ] **Step 7: Update `TestRun`'s parser-skip fixture for the new (real) skip reason**

`TestRun`'s `fakeskipparser` fixture asserted the OLD blanket "always skip any Parser" behavior,
which this task removes. In `pkg/trunk/engine/engine_test.go`, replace the `"fakeskipparser"` entry
inside `TestRun`'s `cfg.Lint.Definitions` map:

```go
					"fakeskipparser": {
						Name: "fakeskipparser", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{{
							Name: "unsupported", Run: "faketool sarif ${target}", Output: "sarif",
							Parser: &config.Parser{Runtime: "python", Run: "convert.py"},
						}},
					},
```

with:

```go
					"fakeskipparserruntime": {
						// cfg has no Runtimes.Definitions["python"] entry at all -- resolveRuntimeShimDir
						// must Skip this one command, not fail the whole linter, since a real unresolvable
						// parser runtime is a per-command config gap, not a run-wide error.
						Name: "fakeskipparserruntime", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{{
							Name: "unsupported", Run: "faketool sarif ${target}", Output: "sarif",
							Parser: &config.Parser{Runtime: "python", Run: "convert.py"},
						}},
					},
```

And replace the matching assertion block:

```go
	skipParserEv, ok := byLinter["fakeskipparser"]
	require.True(t, ok)
	assert.Equal(t, Skipped, skipParserEv.Phase)
	assert.Equal(t, "unsupported parser (native output requires a converter script)", skipParserEv.Note)
```

with:

```go
	skipParserEv, ok := byLinter["fakeskipparserruntime"]
	require.True(t, ok)
	assert.Equal(t, Skipped, skipParserEv.Phase)
	assert.Contains(t, skipParserEv.Note, `parser runtime "python" unavailable`)
	assert.Contains(t, skipParserEv.Note, "not found in resolved config")
```

- [ ] **Step 8: Run the existing suite to confirm nothing else broke**

Run: `go test ./pkg/trunk/engine/... -v -count=1`
Expected: PASS, including the updated `TestRun` and `TestRunOneInvocation_EmptyPathEnvHasNoCwdComponent`.

- [ ] **Step 9: Add two new fake-tool cases for the parser pipeline and template-var tests**

In `pkg/trunk/engine/engine_test.go`, add `"io"` to `fakeToolSrc`'s import block (alphabetically
after `"fmt"`):

```go
import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)
```

Then add these three cases to `fakeToolSrc`'s `switch args[0]` block, right before the closing
`}` of the switch (after the existing `"abspath"` case):

```go
	case "rawtext":
		// Stands in for a real tool's native (non-SARIF) output -- e.g. trufflehog's own NDJSON --
		// that Command.Parser.Run converts into SARIF via the stdin/stdout pipe (see "sarifconvert"
		// below).
		fmt.Print("RAWFINDING:" + strings.Join(args[1:], ","))
	case "sarifconvert":
		// Stands in for a real converter script (e.g. trufflehog_to_sarif.py): reads the tool's
		// raw output on stdin, writes SARIF on stdout. The files it reports come from what it
		// actually read off stdin, not its own argv -- proving the pipe, not argv, carries the
		// real data across the two stages.
		data, _ := io.ReadAll(os.Stdin)
		raw := strings.TrimPrefix(strings.TrimSpace(string(data)), "RAWFINDING:")
		var results []string
		for _, f := range strings.Split(raw, ",") {
			results = append(results, "{\"ruleId\":\"converted-rule\",\"level\":\"error\",\"message\":{\"text\":\"converted from raw\"},\"locations\":[{\"physicalLocation\":{\"artifactLocation\":{\"uri\":\""+f+"\"},\"region\":{\"startLine\":1}}}]}")
		}
		fmt.Print("{\"runs\":[{\"results\":[" + strings.Join(results, ",") + "]}]}")
	case "echoargs":
		// Emits its own argv (minus args[0]) joined by "|" as a single SARIF finding's message --
		// lets a test assert exactly what ${plugin}/${cwd} substituted into Command.Run, without
		// needing a real linter or real plugin source.
		fmt.Print("{\"runs\":[{\"results\":[{\"ruleId\":\"echo\",\"level\":\"error\",\"message\":{\"text\":\""+strings.Join(args[1:], "|")+"\"},\"locations\":[{\"physicalLocation\":{\"artifactLocation\":{\"uri\":\"whatever\"},\"region\":{\"startLine\":1}}}]}]}]}")
```

- [ ] **Step 10: Write the parser end-to-end test**

Append to `pkg/trunk/engine/engine_test.go`:

```go
// TestRun_ParserConvertsRawOutputThroughStdinStdout proves the real Command.Parser contract every
// trunk-io converter script this feature's research found actually uses (trufflehog_to_sarif.py,
// tfsec/parse.py, ruff_to_sarif.py, all read in full): the real command's raw stdout becomes the
// parser script's stdin, and the parser script's own stdout is what gets parsed per cmd.Output --
// not the real command's raw output directly.
func TestRun_ParserConvertsRawOutputThroughStdinStdout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("faketool invoked via sh -c")
	}

	binPath := buildFakeToolBinary(t)

	cacheDir := t.TempDir()
	root, err := download.Root(cacheDir)
	require.NoError(t, err)

	// The linter's own tool.
	toolShim := download.ShimPath(root, "tools", "faketool", "1.0.0", "faketool")
	require.NoError(t, os.MkdirAll(filepath.Dir(toolShim), 0o755))
	require.NoError(t, download.WriteShim(toolShim, binPath))

	// The parser's own runtime -- a real "python" would resolve to a python3 shim; standing in
	// with faketool itself is enough to prove the wiring (runtime lookup, PATH, stdin/stdout)
	// without needing a real Python interpreter in CI.
	runtimeShim := download.ShimPath(root, "runtimes", "python", "3.12.0", "faketool")
	require.NoError(t, os.MkdirAll(filepath.Dir(runtimeShim), 0o755))
	require.NoError(t, download.WriteShim(runtimeShim, binPath))

	repoRoot := t.TempDir()
	target := filepath.Join(repoRoot, "target.txt")
	require.NoError(t, os.WriteFile(target, []byte("content\n"), 0o644))

	cfg := config.Config{
		Tools: map[string]config.Tool{
			"faketool": {Name: "faketool", KnownGoodVersion: "1.0.0"},
		},
		Runtimes: config.CategoryConfig[config.Runtime]{
			Definitions: map[string]config.Runtime{
				"python": {Type: "python", KnownGoodVersion: "3.12.0", Shims: config.ShimList{"faketool"}},
			},
		},
		Lint: config.LintConfig{
			Files: map[string]config.FileType{},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"fakeparsed": {
						Name: "fakeparsed", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{{
							Name: "lint", Run: "faketool rawtext ${target}", Output: "sarif", Batch: true,
							Parser: &config.Parser{Runtime: "python", Run: "faketool sarifconvert"},
						}},
					},
				},
			},
		},
	}

	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: cacheDir, Concurrency: 1}, nil, notFormatter)
	require.NoError(t, err)

	var got Event
	for ev := range events {
		if ev.Linter == "fakeparsed" && ev.Phase == Done {
			got = ev
		}
	}
	require.Len(t, got.Findings, 1)
	assert.Equal(t, "converted-rule", got.Findings[0].RuleID,
		"the finding must come from sarifconvert's output, not rawtext's raw text")
	assert.Equal(t, "converted from raw", got.Findings[0].Message)
	assert.Equal(t, "target.txt", got.Findings[0].File)
}

// TestRun_PluginAndCwdTemplateVarsResolveFromLinterSource proves ${plugin} substitutes to the
// Linter's own SourceRoot and ${cwd} to SourceRoot joined with SourceDir -- the two template vars
// a real trunk-io Command.Run/Parser.Run references to reach its own plugin source's scripts
// (nancy's real run.sh is `sh ${plugin}/linters/nancy/run.sh`; tfsec's real parser.run is
// `python3 ${cwd}/parse.py`, both confirmed by reading the real catalog during this feature's
// design).
func TestRun_PluginAndCwdTemplateVarsResolveFromLinterSource(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("faketool invoked via sh -c")
	}

	binPath := buildFakeToolBinary(t)

	cacheDir := t.TempDir()
	root, err := download.Root(cacheDir)
	require.NoError(t, err)
	shimPath := download.ShimPath(root, "tools", "faketool", "1.0.0", "faketool")
	require.NoError(t, os.MkdirAll(filepath.Dir(shimPath), 0o755))
	require.NoError(t, download.WriteShim(shimPath, binPath))

	repoRoot := t.TempDir()
	target := filepath.Join(repoRoot, "target.txt")
	require.NoError(t, os.WriteFile(target, []byte("content\n"), 0o644))

	pluginRoot := filepath.Join(t.TempDir(), "plugin-source")

	cfg := config.Config{
		Tools: map[string]config.Tool{
			"faketool": {Name: "faketool", KnownGoodVersion: "1.0.0"},
		},
		Lint: config.LintConfig{
			Files: map[string]config.FileType{},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"fakevars": {
						Name: "fakevars", Files: []string{"ALL"}, Tools: []string{"faketool"},
						SourceRoot: pluginRoot, SourceDir: filepath.Join("linters", "fakevars"),
						Commands: []config.Command{{
							Name: "lint", Run: "faketool echoargs ${plugin} ${cwd}", Output: "sarif", Batch: true,
						}},
					},
				},
			},
		},
	}

	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: cacheDir, Concurrency: 1}, nil, notFormatter)
	require.NoError(t, err)

	var got Event
	for ev := range events {
		if ev.Linter == "fakevars" && ev.Phase == Done {
			got = ev
		}
	}
	require.Len(t, got.Findings, 1)
	wantCwd := filepath.Join(pluginRoot, "linters", "fakevars")
	assert.Equal(t, pluginRoot+"|"+wantCwd, got.Findings[0].Message)
}
```

- [ ] **Step 11: Run the new tests**

Run: `go test ./pkg/trunk/engine/... -run TestRun_ParserConvertsRawOutputThroughStdinStdout -run TestRun_PluginAndCwdTemplateVarsResolveFromLinterSource -v`
Expected: PASS.

- [ ] **Step 12: Full build/vet/test, then commit**

Run: `go build ./... && go vet ./... && go test ./... -count=1 -race`
Expected: PASS across every package.

```bash
git add pkg/trunk/engine/engine.go pkg/trunk/engine/engine_test.go
git commit -S -m "$(cat <<'EOF'
+[engine]: Support Command.Parser converter scripts

Real trunk-io catalog research: 27 commands / 19 linters ship a converter
script instead of a supported native output format -- rtunk has always
skipped these outright. Replace the blanket skip with real support: the
real command's raw output is piped into the parser script's stdin, its own
stdout captured as the new "real" output before the existing cmd.Output
dispatch runs (almost always SARIF) -- the universal stdin/stdout contract
every real converter script (trufflehog_to_sarif.py, tfsec/parse.py,
ruff_to_sarif.py) uses. Adds ${plugin}/${cwd} template-var substitution
(resolved from config.Linter.SourceRoot/SourceDir, pkg/trunk/config's own
prior commit), and resolves a parser's own runtime shim by reusing the
existing tool-shim download machinery, keyed by cfg.Runtimes.Definitions
instead of cfg.Tools.

Also renames runOneInvocation's named return from `output` to `out` --
closes a Minor the check-engine-refactor branch's final review deferred
(shadowed the imported output package for that function's whole body),
while its signature was already being touched for this feature.
EOF
)"
```

---

## Self-Review Notes (for whoever runs this plan)

- **Spec coverage:** every section of the design spec has a concrete task -- checkout persistence
  and SourceDir/SourceRoot (Task 1, Steps 1-6), filterEnabled's transitive Parser.Runtime pull
  (Task 1, Step 7), ${plugin}/${cwd} substitution and runtime-shim reuse (Task 2, Steps 1-5). The
  spec's Non-goals (no audit of every other blocker on the 27 real commands, no cache GC, no
  understanding ruff's literal `0`/`1` arg) are deliberately NOT tasks -- nothing to do there.
- **cacheSchemaVersion ordering matters:** Task 1 bumps it to 2 in the same commit that adds
  `SourceDir` -- these must land together, never as two separate commits, or an intermediate state
  would have a real field with no version bump protecting it.
- **Task 1 -> Task 2 dependency:** Task 2's `resolveRuntimeShimDir` only ever finds a real runtime
  in `cfg.Runtimes.Definitions` for an actual `rtunk check` run if Task 1's `filterEnabled` fix
  shipped first (otherwise a parser's own runtime, unreferenced by any Tool/Action, would already
  have been trimmed away before `engine.Run` ever sees it) -- Task 2 is unit-testable independently
  (its tests build `config.Config` directly, bypassing `filterEnabled` entirely), but the two tasks
  must both land for the feature to work end-to-end. Do not skip Task 1.
- **Known, deliberately unhandled gap:** `resolveRuntimeShimDir` doesn't handle a
  `Runtime.SystemVersion` parser runtime (no shim ever gets written for one -- see the function's
  own doc comment in Task 2, Step 3). No real catalog `Command.Parser` reaches this today (both
  `python` and `node` are download-based in the real catalog, confirmed during design), so this is
  a documented non-goal, not a bug to fix in this plan.
