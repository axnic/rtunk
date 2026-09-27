# Plugin object model

How the objects a plugin repository declares are shaped and how they reference each other — the
domain model of the _configuration vocabulary itself_ (plugin.yaml and trunk.yaml), independent of
any component that reads it. This is the schema a redesign's config resolver has to keep
faithfully representable; see [README.md](./README.md) for the components that consume it and
[sources.md](./sources.md) for the resolution/fetch architecture built on top.

The field survey below is based on the trunk-io/plugins catalog at commit
`d1e3af5752059371e206fe8f242b15d7f4205555` (208 plugin.yaml files).

## Plugin repositories, sources, and the catalog

A **plugin repository** is a directory tree with one subdirectory per category — linters, tools,
runtimes, actions — each holding one file per resource, plus category-root and repository-root
files that carry declarations with no enabled/disabled id of their own:

- The **repository root** file declares repository-wide metadata (a minimum required tool version)
  and **global environment tables** (`environments:`, a list of named environment-entry groups) —
  always in effect, not selected by anything in trunk.yaml.
- A **category root** file (for example, the linters category's own root) declares that category's
  shared vocabulary: named **comment formats** (the leading/trailing delimiter pairs used to
  recognize inline ignore directives) and the shared **file-type registry** entries every linter's
  `files:` list references by name.
- Every other file contributes **resource definitions**: one or more linters, tools, runtimes, or
  actions, plus any **download recipes** those definitions need.

trunk.yaml's `plugins.sources` list names one or more plugin repositories to pull from — a local
path, or a remote git repository pinned to a tag or commit. Every source's contributed definitions
merge into one shared, id-keyed catalog per category. **trunk.yaml never declares a linter, tool,
runtime, or action itself** — it only _enables_ an id a source's catalog already declares, via
`lint.enabled` / `runtimes.enabled` / `actions.enabled` (+ `actions.disabled`), each entry optionally
pinning a version with `id@version`. Enabling `id@version` changes which version is resolved for
that id's own downloads; it does not choose among that id's several command variants (see
"Commands" below) — nothing in trunk.yaml does.

**Merge/override**: two sources declaring the same id in the same category is a same-namespace
collision — the catalog is flat and id-keyed per category, so the later-merged source's definition
replaces the earlier one for that id. A real, current example of an intentional same-linter
collision within _one_ source's own catalog (not across sources) is a **deprecated alias id**: an
older linter id kept only so an existing trunk.yaml that still enables it keeps resolving to
_something_, carrying a human-readable `deprecated:` migration message pointing at the id that
superseded it (for example, a bare formatter id superseded by that same tool's fuller, combined
lint+format linter id).

## The class diagram

```mermaid
classDiagram
    class PluginRepository {
        root metadata (min. required version)
        global environment tables
        category-root vocabulary (comment formats, file types)
    }
    class TrunkYAML {
        plugins.sources[]
        lint.enabled[] "id@version"
        runtimes.enabled[] "id@version"
        actions.enabled[] / actions.disabled[]
    }
    class DownloadRecipe {
        name
        per OS/CPU download entries (each optionally version-ranged)
        derived template args (regex-extracted from ${version})
    }
    class ToolDefinition {
        name
        exposed shim names
        known-good/pinned version
        health checks (ignored today)
    }
    class RuntimeDefinition {
        kind (go, node, python, php, rust, ruby, ...)
        shim names
        runtime-environment table
        linter-environment table (added only for tools depending on this runtime)
    }
    class LinterDefinition {
        name
        matched file types
        tool references
        one or more CommandDefinitions
        config-file-presence triggers (direct_configs)
        cache-invalidating file triggers (affects_cache)
    }
    class CommandDefinition {
        invocation template
        output format (+ optional output parser)
        formatter flag
        in-place flag
        batch flag
        run-from location
        supported version range
        platform restriction
        fix prompt / fix verb (quick-fix UX text)
        security-category flag
        supersedes-upstream flag
    }
    class OutputParser {
        runtime kind
        conversion script template
    }
    class ActionDefinition {
        id
        runtime kind (optional)
        invocation template
        one or more Triggers
        interactivity requirement
        notify-on-error flag
    }
    class Trigger {
        git hook names, or
        file-change globs, or
        periodic schedule
    }
    class FileTypeDefinition {
        name
        extensions / filenames / regexes / shebangs
        required YAML keys (disambiguates a YAML dialect)
        comment formats it uses
        inherited file types
    }
    class EnabledEntry["Enabled Entry (trunk.yaml)"] {
        id
        pinned version (optional)
    }

    PluginRepository "1" --> "*" LinterDefinition
    PluginRepository "1" --> "*" ToolDefinition
    PluginRepository "1" --> "*" RuntimeDefinition
    PluginRepository "1" --> "*" ActionDefinition
    PluginRepository "1" --> "*" DownloadRecipe
    PluginRepository "1" --> "*" FileTypeDefinition

    TrunkYAML "1" --> "*" PluginRepository : plugins.sources
    TrunkYAML "1" --> "*" EnabledEntry
    EnabledEntry "*" --> "1" LinterDefinition : selects by id (or Runtime/Action)

    LinterDefinition "1" --> "*" CommandDefinition
    LinterDefinition "*" --> "*" ToolDefinition : invokes, by name
    LinterDefinition "*" --> "*" FileTypeDefinition : matches, by name
    LinterDefinition "0..1" --> "1" DownloadRecipe : inline tool binding (shorthand, no separate ToolDefinition)
    LinterDefinition "0..1" --> "1" RuntimeDefinition : inline tool binding (shorthand)
    CommandDefinition "0..1" --> "1" OutputParser
    ToolDefinition "0..1" --> "1" RuntimeDefinition : runs under, by name (XOR with Download)
    ToolDefinition "0..1" --> "1" DownloadRecipe : fetched via, by name (XOR with Runtime)
    RuntimeDefinition "0..1" --> "1" DownloadRecipe : fetched via, or "expect present on host"
    ActionDefinition "1" --> "*" Trigger
    ActionDefinition "0..1" --> "1" RuntimeDefinition : runs under
    FileTypeDefinition "*" --> "*" FileTypeDefinition : inherits
```

### The inline tool-binding shorthand

A linter definition normally names one or more tools by id (`tools: [...]`), each a separate
`ToolDefinition` naming a runtime+package or a download recipe. A common shorthand in the real
catalog skips that indirection: the linter definition itself carries a `download:` and/or
`runtime:`/`package:` field directly, binding to a download recipe or runtime without a
`ToolDefinition` in between at all. Both forms coexist across the catalog for different linters —
neither supersedes the other — so a redesign's object model has to represent a linter's tool
binding as _either_ a reference to one or more named `ToolDefinition`s _or_ an inline binding of the
same shape a `ToolDefinition` would otherwise carry, not just the reference form.

### The legacy single-command shape

A small number of real, currently-declared linter ids (all carrying a `deprecated:` migration
message pointing at their replacement) use an older shape that predates the `commands: [...]` list:
a single `type`/`command` pair directly on the linter definition instead of one or more
`CommandDefinition` entries. This shape is not merely historical — it is live in the current
catalog on deprecated-but-still-enableable ids, so an existing trunk.yaml that still enables one of
them is a real input a redesign's model has to account for, either by modeling the shape or by
treating it as a resolvable-to-empty legacy id with a surfaced warning. See
[inconsistencies.md](./inconsistencies.md) for what happens today when neither is done.

## Field-category table (conceptual, not exhaustive)

| Object               | Identity | Execution                                                   | Output                                                 | Gating                                                                                                    | Environment                                     |
| -------------------- | -------- | ----------------------------------------------------------- | ------------------------------------------------------ | --------------------------------------------------------------------------------------------------------- | ----------------------------------------------- |
| `LinterDefinition`   | name     | tool references, run-from context                           | —                                                      | matched file types, `direct_configs` (config-presence trigger), `suggest_if` (when to recommend enabling) | linter-scoped environment entries               |
| `CommandDefinition`  | name     | invocation template, batch flag, sandbox mode, target shape | output format, output parser, formatter/in-place flags | version range, platform restriction, enabled flag, success/error exit codes                               | —                                               |
| `ToolDefinition`     | name     | exposed shim names                                          | —                                                      | known-good/pinned version                                                                                 | health checks (declared, unused today)          |
| `RuntimeDefinition`  | kind     | package-install mechanism (by kind)                         | —                                                      | known-good version, "expect present on host"                                                              | runtime-environment + linter-environment tables |
| `ActionDefinition`   | id       | invocation template, runtime reference                      | —                                                      | triggers (hook/file-change/schedule), interactivity requirement                                           | action-scoped environment entries               |
| `DownloadRecipe`     | name     | —                                                           | per-OS/CPU URL templates                               | per-entry version range                                                                                   | —                                               |
| `FileTypeDefinition` | name     | —                                                           | —                                                      | extension/filename/regex/shebang/required-key matchers, inheritance                                       | —                                               |

## Declared fields the current execution engine does not act on

The catalog survey below (counts are occurrences across the full real plugin catalog, not just
distinct ids) groups every field found on `CommandDefinition`/`LinterDefinition`/`ToolDefinition`/
`ActionDefinition` that a redesign's object model should represent, but that today's engine parses
and then never consults, or never parses at all because the corresponding struct has no field for
it. Full detail, evidence, and severity for each is in
[inconsistencies.md](./inconsistencies.md#declared-but-inert-catalog-fields); this is the field-level
index into that.

| Field                       | Declared on | Catalog occurrences | Status today                        |
| --------------------------- | ----------- | ------------------- | ----------------------------------- |
| `suggest_if`                | linter      | 129                 | parsed, never consulted             |
| `run_timeout`               | linter      | 8                   | parsed, never consulted             |
| `cache_results`             | linter      | 4                   | parsed, never consulted             |
| `cache_results`             | command     | 71                  | not modeled (dropped while parsing) |
| `supported_platforms`       | linter      | 9                   | parsed, never consulted             |
| `platforms`                 | command     | 15                  | modeled, consulted (v0.10)          |
| `version` (supported range) | command     | 20                  | parsed, consulted (v0.10)           |
| `is_security`               | command     | 22                  | not modeled (dropped while parsing) |
| `disable_upstream`          | command     | 13                  | not modeled (dropped while parsing) |
| `fix_prompt` / `fix_verb`   | command     | 3 / 3               | parsed, carried through resolution (v0.10); no consumer yet |
| `prepare_run`               | command     | 2                   | not modeled (dropped while parsing) |
| `stdin`                     | command     | 7                   | not modeled (dropped while parsing); its real instances all pair with `formatter: true`/no `in_place`, a shape now run unconditionally piping stdin (v0.10, entry 5) regardless of this field's own value |
| `max_concurrency`           | command     | 4                   | not modeled (dropped while parsing) |
| `health_checks`             | tool        | 20                  | not modeled (dropped while parsing) |
| `extra_packages`            | tool        | 6                   | not modeled (dropped while parsing) |
| `output_type`               | action      | 2                   | not modeled (dropped while parsing) |
