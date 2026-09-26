# trunk config architecture

This document explains how trunk's config and plugin system are architected — the model
`pkg/trunk/config` parses and resolves for `rtunk config {plugins,lint,actions,tools,runtimes}
list`, `... show <id>`, and `rtunk config print` (ROADMAP.md v0.1).

rtunk currently reads only `.trunk/trunk.yaml`.

## Two-layer model

Configuration is split into two kinds of files:

- **`.trunk/trunk.yaml`** — the repo's own config. Only **enables** things by id, optionally
  pinned to a version (`checkov@3.3.16`, `node@22.16.0`). It does not define what a linter runs,
  how a runtime is downloaded, or what an action does.
- **Plugin repositories** — pointed at by `trunk.yaml`'s `plugins.sources` list, these supply the
  actual **definitions** of every linter, tool, runtime, and action available to enable. The
  default/community source is `github.com/trunk-io/plugins`.

Example (this repo's own `.trunk/trunk.yaml`):

```yaml
version: 0.1
cli:
  version: 1.25.0
plugins:
  sources:
    - id: trunk
      ref: v1.11.0
      uri: https://github.com/trunk-io/plugins
runtimes:
  enabled:
    - node@22.16.0
    - python@3.14.4
lint:
  enabled:
    - checkov@3.3.16
    - git-diff-check
    - markdownlint@0.49.1
    - prettier@3.9.6
    - trufflehog@3.97.4
    - yamllint@1.38.0
actions:
  enabled:
    - commitlint
    - trunk-check-pre-push
    - trunk-fmt-pre-commit
    - trunk-upgrade-available
```

### `plugins.sources`

Each entry is either a git source (`{id, uri, ref}`, `ref` always a tag or SHA, never a branch) or
a local filesystem source (`{id, local: <path>}`, used when developing plugins themselves).
trunk-io/plugins' own dogfooding `.trunk/trunk.yaml` uses both in the same file:

```yaml
plugins:
  sources:
    - id: trunk
      local: .
    - id: configs
      uri: https://github.com/trunk-io/configs
      ref: v1.2.1
```

(github.com/trunk-io/plugins, `.trunk/trunk.yaml`)

Multiple sources can coexist, each contributing its own definitions; `id` disambiguates
provenance and is the namespace a plugin repo's contents (`linters/eslint`, and so on) are
resolved under.

## Plugin repository layout

A plugin repository (trunk-io/plugins, or any repo speaking the same dialect) has this top-level
layout (github.com/trunk-io/plugins, repo root):

```
plugin.yaml       # repo-level metadata
config.yml        # present but empty in trunk-io/plugins today
linters/<name>/   # one dir per linter: plugin.yaml (+ optional README.md, *.test.ts, test_data/)
actions/<name>/   # one dir per action group: plugin.yaml
tools/<name>/     # one dir per standalone tool: plugin.yaml
runtimes/<name>/  # one dir per language runtime: plugin.yaml
repo-tools/       # internal tooling for the plugins repo itself; not consumed by trunk/rtunk
```

The root `plugin.yaml` carries repo-level metadata, not category definitions:

```yaml
version: 0.1
required_trunk_version: ">=1.22.2-beta.5"
environments:
  - name: SYSTEM
    environment:
      - name: PATH
        list: ["${env.PATH:-}"]
```

(github.com/trunk-io/plugins, `plugin.yaml`)

rtunk parses `environments:` into `Config.Environments` (`[]NamedEnvironment`). Unlike
`runtimes:`/`lint:`/`actions:` definitions, an environment group has no id `trunk.yaml` enables or
disables — it's global config, always in effect — so it survives `filterEnabled` untouched:
`Resolve` and `ResolveAll` return the same `Config.Environments`.

A category dir's own root `plugin.yaml` (e.g. `linters/plugin.yaml`, a sibling of
`linters/<name>/plugin.yaml`) is the same optional-global-config mechanism, scoped to one
category rather than the whole repo — see `lint.comment_formats:` below.

## Per-category `plugin.yaml` schemas

Each `plugin.yaml` under `linters/`, `actions/`, `tools/`, or `runtimes/` declares one or more of
the top-level keys below. A single file commonly mixes sections — most linters ship their own
`tools:` and `downloads:` alongside `lint:`, since a linter needs a tool to exist before it can be
invoked.

### `downloads:`

Reusable, OS/CPU-templated download recipes, referenced by tools and runtimes via `download:
<name>`.

```yaml
downloads:
  - name: shellcheck
    version: 0.8.0
    downloads:
      - os: { linux: linux }
        cpu: { arm_64: aarch64, x86_64: x86_64 }
        url: https://github.com/koalaman/shellcheck/releases/download/v${version}/shellcheck-v${version}.${os}.${cpu}.tar.xz
        strip_components: 1
      - os: macos
        cpu: x86_64
        url: https://github.com/koalaman/shellcheck/releases/download/v${version}/shellcheck-v${version}.darwin.x86_64.tar.xz
        strip_components: 1
```

(github.com/trunk-io/plugins, `linters/shellcheck/plugin.yaml`)

Fields:

- `name` — id other sections reference via `download: <name>`.
- `version` — default/pinned version substituted into `${version}` below.
- `downloads[].os` / `downloads[].cpu` — either a bare string (no mapping needed) or a map from
  trunk's os/cpu vocabulary (`linux`, `macos`, `windows`, `x86_64`, `arm_64`) to upstream's own
  naming (e.g. `x86_64: aarch64`).
- `downloads[].url` — templated with `${version}`, `${os}`, `${cpu}`.
- `downloads[].strip_components` — archive path components to strip on extract (tar-style).
- `downloads[].executable: true` — the download is a single binary, not an archive.
- `downloads[].version` — gates that entry to a version range (`">=x"` / `"<=y"`), letting one
  logical download evolve its URL scheme over time (seen in hadolint's `<2.13.1` vs `>=2.13.1`
  entries, github.com/trunk-io/plugins, `linters/hadolint/plugin.yaml`).

### `tools:`

A downloadable/runnable tool, fetched one of two ways:

```yaml
# runtime-based (github.com/trunk-io/plugins, linters/eslint/plugin.yaml)
tools:
  definitions:
    - name: eslint
      runtime: node
      package: eslint
      shims: [eslint]
      known_good_version: 8.10.0
```

```yaml
# download-based (github.com/trunk-io/plugins, linters/shellcheck/plugin.yaml)
tools:
  definitions:
    - name: shellcheck
      download: shellcheck
      shims: [shellcheck]
      known_good_version: 0.11.0
```

```yaml
# shim aliasing (github.com/trunk-io/plugins, tools/bazel-differ/plugin.yaml)
tools:
  definitions:
    - name: bazel-differ
      package: github.com/ewhauser/bazel-differ/cli
      runtime: go
      known_good_version: 0.0.5
      shims:
        - name: bazel-differ
          target: cli
```

Fields:

- `name` — id other sections (e.g. `lint.definitions[].tools`) reference.
- `runtime` + `package` — fetches the tool via that runtime's package manager (npm, pip, ...).
  Mutually exclusive with `download`.
- `download` — fetches it via a `downloads:` recipe, for standalone binaries. Mutually exclusive
  with `runtime`/`package`.
- `shims` — executable names the tool exposes on `PATH` once installed. Each entry is either a
  bare string, or a `{name, target}` object aliasing the exposed shim name to a different
  underlying binary (bazel-differ, above, exposes `bazel-differ` on `PATH` but resolves to the
  package's `cli` binary).
- `known_good_version` — default/tested version used when nothing pins a different one.

### `lint:`

A linter definition, tying files, tools, and one or more commands together.

```yaml
lint:
  definitions:
    - name: actionlint
      files: [github-workflow]
      tools: [actionlint]
      description: Verify your Github workflows
      commands:
        - name: lint
          run: actionlint -format "{{json .}}" ${target}
          output: actionlint
          success_codes: [0, 1]
      direct_configs:
        - .github/actionlint.yaml
        - .github/actionlint.yml
      issue_url_format: https://github.com/rhysd/actionlint/blob/main/docs/checks.md
      suggest_if: files_present
      known_good_version: 1.7.8
      version_command: { run: actionlint --version, parse_regex: "${semver}" }
```

(github.com/trunk-io/plugins, `linters/actionlint/plugin.yaml`)

Fields:

- `name` — the linter's id.
- `files` — ids into trunk's file-type registry (see "Built-in / global config" below), not raw
  glob patterns.
- `tools` — the tool(s) (from `tools:`) this linter runs. When more than one, `main_tool`
  disambiguates which one drives versioning.
- `description` — one-line summary shown by `linters list`/`linters enable`.
- `commands` — one or more invocations (see below).
- `direct_configs` — config filenames whose presence enables/influences this linter.
- `affects_cache` — extra files that invalidate the lint cache beyond the target file itself (e.g.
  `package.json`, `.editorconfig`).
- `environment` — env vars/PATH entries scoped to running the linter.
- `issue_url_format` — template for linking a diagnostic's rule id to upstream docs.
- `suggest_if` — when to suggest enabling this linter (`files_present`, `config_present`,
  `never`).
- `known_good_version` / `known_bad_versions` — default/tested version, and versions known not to
  work.
- `supported_platforms` — OS restriction on where this linter can run.
- `run_timeout` — per-invocation timeout override.
- `cache_results: false` — opt out of caching (e.g. shellcheck, which can follow `source`
  includes outside the target file).
- `version_command` — `{run, parse_regex}` to detect the installed tool's version.

`commands[]` fields:

- `name` — command id (commonly `lint` or `format`).
- `run` — the shell invocation, with `${target}`/`${tmpfile}` placeholders.
- `output` — output format id trunk knows how to parse (`sarif`, or a tool-specific parser name).
- `success_codes` / `error_codes` — exit code interpretation.
- `batch: true` — the linter can take multiple files in one invocation.
- `read_output_from: tmp_file|stderr` — where to collect output when it isn't stdout.
- `sandbox_type: copy_targets|expanded` — how target files are staged before running.
- `run_from: ${parent}` — working directory override.
- `version` — gates this command to a tool version range, so the same linter id invokes
  differently depending on the resolved version. eslint has two `lint` commands this way: one
  `version: ">=9.0.0"` (flat config: `eslint.config.js/mjs/cjs`), one `version: "<=8.57.0"`
  (legacy `.eslintrc*`), each with its own `direct_configs` (github.com/trunk-io/plugins,
  `linters/eslint/plugin.yaml`).
- `in_place: true` + `formatter: true` — marks a command that rewrites files instead of reporting
  (a formatter, not a checker).
- `parser` — a secondary `{runtime, run}` script that converts a tool's native output into
  trunk's normalized shape, used when the tool has no native structured output mode:

```yaml
commands:
  - name: format
    run: prettier -w ${target}
    in_place: true
    formatter: true
    output: sarif
    parser:
      runtime: python
      run: python3 ${plugin}/linters/prettier/prettier_to_sarif.py ${exit_code}
```

(github.com/trunk-io/plugins, `linters/prettier/plugin.yaml`)

### `actions:`

A git-hook or file-change-triggered automation.

```yaml
actions:
  definitions:
    - id: commitlint
      display_name: Commitlint
      description: Enforce git commit message standards
      runtime: node
      packages_file: package.json
      run: commitlint --edit ${1}
      triggers:
        - git_hooks: [commit-msg]
```

(github.com/trunk-io/plugins, `actions/commitlint/plugin.yaml`)

```yaml
actions:
  definitions:
    - id: go-mod-tidy
      display_name: Go Mod Tidy
      description: Runs go mod tidy when changes are detected to go.mod
      runtime: go
      run: go mod tidy
      triggers:
        - files: [go.mod]
```

(github.com/trunk-io/plugins, `actions/go-mod-tidy/plugin.yaml`)

Fields:

- `id` — the action's id, as referenced by `actions.enabled`.
- `display_name` / `description` — shown by `actions list`.
- `runtime` — runtime the action runs under (optional; absent for actions that just shell out,
  e.g. `go-mod-tidy` still names `go` since `run` invokes it directly).
- `run` — the shell invocation. `${1}` etc. are trigger-supplied arguments (e.g. the commit
  message file path for a `commit-msg` hook).
- `packages_file` — manifest/lockfile that must be present for this action (e.g. `package.json`
  for a node-runtime action).
- `triggers` — a list of alternative trigger kinds:
  - `git_hooks: [<hook-name>, ...]` — pre-commit, commit-msg, pre-push, post-checkout,
    post-merge, pre-rebase, prepare-commit-msg, ...
  - `files: [<path glob>, ...]` — fires when matching files change.
  - `schedule: <duration>` or `schedule: {interval: <duration>, delay: <duration>}` — periodic
    background trigger. The bare form is shorthand for `{interval: <duration>}` (e.g. `schedule:
24h`, github.com/trunk-io/plugins, `actions/git-blame-ignore-revs/plugin.yaml`).
- `interactive: true|optional` — whether the action needs a TTY.
- `notify_on_error: true|false` — whether a failure surfaces a notification.

### `runtimes:`

A language runtime trunk/rtunk can install and use to run tools.

```yaml
runtimes:
  definitions:
    - type: node
      download: node
      known_good_version: 22.18.0
      shims: [node, npm, npx, corepack]
      version_commands:
        - run: node --version
          parse_regex: "${semver}"
      runtime_environment:
        - { name: PATH, list: ["${runtime}/bin", "${runtime}", "${env.PATH}"] }
        - { name: NODE_OPTIONS, value: "${env.NODE_OPTIONS}", optional: true }
      linter_environment:
        - { name: PATH, list: ["${linter}/node_modules/.bin"] }
        - { name: NODE_PATH, value: "${linter}/node_modules" }
```

(github.com/trunk-io/plugins, `runtimes/node/plugin.yaml`, abbreviated)

Fields:

- `type` — the runtime's id, as referenced by `runtimes.enabled` and tools' `runtime:` field.
- `download` — points at a `downloads:` recipe, same mechanism as tools.
- `system_version: required` — alternative to `download`: expect the runtime already present on
  the host rather than downloading it (php's definition, verified via `version_commands` matched
  against a minimum `version: ">=8.0.0"`, github.com/trunk-io/plugins, `runtimes/php/plugin.yaml`).
- `known_good_version` — default/tested version.
- `shims` — executable names the runtime exposes on `PATH` once installed. Can also use the
  `{name, target}` object form (see `tools:` above).
- `version_commands` — `[{run, parse_regex}, ...]` to detect the installed runtime's version.
- `runtime_environment` — environment used to run the runtime itself.
- `linter_environment` — environment _added_ when a linter depending on this runtime executes
  (e.g. so the linter's own `node_modules/.bin` lands on `PATH`).

## Built-in / global config trunk itself contributes

`trunk config print` renders the fully resolved effective config as one document, with these
top-level sections in this order: `version`, `cli`, `plugins`, `downloads`, `environments`,
`runtimes`, `actions`, `lint`, `notifications`, `tools` (verified against a local `trunk config
print` run). Each of `runtimes`, `actions`, `lint`, `tools` carries both:

- `enabled:` — the flat list from `trunk.yaml`, i.e. what this specific repo turned on
  (`lint.enabled: [checkov@3.3.16, git-diff-check, ...]`, `actions.enabled: [commitlint,
trunk-check-pre-push, ...]`).
- `definitions:` — the full merged catalog of every definition contributed by every resolved
  plugin source, regardless of whether it is enabled here. Most entries carry
  `autogenerated_definition_path: linters/<name>` (or `actions/<name>`, etc.) pointing back at the
  plugin file the definition came from.

trunk's own `config print` always renders the full, untrimmed catalog above — there is no flag to
narrow it. `rtunk config print` differs on purpose: by default it resolves via `Config.Resolve`,
which trims `definitions:` (and `tools:`/`downloads:`) down to what's enabled plus whatever those
enabled definitions reference transitively (`filterEnabled`) — the effective configuration a repo
actually uses, rather than every definition a plugin source happens to contribute. `rtunk plugins
print` resolves via `Config.ResolveAll` instead, matching trunk's own untrimmed view.
`Environments` and `Lint.CommentFormats` are identical either way (see above): they're global,
not trunk.yaml-enableable, so there's nothing for `filterEnabled` to trim.

`lint:` additionally carries global, non-plugin-specific config:

- `comment_formats` — named comment-delimiter styles (`hash`, `slashes-block`, `html-tag`, ...),
  reused by file-type definitions to detect `trunk-ignore` comments. rtunk parses this into
  `Config.Lint.CommentFormats` (`[]CommentFormat`), read from every resolved plugin source's own
  category-root `linters/plugin.yaml` (e.g. github.com/trunk-io/plugins' 13 entries) and
  concatenated — like `Environments` above, this has no enabled id and is never trimmed by
  `filterEnabled`.
- `files` — the file-type registry: named types (`dart`, `c++-header`, `bazel`, ...) matched by
  any of `extensions`/`filenames`/`regexes`/`shebangs` (a `#!/usr/bin/env <shebang>` line, for
  extensionless scripts), with `comments` naming `comment_formats` entries and `required_yaml_keys`
  further narrowing a YAML match (e.g. `cloudformation` vs plain `yaml`); some types compose others
  via `inherit: [...]` instead of repeating their fields (up to two hops deep in
  github.com/trunk-io/plugins, e.g. `bazel` -> `bazel-build`/`bazel-workspace`/`bazel-module`).
  This is what `lint.definitions[].files: [...]` values reference. rtunk parses this into
  `Config.Lint.Files` (`map[string]FileType`, keyed like `Tools`/`Downloads` since it's referenced
  by id) — `filterEnabled` trims it to what a kept linter's `files:` uses plus its `inherit:`
  closure, unlike `CommentFormats` above.
- default `ignore` rules — e.g. all linters ignore `**/trunk`, and lockfiles (`go.sum`,
  `package-lock.json`, `Cargo.lock`) are ignored by all linters except explicitly listed security
  scanners. Not yet parsed by rtunk.
- `bazel` (search paths for a bazel/bazelisk binary), `default_max_file_size`,
  `compile_commands_roots`, `skip_missing_compile_command`. Not yet parsed by rtunk.

`notifications:` configures per-surface rate-limit priorities for background/scheduled actions.
Out of scope for rtunk (no daemon, no background notifications) — noted only for completeness.

Actions defined directly by trunk's CLI itself (not by a plugin) also appear in `trunk config
print`'s `actions.definitions`, without an `autogenerated_definition_path` — e.g.
`trunk-cache-prune`, `trunk-upgrade-available`, `trunk-share-with-everyone`,
`trunk-single-player-auto-upgrade`, `trunk-whoami`. Several of these reference permanently
out-of-scope rtunk features (daemon, whoami/login, share). rtunk's own built-in action set, if any
in later milestones, must exclude these by design, not merely by omission.
