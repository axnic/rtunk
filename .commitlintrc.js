/*
 * rtunk commit convention — symbol-based types, mandatory bracketed scope.
 *
 * Format:           type[scope]: Subject
 * Breaking change:  type![scope]: Subject   (only +!, ~!, -! are valid)
 *
 * The canonical, human-readable reference for this convention lives in
 * .agents/skills/git-commit/SKILL.md — keep that file in sync whenever this
 * one changes.
 */

/**
 * Allowed commit types.
 */
const types = [
  { value: "+", name: "+    Add       — new feature, command, resource" },
  { value: "-", name: "-    Remove    — delete code, file, dead feature" },
  {
    value: "~",
    name: "~    Improve   — perf, config, behavioral improvement (non-bug)",
  },
  { value: "!", name: "!    Fix       — repair a bug or broken behavior" },
  {
    value: "=",
    name: "=    Refactor  — no behavior change (style, tests, DX, CI)",
  },
  {
    value: "^",
    name: "^    Bump      — dependency version upgrade or downgrade",
  },
  { value: ">", name: ">    Move      — rename or relocate resources" },
  { value: "<", name: "<    Revert    — undo a previous commit" },
  { value: "@", name: "@    Docs      — README, AGENTS/ROADMAP, comments" },
  { value: "$", name: "$    Security  — fix, policy, secret management" },
  { value: "?", name: "?    Experiment — POC, investigation, research" },
  { value: "*", name: "*    Wildcard  — does not fit any other type" },
  // Breaking change variants — only addition, improvement, and removal can break.
  {
    value: "+!",
    name: "+!   Add (breaking)     — addition that breaks backward compatibility",
  },
  {
    value: "~!",
    name: "~!   Improve (breaking) — behavioral change that breaks backward compatibility",
  },
  {
    value: "-!",
    name: "-!   Remove (breaking)  — removal that breaks backward compatibility",
  },
];

/**
 * Allowed commit scopes — one per command-surface area of rtunk (see
 * ROADMAP.md) plus a handful of cross-cutting concerns. rtunk is a single Go
 * CLI, not a monorepo, so this list stays flat rather than namespaced.
 */
const scopes = [
  {
    value: "config",
    name: "config    — trunk.yaml/rtunk.yaml parsing, schema, config resolution",
  },
  {
    value: "plugin",
    name: "plugin    — plugin definitions, discovery, linter/runtime/tool resolution",
  },
  {
    value: "cache",
    name: "cache     — download, content-addressed cache, shims",
  },
  {
    value: "check",
    name: "check     — check command policy: which commands run, read-only reporting",
  },
  {
    value: "engine",
    name: "engine    — shared job-queue engine: file matching, RunFrom/SandboxType, execution",
  },
  {
    value: "output",
    name: "output    — linter output-format parsers (SARIF, JSON schemas, parse_regex)",
  },
  { value: "fmt", name: "fmt       — formatters command" },
  { value: "actions", name: "actions   — actions and git-hooks" },
  { value: "upgrade", name: "upgrade   — self-upgrade command" },
  { value: "init", name: "init      — init/deinit command" },
  {
    value: "cli",
    name: "cli       — top-level CLI wiring, flag compatibility, entrypoints",
  },
  { value: "deps", name: "deps      — Go module or tool version bumps" },
  {
    value: "ci",
    name: "ci        — .github workflows, .trunk dogfood config, mise.toml",
  },
  { value: "docs", name: "docs      — README, AGENTS.md, ROADMAP.md, ADRs" },
];

/** @type {import('cz-git').UserConfig} */
module.exports = {
  rules: {
    "body-full-stop": [0, "always", "."],
    "body-leading-blank": [0, "always"],
    "body-empty": [0, "always"],
    "body-max-length": [2, "always", Infinity],
    "body-max-line-length": [2, "always", 80],
    "body-min-length": [2, "always", 0],
    "body-case": [2, "always", "sentence-case"],
    "footer-leading-blank": [2, "always"],
    "footer-empty": [0, "always"],
    "footer-max-length": [2, "always", Infinity],
    "footer-max-line-length": [2, "always", 80],
    "footer-min-length": [2, "always", 0],
    // header-case disabled: symbol prefixes (+, ~, …) have no case.
    "header-case": [0, "always", "sentence-case"],
    "header-full-stop": [2, "never", "."],
    "header-max-length": [2, "always", 100],
    "header-min-length": [2, "always", 0],
    "header-trim": [2, "always"],
    "references-empty": [0, "never"],
    // scope-enum set to warning: multi-scope commits (scope1,scope2) won't
    // match a single enum entry; actual validation is enforced by the
    // cz-git prompt instead.
    "scope-enum": [1, "always", scopes.map((s) => s.value)],
    "scope-case": [2, "always", "lower-case"],
    "scope-empty": [2, "never"],
    "scope-max-length": [2, "always", Infinity],
    "scope-min-length": [2, "always", 0],
    "subject-case": [2, "always", "sentence-case"],
    "subject-empty": [2, "never"],
    "subject-full-stop": [2, "never", "."],
    "subject-max-length": [2, "always", 100],
    "subject-min-length": [2, "always", 0],
    "subject-exclamation-mark": [0, "never"],
    "type-enum": [2, "always", types.map((t) => t.value)],
    "type-case": [0, "always", "lower-case"],
    "type-empty": [2, "never"],
    "type-max-length": [2, "always", Infinity],
    "type-min-length": [2, "always", 0],
    "signed-off-by": [0, "always", "Signed-off-by: "],
  },
  parserPreset: {
    parserOpts: {
      headerPattern: /^(\S+?)\[([^\]]+)\]:\s(.+)$/,
      headerCorrespondence: ["type", "scope", "subject"],
      breakingHeaderPattern: /^([+~-]!)\[([^\]]+)\]:\s(.+)$/,
      breakingHeaderCorrespondence: ["type", "scope", "subject"],
    },
  },
  prompt: {
    allowBreakingChanges: ["+!", "~!", "-!"],
    allowCustomScopes: false,
    allowEmptyScopes: false,
    enableMultipleScopes: true,
    scopeEnumSeparator: ",",
    typesSearchValue: false,
    skipQuestions: ["body", "footerPrefix", "footer"],
    upperCaseSubject: true,
    useCommitSignGPG: true,
    useEmoji: false,
  },
};
