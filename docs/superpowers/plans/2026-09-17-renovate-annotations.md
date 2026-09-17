# Renovate Annotations Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `rtunk renovate annotate`/`rtunk renovate config`, a way to make `trunk.yaml`'s
version-pinned entries legible to Renovate's regex manager, and fix `internal/cli/check.go`'s
`editEnabled` so `check enable`/`disable` no longer silently drops those annotations once a file
has opted in.

**Architecture:** A new, pure-logic package (`pkg/trunk/renovate`) resolves, per entry, whether
rtunk can confidently name a Renovate datasource/depName for it (never guessing — see the spec's
own catalog research). `internal/cli/renovate.go` walks the parsed `*yaml.Node` tree the same way
`internal/cli/check.go`'s existing `editEnabled` already does, sets `HeadComment` where resolvable,
and rewrites an unpinned-but-resolvable entry to `id@knownGoodVersion`. `editEnabled` itself gains
an opt-in branch (triggered only when a category already carries at least one such comment) that
keeps re-deriving annotations across ordinary `check enable`/`disable` edits.

**Tech Stack:** Go, `gopkg.in/yaml.v3` (`*yaml.Node` tree editing — this plan is the first user of
its `HeadComment` field anywhere in this codebase), `github.com/alecthomas/kong`.

**Spec:** `docs/superpowers/specs/2026-09-17-renovate-annotations-design.md`

## Global Constraints

- No changes to `pkg/trunk/config`, `pkg/trunk/download`, or `pkg/trunk/engine` — Task 1's new
  package only ever _reads_ `config.Config`/`config.Tool`/`config.Runtime`/`config.Linter`/
  `config.PluginSource`/`config.DownloadEntry`; it modifies nothing in `pkg/trunk/config`.
- Actions are never annotated (`config.Action` has no `KnownGoodVersion`/`Download`/`Runtime`+
  `Package` fields at all — confirmed in the spec's own "Ground truth" section). No
  Action-related logic anywhere in this plan.
- **`yaml.v3`'s `HeadComment` field has zero prior uses in this codebase** (grepped, confirmed).
  Two non-obvious behaviors were verified empirically against the real vendored
  `gopkg.in/yaml.v3@v3.0.1` before this plan was written, and every task below relies on them
  being followed exactly:
  - For a flat sequence scalar (e.g. a `lint.enabled:` entry `- id@version`), setting
    `HeadComment` **on that scalar node itself** renders the comment on the line immediately
    above it, and round-trips correctly on a later re-parse.
  - For a `key: value` mapping pair (e.g. `ref: v1.11.0`), setting `HeadComment` on the **value**
    node renders the comment _after_ the line and does **not** survive a round-trip re-parse —
    wrong on both counts. Setting it on the **key** node instead renders correctly immediately
    above the `key: value` line and round-trips correctly. **Always set `HeadComment` on the key
    node for a mapping pair, never the value node.**
- New commitlint scope: `renovate`, added to `.agents/skills/git-commit/SKILL.md`'s scope table
  (matches this repo's existing one-scope-per-CLI-subsystem convention — same pattern as
  `upgrade`/`init`/`actions`).
- Signed commits (`git commit -S`) are the normal path for this plan — only fall back to
  `--no-gpg-sign` if signing genuinely fails (an earlier, unrelated part of this session hit a
  transient signing-agent issue; treat that as resolved unless it recurs here).
- Every existing test in `internal/cli/check_enable_test.go` (`TestCheckEnableCmd_*`,
  `TestCheckDisableCmd_*`) must still pass, byte-for-byte unmodified, after Task 4 — none of them
  declare a `# renovate:` comment anywhere in their fixtures, so they are this plan's own proof
  that the `editEnabled` fix is genuinely opt-in and changes nothing for a file that never used
  this feature.

---

### Task 1: `pkg/trunk/renovate` package

**Files:**

- Create: `pkg/trunk/renovate/annotate.go`
- Test: `pkg/trunk/renovate/annotate_test.go`

**Interfaces:**

- Produces: `Annotation{Datasource, DepName string}`,
  `ForLint(cfg config.Config, id string) (Annotation, string, bool)`,
  `ForRuntime(cfg config.Config, id string) (Annotation, string, bool)`,
  `ForPluginSource(src config.PluginSource) (Annotation, bool)` — the exact signatures Task 2's
  `internal/cli/renovate.go` calls directly, and Task 4's `editEnabled` fix calls for the `"lint"`
  category.

- [ ] **Step 1: Write the failing tests**

Create `pkg/trunk/renovate/annotate_test.go`:

```go
package renovate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/trunk/config"
)

func TestForLint_DownloadRecipe_AllGitHubEntries_OK(t *testing.T) {
	cfg := config.Config{
		Lint: config.LintConfig{CategoryConfig: config.CategoryConfig[config.Linter]{
			Definitions: map[string]config.Linter{
				"actionlint": {Tools: []string{"actionlint"}},
			},
		}},
		Tools: map[string]config.Tool{
			"actionlint": {Download: "actionlint-dl", KnownGoodVersion: "1.7.8"},
		},
		Downloads: map[string]config.Download{
			"actionlint-dl": {Downloads: []config.DownloadEntry{
				{URL: "https://github.com/rhysd/actionlint/releases/download/v${version}/actionlint_${version}_linux_amd64.tar.gz"},
				{URL: "https://github.com/rhysd/actionlint/releases/download/v${version}/actionlint_${version}_darwin_arm64.tar.gz"},
			}},
		},
	}

	ann, known, ok := ForLint(cfg, "actionlint")
	require.True(t, ok)
	assert.Equal(t, Annotation{Datasource: "github-releases", DepName: "rhysd/actionlint"}, ann)
	assert.Equal(t, "1.7.8", known)
}

func TestForLint_DownloadRecipe_MismatchedHostAcrossEntries_Skip(t *testing.T) {
	cfg := config.Config{
		Lint: config.LintConfig{CategoryConfig: config.CategoryConfig[config.Linter]{
			Definitions: map[string]config.Linter{"mixed": {Tools: []string{"mixed"}}},
		}},
		Tools: map[string]config.Tool{"mixed": {Download: "mixed-dl", KnownGoodVersion: "1.0.0"}},
		Downloads: map[string]config.Download{
			"mixed-dl": {Downloads: []config.DownloadEntry{
				{URL: "https://github.com/foo/bar/releases/download/v1/a.tar.gz"},
				{URL: "https://github.com/foo/other/releases/download/v1/b.tar.gz"},
			}},
		},
	}

	_, _, ok := ForLint(cfg, "mixed")
	assert.False(t, ok)
}

func TestForLint_DownloadRecipe_NonGitHubHost_Skip(t *testing.T) {
	cfg := config.Config{
		Lint: config.LintConfig{CategoryConfig: config.CategoryConfig[config.Linter]{
			Definitions: map[string]config.Linter{"helm": {Tools: []string{"helm"}}},
		}},
		Tools: map[string]config.Tool{"helm": {Download: "helm-dl", KnownGoodVersion: "3.14.0"}},
		Downloads: map[string]config.Download{
			"helm-dl": {Downloads: []config.DownloadEntry{
				{URL: "https://get.helm.sh/helm-v${version}-linux-amd64.tar.gz"},
			}},
		},
	}

	_, _, ok := ForLint(cfg, "helm")
	assert.False(t, ok)
}

func TestForLint_RuntimePackage_AllFiveEcosystems_OK(t *testing.T) {
	cases := []struct {
		runtime, wantDatasource string
	}{
		{"node", "npm"},
		{"python", "pypi"},
		{"php", "packagist"},
		{"go", "go"},
		{"rust", "crate"},
	}
	for _, c := range cases {
		t.Run(c.runtime, func(t *testing.T) {
			cfg := config.Config{
				Lint: config.LintConfig{CategoryConfig: config.CategoryConfig[config.Linter]{
					Definitions: map[string]config.Linter{"tool": {Tools: []string{"tool"}}},
				}},
				Tools: map[string]config.Tool{
					"tool": {Runtime: c.runtime, Package: "some/package/path", KnownGoodVersion: "9.9.9"},
				},
			}

			ann, known, ok := ForLint(cfg, "tool")
			require.True(t, ok)
			assert.Equal(t, Annotation{Datasource: c.wantDatasource, DepName: "some/package/path"}, ann)
			assert.Equal(t, "9.9.9", known)
		})
	}
}

func TestForLint_RuntimePackage_UnrecognizedEcosystem_Skip(t *testing.T) {
	cfg := config.Config{
		Lint: config.LintConfig{CategoryConfig: config.CategoryConfig[config.Linter]{
			Definitions: map[string]config.Linter{"tool": {Tools: []string{"tool"}}},
		}},
		Tools: map[string]config.Tool{
			"tool": {Runtime: "dotnet", Package: "SomePackage", KnownGoodVersion: "1.0.0"},
		},
	}

	_, _, ok := ForLint(cfg, "tool")
	assert.False(t, ok)
}

func TestForLint_ZeroTools_Skip(t *testing.T) {
	cfg := config.Config{
		Lint: config.LintConfig{CategoryConfig: config.CategoryConfig[config.Linter]{
			Definitions: map[string]config.Linter{"orphan": {Tools: nil}},
		}},
	}

	_, _, ok := ForLint(cfg, "orphan")
	assert.False(t, ok)
}

func TestForLint_TwoTools_AmbiguousSkip(t *testing.T) {
	cfg := config.Config{
		Lint: config.LintConfig{CategoryConfig: config.CategoryConfig[config.Linter]{
			Definitions: map[string]config.Linter{"multi": {Tools: []string{"a", "b"}}},
		}},
		Tools: map[string]config.Tool{
			"a": {Runtime: "node", Package: "pkg-a", KnownGoodVersion: "1.0.0"},
			"b": {Runtime: "node", Package: "pkg-b", KnownGoodVersion: "1.0.0"},
		},
	}

	_, _, ok := ForLint(cfg, "multi")
	assert.False(t, ok)
}

func TestForLint_UnknownLinterID_Skip(t *testing.T) {
	_, _, ok := ForLint(config.Config{}, "does-not-exist")
	assert.False(t, ok)
}

func TestForRuntime_DownloadRecipe_OK(t *testing.T) {
	cfg := config.Config{
		Runtimes: config.CategoryConfig[config.Runtime]{
			Definitions: map[string]config.Runtime{
				"node": {Download: "node-dl", KnownGoodVersion: "22.18.0"},
			},
		},
		Downloads: map[string]config.Download{
			"node-dl": {Downloads: []config.DownloadEntry{
				{URL: "https://github.com/nodejs-release-mirror/node/releases/download/v${version}/node.tar.gz"},
			}},
		},
	}

	ann, known, ok := ForRuntime(cfg, "node")
	require.True(t, ok)
	assert.Equal(t, Annotation{Datasource: "github-releases", DepName: "nodejs-release-mirror/node"}, ann)
	assert.Equal(t, "22.18.0", known)
}

func TestForRuntime_NonGitHubHost_Skip(t *testing.T) {
	cfg := config.Config{
		Runtimes: config.CategoryConfig[config.Runtime]{
			Definitions: map[string]config.Runtime{
				"node": {Download: "node-dl", KnownGoodVersion: "22.18.0"},
			},
		},
		Downloads: map[string]config.Download{
			"node-dl": {Downloads: []config.DownloadEntry{
				{URL: "https://nodejs.org/dist/v${version}/node.tar.gz"},
			}},
		},
	}

	_, _, ok := ForRuntime(cfg, "node")
	assert.False(t, ok)
}

func TestForRuntime_UnknownID_Skip(t *testing.T) {
	_, _, ok := ForRuntime(config.Config{}, "does-not-exist")
	assert.False(t, ok)
}

func TestForPluginSource_GitSource_OK(t *testing.T) {
	src := config.PluginSource{ID: "origin", URI: "https://github.com/trunk-io/plugins", Ref: "v1.11.0"}

	ann, ok := ForPluginSource(src)
	require.True(t, ok)
	assert.Equal(t, Annotation{Datasource: "github-tags", DepName: "trunk-io/plugins"}, ann)
}

func TestForPluginSource_LocalSource_Skip(t *testing.T) {
	src := config.PluginSource{ID: "local", Local: "../pluginrepo"}

	_, ok := ForPluginSource(src)
	assert.False(t, ok)
}

func TestForPluginSource_NonGitHubURI_Skip(t *testing.T) {
	src := config.PluginSource{ID: "gl", URI: "https://gitlab.com/example/plugins", Ref: "v1.0.0"}

	_, ok := ForPluginSource(src)
	assert.False(t, ok)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./pkg/trunk/renovate/... -v`
Expected: FAIL — the package doesn't exist yet (`no Go files in ...` / `undefined: ForLint` etc.).

- [ ] **Step 3: Write the implementation**

Create `pkg/trunk/renovate/annotate.go`:

```go
// Package renovate resolves, for a trunk.yaml/rtunk.yaml version-pinned entry, whether rtunk can
// confidently name the Renovate regex-manager datasource/depName that lets Renovate track and
// bump it on its own -- rtunk never checks an upstream itself (see this package's own design
// spec, docs/superpowers/specs/2026-09-17-renovate-annotations-design.md, "Why not build this
// ourselves"). Every function here either resolves confidently or reports ok=false; none of them
// guess.
package renovate

import (
	"regexp"

	"github.com/xunleii/rtunk/pkg/trunk/config"
)

// Annotation is a resolved Renovate regex-manager target: the datasource and dependency name
// Renovate needs to look up and bump a version on its own.
type Annotation struct {
	Datasource string
	DepName    string
}

// runtimeDatasources maps a Tool's Runtime ecosystem to the Renovate datasource that tracks
// packages published to it -- a small, closed set (the only five ecosystems the real
// trunk-io/plugins catalog uses for Runtime+Package tools as of this package's own design
// research). An ecosystem not in this table is skipped, never guessed at.
var runtimeDatasources = map[string]string{
	"node":   "npm",
	"python": "pypi",
	"php":    "packagist",
	"go":     "go",
	"rust":   "crate",
}

// githubOwnerRepoRE extracts a GitHub (owner, repo) pair from a URL that either names a repo
// directly (a plugins.sources[] URI, e.g. "https://github.com/trunk-io/plugins") or points
// somewhere under it (a DownloadEntry URL, e.g.
// "https://github.com/rhysd/actionlint/releases/download/v${version}/...") -- the
// "(?:/.*)?$" tail makes both shapes match with the same pattern.
var githubOwnerRepoRE = regexp.MustCompile(`^https://github\.com/([^/]+)/([^/]+)(?:/.*)?$`)

func extractGitHubOwnerRepo(url string) (owner, repo string, ok bool) {
	m := githubOwnerRepoRE.FindStringSubmatch(url)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// resolveDownloadRecipe resolves cfg.Downloads[downloadName]'s own (owner, repo) pair -- ok only
// if every one of its DownloadEntry.URL values is a GitHub URL and they all agree on the same
// (owner, repo). A recipe with zero entries, an entry that doesn't match, or entries that
// disagree is not confidently GitHub: skip rather than guess which entry is authoritative.
func resolveDownloadRecipe(cfg config.Config, downloadName string) (owner, repo string, ok bool) {
	dl, exists := cfg.Downloads[downloadName]
	if !exists || len(dl.Downloads) == 0 {
		return "", "", false
	}
	for i, entry := range dl.Downloads {
		o, r, matched := extractGitHubOwnerRepo(entry.URL)
		if !matched {
			return "", "", false
		}
		if i == 0 {
			owner, repo = o, r
			continue
		}
		if o != owner || r != repo {
			return "", "", false
		}
	}
	return owner, repo, true
}

// resolveToolAnnotation is the Tool-source resolution shared by ForLint's bridged Tool. Tries a
// Download recipe first, then Runtime+Package; neither present (or neither resolvable) is
// ok=false.
func resolveToolAnnotation(cfg config.Config, tool config.Tool) (Annotation, string, bool) {
	if tool.Download != "" {
		owner, repo, ok := resolveDownloadRecipe(cfg, tool.Download)
		if !ok {
			return Annotation{}, "", false
		}
		return Annotation{Datasource: "github-releases", DepName: owner + "/" + repo}, tool.KnownGoodVersion, true
	}
	if tool.Runtime != "" && tool.Package != "" {
		datasource, ok := runtimeDatasources[tool.Runtime]
		if !ok {
			return Annotation{}, "", false
		}
		return Annotation{Datasource: datasource, DepName: tool.Package}, tool.KnownGoodVersion, true
	}
	return Annotation{}, "", false
}

// ForLint resolves the Renovate annotation for a lint.enabled entry's bare id (no @version), by
// bridging through the Linter's own Tools[] to the single Tool it references. ok is false --
// annotation omitted, never guessed -- when: the linter id doesn't exist, the linter references
// zero or more than one tool (ambiguous: which tool's version would this even be pinning), or
// that tool's own source doesn't resolve via resolveToolAnnotation.
func ForLint(cfg config.Config, id string) (Annotation, string, bool) {
	linter, exists := cfg.Lint.Definitions[id]
	if !exists || len(linter.Tools) != 1 {
		return Annotation{}, "", false
	}
	tool, exists := cfg.Tools[linter.Tools[0]]
	if !exists {
		return Annotation{}, "", false
	}
	return resolveToolAnnotation(cfg, tool)
}

// ForRuntime is ForLint's runtimes.enabled equivalent. A Runtime carries its own Download recipe
// directly (no Tools[] bridge, and never Runtime+Package -- nothing installs a runtime via
// another runtime), so this only ever tries the Download-recipe path.
func ForRuntime(cfg config.Config, id string) (Annotation, string, bool) {
	rt, exists := cfg.Runtimes.Definitions[id]
	if !exists || rt.Download == "" {
		return Annotation{}, "", false
	}
	owner, repo, ok := resolveDownloadRecipe(cfg, rt.Download)
	if !ok {
		return Annotation{}, "", false
	}
	return Annotation{Datasource: "github-releases", DepName: owner + "/" + repo}, rt.KnownGoodVersion, true
}

// ForPluginSource resolves a plugins.sources[] entry's own git ref. Always datasource
// "github-tags" -- rtunk's plugin sources are always git repos. ok is false for a Local source
// (no upstream ref to track) or a non-GitHub URI (same "don't guess" rule as everywhere else in
// this package -- a GitLab/Bitbucket-hosted plugin source would need its own datasource name,
// not "github-tags").
func ForPluginSource(src config.PluginSource) (Annotation, bool) {
	if src.Local != "" {
		return Annotation{}, false
	}
	owner, repo, ok := extractGitHubOwnerRepo(src.URI)
	if !ok {
		return Annotation{}, false
	}
	return Annotation{Datasource: "github-tags", DepName: owner + "/" + repo}, true
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./pkg/trunk/renovate/... -v`
Expected: PASS for all tests (14 top-level, `TestForLint_RuntimePackage_AllFiveEcosystems_OK` has
5 subtests).

- [ ] **Step 5: Commit**

```bash
git add pkg/trunk/renovate/annotate.go pkg/trunk/renovate/annotate_test.go
git commit -S -m "$(cat <<'EOF'
+[renovate]: Add pkg/trunk/renovate datasource resolution

Pure resolution logic only -- ForLint/ForRuntime/ForPluginSource decide
whether a Renovate datasource+depName can be confidently named for a
version-pinned entry, per the never-guess rule from this feature's own
design (see the design doc's real trunk-io/plugins catalog research:
GitHub releases and 5 package ecosystems cover the confidently-resolvable
case; everything else -- a bespoke non-GitHub host, an ambiguous
multi-tool linter -- is left unannotated, not guessed at).
EOF
)"
```

---

### Task 2: `rtunk renovate annotate` command

**Files:**

- Create: `internal/cli/renovate.go`
- Create: `internal/cli/renovate_test.go`
- Modify: `internal/cli/cli.go` (the `CLI` struct, currently lines 27-59 — add one field)

**Interfaces:**

- Consumes: `renovate.Annotation`, `renovate.ForLint`, `renovate.ForRuntime`,
  `renovate.ForPluginSource` (Task 1). `resolveConfig(configPath, cacheDir string, all bool)
(config.Config, error)` and `findTrunkYAML() (string, error)` (both already exist in
  `internal/cli/config.go`/`internal/cli/findtrunk.go`). `cutVersion(s string) (id, version
string, pinned bool)` (already exists in `internal/cli/download.go`).
- Produces: `annotateDoc(doc *yaml.Node, cfg config.Config) renovateReport` and `findMapKey
(mapping *yaml.Node, key string) (keyNode, valueNode *yaml.Node, ok bool)` — Task 4's
  `editEnabled` fix calls `annotateEnabledSeq`'s underlying per-entry resolution the same way
  (via `renovate.ForLint` directly, not `annotateDoc` itself, since `editEnabled` only ever
  touches one category's `enabled:` sequence, not the whole document).

- [ ] **Step 1: Write the failing tests**

Create `internal/cli/renovate_test.go`:

```go
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeToolLinterFixture is writeLinterFixture's (check_run_test.go) tools+lint extension: it
// writes a plugin repo whose plugin.yaml declares a top-level downloads: recipe (real shape --
// see pkg/trunk/config/testdata/pluginrepo/linters/shellcheck/plugin.yaml -- a flat list, NOT
// nested under definitions:), a tools.definitions[] entry referencing it via download:, and a
// lint.definitions[] entry referencing that tool via tools: [<toolName>] -- the minimal real
// shape renovate.ForLint's Linter->Tools[0]->Tool bridge needs. Also writes a second
// plugins.sources[] entry (git-shaped: uri+ref, no local) purely as *yaml.Node content for
// annotateDoc to walk -- it is never resolved by config.ResolveAll (Local is the only source
// config.Resolve actually reads in this repo's test fixtures; see the shape below), so this
// stays hermetic (no network fetch).
func writeToolLinterFixture(t *testing.T, enabled []string, toolName, owner, repo, knownGoodVersion string) (cfgPath, repoRoot string) {
	t.Helper()
	repoRoot = t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, ".trunk"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "pluginrepo", "linters", "fixture"), 0o755))

	enabledYAML := ""
	for _, e := range enabled {
		enabledYAML += "    - " + e + "\n"
	}
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, ".trunk", "trunk.yaml"), []byte(`version: "0.1"
plugins:
  sources:
    - id: local
      local: ../pluginrepo
    - id: origin
      uri: https://github.com/`+owner+`/`+repo+`
      ref: v1.0.0
lint:
  enabled:
`+enabledYAML), 0o644))

	pluginYAML := fmt.Sprintf(`downloads:
  - name: %[1]s-download
    version: 1.0.0
    downloads:
      - os: { linux: linux, macos: macos, windows: windows }
        cpu: { x86_64: x86_64, arm_64: arm_64 }
        url: https://github.com/%[2]s/%[3]s/releases/download/v${version}/%[1]s.tar.gz
tools:
  definitions:
    - name: %[1]s
      download: %[1]s-download
      known_good_version: %[4]s
lint:
  definitions:
    - name: %[1]s
      files: [ALL]
      tools: [%[1]s]
      description: fixture linter
      commands:
        - name: lint
          run: echo unused
          output: xml
`, toolName, owner, repo, knownGoodVersion)
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "pluginrepo", "linters", "fixture", "plugin.yaml"), []byte(pluginYAML), 0o644))
	return filepath.Join(repoRoot, ".trunk", "trunk.yaml"), repoRoot
}

func TestRenovateAnnotate_UnpinnedResolvableEntry_GetsCommentAndPin(t *testing.T) {
	cfgPath, _ := writeToolLinterFixture(t, []string{"fixture"}, "fixture", "acme", "widget", "1.2.3")

	_, stderr, err := run2(t, "--config", cfgPath, "renovate", "annotate")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Contains(t, string(got), "# renovate: datasource=github-releases depName=acme/widget\n    - fixture@1.2.3\n")
}

func TestRenovateAnnotate_AlreadyPinnedEntry_GetsCommentKeepsVersion(t *testing.T) {
	cfgPath, _ := writeToolLinterFixture(t, []string{"fixture@9.9.9"}, "fixture", "acme", "widget", "1.2.3")

	_, stderr, err := run2(t, "--config", cfgPath, "renovate", "annotate")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Contains(t, string(got), "# renovate: datasource=github-releases depName=acme/widget\n    - fixture@9.9.9\n")
}

func TestRenovateAnnotate_UnresolvableLinter_LeftUntouched(t *testing.T) {
	// A linter enabled with no matching definition at all is never resolvable (no Tools[]
	// bridge exists) -- must be left exactly as-is, no comment, no forced pin.
	cfgPath, _ := writeToolLinterFixture(t, []string{"fixture", "phantom"}, "fixture", "acme", "widget", "1.2.3")

	_, stderr, err := run2(t, "--config", cfgPath, "renovate", "annotate")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Contains(t, string(got), "phantom\n")
	assert.NotContains(t, string(got), "phantom@")
}

func TestRenovateAnnotate_PluginSourceRef_GetsCommentOnKeyLine(t *testing.T) {
	cfgPath, _ := writeToolLinterFixture(t, nil, "fixture", "acme", "widget", "1.2.3")

	_, stderr, err := run2(t, "--config", cfgPath, "renovate", "annotate")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Contains(t, string(got), "# renovate: datasource=github-tags depName=acme/widget\n      ref: v1.0.0\n")
}

func TestRenovateAnnotate_LocalPluginSource_NeverAnnotated(t *testing.T) {
	cfgPath, _ := writeToolLinterFixture(t, nil, "fixture", "acme", "widget", "1.2.3")

	_, stderr, err := run2(t, "--config", cfgPath, "renovate", "annotate")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.NotContains(t, string(got), "depName=local")
}

func TestRenovateAnnotate_PrintsSummary(t *testing.T) {
	cfgPath, _ := writeToolLinterFixture(t, []string{"fixture"}, "fixture", "acme", "widget", "1.2.3")

	stdout, stderr, err := run2(t, "--config", cfgPath, "renovate", "annotate")
	require.NoError(t, err, "stderr: %s", stderr)
	assert.Contains(t, stdout, "annotated lint/fixture")
	assert.Contains(t, stdout, "annotated plugins.sources/origin")
	// writeToolLinterFixture always declares a second plugins.sources[] entry, "local" (a Local
	// source, per its own doc comment) -- that one is always skipped (renovate.ForPluginSource
	// reports ok=false for any Local source), so the fixture used by this whole file always
	// produces 2 annotated (lint/fixture, plugins.sources/origin) + 1 skipped
	// (plugins.sources/local), never 0 skipped.
	assert.Contains(t, stdout, "skipped plugins.sources/local")
	assert.Contains(t, stdout, "2 annotated, 1 skipped")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/... -run TestRenovateAnnotate -v`
Expected: FAIL — `renovate` isn't a registered command yet.

- [ ] **Step 3: Write `internal/cli/renovate.go`**

```go
package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/xunleii/rtunk/pkg/trunk/config"
	"github.com/xunleii/rtunk/pkg/trunk/renovate"
)

// renovateCmd is `rtunk renovate`: ROADMAP.md's post-v1.0 addition, generating Renovate
// annotations for trunk.yaml's version pins (see
// docs/superpowers/specs/2026-09-17-renovate-annotations-design.md).
type renovateCmd struct {
	Annotate renovateAnnotateCmd `cmd:"" help:"Annotate trunk.yaml's version pins for Renovate."`
	Config   renovateConfigCmd   `cmd:"" help:"Print the Renovate regexManagers config to add."`
}

type renovateAnnotateCmd struct{}

func (c *renovateAnnotateCmd) Run(cli *CLI, stdout io.Writer) error {
	configPath := cli.Config
	if configPath == "" {
		found, err := findTrunkYAML()
		if err != nil {
			return err
		}
		configPath = found
	}
	cfg, err := resolveConfig(configPath, cli.CacheDir, true)
	if err != nil {
		return err
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return err
	}

	report := annotateDoc(&doc, cfg)

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(detectIndentWidth(data))
	if err := enc.Encode(&doc); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	if err := os.WriteFile(configPath, buf.Bytes(), 0o644); err != nil {
		return err
	}

	printRenovateReport(stdout, report)
	return nil
}

// renovateReport summarizes what `rtunk renovate annotate` did.
type renovateReport struct {
	Annotated []string // "category/id" entries newly or already correctly annotated
	Skipped   []string // "category/id: reason" entries left untouched
}

func printRenovateReport(w io.Writer, r renovateReport) {
	sorted := append([]string(nil), r.Annotated...)
	sort.Strings(sorted)
	for _, a := range sorted {
		fmt.Fprintln(w, "annotated", a)
	}
	sortedSkipped := append([]string(nil), r.Skipped...)
	sort.Strings(sortedSkipped)
	for _, s := range sortedSkipped {
		fmt.Fprintln(w, "skipped", s)
	}
	fmt.Fprintf(w, "\n%d annotated, %d skipped\n", len(r.Annotated), len(r.Skipped))
}

// findMapKey returns mapping's key and value nodes for key, or ok=false if key is absent or
// mapping isn't a mapping -- a read-only counterpart to check.go's findOrCreateMapKey (which
// creates missing keys); annotateDoc must never author a new section a file didn't already have.
// Both nodes are returned because annotatePluginSources needs the KEY node specifically -- see
// this plan's own Global Constraints for why (HeadComment on a mapping's VALUE node renders
// wrong and doesn't round-trip; the KEY node is the one that works, verified against the real
// vendored yaml.v3).
func findMapKey(mapping *yaml.Node, key string) (keyNode, valueNode *yaml.Node, ok bool) {
	if mapping.Kind != yaml.MappingNode {
		return nil, nil, false
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i], mapping.Content[i+1], true
		}
	}
	return nil, nil, false
}

// annotateDoc walks doc's lint.enabled, runtimes.enabled, and plugins.sources[] nodes in place
// (doc is the parsed *yaml.Node document root, same tree shape editEnabled already parses),
// annotating every entry renovate.ForLint/ForRuntime/ForPluginSource resolves and rewriting an
// unpinned lint/runtimes entry to id@knownGoodVersion. cfg must already be resolved via
// config.ResolveAll (the full catalog, independent of what's currently enabled) -- annotating an
// entry needs its definition regardless of enabled state. A section entirely absent from doc (no
// lint:, no runtimes:, no plugins:) is left absent -- this function never authors a new empty
// section, unlike editEnabled's own find-or-create convention.
func annotateDoc(doc *yaml.Node, cfg config.Config) renovateReport {
	var report renovateReport
	if len(doc.Content) == 0 {
		return report
	}
	root := doc.Content[0]

	annotateEnabledSeq(root, "lint", &report, func(id string) (renovate.Annotation, string, bool) {
		return renovate.ForLint(cfg, id)
	})
	annotateEnabledSeq(root, "runtimes", &report, func(id string) (renovate.Annotation, string, bool) {
		return renovate.ForRuntime(cfg, id)
	})
	annotatePluginSources(root, cfg, &report)

	return report
}

// annotateEnabledSeq handles one category's "enabled:" sequence (a flat list of "id" or
// "id@version" scalars) -- shared by lint and runtimes, which have the identical node shape.
// resolve is renovate.ForLint or renovate.ForRuntime, already closed over cfg by the caller.
func annotateEnabledSeq(root *yaml.Node, category string, report *renovateReport, resolve func(id string) (renovate.Annotation, string, bool)) {
	_, catNode, ok := findMapKey(root, category)
	if !ok {
		return
	}
	_, enabledNode, ok := findMapKey(catNode, "enabled")
	if !ok || enabledNode.Kind != yaml.SequenceNode {
		return
	}

	for _, entry := range enabledNode.Content {
		id, _, pinned := cutVersion(entry.Value)
		ann, knownGoodVersion, ok := resolve(id)
		label := category + "/" + id
		if !ok {
			report.Skipped = append(report.Skipped, label+": no confident datasource")
			continue
		}
		if !pinned {
			if knownGoodVersion == "" {
				report.Skipped = append(report.Skipped, label+": no known_good_version to pin")
				continue
			}
			entry.Value = id + "@" + knownGoodVersion
		}
		entry.HeadComment = "# renovate: datasource=" + ann.Datasource + " depName=" + ann.DepName
		report.Annotated = append(report.Annotated, label)
	}
}

// annotatePluginSources handles plugins.sources[] -- a sequence of {id, uri, ref, local?}
// mappings, structurally different from lint/runtimes' flat "id@version" scalars: the comment
// goes on the "ref:" KEY node within each source's own mapping (see this plan's Global
// Constraints on why the key node, not the value node).
func annotatePluginSources(root *yaml.Node, cfg config.Config, report *renovateReport) {
	_, pluginsNode, ok := findMapKey(root, "plugins")
	if !ok {
		return
	}
	_, sourcesNode, ok := findMapKey(pluginsNode, "sources")
	if !ok || sourcesNode.Kind != yaml.SequenceNode {
		return
	}

	for _, entry := range sourcesNode.Content {
		_, idVal, ok := findMapKey(entry, "id")
		if !ok {
			continue
		}
		id := idVal.Value
		label := "plugins.sources/" + id
		src, exists := cfg.Plugins.Sources[id]
		if !exists {
			report.Skipped = append(report.Skipped, label+": not found in resolved config")
			continue
		}
		ann, ok := renovate.ForPluginSource(src)
		if !ok {
			report.Skipped = append(report.Skipped, label+": no confident datasource (local source or non-GitHub URI)")
			continue
		}
		refKey, _, ok := findMapKey(entry, "ref")
		if !ok {
			report.Skipped = append(report.Skipped, label+": no ref: to annotate")
			continue
		}
		refKey.HeadComment = "# renovate: datasource=" + ann.Datasource + " depName=" + ann.DepName
		report.Annotated = append(report.Annotated, label)
	}
}
```

- [ ] **Step 4: Wire `renovate` into the CLI grammar**

In `internal/cli/cli.go`, add one field to the `CLI` struct (after `RunCmd`, currently the last
field before the closing `}` at line 59):

```go
	RunCmd actionsRunCmd `cmd:"" name:"run" help:"Run a specified action (shortcut for 'actions run')."`
	// RenovateCmd is `rtunk renovate`: generates Renovate annotations for trunk.yaml's version
	// pins (see docs/superpowers/specs/2026-09-17-renovate-annotations-design.md).
	RenovateCmd renovateCmd `cmd:"" name:"renovate" help:"Generate Renovate annotations for trunk.yaml's version pins."`
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/cli/... -run TestRenovateAnnotate -v`
Expected: PASS for all 6 tests.

If `TestRenovateAnnotate_PluginSourceRef_GetsCommentOnKeyLine` or
`TestRenovateAnnotate_UnpinnedResolvableEntry_GetsCommentAndPin` fail on exact comment placement
or indentation, print the actual file content (`t.Log(string(got))`) and compare against this
plan's Global Constraints section on `HeadComment` placement — the two behaviors documented there
were verified against the real library, but the exact indentation in your test's specific fixture
depends on `detectIndentWidth`'s own sniffing of the file's leading whitespace, so confirm the
literal number of spaces in your assertion matches what `writeToolLinterFixture` actually writes
(4 spaces for `lint.enabled:` entries, 6 for `plugins.sources[].ref:` — both already reflected in
the test bodies above, but re-verify against your own fixture's actual indentation if you change
anything about `writeToolLinterFixture`).

- [ ] **Step 6: Run the full package test suite**

Run: `go test ./internal/cli/... -v`
Expected: PASS, zero regressions among the pre-existing suite.

- [ ] **Step 7: Commit**

```bash
git add internal/cli/renovate.go internal/cli/renovate_test.go internal/cli/cli.go
git commit -S -m "$(cat <<'EOF'
+[renovate]: Add rtunk renovate annotate

Walks lint.enabled/runtimes.enabled/plugins.sources[] via the same
*yaml.Node tree editEnabled already parses, setting a "# renovate:
datasource=... depName=..." HeadComment wherever pkg/trunk/renovate
resolves one, and pinning an otherwise-unpinned resolvable entry to its
known_good_version -- Renovate's regex manager needs a literal version
string in the file to capture and later bump.
EOF
)"
```

---

### Task 3: `rtunk renovate config` command

**Files:**

- Modify: `internal/cli/renovate.go` (add `renovateConfigCmd`)
- Test: `internal/cli/renovate_test.go` (add cases)

**Interfaces:**

- Consumes: nothing new.
- Produces: `renovateConfigSnippet` (const string), printed verbatim by `renovateConfigCmd.Run`.

The design spec flagged its own proposed `matchStrings` regex as unverified. It has since been
verified directly (in real Node.js, the engine Renovate actually runs, against literal output
matching both annotated shapes Task 2 produces) and is **confirmed correct** — use it verbatim
below; no further empirical verification is required for this task, only the exact-string test.

- [ ] **Step 1: Write the failing test**

Add to `internal/cli/renovate_test.go`:

```go
func TestRenovateConfig_PrintsSnippet(t *testing.T) {
	stdout, stderr, err := run2(t, "renovate", "config")
	require.NoError(t, err, "stderr: %s", stderr)
	assert.Equal(t, renovateConfigSnippet, stdout)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/... -run TestRenovateConfig -v`
Expected: FAIL — `renovate config` isn't a registered command yet, `renovateConfigSnippet`
undefined.

- [ ] **Step 3: Implement `renovateConfigCmd`**

Add to `internal/cli/renovate.go` (the `renovateCmd` struct already declares `Config
renovateConfigCmd` from Task 2's Step 3 — this step only adds the command's own type and the
const it prints):

```go
// renovateConfigSnippet is the static regexManagers block to add to the user's own
// renovate.json5 -- static because it matches the generic "# renovate: ..." comment shape Task
// 2's annotate command produces, not any specific tool, so it never needs regenerating as
// enabled linters/tools/runtimes change. Verified directly against real annotated output (both
// the flat "- id@version" sequence-entry shape and the "ref: <value>" mapping-entry shape) in
// Node.js (the engine Renovate actually runs), not just eyeballed.
const renovateConfigSnippet = `{
  "regexManagers": [
    {
      "fileMatch": ["(^|/)\\.trunk/trunk\\.yaml$", "(^|/)\\.rtunk/rtunk\\.yaml$"],
      "matchStrings": [
        "# renovate: datasource=(?<datasource>\\S+) depName=(?<depName>\\S+)\\s*\\n\\s*(?:-\\s*\\S+@|ref:\\s*)(?<currentValue>\\S+)"
      ]
    }
  ]
}
`

type renovateConfigCmd struct{}

func (c *renovateConfigCmd) Run(stdout io.Writer) error {
	_, err := io.WriteString(stdout, renovateConfigSnippet)
	return err
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cli/... -run TestRenovateConfig -v`
Expected: PASS.

- [ ] **Step 5: Run the full package test suite**

Run: `go test ./internal/cli/... -v`
Expected: PASS, zero regressions.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/renovate.go internal/cli/renovate_test.go
git commit -S -m "$(cat <<'EOF'
+[renovate]: Add rtunk renovate config

Prints the static regexManagers snippet a user pastes into their own
renovate.json5 -- verified in Node.js against both real annotated shapes
(the flat id@version sequence entry and the plugin source ref: mapping
entry) rtunk renovate annotate produces, not just designed on paper.
EOF
)"
```

---

### Task 4: `editEnabled` fix — keep annotations alive across `check enable`/`disable`

**Files:**

- Modify: `internal/cli/check.go` (the `editEnabled` function)
- Test: `internal/cli/check_enable_test.go` (add cases)

**Interfaces:**

- Consumes: `renovate.ForLint` (Task 1), `resolveConfig` (existing), `cutVersion` (existing).
- Produces: nothing new — this is a behavior change inside an existing function's existing
  signature (`editEnabled(cli *CLI, category string, edit func([]string) []string) error` is
  unchanged).

First, read `internal/cli/check.go`'s current `editEnabled` in full (it was last touched before
this plan's Task 1-3 work; re-read it now rather than trusting a stale line-number memory) to
confirm the exact current line range and surrounding code before editing.

- [ ] **Step 1: Write the failing tests**

Add to `internal/cli/check_enable_test.go`:

```go
func TestCheckEnableCmd_NoAnnotations_BehaviorUnchanged(t *testing.T) {
	// Same fixture/assertion as TestCheckEnableCmd_AddsAndPreservesComments -- no # renovate:
	// comment anywhere means the new annotation-aware branch in editEnabled must never trigger.
	// This is this plan's own proof the fix is genuinely opt-in.
	path := writeScratchTrunkYAML(t, "version: \"0.1\"\n# a leading comment, must survive\nlint:\n  enabled: []\n")

	_, stderr, err := run2(t, "--config", path, "check", "enable", "shellcheck")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(got), "# a leading comment, must survive")
	assert.Contains(t, string(got), "shellcheck")
	assert.NotContains(t, string(got), "# renovate:")
}

func TestCheckEnableCmd_AnnotatedSurvivor_KeepsExactComment(t *testing.T) {
	cfgPath, _ := writeToolLinterFixture(t, []string{"fixture@1.0.0"}, "fixture", "acme", "widget", "1.2.3")
	_, stderr, err := run2(t, "--config", cfgPath, "renovate", "annotate")
	require.NoError(t, err, "stderr: %s", stderr)
	before, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	require.Contains(t, string(before), "# renovate: datasource=github-releases depName=acme/widget")

	// An unrelated enable of a second, unresolvable id must not disturb fixture's own comment.
	_, stderr, err = run2(t, "--config", cfgPath, "check", "enable", "unrelated")
	require.NoError(t, err, "stderr: %s", stderr)

	after, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Contains(t, string(after), "# renovate: datasource=github-releases depName=acme/widget\n    - fixture@1.0.0\n")
}

func TestCheckEnableCmd_NewEntryInAnnotatedCategory_GetsFreshComment(t *testing.T) {
	cfgPath, repoRoot := writeToolLinterFixture(t, []string{"fixture"}, "fixture", "acme", "widget", "1.2.3")
	_, stderr, err := run2(t, "--config", cfgPath, "renovate", "annotate")
	require.NoError(t, err, "stderr: %s", stderr)

	// Add a second, independently-resolvable tool+linter to the same fixture repo before
	// enabling it, so editEnabled's own config.ResolveAll (triggered by the existing annotation
	// on "fixture") can actually resolve it too.
	secondPluginYAML := `downloads:
  - name: second-download
    version: 1.0.0
    downloads:
      - os: { linux: linux, macos: macos, windows: windows }
        cpu: { x86_64: x86_64, arm_64: arm_64 }
        url: https://github.com/other/second/releases/download/v${version}/second.tar.gz
tools:
  definitions:
    - name: second
      download: second-download
      known_good_version: 4.5.6
lint:
  definitions:
    - name: second
      files: [ALL]
      tools: [second]
      description: second fixture linter
      commands:
        - name: lint
          run: echo unused
          output: xml
`
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "pluginrepo", "linters", "second"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "pluginrepo", "linters", "second", "plugin.yaml"), []byte(secondPluginYAML), 0o644))

	_, stderr, err = run2(t, "--config", cfgPath, "check", "enable", "second")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Contains(t, string(got), "# renovate: datasource=github-releases depName=other/second\n    - second@4.5.6\n")
}

func TestCheckEnableCmd_UnresolvableNewEntryInAnnotatedCategory_NoCommentNoForcedPin(t *testing.T) {
	cfgPath, _ := writeToolLinterFixture(t, []string{"fixture"}, "fixture", "acme", "widget", "1.2.3")
	_, stderr, err := run2(t, "--config", cfgPath, "renovate", "annotate")
	require.NoError(t, err, "stderr: %s", stderr)

	_, stderr, err = run2(t, "--config", cfgPath, "check", "enable", "phantom")
	require.NoError(t, err, "stderr: %s", stderr)

	got, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Contains(t, string(got), "phantom\n")
	assert.NotContains(t, string(got), "phantom@")
	assert.NotContains(t, string(got), "depName=") // no annotation for phantom specifically
}
```

`writeToolLinterFixture` is Task 2's own helper (`internal/cli/renovate_test.go`) — reused here
unmodified since both files are `package cli`.

Add `"path/filepath"` to this file's imports if not already present (needed by
`TestCheckEnableCmd_NewEntryInAnnotatedCategory_GetsFreshComment`'s `filepath.Join` call) — check
the existing import block first, this file already imports `"os"`/`"os/exec"`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/... -run TestCheckEnableCmd -v`
Expected: `TestCheckEnableCmd_NoAnnotations_BehaviorUnchanged` PASSes already (nothing to fix for
that path). The three annotation-aware tests FAIL — `editEnabled` doesn't look at `HeadComment`
at all yet, so no comment ever appears.

- [ ] **Step 3: Implement the fix**

In `internal/cli/check.go`, modify `editEnabled`. Locate its current body (rebuild loop currently
reads roughly as shown below — re-confirm against the real current file before editing, since
exact surrounding line numbers may have shifted since this plan was written):

```go
func editEnabled(cli *CLI, category string, edit func([]string) []string) error {
	configPath := cli.Config
	if configPath == "" {
		found, err := findTrunkYAML()
		if err != nil {
			return err
		}
		configPath = found
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return err
	}
	if len(doc.Content) == 0 {
		doc.Kind = yaml.DocumentNode
		doc.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
	}
	root := doc.Content[0]

	catNode := findOrCreateMapKey(root, category)
	enabledNode := findOrCreateMapKey(catNode, "enabled")
	if enabledNode.Kind != yaml.SequenceNode {
		enabledNode.Kind = yaml.SequenceNode
		enabledNode.Tag = "!!seq"
		enabledNode.Content = nil
	}

	existing := make([]string, len(enabledNode.Content))
	for i, n := range enabledNode.Content {
		existing[i] = n.Value
	}

	updated := edit(existing)

	enabledNode.Content = make([]*yaml.Node, len(updated))
	for i, v := range updated {
		enabledNode.Content[i] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(detectIndentWidth(data))
	if err := enc.Encode(&doc); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	return os.WriteFile(configPath, buf.Bytes(), 0o644)
}
```

Replace the block from `existing := make([]string, len(enabledNode.Content))` through
`enabledNode.Content = make([]*yaml.Node, len(updated))` / the fill loop with:

```go
	existing := make([]string, len(enabledNode.Content))
	priorComments := map[string]string{} // bare id -> its exact HeadComment, only populated below
	hadAnnotations := false
	for i, n := range enabledNode.Content {
		existing[i] = n.Value
		if trimmed := strings.TrimSpace(n.HeadComment); strings.HasPrefix(trimmed, "# renovate:") {
			hadAnnotations = true
			bareID, _, _ := cutVersion(n.Value)
			priorComments[bareID] = n.HeadComment
		}
	}

	updated := edit(existing)

	var cfg config.Config
	if hadAnnotations && category == "lint" {
		cfg, err = resolveConfig(configPath, cli.CacheDir, true)
		if err != nil {
			return err
		}
	}

	enabledNode.Content = make([]*yaml.Node, len(updated))
	for i, v := range updated {
		node := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
		if hadAnnotations {
			bareID, _, pinned := cutVersion(v)
			if comment, ok := priorComments[bareID]; ok {
				node.HeadComment = comment
			} else if category == "lint" {
				if ann, knownGoodVersion, ok := renovate.ForLint(cfg, bareID); ok {
					if !pinned && knownGoodVersion != "" {
						node.Value = bareID + "@" + knownGoodVersion
					}
					node.HeadComment = "# renovate: datasource=" + ann.Datasource + " depName=" + ann.DepName
				}
			}
		}
		enabledNode.Content[i] = node
	}
```

This directly implements the plan's own algorithm: a category with no prior `# renovate:` comment
never sets `hadAnnotations`, so every new node has an empty `HeadComment` (byte-identical to
today's behavior — no `config.ResolveAll` call, no new failure mode, no change to any existing
test). A category that does carry at least one keeps a survivor's exact comment verbatim, and
resolves a fresh one (with the same default-pin rule Task 2's `annotateEnabledSeq` uses) for any
bare id that's new to the list or was never annotated despite the category being active.

`internal/cli/check.go` already imports `"strings"`, `"bytes"`, `"gopkg.in/yaml.v3"`, and
`"github.com/xunleii/rtunk/pkg/trunk/config"` (confirmed by reading the file's current import
block) — the only genuinely new import this fix needs is:

```go
	"github.com/xunleii/rtunk/pkg/trunk/renovate"
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/cli/... -run 'TestCheckEnableCmd|TestCheckDisableCmd' -v`
Expected: PASS for every test in `check_enable_test.go` — both the pre-existing ones (unchanged,
proving the fix is genuinely opt-in) and the 4 new ones from Step 1.

- [ ] **Step 5: Run the full test suite**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: PASS, zero regressions across every package.

- [ ] **Step 6: Add the `renovate` commitlint scope**

In `.agents/skills/git-commit/SKILL.md`, add a row to the scope table (currently between the
`upgrade`/`init` and `cli` rows, matching this repo's existing one-scope-per-CLI-subsystem
ordering — insert it alphabetically-ish near its siblings, exact position is a judgment call, not
load-bearing):

```markdown
| `renovate` | Renovate annotation generation (`rtunk renovate`) |
```

- [ ] **Step 7: Commit**

```bash
git add internal/cli/check.go internal/cli/check_enable_test.go .agents/skills/git-commit/SKILL.md
git commit -S -m "$(cat <<'EOF'
+[renovate]: Keep Renovate annotations alive across check enable/disable

editEnabled previously rebuilt its whole enabled: sequence as bare
scalar nodes on every call, silently dropping any "# renovate: ..."
comment renovate annotate had written. Now opt-in: a category with no
prior annotation behaves identically to before (proven by the existing
test suite passing unmodified); one with at least one annotation keeps
survivors' exact comments and resolves a fresh one for any newly
enabled or previously-unannotated entry, via the same pkg/trunk/renovate
resolution renovate annotate itself uses.

Also adds the `renovate` commitlint scope this feature's own commits use.
EOF
)"
```

## Self-Review Notes

- **Spec coverage:** every "Design" subsection of the spec maps to a task —
  `pkg/trunk/renovate` (Task 1), `rtunk renovate annotate` (Task 2), `rtunk renovate config`
  (Task 3), the `editEnabled` fix (Task 4). The spec's own two empirically-flagged risks (the
  `matchStrings` regex needing verification against real output, and — discovered while writing
  this plan, not in the original spec text — the `HeadComment`-on-value-vs-key-node behavior for
  mapping entries) were both resolved by direct experimentation before this plan was written, not
  left for an implementer to discover the hard way; both are now stated as plain facts in the
  Global Constraints section and baked into Task 2/3's code verbatim.
- **Type consistency:** `renovate.Annotation`, `ForLint`/`ForRuntime` (both `(Annotation, string,
bool)`), and `ForPluginSource` (`(Annotation, bool)`) are defined once in Task 1 and consumed
  with the identical shape in Task 2 (`annotateEnabledSeq`'s `resolve func(id string)
(renovate.Annotation, string, bool)` parameter) and Task 4 (`editEnabled`'s own direct call to
  `renovate.ForLint`) — no drift between the three consumption sites.
- **No placeholders:** every code block is complete, runnable Go. Task 4's instruction to
  "re-confirm against the real current file before editing" is a legitimate TDD/refactor-safety
  step (the function is being modified, not written from scratch, and this plan's own earlier
  tasks already touch other parts of `internal/cli`), not an unspecified requirement — the exact
  before/after code for the edit itself is given in full.
