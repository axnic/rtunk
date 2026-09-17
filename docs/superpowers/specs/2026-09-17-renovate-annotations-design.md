# Renovate Annotations: Design

**Status:** approved by the user through live discussion (a spike on self-built version-checking
complexity, followed by an architectural design walked through section by section — this document
formalizes what was agreed).

## Goal

rtunk deliberately does not check or apply upstream version updates for linters/tools/runtimes
itself (a scope ruling made during this design's discussion, see "Why not build this ourselves"
below) — that job is delegated to [Renovate](https://docs.renovatebot.com/). This feature gives
rtunk a way to make `trunk.yaml`/`rtunk.yaml` legible to Renovate: it writes
[regex-manager](https://docs.renovatebot.com/modules/manager/regex/) annotation comments above
every version-pinned entry rtunk can confidently identify a datasource for, and prints the small,
static Renovate config snippet the user pastes into their own `renovate.json5` to activate it.

## Why not build this ourselves

Real trunk's `upgrade` command checks/applies new versions across `check`/`plugins`/`runtimes`/
`tools`/`cli` scopes. Investigated whether rtunk should grow an equivalent, self-built
"check latest version per tool" mechanism. Findings, from the real `trunk-io/plugins@v1.11.0`
catalog cached on this machine:

- Of 201 tool definitions, 60 install via a `Runtime`+`Package` pair (node/python/php/go/rust —
  a small, closed set mapping cleanly to package-registry datasources) and ~141 via a `Download`
  recipe. Of the ~141, most (77 sampled) point at `github.com` release assets, but a real long
  tail points at bespoke, non-API hosts: `download.visualstudio.microsoft.com`,
  `cache.agilebits.com` (1Password), an AWS S3 bucket, `releases.hashicorp.com`,
  `get.helm.sh`, `dl.k8s.io`.
- Runtimes (7 total) are similarly split: some on `github.com`, others on `nodejs.org`,
  `golang.org`, `static.rust-lang.org`, `cdn.azul.com` (Azul's own Java CDN) — each its own
  bespoke release mechanism.

Building and maintaining a "latest version" prober per one of these ~10 distinct, ungrowing-set
hosting mechanisms is exactly the job Renovate already does, with maintained datasource support
for nearly all of them. Ruling: rtunk never queries any upstream for "is there a newer version" —
not for tools/linters/runtimes, and not for `plugins.sources[].ref` either (also always a git
repo, so also just as directly Renovate-annotatable via `github-tags`). rtunk's own job stops at
generating annotations Renovate can act on.

## Ground truth: how a `lint.enabled`/`runtimes.enabled` entry maps to a version source

- A `lint.enabled` entry's id is a **Linter** id (`config.Linter`, keyed in
  `cfg.Lint.Definitions`). A `Linter` does not carry its own download recipe — it references one
  or more **Tool**s by name via `Linter.Tools []string` (real catalog example:
  `linters/golangci-lint/plugin.yaml`'s `lint.definitions[].tools: [golangci-lint]`, pointing at
  a same-name entry under that same file's own `tools.definitions[]`). The tool actually carries
  `KnownGoodVersion` and either `Download` (a recipe name into `cfg.Downloads`) or `Runtime`+
  `Package` (an ecosystem + package identifier).
- A `runtimes.enabled` entry's id is a **Runtime** id (`config.Runtime`, keyed in
  `cfg.Runtimes.Definitions`) — no bridging needed, it carries its own `Download`/
  `KnownGoodVersion` directly (a Runtime cannot itself be `Runtime`+`Package`-installed; nothing
  installs a runtime via another runtime).
- **Actions have no version-pinning concept in rtunk at all**: `config.Action` (definitions.go)
  has neither `KnownGoodVersion` nor `Download`/`Runtime`+`Package` fields, and
  `internal/cli/config.go`'s own `resolvedVersionFor` already returns `""` (no fallback) for the
  `"actions"` category. This isn't a design gap to route around — there is structurally nothing
  to annotate on an Action. Actions are out of scope for this feature.

## Design

### `pkg/trunk/renovate` (new package, pure resolution logic, no I/O)

```go
package renovate

// Annotation is a resolved Renovate regex-manager target: the datasource and dependency name
// Renovate needs to look up and bump a version on its own.
type Annotation struct {
	Datasource string
	DepName    string
}

// ForLint resolves the Renovate annotation for a lint.enabled entry's bare id (no @version),
// by bridging through the Linter's own Tools[] to the single Tool it references. Returns the
// tool's KnownGoodVersion (the default-pin fallback, see below) alongside the Annotation.
// ok is false -- annotation omitted, never guessed -- when: the linter id doesn't exist, the
// linter references zero or more than one tool (ambiguous: which tool's version would this even
// be pinning), or that tool's own source doesn't resolve via the rules below.
func ForLint(cfg config.Config, id string) (ann Annotation, knownGoodVersion string, ok bool)

// ForRuntime is ForLint's runtimes.enabled equivalent. A Runtime carries its own Download
// recipe directly (no Tools[] bridge, and never Runtime+Package -- nothing installs a runtime
// via another runtime).
func ForRuntime(cfg config.Config, id string) (ann Annotation, knownGoodVersion string, ok bool)

// ForPluginSource resolves a plugins.sources[] entry's own git ref. Always datasource
// "github-tags" -- rtunk's plugin sources are always git repos. ok is false only for a Local
// source (config.PluginSource.Local set), which has no upstream ref to track.
func ForPluginSource(src config.PluginSource) (ann Annotation, ok bool)
```

**Resolving a Tool's own source** (shared by `ForLint`'s bridged Tool and directly by
`ForRuntime`'s Runtime, since both `Tool` and `Runtime` carry the same `Download string` /
`KnownGoodVersion string` field shapes):

1. **`Download` recipe set** (`tool.Download`/`runtime.Download` names an entry in
   `cfg.Downloads`): resolve `cfg.Downloads[name].Downloads []DownloadEntry`. Extract the
   `(owner, repo)` pair from every entry's `URL` field via `^https://github\.com/([^/]+)/([^/]+)/`.
   `ok` only if the regex matches **every** entry and every match agrees on the same
   `(owner, repo)` pair (a recipe with entries split across hosts, or that doesn't match at all,
   is not confidently GitHub — skip, never guess which entry is authoritative).
   `Annotation{Datasource: "github-releases", DepName: owner + "/" + repo}`.
2. **`Runtime`+`Package` set** (Tool only — `tool.Runtime`/`tool.Package`, e.g.
   `{runtime: go, package: "github.com/golangci/golangci-lint/cmd/golangci-lint"}`): map
   `tool.Runtime` through a small, closed table —

   | `Runtime` value | Renovate `datasource` |
   | --------------- | --------------------- |
   | `node`          | `npm`                 |
   | `python`        | `pypi`                |
   | `php`           | `packagist`           |
   | `go`            | `go`                  |
   | `rust`          | `crate`               |

   `ok` only if `tool.Runtime` is a key in this table (an unrecognized ecosystem is skipped, not
   guessed at). `DepName` is `tool.Package` **verbatim** — no attempt to trim a Go subpackage
   path down to its true module root, or similarly "clean up" an npm/pypi identifier; Renovate's
   own datasource resolution is expected to handle whatever shape the catalog already uses here,
   consistent with "Renovate does the actual version-source work, rtunk only names the target."

3. Neither set, or the tool/runtime id doesn't exist: not `ok`.

### Datasource summary

| Source                                                                                                         | Renovate `datasource`                 | Condition                                                           |
| -------------------------------------------------------------------------------------------------------------- | ------------------------------------- | ------------------------------------------------------------------- |
| `plugins.sources[].ref`                                                                                        | `github-tags`                         | always, unless `Local` (skip)                                       |
| Tool (bridged from Linter) / Runtime, `Download` recipe                                                        | `github-releases`                     | every `DownloadEntry.URL` agrees on one `github.com/<owner>/<repo>` |
| Tool (bridged from Linter), `Runtime`+`Package`                                                                | `npm`/`pypi`/`packagist`/`go`/`crate` | `Runtime` value is one of the 5 above                               |
| Anything else (non-GitHub `Download` host, unrecognized `Runtime`, ambiguous/missing `Tools[]` bridge, Action) | —                                     | not annotated                                                       |

### Default version-pin rule

An entry (`lint.enabled`/`runtimes.enabled`) that resolves to `ok` via the table above, but has
no explicit `@version` pin, gets one written: `id` → `id@<knownGoodVersion>` (the value `ForLint`/
`ForRuntime` returned alongside the `Annotation`). Renovate's regex manager needs a literal
version string in the file to capture and later replace — an unpinned entry has nothing for it to
act on. If the resolved tool/runtime has no `KnownGoodVersion` of its own either (real catalog
data: always present in every case sampled during this design, but not schema-guaranteed), the
entry is left unpinned and unannotated rather than writing an empty/synthetic version.

### `rtunk renovate annotate` (new, `internal/cli/renovate.go`)

Resolves the config file via `findTrunkYAML()`/`--config`, parses the same `*yaml.Node` tree
`editEnabled` already uses (`internal/cli/check.go`), and `config.ResolveAll(configPath,
cli.CacheDir)` (the same "full catalog regardless of current enabled state" resolution
`checkListCmd` already uses — needed here because annotating an entry requires its definition to
exist in the catalog, independent of whether it's currently enabled). For each of
`lint.enabled`, `runtimes.enabled` (both flat `id`/`id@version` sequences), and
`plugins.sources[]` (a sequence of `{id, uri, ref}` mappings): resolves each entry's Annotation
via the functions above; where `ok`, sets that scalar node's `HeadComment` to
`"# renovate: datasource=<Datasource> depName=<DepName>"` (a fresh, from-scratch rebuild of every
comment — this command is the "full resync" path, unlike `editEnabled`'s more surgical fix
below) and, per the default-pin rule, rewrites an unpinned `id` to `id@<knownGoodVersion>`; where
not `ok`, leaves the entry as-is with no comment. Reports (stdout) a summary: how many entries
were annotated, how many were left unannotated and why (grouped by reason: no datasource match,
ambiguous tool bridge, local plugin source), mirroring this codebase's existing
`printFmtReport`-style "what happened" summaries.

### `rtunk renovate config` (new, same file)

Prints a `const` Go string to stdout — the static, generic `regexManagers` snippet — and touches
no file. This snippet never changes based on which linters/tools/runtimes are enabled (it matches
the generic `# renovate: ...` comment shape, not any specific tool), so it is safe to hardcode:

```jsonc
{
  "regexManagers": [
    {
      "fileMatch": ["(^|/)\\.trunk/trunk\\.yaml$", "(^|/)\\.rtunk/rtunk\\.yaml$"],
      "matchStrings": [
        "# renovate: datasource=(?<datasource>\\S+) depName=(?<depName>\\S+)\\s*\\n\\s*-?\\s*\\S*?@?(?<currentValue>\\S+)?\\s*(ref:\\s*)?(?<currentValue2>\\S+)?",
      ],
    },
  ],
}
```

(The implementer must verify this `matchStrings` regex actually captures `currentValue` correctly
against both annotated shapes rtunk produces — a flat `- id@version` sequence entry and a
`ref: <value>` mapping entry — using real annotated fixture output from `renovate annotate`, not
just eyeballing the regex. If one regex can't cleanly cover both shapes, two separate
`regexManagers` entries in the same printed snippet is an acceptable fallback — correctness of the
printed snippet matters more than its brevity.)

### `editEnabled` fix (`internal/cli/check.go`) — keep annotations alive across `check enable`/`disable`

`editEnabled` currently rebuilds `enabledNode.Content` from scratch as bare scalar nodes on every
call (see its own doc comment — this is why comments were getting silently dropped). Fix, opt-in
and additive — a category that has never used `renovate annotate` sees **zero** behavior change:

1. Before rebuilding, scan the _current_ `enabledNode.Content` for any scalar node whose
   `HeadComment`, trimmed, starts with `"# renovate:"`. If none do, proceed exactly as today (no
   annotation logic runs at all — this is the unchanged path for every existing test and every
   user who has never touched this feature).
2. If at least one does, capture a `map[bareID]headComment` from every such node (bare id via the
   existing `cutVersion` helper), and resolve `config.ResolveAll(configPath, cli.CacheDir)` once
   (only reached in this branch — no extra config resolution cost for the common case).
3. Rebuild `updated`'s scalar nodes as today, but for each:
   - bare id found in the captured map → reuse that exact `HeadComment` string verbatim (a
     survivor's datasource/depName never changes just because its version pin did).
   - bare id not found (newly added by this edit, or pre-existing but never annotated even
     though the category is renovate-active — e.g. a hand-added entry) → resolve via
     `renovate.ForLint` (category is always `"lint"` here — `editEnabled` is only ever called
     with that category today; a future caller with a different category simply gets no
     annotation logic, since only `"lint"` is wired to a resolver) using the `config.ResolveAll`
     result from step 2. If `ok`, set the `HeadComment` and apply the default-pin rule from
     above when the entry has no explicit `@version`. If not `ok`, leave uncommented.
4. Encode and write, same as today.

This directly implements "si on a des commentaires renovate, on fait edit + renovate annotate
dans la même boucle" — the loop is `editEnabled`'s own existing rebuild loop, now annotation-aware
when (and only when) the file already opted in.

### CLI wiring (`internal/cli/cli.go`)

```go
RenovateCmd renovateCmd `cmd:"" name:"renovate" help:"Generate Renovate annotations for trunk.yaml's version pins."`
```

```go
type renovateCmd struct {
	Annotate renovateAnnotateCmd `cmd:"" help:"Annotate trunk.yaml's version pins for Renovate."`
	Config   renovateConfigCmd   `cmd:"" help:"Print the Renovate regexManagers config to add."`
}
```

## Testing strategy

- `pkg/trunk/renovate`: table tests per resolution function — `ForLint`/`ForRuntime` against
  synthetic `config.Config` fixtures covering: a `Download`-recipe tool with all-GitHub entries
  (ok), one with a mismatched host across entries (skip), one with a non-GitHub host (skip); each
  of the 5 `Runtime`+`Package` ecosystems (ok) and one unrecognized ecosystem (skip); a `Linter`
  with 0, 1, and 2 `Tools[]` entries; `ForPluginSource` for a git source (ok) and a `Local` source
  (skip).
- `internal/cli/renovate_test.go`: `renovate annotate` end-to-end via `run2` against a real fixture
  repo/plugin catalog (reusing `writeLinterFixture`'s pattern, extended with a `tools:` block and
  a `plugins.sources[]` entry) — assert the exact resulting YAML content (comment placement,
  preserved indentation, the unpinned-entry-gets-known_good_version rewrite) byte-for-byte, not
  just "no error." `renovate config` — assert the exact printed snippet.
- `internal/cli/check_run_test.go` (or a new file): `check enable`/`disable` regression coverage
  for the `editEnabled` fix — a category with no prior annotations behaves identically to before
  (exact byte-for-byte output unchanged from the pre-fix behavior); a category with an existing
  annotated survivor entry keeps its exact comment after an unrelated enable/disable; a newly
  enabled entry in an already-annotated category gets a freshly resolved comment; an unresolvable
  newly-enabled entry (no datasource match) gets no comment and no forced version pin.

## Non-goals (ruled, disclosed)

- No live upstream version-checking anywhere in rtunk, for anything — see "Why not build this
  ourselves."
- Actions are never annotated — no version-pinning concept exists on `config.Action`.
- No support for any dependency-update tool other than Renovate ("pour le moment, uniquement
  Renovate" — the user's own wording; a future tool would need its own `pkg/trunk/<tool>` +
  `rtunk <tool> annotate`/`config` pair, not attempted here).
- No attempt to trim a Go module subpackage path, or otherwise "clean up" a `Runtime`+`Package`
  tool's `Package` value — used verbatim as `DepName`.
- Ambiguous linters (0 or 2+ `Tools[]` entries) are never annotated — no heuristic for picking
  "the" tool among several.
- `editEnabled`'s fix only wires up the `"lint"` category (the only category it's ever called
  with today) — `runtimes.enabled` has no CLI-level enable/disable command at all yet (only
  `renovate annotate`'s own full-file rewrite touches it), so there is no existing edit loop for
  runtimes that could drop a comment in the first place.
