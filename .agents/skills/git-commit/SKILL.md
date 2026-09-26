---
name: git-commit
description: Use when the user wants to commit, stage, or finalize work in the rtunk repository — creating a commit message, splitting staged changes into atomic commits, or checking a message against the project's commitlint rules.
---

# Git commit convention

## Why we commit this way

A commit is a historical record, not just a sync mechanism to move code from
one place to another. Future readers — human or AI — will use `git log` and
`git blame` to understand _why_ a line looks the way it does, long after the
diff itself stops being interesting. That only works if messages carry intent
instead of restating what the diff already shows.

Prefer small, imperfect, atomic commits over batching everything into one
large commit and squashing later. One logical change per commit: it makes
review, bisection, and revert all cheaper.

## Commit format

```
type[scope]: Subject starting with uppercase

Body providing context and intent, max 80 chars per line

Assisted-by: <provider>:<model-id>
```

Breaking change variant — the `BREAKING CHANGE:` paragraph is mandatory:

```
+![check]: Subject starting with uppercase

Body providing context and intent, max 80 chars per line

BREAKING CHANGE: what breaks and what the caller must do about it

Assisted-by: <provider>:<model-id>
```

## Types

| Symbol         | Name       | Use for                                                    |
| -------------- | ---------- | ---------------------------------------------------------- |
| `+`            | Add        | New feature, command, resource                             |
| `-`            | Remove     | Delete code, file, dead feature                            |
| `~`            | Improve    | Perf, config, behavioral improvement (non-bug)             |
| `!`            | Fix        | Repair a bug or broken behavior                            |
| `=`            | Refactor   | No behavior change (style, tests, DX, CI)                  |
| `^`            | Bump       | Dependency version upgrade or downgrade                    |
| `>`            | Move       | Rename or relocate resources                               |
| `<`            | Revert     | Undo a previous commit                                     |
| `@`            | Docs       | README, AGENTS/ROADMAP, comments                           |
| `$`            | Security   | Fix, policy, secret management                             |
| `?`            | Experiment | POC, investigation, research                               |
| `*`            | Wildcard   | Does not fit any other type                                |
| `+!` `~!` `-!` | Breaking   | Only Add, Improve, Remove can break backward compatibility |

Disambiguation rules:

- **`~` vs `!`** — did the previous behavior have a bug? Use `!` if you fixed
  something broken. Use `~` if the previous behavior was correct and you
  changed it to something better (faster, clearer, more configurable).
- **`=` vs `~`** — is the change observable by a user of rtunk (a person
  running the CLI, or reading its config schema)? Observable → `~`. Invisible
  (internal refactor, added tests, CI tweak) → `=`.
- **`^`** is always used for dependency bumps, even when done by hand rather
  than by an automated tool.
- CI/CD is not its own type. Use the type that describes what actually
  changed (usually `=` or `~`) with the `ci` scope.

## Scopes

Scope is mandatory and bracketed: `type[scope]: Subject`. rtunk is a single
Go CLI, not a monorepo, so the scope list stays flat rather than namespaced
like `project:*`/`catalog:*` would be.

| Scope      | Covers                                                                             |
| ---------- | ---------------------------------------------------------------------------------- |
| `config`   | `trunk.yaml`/`rtunk.yaml` parsing, schema, config resolution                       |
| `plugin`   | Plugin definitions, discovery, linter/runtime/tool resolution                      |
| `cache`    | Download, content-addressed cache, shims                                           |
| `check`    | Check command policy: which commands run (`Formatter: false`), read-only reporting |
| `engine`   | Shared job-queue engine: file matching, `RunFrom`/`SandboxType`, execution         |
| `output`   | Linter output-format parsers (SARIF, per-tool JSON schemas, `parse_regex`)         |
| `fmt`      | Formatters command                                                                 |
| `actions`  | Actions and git-hooks                                                              |
| `upgrade`  | Self-upgrade command                                                               |
| `init`     | `init`/`deinit` command                                                            |
| `renovate` | Renovate annotation generation (`rtunk toolbox renovate`)                          |
| `cli`      | Top-level CLI wiring, flag compatibility, entrypoints                              |
| `deps`     | Go module or tool version bumps                                                    |
| `ci`       | `.github/` workflows, `.trunk/` dogfood config, `mise.toml`                        |
| `docs`     | README, AGENTS.md, ROADMAP.md, ADRs                                                |

Decision tree: which files did the change touch?

1. Only `.github/`, `.trunk/`, `mise.toml` → `ci`.
2. Only `go.mod`/`go.sum` (or a pinned tool version) with no code change →
   `deps`.
3. Only `*.md` prose (no code) → `docs`.
4. Otherwise, match the package/command area to the table above (e.g.
   `pkg/trunk/config` → `config`, `pkg/trunk/download` → `cache`,
   `pkg/trunk/check` → `check`, `pkg/trunk/engine` (including
   `pkg/trunk/engine/security`) → `engine`, `pkg/trunk/output` → `output`).
5. Ambiguous, or genuinely spans more than one area → ask the user, never
   guess.

Multiple scopes are allowed, comma-separated (`type[scope1,scope2]:`), when
one atomic change truly spans areas — cap at 3. In a single-CLI repo this is
the exception, not the norm: most commits should carry exactly one scope.

## Subject

- Imperative mood ("Add", not "Added" or "Adds").
- Starts with an uppercase letter.
- No trailing period.
- Max 100 characters.

## Body

Optional for trivial changes (a one-line dependency bump, a typo fix),
mandatory otherwise. It must explain **why**, never restate **what** — the
diff already shows what changed.

- Motivation, trade-offs, or user impact — not a narration of the diff.
- Max 80 characters per line, sentence-case.
- Sentence-case applies to the body's very first character, not each line.
  `rtunk`/`trunk` are lowercase by convention, so if the first sentence would
  otherwise start with one of them, rephrase around it instead
  (e.g. "Pins go and trunk because..." not "trunk needs...").
- The "why" must come from the user's own words in conversation. Never infer
  it from the diff. If the user hasn't stated it, ask before writing the
  body.

## `Assisted-by:` trailer

When an AI assistant helps draft a commit, disclose it with `Assisted-by:`
rather than `Co-authored-by:` — an AI assistant is a tool the human directs,
not a co-author with legal authorship standing.

Format: `Assisted-by: <provider>:<model-id>` (dots in version numbers, not
hyphens — e.g. `Assisted-by: anthropic:claude-sonnet-5`).

## Signing

Always sign with GPG: `git commit -S -m "..."`. Never add `-s`/`--signoff` —
DCO sign-off is the human committer's own attestation that they have the
right to submit the change; an AI assistant must stay out of it.

## Commitlint rules (canonical reference)

This section mirrors `.commitlintrc.js`. **Keep it in sync whenever that file
changes — this section IS the reference for anyone reading the skill instead
of the config.**

Header pattern: `^(\S+?)\[([^\]]+)\]:\s(.+)$` (breaking: `^([+~-]!)\[([^\]]+)\]:\s(.+)$`).

| Rule                     | Level | Value                                                                              |
| ------------------------ | ----- | ---------------------------------------------------------------------------------- |
| `header-max-length`      | error | 100                                                                                |
| `header-full-stop`       | error | never `.`                                                                          |
| `header-trim`            | error | always                                                                             |
| `header-case`            | off   | symbols have no case                                                               |
| `type-empty`             | error | never empty                                                                        |
| `type-enum`              | error | see Types table                                                                    |
| `scope-empty`            | error | never empty                                                                        |
| `scope-case`             | error | lower-case                                                                         |
| `scope-enum`             | warn  | see Scopes table (warn only — multi-scope commits won't match a single enum entry) |
| `subject-empty`          | error | never empty                                                                        |
| `subject-case`           | error | sentence-case                                                                      |
| `subject-full-stop`      | error | never `.`                                                                          |
| `subject-max-length`     | error | 100                                                                                |
| `body-case`              | error | sentence-case                                                                      |
| `body-max-line-length`   | error | 80                                                                                 |
| `footer-leading-blank`   | error | always                                                                             |
| `footer-max-line-length` | error | 80                                                                                 |

Prompt configuration (for interactive commit tooling, if wired up later):
`allowBreakingChanges: ["+!", "~!", "-!"]`, `allowCustomScopes: false`,
`allowEmptyScopes: false`, `enableMultipleScopes: true`,
`scopeEnumSeparator: ","`, `useCommitSignGPG: true`, `useEmoji: false`.

### Keeping this skill in sync

1. Read `.commitlintrc.js`.
2. Update the matching table above (Types, Scopes, or the rule table).
3. Verify the two files agree before committing the change (commit it with
   `type[docs]:` or `type[ci]:` depending on what actually changed).

### If commitlint fails

| Rule                   | Likely cause                                              | Fix                                                                  |
| ---------------------- | --------------------------------------------------------- | -------------------------------------------------------------------- |
| `type-enum`            | Symbol missing or misspelled                              | Use one of the 12 base symbols, or a `+!`/`~!`/`-!` breaking variant |
| `scope-empty`          | No `[scope]` bracket                                      | Add a bracketed scope from the Scopes table                          |
| `header-max-length`    | Subject too long                                          | Trim to ≤100 chars total, move detail to the body                    |
| `subject-full-stop`    | Trailing period on subject                                | Remove it                                                            |
| `body-max-line-length` | Body line >80 chars                                       | Rewrap                                                               |
| `body-case`            | Body starts with a lowercase word (often `rtunk`/`trunk`) | Rephrase the opening so the first character is uppercase             |
| `footer-leading-blank` | No blank line before `Assisted-by:`/`BREAKING CHANGE:`    | Add a blank line before the footer                                   |

## Workflow

1. Survey the workspace:
   `git status`, `git diff --cached --name-only`, `git diff --name-only`,
   `git log --oneline --no-merges -10`.
2. If staged changes span multiple scopes and aren't one atomic change,
   split them into separate commits instead of forcing a multi-scope header.
3. Select the type using the disambiguation rules above.
4. Determine the scope (or scopes, comma-separated, capped at 3) using the
   decision tree above.
5. Draft the subject: imperative, uppercase start, no trailing period,
   ≤100 chars.
6. Write the body: ask the user for the "why" if it hasn't already come up
   in conversation. Skip the body only for genuinely trivial changes.
7. Stage and commit:
   ```
   git commit -S -m "type[scope]: Subject" -m "Body explaining why" -m "Assisted-by: <provider>:<model-id>"
   ```
8. Never add `-s`/`--signoff` to the command above.

## Examples

**Good — simple add:**

```
+[check]: Add SARIF output normalization for gitleaks

Trunk-compatible tooling expects SARIF; without it, downstream
consumers (editors, CI annotators) can't parse gitleaks findings
the same way they parse every other linter's output.

Assisted-by: anthropic:claude-sonnet-5
```

**Good — dependency bump, no body needed:**

```
^[deps]: Bump golang.org/x/tools to v0.27.0
```

**Good — breaking change:**

```
-![config]: Drop support for trunk.yaml v0.0 schema

v0.0 lacked a version field, which made every later schema
migration ambiguous to detect. Every real-world config already
declares a version, so keeping v0.0 support only hid config bugs.

BREAKING CHANGE: configs without a `version` field are now
rejected at load time instead of falling back to v0.0 defaults.

Assisted-by: anthropic:claude-sonnet-5
```

**Bad — no type/scope, restates the diff:**

```
Updated config.go to add new validation function
```

Missing `type[scope]:` entirely, and the body (if any) would just repeat
what the diff already shows instead of explaining why validation was added.

**Bad — wrong trailer, wrong signing flag:**

```
![check]: Fix nil pointer in report renderer

Co-authored-by: Claude <claude@anthropic.com>
```

committed with `git commit -s -S`. Should use `Assisted-by:`, not
`Co-authored-by:`, and must never carry `-s` (DCO sign-off is the human's
own attestation).

**Bad — invalid type:**

```
!![check]: Fix and majorly change the reporting pipeline
```

`!!` isn't a valid symbol — only `+`, `~`, `-` can take the `!` breaking
suffix, and a fix (`!`) can't itself be marked breaking.

## References

- [Trailers for AI-assisted commits — All Things Open](https://allthingsopen.org/articles/ai-assisted-commits)
- [Linux kernel documentation on coding assistants](https://docs.kernel.org/process/ai.html)
