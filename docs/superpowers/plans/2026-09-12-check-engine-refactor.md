# Check Engine Refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extract `pkg/trunk/check`'s execution engine, `RunFrom`/`SandboxType` resolution, and
output parsing into packages a future `pkg/trunk/fmt` can reuse, fix a real path-traversal gap
found along the way, and replace v0.3.1's best-effort generic regex parser with a declarative one
driven by the `Command.ParseRegex` field the real trunk-io catalog has always carried.

**Architecture:** `pkg/trunk/output` (parsers, one file per format), `pkg/trunk/engine` (job
queue/worker pool + file matching), `pkg/trunk/engine/security` (`RunFrom`/`SandboxType`), thin
`pkg/trunk/check` left as the check-specific adapter.

**Tech Stack:** Go stdlib only -- `context.Context` for cancellation (`exec.CommandContext`), no
new `go.mod` dependencies.

**Spec:** `docs/superpowers/specs/2026-09-12-check-engine-refactor-design.md`

## Global Constraints

- No new `go.mod` dependencies.
- Every test asserts exact values, never just "no error."
- Every moved file's existing tests keep passing verbatim (import-path/package-name updates
  aside) -- a test needing behavioral changes to keep passing is a sign something moved wrong.
- `ParseFromRegex`'s name-to-field mapping is a fixed, direct lookup table (`path`->`File`,
  `line`->`Line`, `col`->`Column`, `code`->`RuleID`, `message`->`Message`,
  `severity`->`Severity`) with **zero linter-specific logic**. A name not in the table is never
  looked up -- not guessed, not aliased to its closest match (e.g. `ty`'s `column` group must stay
  unmapped, never treated as an alias for `col`).
- `go build ./... && go vet ./... && go test ./... -count=1 -race` clean before every task is done.
- Commits signed (`-S`), commitlint conventions per `.agents/skills/git-commit/SKILL.md`.
- Do not add `lsp_json`/`arcanist` output support, nested-workspace support, or
  `${compile_command}` support -- all explicitly out of scope (see spec's Non-goals).

---

### Task 1: `pkg/trunk/output` -- move and split the existing 14 parsers

**Files:**

- Create: `pkg/trunk/output/finding.go`, `sarif.go`, `pass_fail.go`, `taplo.go`, `actionlint.go`,
  `bandit.go`, `buildifier.go`, `cfnlint.go`, `eslint.go`, `hadolint.go`, `haml_lint.go`,
  `markdownlint.go`, `pylint.go`, `rubocop.go`, `stylelint.go` (plus each `_test.go`)
- Delete: `pkg/trunk/check/parse.go`, `pkg/trunk/check/parse_test.go` (their content moves, split,
  into the files above)

**Interfaces:**

- Produces: `output.Finding`, `output.ApplyIssueURL`, and one `output.ParseXxx` function per
  format below -- Task 2 (`output.ParseFromRegex`) and Task 4 (`engine`'s dispatch) both import
  this package.

This task is a **pure move + split, zero behavior change**: every function's body, every test's
fixture, is byte-for-byte identical to today's `pkg/trunk/check/parse.go` /
`pkg/trunk/check/parse_test.go`, with only two mechanical edits: `package check` -> `package
output`, and one file's worth of functions per new file instead of one 664-line file. `ParsePerlCritic`
and `ParseGenericRegex` (and their tests) are the two exceptions -- they do **not** move here, see
Task 2.

- [ ] **Step 1: Read the current files**

Read `pkg/trunk/check/parse.go` and `pkg/trunk/check/parse_test.go` in full before starting --
this task's "no placeholder" compliance depends on transcribing their real current content
exactly, not from memory.

- [ ] **Step 2: Create `pkg/trunk/output/finding.go`**

Move: the `Finding` struct, `ApplyIssueURL`. Both currently sit right after the package's imports
in `parse.go` (lines 11-21 and 97-111 in the current file). Imports needed: `"strings"` only.

```go
// Package output normalizes every check-engine linter output format (SARIF, per-tool JSON
// schemas, and config-declared regex patterns) into a common Finding shape.
package output

import "strings"

// Finding is one normalized check result.
type Finding struct {
	Linter   string
	File     string
	Line     int    // 0 when the format doesn't report one (pass_fail)
	Column   int    // 0 when not reported
	Severity string // "error", "warning", or "info"
	RuleID   string
	Message  string
	URL      string // filled by ApplyIssueURL when both IssueURLFormat and RuleID are set
}

// ApplyIssueURL fills each finding's URL from format (a config.Linter.IssueURLFormat, using "{}"
// as RuleID's placeholder -- trunk-io's own plugin.yaml convention, e.g. shellcheck's
// "https://github.com/koalaman/shellcheck/wiki/{}"). A no-op per finding when format is empty or
// the finding has no RuleID.
func ApplyIssueURL(findings []Finding, format string) {
	if format == "" {
		return
	}
	for i := range findings {
		if findings[i].RuleID == "" {
			continue
		}
		findings[i].URL = strings.ReplaceAll(format, "{}", findings[i].RuleID)
	}
}
```

- [ ] **Step 3: Create `pkg/trunk/output/sarif.go`**

Move: `sarifDocument`, `ParseSARIF`, `sarifSeverity` (current `parse.go` lines 23-85). Same body,
`package output`, imports `"encoding/json"`, `"fmt"`, `"strings"`.

- [ ] **Step 4: Create `pkg/trunk/output/pass_fail.go`**

Move: `ParsePassFail` (current `parse.go` lines 87-95). Same body, `package output`, no imports
needed beyond none (it only uses builtins -- check and add `"strings"`... it doesn't need any,
verify against the real current body before finalizing imports).

- [ ] **Step 5: Create `pkg/trunk/output/actionlint.go`, `bandit.go`, `cfnlint.go`, `hadolint.go`,
      `pylint.go`, `eslint.go`, `buildifier.go`, `haml_lint.go`, `markdownlint.go`, `rubocop.go`,
      `stylelint.go`**

Move each parser's own type + `ParseXxx` + any `xxxSeverity` helper verbatim into its own file
(`package output`), in the same order they appear in the current `parse.go`:
`actionlintFinding`/`ParseActionlint` -> `actionlint.go`; `banditDocument`/`ParseBandit`/
`banditSeverity` -> `bandit.go`; `cfnLintFinding`/`ParseCfnLint`/`cfnLintSeverity` -> `cfnlint.go`;
`hadolintFinding`/`ParseHadolint`/`hadolintSeverity` -> `hadolint.go`;
`pylintFinding`/`ParsePylint`/`pylintSeverity` -> `pylint.go`;
`eslintFileResult`/`ParseESLint`/`eslintSeverity` -> `eslint.go`;
`buildifierDocument`/`ParseBuildifier` -> `buildifier.go`; `hamlLintDocument`/`ParseHamlLint` ->
`haml_lint.go`; `markdownlintViolation`/`ParseMarkdownlint` -> `markdownlint.go`;
`rubocopDocument`/`ParseRubocop`/`rubocopSeverity` -> `rubocop.go`;
`stylelintFileResult`/`ParseStylelint` -> `stylelint.go`. Each file: `package output`, `import
("encoding/json"; "fmt"; "strings")` (trim to only what that file's own body actually uses -- e.g.
a file with no `xxxSeverity` helper doing string comparison may not need `"strings"`).

- [ ] **Step 6: Create `pkg/trunk/output/taplo.go`**

Move: `taploMessageRE`, `taploLocationRE`, `ParseTaplo` (current `parse.go` lines 555-599). Same
body, `package output`, imports `"regexp"`, `"strconv"`, `"strings"`.

- [ ] **Step 7: Split `parse_test.go` the same way**

Each `TestParseXxx` (and its fixture, e.g. the real captured markdownlint/taplo samples) moves into
`<same-basename>_test.go` alongside its parser, `package output`. `TestParsePerlCritic` and
`TestParseGenericRegex` do **not** move -- Task 2 replaces both with `TestParseFromRegex_*`.

- [ ] **Step 8: Delete the old files and verify**

```bash
rm pkg/trunk/check/parse.go pkg/trunk/check/parse_test.go
go build ./pkg/trunk/output/... && go vet ./pkg/trunk/output/... && go test ./pkg/trunk/output/... -count=1 -v
```

Expected: `pkg/trunk/check` no longer builds (its `run.go` still references `Finding`/`ParseXxx` --
expected and fixed in Task 4, not this task). Confirm `go build ./pkg/trunk/output/...` alone is
clean and every moved test passes with identical output to before the move.

- [ ] **Step 9: Commit**

```bash
git add pkg/trunk/output/ pkg/trunk/check/parse.go pkg/trunk/check/parse_test.go
git commit -S -m "$(cat <<'EOF'
>[output]: Move output parsers into a dedicated package

pkg/trunk/check/parse.go bundled every output-format parser into
one 664-line file, package-private to check -- a future pkg/trunk/fmt
needs these same parsers (a formatter's own check-mode commands use
the same Output values) without duplicating them. Pure move, one
file per parser, zero behavior change: ParsePerlCritic and
ParseGenericRegex are the two exceptions, replaced by a declarative
parser in the next commit rather than moved as-is.

Assisted-by: anthropic:claude-sonnet-5
EOF
)"
```

(This commit intentionally leaves `pkg/trunk/check` non-building -- Task 4 fixes it. If your
harness's CI or pre-commit hook requires a fully green build at every commit, squash Tasks 1-4
into fewer commits instead; do not skip the hook to force a red intermediate state through.)

---

### Task 2: `output.ParseFromRegex` -- declarative regex parsing from `Command.ParseRegex`

**Files:**

- Modify: `pkg/trunk/config/definitions.go` (add `Command.ParseRegex`)
- Create: `pkg/trunk/output/regex.go`, `pkg/trunk/output/regex_test.go`
- Delete: the `ParsePerlCritic`/`ParseGenericRegex` bodies and their tests (never moved in Task 1)

**Interfaces:**

- Consumes: `output.Finding` (Task 1).
- Produces: `output.ParseFromRegex(pattern string, data []byte, linter string) ([]Finding, error)`
  -- Task 4's engine dispatch calls this for every `Output: "regex"` command, using
  `cmd.ParseRegex` as `pattern`.

- [ ] **Step 1: Add the config field**

In `pkg/trunk/config/definitions.go`, inside the existing `Command` struct (find it by its doc
comment `// Command is one invocation of a Linter...`), add one field, keeping the struct's
existing field order otherwise unchanged:

```go
	SandboxType    string  `yaml:"sandbox_type,omitempty"`
	RunFrom        string  `yaml:"run_from,omitempty"`
	ParseRegex     string  `yaml:"parse_regex,omitempty"`
```

(Insert `ParseRegex` right after `RunFrom` -- both are `Output: "regex"`-adjacent fields in the
real schema.) Do not touch `VersionCommand.ParseRegex` (a different struct, a different concept --
a tool's own version-string extraction, unrelated to this field).

- [ ] **Step 2: Write `pkg/trunk/output/regex.go`**

```go
package output

import (
	"regexp"
	"strconv"
)

// ParseFromRegex parses data using pattern's named capture groups, straight from the real
// trunk-io catalog's own Command.ParseRegex field -- the same mechanism trunk's own closed-source
// engine uses for every "regex"-output linter (see docs/superpowers/specs/
// 2026-09-12-check-engine-refactor-design.md). A fixed name->Finding-field table does the mapping
// -- nothing else, no linter-specific logic anywhere in this function. A name the table has no
// entry for is simply never looked up: it isn't mapped to anything, on principle, not detected and
// aliased to its closest match (trunk's own catalog has at least one real inconsistency of this
// exact shape -- see the design spec). A table entry whose group is absent from a given pattern
// (e.g. no severity, no col) leaves that Finding field zero-valued, the same convention every
// other parser in this package already uses. Real trunk docs mark "path" as the only required
// group; a match missing it produces a Finding with an empty File rather than erroring, since a
// missing group is a config-authoring mistake in the catalog, not a signal to fail the whole run.
func ParseFromRegex(pattern string, data []byte, linter string) ([]Finding, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	names := re.SubexpNames()

	var findings []Finding
	for _, m := range re.FindAllStringSubmatch(string(data), -1) {
		f := Finding{Linter: linter}
		for i, name := range names {
			if i == 0 || i >= len(m) {
				continue
			}
			switch name {
			case "path":
				f.File = m[i]
			case "line":
				f.Line, _ = strconv.Atoi(m[i])
			case "col":
				f.Column, _ = strconv.Atoi(m[i])
			case "code":
				f.RuleID = m[i]
			case "message":
				f.Message = m[i]
			case "severity":
				f.Severity = regexSeverity(m[i])
			}
		}
		findings = append(findings, f)
	}
	return findings, nil
}

// regexSeverity normalizes trunk's real 8-value severity vocabulary (note, notice, allow, deny,
// disabled, error, info, warning -- per trunk's own docs) down to this project's existing 3-value
// Finding.Severity convention, the same way every other parser in this package already does via
// its own xxxSeverity helper. "allow"/"deny"/"disabled" describe a severity *override*
// configuration, not something a linter's own text output would literally emit -- included here
// for completeness against the documented vocabulary, not because any real captured output uses
// them.
func regexSeverity(s string) string {
	switch s {
	case "error", "deny":
		return "error"
	case "warning", "allow":
		return "warning"
	default: // "info", "note", "notice", "disabled", or anything else
		return "info"
	}
}
```

- [ ] **Step 3: Write `pkg/trunk/output/regex_test.go`**

One test per real catalog pattern this project has already confirmed compiles (see the design
spec's verification), asserting exact `Finding` values, plus the "unmapped group name" and
"missing optional group" behaviors explicitly:

```go
package output

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseFromRegex_Perlcritic(t *testing.T) {
	pattern := `path=(?P<path>.*?),line=(?P<line>\d+),col=(?P<col>\d+),code=(?P<code>.*?),message=(?P<message>.*)`
	data := "path=lib/Foo.pm,line=12,col=3,code=Perl::Critic::Policy::Foo,message=some violation\n"
	got, err := ParseFromRegex(pattern, []byte(data), "perlcritic")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, Finding{
		Linter: "perlcritic", File: "lib/Foo.pm", Line: 12, Column: 3,
		Severity: "", RuleID: "Perl::Critic::Policy::Foo", Message: "some violation",
	}, got[0], "perlcritic's real parse_regex has no severity group at all -- Severity must stay empty, not default to anything")
}

func TestParseFromRegex_Yamllint(t *testing.T) {
	pattern := `((?P<path>.*):(?P<line>\d+):(?P<col>\d+): \[(?P<severity>.*)\] (?P<message>.*) \((?P<code>.*)\))`
	data := ".trunk/trunk.yaml:7:81: [error] line too long (82 > 80 characters) (line-length)\n"
	got, err := ParseFromRegex(pattern, []byte(data), "yamllint")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, Finding{
		Linter: "yamllint", File: ".trunk/trunk.yaml", Line: 7, Column: 81,
		Severity: "error", RuleID: "line-length", Message: "line too long (82 > 80 characters)",
	}, got[0], "this is trunk's own real documented example -- see the design spec")
}

func TestParseFromRegex_GitDiffCheck_NoColumnOrCodeGroup(t *testing.T) {
	pattern := `((?P<path>.*):(?P<line>-?\d+):(?P<message>.*))`
	data := "foo.go:12: trailing whitespace.\n"
	got, err := ParseFromRegex(pattern, []byte(data), "git-diff-check")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, Finding{
		Linter: "git-diff-check", File: "foo.go", Line: 12, Column: 0,
		Severity: "", RuleID: "", Message: " trailing whitespace.",
	}, got[0], "no col/severity/code group in this pattern at all -- those Finding fields must stay zero-valued")
}

func TestParseFromRegex_MultipleMatches(t *testing.T) {
	pattern := `(?P<path>.*):(?P<line>\d+): (?P<message>.*)`
	data := "a.txt:1: first\nb.txt:2: second\n"
	got, err := ParseFromRegex(pattern, []byte(data), "fake")
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "a.txt", got[0].File)
	assert.Equal(t, "b.txt", got[1].File)
}

func TestParseFromRegex_UnmappedGroupNameIsIgnoredNotAliased(t *testing.T) {
	// Mirrors ty's real parse_regex, which names its column group "column", not "col". "column"
	// has no entry in ParseFromRegex's mapping table -- must NOT be treated as an alias for "col".
	pattern := `(?P<path>.*):(?P<line>\d+):(?P<column>\d+): (?P<message>.*)`
	data := "a.py:1:5: bad thing\n"
	got, err := ParseFromRegex(pattern, []byte(data), "ty")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, 0, got[0].Column, `"column" is not "col" -- must stay unmapped, never guessed`)
}

func TestParseFromRegex_SeverityNormalization(t *testing.T) {
	pattern := `(?P<path>.*):(?P<severity>\w+):(?P<message>.*)`
	for raw, want := range map[string]string{
		"error": "error", "deny": "error",
		"warning": "warning", "allow": "warning",
		"info": "info", "note": "info", "notice": "info", "disabled": "info", "unknown": "info",
	} {
		got, err := ParseFromRegex(pattern, []byte("f.txt:"+raw+":msg\n"), "fake")
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, want, got[0].Severity, "raw severity %q", raw)
	}
}

func TestParseFromRegex_InvalidPattern(t *testing.T) {
	_, err := ParseFromRegex(`(unterminated`, []byte("x"), "fake")
	assert.Error(t, err)
}

func TestParseFromRegex_NoMatchesReturnsEmpty(t *testing.T) {
	got, err := ParseFromRegex(`(?P<path>.*):(?P<line>\d+): (?P<message>.*)`, []byte("not matching at all\n"), "fake")
	require.NoError(t, err)
	assert.Empty(t, got)
}
```

- [ ] **Step 4: Delete `ParsePerlCritic`/`ParseGenericRegex` and their tests**

These were never moved in Task 1 -- confirm no file in `pkg/trunk/output/` still defines
`ParsePerlCritic`, `ParseGenericRegex`, `perlCriticLineRE`, `genericRegexLineRE`, or
`genericRegexSeverityRE` (they should only ever have existed in the now-deleted
`pkg/trunk/check/parse.go`/`parse_test.go` from Task 1 -- this step is a verification, not new
deletion work, unless Task 1 was done before this plan's final review and missed something).

- [ ] **Step 5: Run tests**

```bash
go build ./pkg/trunk/output/... ./pkg/trunk/config/... && go vet ./pkg/trunk/output/... ./pkg/trunk/config/...
go test ./pkg/trunk/output/... ./pkg/trunk/config/... -count=1 -v
```

- [ ] **Step 6: Commit**

```bash
git add pkg/trunk/config/definitions.go pkg/trunk/output/regex.go pkg/trunk/output/regex_test.go
git commit -S -m "$(cat <<'EOF'
+[config,output]: Add declarative Command.ParseRegex parsing

Every real trunk-io Output: "regex" command's plugin.yaml carries a
parse_regex field -- a regex with named capture groups (path, line,
col, code, message, severity) describing that tool's exact output
shape. Command never had a field to receive it, so yaml.v3 silently
dropped it on every unmarshal since v0.1 -- confirmed against both
the real cached plugin repo and this project's own resolved config
output. ParseFromRegex replaces v0.3.1's ParsePerlCritic (a
hand-transcribed duplicate of perlcritic's own parse_regex) and its
best-effort ParseGenericRegex (whose documented false negatives for
biome/rome/djlint/ty/dustilock/stringslint are now moot -- their real
parse_regex values parse correctly, including biome/rome's
multi-line shapes, with zero special-casing).

Assisted-by: anthropic:claude-sonnet-5
EOF
)"
```

---

### Task 3: `pkg/trunk/engine/security` -- move + harden `RunFrom`/`SandboxType`

**Files:**

- Create: `pkg/trunk/engine/security/runfrom.go`, `runfrom_test.go`, `sandbox.go`,
  `sandbox_test.go`
- Delete: `pkg/trunk/check/runfrom.go`, `runfrom_test.go`, `sandbox.go`, `sandbox_test.go`

**Interfaces:**

- Produces: `security.ResolveRunFrom(runFrom, target, repoRoot string, directConfigs []string)
(dir string, ok bool)`, `security.StageSandbox(sandboxType, dir string, targets []string)
(sandboxDir string, cleanup func(), err error)`, `security.RemapFindings(findings
[]output.Finding, base, repoRoot string)` -- all now exported (capitalized) since this is a
  separate package from its caller; Task 4's `engine` package imports all three.
- Consumes: `output.Finding` (Task 1) -- `RemapFindings`'s signature changes from `[]Finding` (the
  old package-local type) to `[]output.Finding`.

This is a move (package-private -> exported, `check` -> `security`) plus **two security fixes**
plus **new adversarial tests**, not a behavior-preserving-only task like Task 1 -- read the design
spec's "Security" and "Testing approach" sections before starting.

- [ ] **Step 1: Read the current files**

Read `pkg/trunk/check/runfrom.go`, `runfrom_test.go`, `sandbox.go`, `sandbox_test.go` in full.

- [ ] **Step 2: Move `runfrom.go` -> `security/runfrom.go`, exporting `resolveRunFrom`**

Same body as today, `package security`, with:

- `resolveRunFrom` renamed `ResolveRunFrom` (exported -- this package's only real caller is now
  outside the package).
- `walkUpFor`, `anyFileExists`, `anyEntryMatches`, `runFromWithFileRE`, `runFromWithRegexRE` stay
  unexported (package-internal helpers, no outside caller needs them).
- Update the function's own doc comment's first word to match its new exported name
  (`// ResolveRunFrom resolves...`).

- [ ] **Step 3: Move `runfrom_test.go` -> `security/runfrom_test.go`**

Same tests, `package security`, every call site's `resolveRunFrom(...)` becomes
`ResolveRunFrom(...)`.

- [ ] **Step 4: Move `sandbox.go` -> `security/sandbox.go`, exporting + fixing the path-traversal
      gap**

Same body as today, `package security`, `stageSandbox` -> `StageSandbox`, `remapFindings` ->
`RemapFindings` (its `findings []Finding` parameter becomes `findings []output.Finding` -- add
`"github.com/xunleii/rtunk/pkg/trunk/output"` to imports), `copySandboxFile` stays unexported.
Then add the fix:

```go
// copySandboxFile copies src to dst, creating dst's parent directories as needed, preserving
// src's file mode. Refuses to write outside sandboxDir even if dst was computed from a rel that
// somehow escaped it (see StageSandbox's callers -- Files() is expected to reject an out-of-repo
// path before it ever reaches here, but this function does not trust that upstream check alone: a
// path-traversal write here would be a real security bug, not a cosmetic one, so it is guarded
// independently).
func copySandboxFile(sandboxDir, src, dst string) error {
	relCheck, err := filepath.Rel(sandboxDir, dst)
	if err != nil || relCheck == ".." || strings.HasPrefix(relCheck, ".."+string(filepath.Separator)) || filepath.IsAbs(relCheck) {
		return fmt.Errorf("security: refusing to stage %q outside sandbox %q", dst, sandboxDir)
	}

	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
```

(`copySandboxFile` gains a `sandboxDir` parameter -- update its two call sites inside
`StageSandbox` to pass it: `copySandboxFile(sandboxDir, filepath.Join(dir, rel),
filepath.Join(sandboxDir, rel))`. Add `"fmt"` and `"strings"` to this file's imports.)

- [ ] **Step 5: Move `sandbox_test.go` -> `security/sandbox_test.go`**

Same tests, `package security`, `stageSandbox`/`remapFindings` -> `StageSandbox`/`RemapFindings`,
`remapFindings`'s `Finding` literals become `output.Finding` (add the import). Add these new
tests:

```go
func TestCopySandboxFile_RefusesPathOutsideSandbox(t *testing.T) {
	sandboxDir := t.TempDir()
	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "target.txt")
	require.NoError(t, os.WriteFile(src, []byte("x"), 0o644))

	// A dst outside sandboxDir must be refused, even called directly with a malicious-looking
	// destination -- proving the guard holds independently of how StageSandbox itself computes dst.
	escapedDst := filepath.Join(sandboxDir, "..", "..", "etc", "evil.txt")
	err := copySandboxFile(sandboxDir, src, escapedDst)
	require.Error(t, err)

	_, statErr := os.Stat(escapedDst)
	assert.True(t, os.IsNotExist(statErr), "the escaped file must never actually be written")
}

func TestStageSandbox_ExpandedHandlesManyFilesWithoutPathologicalBehavior(t *testing.T) {
	dir := t.TempDir()
	const n = 500
	for i := 0; i < n; i++ {
		require.NoError(t, os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%03d.txt", i)), []byte("x"), 0o644))
	}

	sandboxDir, cleanup, err := StageSandbox("expanded", dir, []string{"f000.txt"})
	require.NoError(t, err)
	defer cleanup()

	entries, err := os.ReadDir(sandboxDir)
	require.NoError(t, err)
	assert.Len(t, entries, n, "every file must be staged, none silently dropped")
}

func TestCopySandboxFile_SymlinkedTargetCopiesContentNotLinkItself(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.txt")
	require.NoError(t, os.WriteFile(real, []byte("real content"), 0o644))
	linkDir := t.TempDir()
	link := filepath.Join(linkDir, "link.txt")
	require.NoError(t, os.Symlink(real, link))

	sandboxDir := t.TempDir()
	dst := filepath.Join(sandboxDir, "link.txt")
	require.NoError(t, copySandboxFile(sandboxDir, link, dst))

	data, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, "real content", string(data), "os.Open follows the symlink -- the staged copy must be a real, independent file, not another link")

	info, err := os.Lstat(dst)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0), info.Mode()&os.ModeSymlink, "the staged copy itself must not be a symlink")
}
```

- [ ] **Step 6: Delete the old files and verify**

```bash
rm pkg/trunk/check/runfrom.go pkg/trunk/check/runfrom_test.go pkg/trunk/check/sandbox.go pkg/trunk/check/sandbox_test.go
go build ./pkg/trunk/engine/security/... && go vet ./pkg/trunk/engine/security/... && go test ./pkg/trunk/engine/security/... -count=1 -race -v
```

- [ ] **Step 7: Commit**

```bash
git add pkg/trunk/engine/security/ pkg/trunk/check/runfrom.go pkg/trunk/check/runfrom_test.go pkg/trunk/check/sandbox.go pkg/trunk/check/sandbox_test.go
git commit -S -m "$(cat <<'EOF'
$[engine]: Move RunFrom/SandboxType to engine/security, fix path-traversal gap

Moves resolveRunFrom/stageSandbox/remapFindings out of check
(exported, since a future pkg/trunk/fmt needs them too) and fixes a
real gap found while researching this move: copySandboxFile computed
its destination via filepath.Join(sandboxDir, rel) with no guarantee
rel couldn't contain ".." -- a target outside repoRoot (e.g. `rtunk
check /etc`, nothing today validates CLI path arguments stay inside
the repo) could make a SandboxType: copy_targets/expanded command
write outside its own throwaway temp directory. copySandboxFile now
refuses to write anywhere outside its own sandboxDir, independent of
whatever validation its caller does or doesn't do upstream (Task 4
adds the complementary boundary check in Files()). Also adds
adversarial coverage this package never had: a direct proof the new
guard holds even when called with an escaping path, a
many-files staging test, and a symlinked-target test.

Assisted-by: anthropic:claude-sonnet-5
EOF
)"
```

---

### Task 4: `pkg/trunk/engine` -- move `match.go` + `run.go`, add `Env`/`context.Context`/predicate

**Files:**

- Create: `pkg/trunk/engine/engine.go` (from `check/run.go`), `pkg/trunk/engine/engine_test.go`
  (from `check/run_test.go`), `pkg/trunk/engine/match.go` (from `check/match.go`),
  `pkg/trunk/engine/match_test.go` (from `check/match_test.go`)
- Delete: `pkg/trunk/check/run.go`, `run_test.go`, `match.go`, `match_test.go`

**Interfaces:**

- Consumes: `output.Finding`/`output.ApplyIssueURL`/`output.ParseXxx`/`output.ParseFromRegex`
  (Tasks 1-2), `security.ResolveRunFrom`/`security.StageSandbox`/`security.RemapFindings` (Task 3).
- Produces: `engine.Env`, `engine.Phase`, `engine.Event`, `engine.Run(ctx context.Context, env
Env, paths []string, include func(config.Command) bool) (<-chan Event, error)` -- Task 5's
  `pkg/trunk/check` is this package's only consumer today.

This is the largest task: the job-queue/worker-pool engine moves wholesale, gains `context.Context`
threading (real cancellation via `exec.CommandContext`, not a bespoke stop mechanism), an `Env`
struct replacing the `cfg, cacheDir, repoRoot, concurrency` parameter list, a `filter
func(config.Command) bool` parameter replacing the hardcoded `if cmd.Formatter { continue }`, and
`Files()` gains the path-traversal boundary check (Task 3's complementary fix).

- [ ] **Step 1: Read the current files**

Read `pkg/trunk/check/run.go`, `run_test.go`, `match.go`, `match_test.go` in full.

- [ ] **Step 2: Write `pkg/trunk/engine/match.go`**

Move `Files`, `filterGitignored`, `matchesAny`, `matchesFileType`, `matchesExtension`,
`matchesFilename`, `matchesRegex`, `matchesShebang`, `matchesRequiredYAMLKeys` verbatim,
`package engine`, same imports, with one addition -- the path-traversal boundary check:

```go
// Package engine implements the check/fmt-shared job-queue execution engine: matching files
// against a linter's Files criteria, resolving RunFrom/SandboxType (see engine/security), and
// running commands against the result. See docs/superpowers/specs/
// 2026-09-12-check-engine-refactor-design.md for the full design.
package engine

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/xunleii/rtunk/pkg/trunk/config"
	"gopkg.in/yaml.v3"
)

// Files resolves which files under paths match linter's Files (ids into cfg.Lint.Files),
// walking directories recursively (skipping .git), then drops any match git considers ignored
// (see filterGitignored). repoRoot anchors the gitignore lookup -- it need not equal paths, e.g.
// paths can be a subset of repoRoot passed explicitly on the command line. A directory entry
// that is itself a file (not a directory) is taken as-is, matched or not, without a walk.
//
// Every path in paths must resolve inside repoRoot -- a path outside it is rejected outright
// (rather than silently matched and only failing later, deep inside sandbox staging, if any
// enabled linter's command happens to use SandboxType: copy_targets/expanded; see the design
// spec's "Security" section for the real gap this closes).
//
// ponytail: walks the filesystem once per linter call rather than combining every enabled
// linter's file set into one shared walk -- simpler, correct, and fine at v0.3's scale; combine
// them if `rtunk check` on a large repo with many enabled linters gets slow.
func Files(cfg config.Config, linter config.Linter, repoRoot string, paths []string) ([]string, error) {
	if len(linter.Files) == 0 {
		return nil, nil
	}

	for _, p := range paths {
		rel, err := filepath.Rel(repoRoot, p)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("engine: path %q is outside repository root %q", p, repoRoot)
		}
	}

	var out []string
	seen := make(map[string]bool)
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			if !seen[p] && matchesAny(cfg.Lint.Files, linter.Files, p) {
				seen[p] = true
				out = append(out, p)
			}
			continue
		}

		err = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == ".git" {
					return filepath.SkipDir
				}
				return nil
			}
			if seen[path] {
				return nil
			}
			if matchesAny(cfg.Lint.Files, linter.Files, path) {
				seen[path] = true
				out = append(out, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return filterGitignored(repoRoot, out), nil
}
```

(`filterGitignored` and everything below it in the current `match.go` are unchanged -- copy them
verbatim after `Files`.)

- [ ] **Step 3: Write `pkg/trunk/engine/match_test.go`**

Move every existing test verbatim, `package engine`, plus one new test for the boundary check
(all existing `dir := t.TempDir()` + `paths: []string{dir}` fixtures already pass `dir` as both
`repoRoot` and the sole path, so they're unaffected by the new check -- it only rejects a path that
is genuinely outside `repoRoot`):

```go
func TestFiles_RejectsPathOutsideRepoRoot(t *testing.T) {
	repoRoot := t.TempDir()
	outside := t.TempDir() // a sibling temp dir, guaranteed not under repoRoot
	mustWrite(t, filepath.Join(outside, "file.go"), "package main\n")

	cfg := config.Config{Lint: config.LintConfig{Files: map[string]config.FileType{
		"go": {Name: "go", Extensions: []string{"go"}},
	}}}
	_, err := Files(cfg, config.Linter{Files: []string{"go"}}, repoRoot, []string{outside})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "outside repository root")
}
```

- [ ] **Step 4: Write `pkg/trunk/engine/engine.go`**

Move `Phase`, `Event`, `job`, `linterState`, `Run`, `buildJobs`, `groupByRunFrom`, `sortedKeys`,
`findUnsupportedVar`, `runJob`, `runBatch`, `containsInt`, `resolveShimDirs`, `runOneInvocation`,
`quoteAll` from `check/run.go`, with these changes:

1. `package engine`; drop the package doc comment here (already on `match.go` in this same
   package -- one doc comment per package, not two, matching this project's own existing
   convention of never duplicating a package doc comment across files in one package).
2. Import `"context"` and `"github.com/xunleii/rtunk/pkg/trunk/output"` and
   `"github.com/xunleii/rtunk/pkg/trunk/engine/security"`.
3. New `Env` struct, and `Run`'s signature changes to take `ctx context.Context`, `env Env`, and
   `include func(config.Command) bool` instead of separate `cfg`/`cacheDir`/`repoRoot`/
   `concurrency` parameters.
4. `job.cmd`'s formatter-skip check (`if cmd.Formatter { continue }`) becomes `if !include(cmd) {
continue }`.
5. `resolveRunFrom`/`stageSandbox`/`remapFindings` calls become
   `security.ResolveRunFrom`/`security.StageSandbox`/`security.RemapFindings`.
6. `ParseSARIF`/`ParsePassFail`/`ParseActionlint`/.../`ParseTaplo` calls become
   `output.ParseSARIF`/`output.ParsePassFail`/.../`output.ParseTaplo`; the `Output: "regex"` case
   in `runBatch`'s switch changes from the old linter-name-keyed dispatch
   (`perlcritic`-vs-`default`) to a single call: `findings, err =
output.ParseFromRegex(j.cmd.ParseRegex, []byte(out), j.linterName)`.
7. `runOneInvocation` uses `exec.CommandContext(ctx, "sh", "-c", run)` instead of
   `exec.Command(...)`, and takes `ctx context.Context` as its first parameter.
8. Each worker goroutine checks `ctx.Err()` before picking up its next job (not mid-invocation --
   an in-flight invocation is only interrupted by `exec.CommandContext` itself killing the
   subprocess when `ctx` is canceled).
9. `Finding` (the type) is now `output.Finding` everywhere it appeared (`linterState.findings`,
   `runBatch`'s return type, etc.) -- this package defines no `Finding` type of its own anymore.

```go
package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/xunleii/rtunk/pkg/trunk/config"
	"github.com/xunleii/rtunk/pkg/trunk/download"
	"github.com/xunleii/rtunk/pkg/trunk/engine/security"
	"github.com/xunleii/rtunk/pkg/trunk/output"
)

// Env is every piece of shared configuration a job needs to run.
type Env struct {
	Cfg         config.Config
	RepoRoot    string // always absolute, see Run
	CacheDir    string
	Concurrency int // workers, at least 1 -- Run clamps a lower value up to 1
}

// Phase is one linter's point in the run lifecycle.
type Phase int

const (
	Running Phase = iota // File is set; a progress update, not a terminal outcome
	Done                 // Findings is set (possibly empty -- the command ran clean)
	Skipped              // an unsupported feature; Note says which -- never an error
	Failed               // the command itself errored; Err is set
)

// Event reports one linter's run progress or outcome, streamed on the channel Run returns. A
// linter with no matching files, or none of whose commands pass include, produces no event at all
// -- there is nothing to report. A linter that does run produces one Running event per command
// invocation (per batch, or per file for a non-Batch command), followed by exactly one terminal
// event (Done, Skipped, or Failed).
type Event struct {
	Linter   string
	Phase    Phase
	Findings []output.Finding // Done only
	Note     string           // Skipped (why) or Failed (which command)
	Err      error            // Failed only
	File     string           // Running only -- the file (or comma-joined batch) about to be checked
}

// templateVarRE matches every ${...} placeholder in a Command.Run string.
var templateVarRE = regexp.MustCompile(`\$\{[^}]*\}`)

// supportedOutputFormats is every Command.Output value this package knows how to parse -- anything
// else is Skipped. "taplo" gets its own top-level dispatch (its real Command.Output value, not a
// "regex" special case). "regex" is always dispatched through output.ParseFromRegex, using the
// command's own ParseRegex field.
var supportedOutputFormats = map[string]bool{
	"sarif": true, "sarif_uri": true, "pass_fail": true,
	"actionlint": true, "bandit": true, "buildifier": true, "cfnlint": true,
	"eslint": true, "hadolint": true, "haml_lint": true, "markdownlint": true,
	"pylint": true, "rubocop": true, "stylelint": true, "taplo": true, "regex": true,
}

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

// linterState accumulates one linter's concurrently-completing jobs into the single terminal
// event (Done or Failed) a sequential run would send once its last command finished. Jobs for the
// same linter can now finish on different workers in any order, so "is this linter done" is
// tracked by a remaining-jobs counter guarded by mu, not by loop position.
type linterState struct {
	mu           sync.Mutex
	remaining    int
	findings     []output.Finding
	terminalSent bool
	failed       bool // once true, workers skip any not-yet-started job for this linter
}

// Run executes every command include selects, across every enabled linter, against the files
// matched under paths (env.RepoRoot is the default walk root when paths is empty, and every
// command's default working directory), downloading any missing tool shim first, and streams one
// Event per linter that had something to report. env.Cfg is expected already
// enabled+used-trimmed (config.Resolve's output).
//
// ctx cancellation stops the run: exec.CommandContext kills an in-flight linter subprocess the
// moment ctx is canceled, and each worker checks ctx.Err() before picking up its next queued job,
// so cancellation also stops new work from starting, not just kills whatever's already running.
//
// include selects which of a linter's commands are runnable -- pkg/trunk/check passes
// `func(c config.Command) bool { return !c.Formatter }`; a future pkg/trunk/fmt passes the
// complement. Every other check-specific behavior (Output-format dispatch, Finding-based
// reporting) is unaffected by this predicate -- it only decides which commands are considered.
//
// env.Concurrency workers (at least 1) run the queued command invocations in parallel; the queue
// itself is built sequentially and in a fixed order -- linters sorted by name, each linter's own
// batches sorted by resolved directory then file -- so which job a worker happens to pick up next
// is the only source of nondeterminism, never the queue's own order.
func Run(ctx context.Context, env Env, paths []string, include func(config.Command) bool) (<-chan Event, error) {
	root, err := download.Root(env.CacheDir)
	if err != nil {
		return nil, err
	}

	// Every match Files() returns is later relativized against repoRoot (here, for gitignore
	// lookups; below, for RunFrom resolution) via filepath.Rel, which errors outright if one side
	// is absolute and the other relative -- e.g. `rtunk check .` passes paths=["."], a relative
	// walk root, while repoRoot is always absolute. Absolutizing both up front means every path
	// Files() walks and returns is comparable to repoRoot.
	repoRoot, err := filepath.Abs(env.RepoRoot)
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		paths = []string{repoRoot}
	} else {
		for i, p := range paths {
			abs, err := filepath.Abs(p)
			if err != nil {
				return nil, err
			}
			paths[i] = abs
		}
	}

	concurrency := env.Concurrency
	if concurrency < 1 {
		concurrency = 1
	}

	events := make(chan Event)
	go func() {
		defer close(events)

		names := make([]string, 0, len(env.Cfg.Lint.Definitions))
		for name := range env.Cfg.Lint.Definitions {
			names = append(names, name)
		}
		sort.Strings(names)

		states := make(map[string]*linterState, len(names))
		var jobs []job
		for _, name := range names {
			linterJobs := buildJobs(env.Cfg, root, env.CacheDir, repoRoot, name, env.Cfg.Lint.Definitions[name], paths, include, events)
			if len(linterJobs) == 0 {
				continue
			}
			states[name] = &linterState{remaining: len(linterJobs)}
			jobs = append(jobs, linterJobs...)
		}

		jobCh := make(chan job, len(jobs))
		for _, j := range jobs {
			jobCh <- j
		}
		close(jobCh)

		var wg sync.WaitGroup
		for i := 0; i < concurrency; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := range jobCh {
					if ctx.Err() != nil {
						return
					}
					runJob(ctx, j, states[j.linterName], repoRoot, events)
				}
			}()
		}
		wg.Wait()
	}()
	return events, nil
}

// buildJobs resolves name's matched files and queues one job per runnable command invocation
// (include selects which commands are runnable), emitting a Skipped event immediately for every
// command an unsupported feature rules out (var, output format, parser -- unchanged from v0.3.1;
// SandboxType/RunFrom attempt real resolution instead of a blanket skip, per v0.3.2) and a Failed
// event (returning no jobs) if matching files or resolving tools errors outright. Shim resolution
// -- which may download a tool -- runs at most once per linter, lazily, on the first command that
// needs it.
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
		if cmd.Parser != nil {
			events <- Event{Linter: name, Phase: Skipped, Note: "unsupported parser (native output requires a converter script)"}
			continue
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
				batches = [][]string{relFiles}
			} else {
				for _, f := range relFiles {
					batches = append(batches, []string{f})
				}
			}
			for _, batch := range batches {
				jobs = append(jobs, job{
					linterName: name, linter: linter, cmd: cmd, batch: batch,
					pathEnv: pathEnv, resolvedDir: dir,
				})
			}
		}
	}
	return jobs
}

// groupByRunFrom resolves runFrom for every file in files (absolute paths, already matched under
// repoRoot), grouping them by resolved directory -- each group's files are returned relative to
// that directory (sorted), ready for ${target} substitution once the invocation's cwd becomes that
// directory (or a sandbox mirroring it). ok is false if runFrom isn't a recognized form.
func groupByRunFrom(runFrom string, files []string, repoRoot string, directConfigs []string) (map[string][]string, bool) {
	groups := map[string][]string{}
	for _, f := range files {
		dir, ok := security.ResolveRunFrom(runFrom, f, repoRoot, directConfigs)
		if !ok {
			return nil, false
		}
		rel, err := filepath.Rel(dir, f)
		if err != nil {
			return nil, false
		}
		groups[dir] = append(groups[dir], rel)
	}
	for dir := range groups {
		sort.Strings(groups[dir])
	}
	return groups, true
}

func sortedKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

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

// runJob executes one queued job: sends a Running event, runs the invocation, then folds the
// result into state -- findings accumulate across every job of the same linter, the first failure
// marks the linter failed (any of its not-yet-started jobs are then skipped, best-effort: a job
// already picked up by a worker still runs to completion), and the linter's single terminal event
// fires exactly once, the moment its last job finishes.
func runJob(ctx context.Context, j job, state *linterState, repoRoot string, events chan<- Event) {
	state.mu.Lock()
	if state.failed {
		state.mu.Unlock()
		return
	}
	state.mu.Unlock()

	events <- Event{Linter: j.linterName, Phase: Running, File: strings.Join(j.batch, ", ")}
	findings, err := runBatch(ctx, j, repoRoot)

	state.mu.Lock()
	defer state.mu.Unlock()
	state.remaining--
	if state.terminalSent {
		return
	}

	if err != nil {
		state.failed = true
		state.terminalSent = true
		events <- Event{Linter: j.linterName, Phase: Failed, Note: j.cmd.Name, Err: err}
		return
	}

	state.findings = append(state.findings, findings...)
	if state.remaining == 0 {
		state.terminalSent = true
		events <- Event{Linter: j.linterName, Phase: Done, Findings: state.findings}
	}
}

// runBatch runs one job's invocation and parses its output per cmd.Output, remapping every
// finding's File back to repoRoot-relative before returning. If j.cmd.SandboxType is set, the
// invocation actually runs against a temporary staged copy (see security.StageSandbox); the
// parser only ever sees paths relative to j.resolvedDir, exactly as when no sandboxing is
// involved -- security.RemapFindings is what turns those back into repoRoot-relative paths either
// way.
func runBatch(ctx context.Context, j job, repoRoot string) ([]output.Finding, error) {
	workDir := j.resolvedDir
	if j.cmd.SandboxType != "" {
		sandboxDir, cleanup, err := security.StageSandbox(j.cmd.SandboxType, j.resolvedDir, j.batch)
		if cleanup != nil {
			defer cleanup()
		}
		if err != nil {
			return nil, err
		}
		workDir = sandboxDir
	}

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

	// Real plugin data confirms SuccessCodes already enumerates every "ran fine, here's the
	// verdict" code, "found issues" included (e.g. ansible-lint sarif: [0,2,5]) -- there is no
	// third bucket. Every exit code not in ErrorCodes parses normally.
	var findings []output.Finding

	// Every JSON-shaped Output format's parser fails to unmarshal empty/whitespace-only input
	// ("unexpected end of JSON input"), which would otherwise abort this whole Run for every
	// linter, not just this one. A real linter can produce genuinely empty output on a clean run
	// (e.g. markdownlint), or when an OS/version-gated command variant of the same linter
	// silently produces nothing on this platform (a known, separate architectural gap -- out of
	// scope here) -- either way, empty output means zero findings, not a parse failure. "pass_fail"
	// never parses JSON (exit-code only) and "regex" (output.ParseFromRegex) already tolerates
	// empty input (zero regex matches), so both are excluded from this guard.
	isJSONFormat := j.cmd.Output != "pass_fail" && j.cmd.Output != "regex"
	if isJSONFormat && strings.TrimSpace(out) == "" {
		security.RemapFindings(findings, j.resolvedDir, repoRoot)
		output.ApplyIssueURL(findings, j.linter.IssueURLFormat)
		return findings, nil
	}

	switch j.cmd.Output {
	case "sarif", "sarif_uri":
		// sarif_uri (checkov): ReadOutputFrom "tmp_file" already resolved the real SARIF bytes
		// written to ${tmpfile} into out -- no separate parser needed.
		findings, err = output.ParseSARIF([]byte(out), j.linterName)
	case "pass_fail":
		if exitCode != 0 {
			findings = output.ParsePassFail(j.linterName, j.batch)
		}
	case "actionlint":
		findings, err = output.ParseActionlint([]byte(out), j.linterName)
	case "bandit":
		findings, err = output.ParseBandit([]byte(out), j.linterName)
	case "buildifier":
		findings, err = output.ParseBuildifier([]byte(out), j.linterName)
	case "cfnlint":
		findings, err = output.ParseCfnLint([]byte(out), j.linterName)
	case "eslint":
		findings, err = output.ParseESLint([]byte(out), j.linterName)
	case "hadolint":
		findings, err = output.ParseHadolint([]byte(out), j.linterName)
	case "haml_lint":
		findings, err = output.ParseHamlLint([]byte(out), j.linterName)
	case "markdownlint":
		findings, err = output.ParseMarkdownlint([]byte(out), j.linterName)
	case "pylint":
		findings, err = output.ParsePylint([]byte(out), j.linterName)
	case "rubocop":
		findings, err = output.ParseRubocop([]byte(out), j.linterName)
	case "stylelint":
		findings, err = output.ParseStylelint([]byte(out), j.linterName)
	case "taplo":
		findings, err = output.ParseTaplo([]byte(out), j.linterName)
	case "regex":
		findings, err = output.ParseFromRegex(j.cmd.ParseRegex, []byte(out), j.linterName)
	}
	if err != nil {
		return nil, err
	}
	security.RemapFindings(findings, j.resolvedDir, repoRoot)
	output.ApplyIssueURL(findings, j.linter.IssueURLFormat)
	return findings, nil
}

func containsInt(codes []int, code int) bool {
	for _, c := range codes {
		if c == code {
			return true
		}
	}
	return false
}

// resolveShimDirs resolves (downloading first if not already cached) every tool id's shim, and
// returns the directory each shim lives in -- a Command.Run string references its tool(s) by bare
// name, so those directories become the PATH prefix that lets `sh -c` find them.
func resolveShimDirs(cfg config.Config, root, cacheDir string, toolIDs []string) ([]string, error) {
	dirs := make([]string, 0, len(toolIDs))
	for _, id := range toolIDs {
		tool, ok := cfg.Tools[id]
		if !ok {
			return nil, fmt.Errorf("engine: tool %q referenced but not found in resolved config", id)
		}
		version := download.ResolveVersion(cfg.Lint.Enabled, id, tool.KnownGoodVersion)
		shimPath := download.ShimPath(root, "tools", id, version, id)
		if _, statErr := os.Stat(shimPath); statErr != nil {
			evs, err := download.Download(cfg, cacheDir, download.Ref{Category: "tools", ID: id, Version: version})
			if err != nil {
				return nil, err
			}
			for ev := range evs {
				if ev.Phase == download.Failed {
					return nil, ev.Err
				}
			}
		}
		dirs = append(dirs, filepath.Dir(shimPath))
	}
	return dirs, nil
}

// runOneInvocation substitutes ${target}/${tmpfile} into cmd.Run and executes it through a shell
// (a Command.Run string is a shell command line referencing its tool(s) by bare name, not a
// path), with pathEnv prefixed onto PATH (verbatim PATH when pathEnv is empty -- a leading empty
// PATH component means "current directory" on POSIX, which would let workDir's own files shadow
// real binaries) and workDir as the working directory. ctx cancellation kills the subprocess
// immediately via exec.CommandContext. Returns the output named by cmd.ReadOutputFrom (default
// stdout), the process's raw stderr (always captured, regardless of ReadOutputFrom, so callers
// can surface it on a crash), and the exit code; err is only ever a launch failure (e.g. "sh"
// missing), never a non-zero exit -- callers read exitCode for that.
func runOneInvocation(ctx context.Context, cmd config.Command, workDir, pathEnv string, files []string) (output, stderrOut string, exitCode int, err error) {
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

	run := strings.NewReplacer("${target}", target, "${tmpfile}", tmpfile).Replace(cmd.Run)

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
		output = stderr.String()
	case "tmp_file":
		data, readErr := os.ReadFile(tmpfile)
		if readErr != nil {
			return "", stderr.String(), code, readErr
		}
		output = string(data)
	default: // "" or "stdout"
		output = stdout.String()
	}
	return output, stderr.String(), code, nil
}

func quoteAll(files []string) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = "'" + strings.ReplaceAll(f, "'", `'\''`) + "'"
	}
	return out
}
```

- [ ] **Step 5: Write `pkg/trunk/engine/engine_test.go`**

Move every test from `check/run_test.go` verbatim, with these mechanical updates throughout:

- `package engine`.
- Every `Run(cfg, cacheDir, repoRoot, paths, concurrency)` call becomes `Run(context.Background(),
Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: cacheDir, Concurrency: concurrency}, paths, func(c
config.Command) bool { return !c.Formatter })` -- every existing test's fixtures never set
  `Formatter: true` on a command they expect to RUN, so `!c.Formatter` reproduces today's behavior
  exactly for all of them.
- `Finding{...}` literals become `output.Finding{...}` (add the import).
- Add `"context"` and `"github.com/xunleii/rtunk/pkg/trunk/output"` to imports.
- `runOneInvocation(cmd, repoRoot, "", nil)` (in `TestRunOneInvocation_EmptyPathEnvHasNoCwdComponent`)
  becomes `runOneInvocation(context.Background(), cmd, repoRoot, "", nil)`.

Then add:

```go
// TestRun_ContextCancellationStopsNewWorkAndKillsInFlight covers "quick stop mid-work": a
// canceled context must (a) let an already-started invocation's subprocess actually die (proven
// by a fake tool that sleeps far longer than the test's own timeout budget -- if the subprocess
// weren't killed, this test would itself time out) and (b) prevent any not-yet-started job from
// running at all.
func TestRun_ContextCancellationStopsNewWorkAndKillsInFlight(t *testing.T) {
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
	for _, name := range []string{"a.txt", "b.txt", "c.txt", "d.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(repoRoot, name), []byte("x\n"), 0o644))
	}

	cfg := config.Config{
		Tools: map[string]config.Tool{"faketool": {Name: "faketool", KnownGoodVersion: "1.0.0"}},
		Lint: config.LintConfig{
			Files: map[string]config.FileType{},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"slow": {
						Name: "slow", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{{Name: "lint", Run: "faketool sleep ${target}", Output: "pass_fail"}},
					},
				},
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	events, err := Run(ctx, Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: cacheDir, Concurrency: 1}, nil, func(c config.Command) bool { return !c.Formatter })
	require.NoError(t, err)

	start := time.Now()
	cancel()
	for range events {
		// drain fully -- Run must still close the channel promptly after cancellation
	}
	elapsed := time.Since(start)

	// The fake tool's "sleep" case runs 250ms; this command has ${target} and Batch is unset, so
	// this is 4 separate per-file jobs, at concurrency 1 -- uncanceled, 4 sequential 250ms
	// invocations would take ~1s. Whichever race outcome actually happens here -- cancel() lands
	// before the first job is even picked up (the worker's ctx.Err() check stops it cold) or lands
	// after the first job's subprocess is already sleeping (exec.CommandContext kills it
	// immediately regardless) -- elapsed must stay far under even one full sleep, let alone four.
	assert.Less(t, elapsed, 200*time.Millisecond, "canceling must not wait out even one of the fake tool's 250ms sleeps, let alone all four")
}
```

(Add `"context"` and `"time"` to `engine_test.go`'s imports if not already present from the move.)

- [ ] **Step 6: Delete the old files and update `pkg/trunk/check`'s now-broken imports**

```bash
rm pkg/trunk/check/run.go pkg/trunk/check/run_test.go pkg/trunk/check/match.go pkg/trunk/check/match_test.go
```

`pkg/trunk/check` has no `.go` files left after this -- Task 5 recreates it. Do not attempt to
build `./...` yet; build only what exists:

```bash
go build ./pkg/trunk/engine/... && go vet ./pkg/trunk/engine/... && go test ./pkg/trunk/engine/... -count=1 -race -v
```

- [ ] **Step 7: Commit**

```bash
git add pkg/trunk/engine/ pkg/trunk/check/run.go pkg/trunk/check/run_test.go pkg/trunk/check/match.go pkg/trunk/check/match_test.go
git commit -S -m "$(cat <<'EOF'
>[engine]: Move check's execution engine into pkg/trunk/engine

Moves the job-queue/worker-pool (run.go) and file matching
(match.go) out of check into a package a future pkg/trunk/fmt can
share. Three real changes ride along with the move, not just a
relocation: Env replaces the growing cfg/repoRoot/cacheDir/
concurrency parameter list; a real context.Context now threads
through to exec.CommandContext, so canceling it kills an in-flight
linter subprocess immediately and stops any not-yet-started job --
"quick stop mid-work" via the stdlib mechanism built for exactly
this, not a bespoke one; and the hardcoded Formatter-command skip
becomes an `include func(config.Command) bool` parameter, so check
and a future fmt select opposite command subsets from the same
engine rather than each needing their own copy of it.

Assisted-by: anthropic:claude-sonnet-5
EOF
)"
```

---

### Task 5: Thin `pkg/trunk/check` adapter + `internal/cli/check.go` wiring

**Files:**

- Create: `pkg/trunk/check/check.go`
- Modify: `internal/cli/check.go`

**Interfaces:**

- Consumes: `engine.Env`/`engine.Run`/`engine.Event`/`engine.Phase` (Task 4),
  `output.Finding` (Task 1).
- Produces: `check.Run(ctx context.Context, env engine.Env, paths []string) (<-chan engine.Event,
error)` -- `internal/cli/check.go`'s only remaining `check.` call site.

- [ ] **Step 1: Read the current `internal/cli/check.go` in full**

- [ ] **Step 2: Write `pkg/trunk/check/check.go`**

```go
// Package check implements ROADMAP.md's v0.3 milestone: running enabled linters against source
// files and reporting findings, read-only. The actual job-queue engine, file matching, and
// RunFrom/SandboxType resolution live in pkg/trunk/engine (shared with a future pkg/trunk/fmt) --
// this package is the check-specific policy on top of it: run every non-formatter command. See
// docs/superpowers/specs/2026-09-12-check-engine-refactor-design.md for the full design.
package check

import (
	"context"

	"github.com/xunleii/rtunk/pkg/trunk/config"
	"github.com/xunleii/rtunk/pkg/trunk/engine"
)

// Run runs every enabled linter's non-formatter command against the files matched under paths,
// streaming one engine.Event per linter that had something to report. See engine.Run for the
// full contract (cancellation, concurrency, event semantics).
func Run(ctx context.Context, env engine.Env, paths []string) (<-chan engine.Event, error) {
	return engine.Run(ctx, env, paths, func(c config.Command) bool { return !c.Formatter })
}
```

- [ ] **Step 3: Update `internal/cli/check.go`**

Read the current file's `checkRunCmd.Run` method and every other place it references
`check.Finding`, `check.Event`, `check.Done`, `check.Skipped`, `check.Failed`, `check.Running`, or
calls `check.Run(cfg, cli.CacheDir, repoRoot, c.Paths, jobs)`. Update:

1. Add imports `"context"` and `"github.com/xunleii/rtunk/pkg/trunk/engine"`.
2. Every `check.Finding` -> `output.Finding` (add `"github.com/xunleii/rtunk/pkg/trunk/output"`
   import).
3. Every `check.Event`/`check.Done`/`check.Skipped`/`check.Failed`/`check.Running` ->
   `engine.Event`/`engine.Done`/`engine.Skipped`/`engine.Failed`/`engine.Running`.
4. The call site:

```go
	events, err := check.Run(context.Background(), engine.Env{
		Cfg: cfg, RepoRoot: repoRoot, CacheDir: cli.CacheDir, Concurrency: jobs,
	}, c.Paths)
```

replaces the current `events, err := check.Run(cfg, cli.CacheDir, repoRoot, c.Paths, jobs)`.
`context.Background()` here, not a cancelable context wired to e.g. SIGINT, is a deliberate
choice for this task -- wiring the CLI's own process-signal handling into a cancellation context
is a separate concern (worth its own small follow-up once `fmt` exists and both consumers need
it), not something to fold into this refactor's scope.

- [ ] **Step 4: Verify**

```bash
go build ./... && go vet ./... && go test ./... -count=1 -race
```

Expected: fully clean across every package.

- [ ] **Step 5: Real smoke test**

```bash
go run ./cmd/rtunk/main.go check . 2>&1 | tail -30
```

Compare the summary line's issue/file counts against the pre-refactor baseline (the same command
run on `main` before this branch) -- they must match exactly. This refactor changes zero runtime
behavior for any already-passing command; a count that differs means something moved wrong.

- [ ] **Step 6: Commit**

```bash
git add pkg/trunk/check/check.go internal/cli/check.go
git commit -S -m "$(cat <<'EOF'
=[check,cli]: Wire check onto the extracted engine package

check.Run is now a two-line adapter over engine.Run, selecting
non-formatter commands -- the entire check-specific surface left in
this package. internal/cli/check.go updates its imports accordingly
(check.Finding -> output.Finding, check.Event/Done/Skipped/Failed/
Running -> their engine.* equivalents). No behavior change: a real
`rtunk check .` run against this repo reports the same issue/file
counts before and after this refactor.

Assisted-by: anthropic:claude-sonnet-5
EOF
)"
```

---

## Final review

After Task 5, use `superpowers:subagent-driven-development`'s whole-branch final review
(dispatched on the most capable available model). Beyond the usual checklist, specifically verify:
the real smoke-test comparison in Task 5 Step 5 actually holds (re-run it independently, don't
just trust the task's own report); that `pkg/trunk/output`'s 11 moved JSON parsers' tests are
genuinely byte-for-byte the same fixtures as before the move (a real risk in a mechanical
move-and-split task is a copy-paste slip, not a logic bug); and that no file anywhere still
imports `pkg/trunk/check` for anything other than `check.Run` (a leftover import of a type that
used to live in `check` but moved to `engine`/`output` would be a compile error today, but is
worth calling out explicitly if the reviewer notices any awkward re-export shim that shouldn't
exist).
