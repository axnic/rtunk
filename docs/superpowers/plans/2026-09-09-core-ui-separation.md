# Core/UI Separation (pkg/worker + pkg/diagnostic) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Promote `internal/diagnostic` to `pkg/diagnostic` and fuse `pkg/linter` + `internal/ignore` + `internal/output` into a new `pkg/worker`, whose `Runner.Run` streams a channel of `Event`s instead of returning a synchronous slice — closing the two real core/UI leaks (`diagnostic.Diagnostic` and `*progress.Tracker` in `pkg/linter`'s exported signatures) and giving `cmd/rtunk` (and any future consumer) a pure-Go, presentation-free execution API.

**Architecture:** Six mechanical `git mv` + unexport passes move existing, already-tested logic into `pkg/worker` unchanged; two new-code passes add the `Event` sum type and the `Runner`/`Definition`/`RunScope` streaming API around that moved logic; a final pass rewires `cmd/rtunk/main.go` to consume the channel.

**Tech Stack:** Go, `golang.org/x/sync/{errgroup,semaphore}`, existing `pkg/config`/`pkg/plugin`/`pkg/shim`/`pkg/tool`/`pkg/workspace`.

**Spec:** `docs/superpowers/specs/2026-09-09-core-ui-separation-design.md`

## Global Constraints

- Module path: `gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk` — every import rewrite below uses this prefix.
- `pkg/config`, `pkg/plugin`, `pkg/shim`, `pkg/runtime`, `pkg/tool`, `pkg/download`, `pkg/cache`, `pkg/workspace` are already flat under `pkg/` and are NOT touched by this plan (spec §2).
- Every task ends with `go build ./...` and `go test ./...` passing before its commit — never leave the tree in a broken state between tasks.
- Behavior parity: `rtunk check`/`rtunk fmt`'s target resolution, output formats, ignore rules (config-level and inline-directive), and autofix prompt flow must not change (spec §7) — only where the code that implements them lives, and (per spec addenda) the `worker.FilterDirectives` global pass and the new `FileChanged` event, both required for correctness/parity and covered below.
- macOS `sed -i` requires an explicit (empty) backup-suffix argument (`sed -i ''`); every sed command below is written for that form.

---

### Task 1: Promote `internal/diagnostic` → `pkg/diagnostic`

**Files:**
- Move: `internal/diagnostic/diagnostic.go` → `pkg/diagnostic/diagnostic.go`
- Modify (import path only, no logic change): `cmd/rtunk/main.go`, `internal/autofix/autofix.go`, `internal/autofix/autofix_test.go`, `internal/ignore/filter.go`, `internal/ignore/ignore_test.go`, `internal/output/{gitleaks,markdownlint,regex,sarif,taplo}.go`, `internal/output/sarif_test.go`, `internal/report/report.go`, `pkg/linter/linter.go`

**Interfaces:**
- Produces: `pkg/diagnostic.Diagnostic`, `pkg/diagnostic.Fix`, `pkg/diagnostic.Severity`, `pkg/diagnostic.ParseSeverity(string) (Severity, error)` — same names/fields as today's `internal/diagnostic`, every later task imports this path.

- [ ] **Step 1: Move the package**

```bash
git mv internal/diagnostic pkg/diagnostic
```

- [ ] **Step 2: Rewrite every import of the old path**

```bash
grep -rl 'rtunk/internal/diagnostic' --include='*.go' . | xargs sed -i '' 's#rtunk/internal/diagnostic#rtunk/pkg/diagnostic#g'
```

- [ ] **Step 3: Verify the package doc comment still makes sense at its new location**

Open `pkg/diagnostic/diagnostic.go:1` and confirm the comment (`// Package diagnostic defines the normalized issue structure every linter output is converted to.`) needs no wording change — it doesn't reference `internal/`, leave as-is.

- [ ] **Step 4: Build and test**

```bash
go build ./... && go test ./...
```

Expected: builds clean, all existing tests pass (no behavior changed, only an import path).

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor: promote internal/diagnostic to pkg/diagnostic"
```

---

### Task 2: Move `internal/output` parsers into `pkg/worker` (unexported)

**Files:**
- Move: `internal/output/gitleaks.go` → `pkg/worker/output_gitleaks.go`
- Move: `internal/output/gitleaks_test.go` → `pkg/worker/output_gitleaks_test.go`
- Move: `internal/output/markdownlint.go` → `pkg/worker/output_markdownlint.go`
- Move: `internal/output/markdownlint_test.go` → `pkg/worker/output_markdownlint_test.go`
- Move: `internal/output/regex.go` → `pkg/worker/output_regex.go`
- Move: `internal/output/regex_test.go` → `pkg/worker/output_regex_test.go`
- Move: `internal/output/sarif.go` → `pkg/worker/output_sarif.go`
- Move: `internal/output/sarif_test.go` → `pkg/worker/output_sarif_test.go`
- Move: `internal/output/taplo.go` → `pkg/worker/output_taplo.go`
- Move: `internal/output/taplo_test.go` → `pkg/worker/output_taplo_test.go`

**Interfaces:**
- Consumes: `pkg/diagnostic.Diagnostic`, `pkg/diagnostic.Severity` (Task 1)
- Produces (unexported, package `worker`): `parseGitleaksJSON`, `parseMarkdownlint`, `parseRegex`, `parseSarif`, `parseTaplo`, `sarifSeverity`, `splitLines` — Task 6 (`exec.go`, moved from `pkg/linter/linter.go`) calls `parseGitleaksJSON`/`parseSarif`/`parseMarkdownlint`/`parseTaplo`/`parseRegex` by these exact names.

- [ ] **Step 1: Move every file, changing the package name**

```bash
mkdir -p pkg/worker
for f in gitleaks gitleaks_test markdownlint markdownlint_test regex regex_test sarif sarif_test taplo taplo_test; do
  git mv "internal/output/${f}.go" "pkg/worker/output_${f}.go"
done
sed -i '' 's/^package output$/package worker/' pkg/worker/output_*.go
```

- [ ] **Step 2: Unexport every parser function and its call sites**

```bash
sed -i '' \
  -e 's/\bParseGitleaksJSON\b/parseGitleaksJSON/g' \
  -e 's/\bParseMarkdownlint\b/parseMarkdownlint/g' \
  -e 's/\bParseRegex\b/parseRegex/g' \
  -e 's/\bParseSarif\b/parseSarif/g' \
  -e 's/\bParseTaplo\b/parseTaplo/g' \
  pkg/worker/output_*.go
```

- [ ] **Step 3: Rewrite the `internal/diagnostic` import left in these files**

```bash
sed -i '' 's#rtunk/internal/diagnostic#rtunk/pkg/diagnostic#g' pkg/worker/output_*.go
```

(These files import `diagnostic` under its own package name already, no alias change needed — only the path.)

- [ ] **Step 4: Build and test the new package in isolation**

```bash
go build ./pkg/worker/... && go test ./pkg/worker/...
```

Expected: `pkg/worker` builds and its 5 moved test files pass unchanged (only names/imports moved, no logic touched). `pkg/linter/linter.go` (Task 6) and `cmd/rtunk/main.go` (which never imported `internal/output` directly) do not yet reference the new location — full-repo build still passes since nothing outside `pkg/worker` referenced `internal/output`'s exported names except `pkg/linter/linter.go`, fixed next in Task 6.

```bash
go build ./... && go test ./...
```

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor: move internal/output parsers into pkg/worker, unexported"
```

---

### Task 3: Move config-based ignore + glob matching into `pkg/worker` (unexported)

**Files:**
- Move: `internal/ignore/config.go` → `pkg/worker/config_ignore.go`
- Move: `internal/ignore/glob.go` → `pkg/worker/glob.go`
- Modify: `internal/ignore/ignore_test.go` — split off the `TestMatchesConfigIgnore_DoublestarAndNegation`/`TestFilterPaths` cases (the only two using `config.go`'s exports) into a new `pkg/worker/config_ignore_test.go`; the rest of `ignore_test.go` (directive/filter tests) stays in `internal/ignore` until Tasks 4–5 move it too.

**Interfaces:**
- Consumes: `pkg/config.LintIgnore`, `pkg/config.GlobPattern` (unchanged, already `pkg/`)
- Produces (unexported, package `worker`): `matchesConfigIgnore(path, linterName string, entries []config.LintIgnore) (bool, error)`, `filterPaths(paths []string, linterName string, entries []config.LintIgnore) ([]string, error)`, `globToRegexp` (already unexported) — Task 8 (`worker.go`) calls `filterPaths` from `Runner.Run`/`CountJobs`.

- [ ] **Step 1: Move the two files, changing the package name**

```bash
git mv internal/ignore/config.go pkg/worker/config_ignore.go
git mv internal/ignore/glob.go pkg/worker/glob.go
sed -i '' 's/^package ignore$/package worker/' pkg/worker/config_ignore.go pkg/worker/glob.go
```

- [ ] **Step 2: Unexport `MatchesConfigIgnore`/`FilterPaths`**

```bash
sed -i '' \
  -e 's/\bMatchesConfigIgnore\b/matchesConfigIgnore/g' \
  -e 's/\bFilterPaths\b/filterPaths/g' \
  pkg/worker/config_ignore.go
```

- [ ] **Step 3: Split the two now-moved test cases out of `internal/ignore/ignore_test.go`**

Read `internal/ignore/ignore_test.go`, cut `TestMatchesConfigIgnore_DoublestarAndNegation` and `TestFilterPaths` (and the `entries := []config.LintIgnore{...}` fixture each uses — check whether they share one or each declares its own) out of it, and create:

```go
// pkg/worker/config_ignore_test.go
package worker

import (
	"testing"

	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/config"
)

func TestMatchesConfigIgnore_DoublestarAndNegation(t *testing.T) {
	entries := []config.LintIgnore{
		{Linters: []string{"ALL"}, Paths: []config.GlobPattern{"**/generated/**", "!**/generated/**/*.keep"}},
	}
	got, err := matchesConfigIgnore("src/generated/api/a.go", "eslint", entries)
	if err != nil || !got {
		t.Errorf("expected src/generated/api/a.go ignored, got %v, err %v", got, err)
	}
	got, err = matchesConfigIgnore("src/generated/a.keep", "eslint", entries)
	if err != nil || got {
		t.Errorf("expected src/generated/a.keep NOT ignored (negated), got %v, err %v", got, err)
	}
	got, err = matchesConfigIgnore("src/main.go", "eslint", entries)
	if err != nil || got {
		t.Errorf("expected src/main.go NOT ignored, got %v, err %v", got, err)
	}
}

func TestFilterPaths(t *testing.T) {
	entries := []config.LintIgnore{{Linters: []string{"eslint"}, Paths: []config.GlobPattern{"dist/**"}}}
	out, err := filterPaths([]string{"dist/a.js", "src/a.js"}, "eslint", entries)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0] != "src/a.js" {
		t.Errorf("expected [src/a.js], got %+v", out)
	}
}
```

Adjust the exact glob/path literals above to match whatever `internal/ignore/ignore_test.go` actually asserts (read the file first — the plan's job is to preserve the existing assertions verbatim under the new unexported names, not invent new ones).

- [ ] **Step 4: Build and test**

```bash
go build ./... && go test ./...
```

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor: move config-based lint.ignore matching into pkg/worker, unexported"
```

---

### Task 4: Move inline-directive parsing into `pkg/worker` (unexported)

**Files:**
- Move: `internal/ignore/directive.go` → `pkg/worker/directive.go`

**Interfaces:**
- Produces (unexported, package `worker`): `kind` (was `Kind`), `kindLine`/`kindAll`/`kindBlockStart`/`kindBlockEnd` (was `KindLine`/etc.), `target` (already unexported), `directive` (was `Directive`, still carries `Used bool` — Task 5's `apply`/`suppressed` set it), `parseDirectives` (was `ParseDirectives`) — Task 5 (`filter.go`, moved next) and Task 8 (`worker.go`, only indirectly via `FilterDirectives`) are the only other files in the package referencing these.

- [ ] **Step 1: Move the file, changing the package name**

```bash
git mv internal/ignore/directive.go pkg/worker/directive.go
sed -i '' 's/^package ignore$/package worker/' pkg/worker/directive.go
```

- [ ] **Step 2: Unexport `Kind`, its four constants, `Directive`, and `ParseDirectives`**

```bash
sed -i '' \
  -e 's/\bKindLine\b/kindLine/g' \
  -e 's/\bKindAll\b/kindAll/g' \
  -e 's/\bKindBlockStart\b/kindBlockStart/g' \
  -e 's/\bKindBlockEnd\b/kindBlockEnd/g' \
  -e 's/\bKind\b/kind/g' \
  -e 's/\bDirective\b/directive/g' \
  -e 's/\bParseDirectives\b/parseDirectives/g' \
  pkg/worker/directive.go
```

Note the ordering: `Kind` is renamed last among the `Kind*` group so the `KindLine`→`kindLine` etc. substitutions run against the still-capitalized text first (the `\b...\b` word-boundary anchors keep `KindLine` and `Kind` from cross-matching regardless of order, but keeping this order is the least error-prone to eyeball afterward).

- [ ] **Step 3: There is no test file to move — `directive.go`'s behavior is only exercised via `internal/ignore/ignore_test.go`'s `TestParseDirectives*` cases, which move in Task 5 alongside `filter.go` (they call `Apply`, which lives in `filter.go`, in the same test functions)**

No action this step; confirmed by re-reading `internal/ignore/ignore_test.go`'s structure (every `ParseDirectives` call in it is immediately followed by an `Apply` call in the same test function).

- [ ] **Step 4: Build the package (test will still fail to compile until Task 5 moves the tests — skip full-repo test here)**

```bash
go build ./pkg/worker/...
```

Expected: builds (nothing in `pkg/worker` yet calls `parseDirectives`, but an unused-but-exported-within-package function is not a compile error in Go). Full-repo `go build ./...` and `go test ./...` still pass too since `internal/ignore` (not yet touched) still compiles against its own now-stale copy... wait: `directive.go` no longer exists in `internal/ignore` after the `git mv` — `internal/ignore/filter.go` and `internal/ignore/ignore_test.go` (both still in `internal/ignore`) reference `Directive`/`ParseDirectives`/`Kind*`, which just moved out from under them.

```bash
go build ./... 2>&1 | head -40
```

Expected: this **fails** — `internal/ignore/filter.go` and `internal/ignore/ignore_test.go` don't compile anymore (undefined `Directive`, `ParseDirectives`, `KindLine`, etc.). This is expected and resolved by Task 5, which moves `filter.go` (and the rest of `ignore_test.go`) in the same commit that fixes this — **do not commit at the end of this task**; Tasks 4 and 5 land as one commit.

---

### Task 5: Move directive-based filtering into `pkg/worker`; expose `FilterDirectives`

Continues directly from Task 4 (same commit) — `internal/ignore/filter.go` is the last file in `internal/ignore`, so this empties and removes that package.

**Files:**
- Move: `internal/ignore/filter.go` → `pkg/worker/filter.go`
- Move: `internal/ignore/ignore_test.go` → `pkg/worker/directive_test.go` (the remaining directive/filter test cases, after Task 3 already split the config-ignore ones out)

**Interfaces:**
- Consumes: `pkg/diagnostic.Diagnostic` (Task 1), `directive`/`kind*`/`parseDirectives`/`target` (Task 4)
- Produces: `FilterDirectives(root string, targets []string, diags []diagnostic.Diagnostic) ([]diagnostic.Diagnostic, error)` — **exported**, the one deliberate exception in this fusion (spec addendum §3): unlike every other moved helper, this one stays public because `cmd/rtunk/main.go` (Task 9) must call it once, globally, after every `Runner`'s channel has drained — see the spec's note on why per-`Runner` directive filtering would misreport unrelated linters' unused directives.
- Produces (unexported): `apply` (was `Apply`), `suppressed`, `matchesTargets`, `hasSilencer`, `resolveBlocks`, `block`

- [ ] **Step 1: Move the file, changing the package name**

```bash
git mv internal/ignore/filter.go pkg/worker/filter.go
sed -i '' 's/^package ignore$/package worker/' pkg/worker/filter.go
```

- [ ] **Step 2: Rename `Apply` → `apply` (unexported) and `FilterAll` → `FilterDirectives` (stays exported, renamed for clarity now that it's the package's one public entry point for this concern)**

```bash
sed -i '' \
  -e 's/\bApply\b/apply/g' \
  -e 's/\bFilterAll\b/FilterDirectives/g' \
  pkg/worker/filter.go
```

- [ ] **Step 3: Fix `filter.go`'s own import of `internal/diagnostic`**

```bash
sed -i '' 's#rtunk/internal/diagnostic#rtunk/pkg/diagnostic#g' pkg/worker/filter.go
```

- [ ] **Step 4: Move and rename the remaining test file**

```bash
git mv internal/ignore/ignore_test.go pkg/worker/directive_test.go
sed -i '' 's/^package ignore$/package worker/' pkg/worker/directive_test.go
sed -i '' \
  -e 's/\bParseDirectives\b/parseDirectives/g' \
  -e 's/\bApply\b/apply/g' \
  -e 's/\bFilterAll\b/FilterDirectives/g' \
  -e 's#rtunk/internal/diagnostic#rtunk/pkg/diagnostic#g' \
  pkg/worker/directive_test.go
```

This file should now only contain the directive/filter tests (`TestParseDirectives*`, `TestApply*`/whatever they're actually named, `TestFilterAll`/now `TestFilterDirectives`) — confirm `TestMatchesConfigIgnore_DoublestarAndNegation` and `TestFilterPaths` are NOT still present (they were cut out to `pkg/worker/config_ignore_test.go` in Task 3); if the `git mv` in this step somehow reintroduces them (it won't — Task 3 already edited this file in place before this move), delete the duplicates.

- [ ] **Step 5: Confirm `internal/ignore` is now empty and gone**

```bash
ls internal/ignore 2>&1
```

Expected: `No such file or directory` (the last file's `git mv` removes the now-empty directory).

- [ ] **Step 6: Build and test**

```bash
go build ./... && go test ./...
```

Expected: full repo builds again (this resolves Task 4's expected failure); `pkg/worker`'s directive/filter tests pass under their new unexported names.

- [ ] **Step 7: Commit (covers both Task 4 and Task 5)**

```bash
git add -A
git commit -m "refactor: move rtunk-ignore directive filtering into pkg/worker, expose FilterDirectives"
```

---

### Task 6: Move the linter exec engine into `pkg/worker/exec.go`

**Files:**
- Move: `pkg/linter/linter.go` → `pkg/worker/exec.go` (only the free functions — the `Linter` struct and its methods are dropped here, replaced by `Runner` in Task 8)
- Move: `pkg/linter/linter_test.go` → `pkg/worker/exec_test.go`

**Interfaces:**
- Consumes: `pkg/diagnostic` (Task 1), `parseGitleaksJSON`/`parseSarif`/`parseMarkdownlint`/`parseTaplo`/`parseRegex` (Task 2), `pkg/config`, `pkg/plugin`, `pkg/shim`, `pkg/tool` (all unchanged)
- Produces (unexported, package `worker`): `group` (struct), `filterMatches(def config.LinterDefinition, targets []string) []string`, `groupTargets(targetTpl string, files []string) ([]group, error)`, `resolveConfigArgs(root string, def config.LinterDefinition) []string`, `runInPlace(root string, s shim.Shim, configArgs []string, c config.Command, g group) ([]string, error)`, `runRewrite(root string, s shim.Shim, configArgs []string, c config.Command, g group, linterName string) (*diagnostic.Fix, error)`, `runParser(root, pluginDir string, p config.Parser, raw string) (string, error)`, `runCommand(root string, s shim.Shim, configArgs []string, c config.Command, target string) (string, int, error)`, `substitute(tpl, target, tmpfile string) string`, `run(root string, s shim.Shim, args []string) (stdout, stderr string, exitCode int, err error)`, `severityOf(s string) (diagnostic.Severity, error)`, `containsInt(list []int, v int) bool`, `hashFile(path string) (string, error)`, `parseOutput(root string, c config.Command, raw, linterName string) ([]diagnostic.Diagnostic, error)` — Task 8's `Runner` methods call every one of these by these exact names.

- [ ] **Step 1: Move the file, changing the package name**

```bash
git mv pkg/linter/linter.go pkg/worker/exec.go
sed -i '' 's/^package linter$/package worker/' pkg/worker/exec.go
```

- [ ] **Step 2: Delete the `Linter` struct, `NewLinter`, `reportStart`, `reportDone`, `acquire`, `release`, `Run`, `CountJobs`, and the exported `run` method's body (the orchestration methods) — everything from the package doc comment through the end of the `run` method (today's lines 1–272) is replaced**

Read `pkg/worker/exec.go` (it's the file just moved — same content as the `pkg/linter/linter.go` read earlier in this plan's design phase) and replace its header through the end of the `(l *Linter) run` method with:

```go
// Package worker (this file) runs a resolved config.LinterDefinition's
// commands: variable substitution, target grouping, exit-code check,
// output parsing. It uses pkg/tool to resolve (installing if needed) the
// definition's own binary before running it. See worker.go for the
// Runner/Event streaming API built on top of these unexported helpers.
//
// ponytail: only two target modes (${file}, ${parent}) and three output
// types (regex, sarif, gitleaks_json) are implemented — extend once a
// bundled linter needs batching, ${parent_with()}, or lsp_json/arcanist.
package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	oexec "os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/config"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/diagnostic"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/shim"
)

type group struct {
	files    []string // files belonging to this group, for in_place hashing
	resolved string   // ${target} substitution value
}
```

Everything from `func parseOutput(...)` (today's line 274) through the end of the file (today's line 564, `hashFile`) is kept **verbatim** — those are the pure helper functions with no `Linter`/progress/semaphore dependency. Confirm no leftover reference to `l.Def`/`l.LintersDir`/`l.Runtimes`/`l.Sem`/`l.Progress`/`l.ProjectCacheDir` remains anywhere in the file (`grep -n '\bl\.' pkg/worker/exec.go` should print nothing).

Note what got dropped and why it's not missed: `sync`, `golang.org/x/sync/errgroup`, `golang.org/x/sync/semaphore`, and `"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/plugin"` were only used by the deleted orchestration methods — drop those four imports too (`plugin.ToolFor`/`tool.EnsureOneProject` move to Task 8's `worker.go`, which owns preflight now).

- [ ] **Step 3: Move and adapt the test file**

```bash
git mv pkg/linter/linter_test.go pkg/worker/exec_test.go
sed -i '' 's/^package linter$/package worker/' pkg/worker/exec_test.go
```

No further renames needed — `exec_test.go` only calls the already-unexported helpers (`groupTargets`, `substitute`, `severityOf`, `containsInt`, `resolveConfigArgs`, `runParser`), which keep their exact names across the move.

- [ ] **Step 4: `pkg/linter` is now empty — confirm it's gone**

```bash
ls pkg/linter 2>&1
```

Expected: `No such file or directory`.

- [ ] **Step 5: Build `pkg/worker` in isolation**

```bash
go build ./pkg/worker/... && go test ./pkg/worker/...
```

Expected: builds and all moved tests pass. Full-repo build (`go build ./...`) is expected to **fail** at this point — `pkg/workspace/workspace.go` and `cmd/rtunk/main.go` still import the now-deleted `pkg/linter` package; that's fixed in Tasks 7–9, so don't chase it here.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "refactor: move the linter exec engine into pkg/worker/exec.go"
```

(Full-repo build stays red across this and the next two tasks — expected, since `pkg/workspace` and `cmd/rtunk` aren't fixed up until Task 9. `pkg/worker` itself is green after every task from here on, which is what each task's own build/test step checks.)

---

### Task 7: Define the `Event` sum type

**Files:**
- Create: `pkg/worker/event.go`
- Test: `pkg/worker/event_test.go`

**Interfaces:**
- Produces: `Event` (interface), `JobStarted{Target string}`, `JobDone{Target string}`, `DiagnosticFound{diagnostic.Diagnostic}`, `FixProposed{diagnostic.Fix}`, `FileChanged{Path string}`, `RunError{Target string; Err error}` — Task 8's `Runner.Run`/`dispatch`/`runGroup` send these on the returned channel; Task 9's `cmd/rtunk/main.go` type-switches on them.

- [ ] **Step 1: Write the test**

```go
// pkg/worker/event_test.go
package worker

import "testing"

func TestEvent_TypeSwitch(t *testing.T) {
	events := []Event{
		JobStarted{Target: "a"},
		JobDone{Target: "a"},
		DiagnosticFound{},
		FixProposed{},
		FileChanged{Path: "a.go"},
		RunError{Target: "a", Err: nil},
	}
	var started, done, diag, fix, changed, errEvt int
	for _, e := range events {
		switch e.(type) {
		case JobStarted:
			started++
		case JobDone:
			done++
		case DiagnosticFound:
			diag++
		case FixProposed:
			fix++
		case FileChanged:
			changed++
		case RunError:
			errEvt++
		default:
			t.Fatalf("unhandled event type %T", e)
		}
	}
	if started != 1 || done != 1 || diag != 1 || fix != 1 || changed != 1 || errEvt != 1 {
		t.Fatalf("expected exactly one of each event type, got started=%d done=%d diag=%d fix=%d changed=%d err=%d",
			started, done, diag, fix, changed, errEvt)
	}
}
```

- [ ] **Step 2: Run it to confirm it fails (the type doesn't exist yet)**

```bash
go test ./pkg/worker/... -run TestEvent_TypeSwitch -v
```

Expected: FAIL — `undefined: Event` (and every concrete type).

- [ ] **Step 3: Write `event.go`**

```go
// pkg/worker/event.go
package worker

import "gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/diagnostic"

// Event is a sum type: a value is exactly one of the concrete types below,
// each carrying only the data relevant to its own case (design doc §3).
type Event interface{ isEvent() }

// JobStarted reports one subprocess invocation (one file, or one grouped
// batch, depending on the command's target template) about to run.
type JobStarted struct{ Target string }

// JobDone reports a JobStarted invocation has finished, success or not.
type JobDone struct{ Target string }

// DiagnosticFound carries one issue a lint command reported.
type DiagnosticFound struct{ diagnostic.Diagnostic }

// FixProposed carries one proposed rewrite from an `output: rewrite`
// command — the caller decides whether/when to apply it.
type FixProposed struct{ diagnostic.Fix }

// FileChanged reports one file an `in_place: true` command rewrote
// directly (e.g. `gofmt -w`) — the in_place equivalent of FixProposed,
// since an in_place command applies its own change and has no diff to
// propose.
type FileChanged struct{ Path string }

// RunError reports a single job's own failure (a process crash, unparsable
// output, an unsupported target template) — every other job keeps running;
// see Runner.Run's doc comment for the preflight-vs-per-job error split.
type RunError struct {
	Target string
	Err    error
}

func (JobStarted) isEvent()      {}
func (JobDone) isEvent()         {}
func (DiagnosticFound) isEvent() {}
func (FixProposed) isEvent()     {}
func (FileChanged) isEvent()     {}
func (RunError) isEvent()        {}
```

- [ ] **Step 4: Run the test again to confirm it passes**

```bash
go test ./pkg/worker/... -run TestEvent_TypeSwitch -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "feat(worker): define the Event sum type"
```

---

### Task 8: Define `Definition`/`RunScope`/`Runner` and the streaming `Run`

**Files:**
- Create: `pkg/worker/worker.go`
- Test: `pkg/worker/worker_test.go`

**Interfaces:**
- Consumes: `group`/`filterMatches`/`groupTargets`/`resolveConfigArgs`/`runInPlace`/`runRewrite`/`runParser`/`runCommand`/`severityOf`/`containsInt`/`parseOutput` (Task 6), `filterPaths` (Task 3), `Event`/`JobStarted`/`JobDone`/`DiagnosticFound`/`FixProposed`/`FileChanged`/`RunError` (Task 7)
- Produces: `Definition{Linter config.LinterDefinition; LintersDir string; Runtimes map[string]shim.Shim; ProjectCacheDir string}`, `RunScope{Root string; Targets []string; IgnoreRules []config.LintIgnore; Formatter bool}`, `Runner`, `NewRunner(def Definition, sem *semaphore.Weighted) *Runner`, `(*Runner) CountJobs(scope RunScope) (int, error)`, `(*Runner) Run(scope RunScope) (<-chan Event, error)` — Task 9's `cmd/rtunk/main.go` is the only consumer.

- [ ] **Step 1: Write the failing tests**

```go
// pkg/worker/worker_test.go
package worker

import (
	"testing"

	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/config"
)

func TestRunner_CountJobs(t *testing.T) {
	def := Definition{Linter: config.LinterDefinition{
		Name:  "x",
		Files: []config.GlobPattern{"*.go"},
		Commands: []config.Command{
			{Name: "lint", Target: "${file}", Formatter: false},
			{Name: "fmt", Target: "${file}", Formatter: true},
		},
	}}
	r := NewRunner(def, nil)
	scope := RunScope{Targets: []string{"a.go", "b.go", "c.txt"}, Formatter: false}

	n, err := r.CountJobs(scope)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("expected 2 jobs (a.go, b.go match *.go; c.txt doesn't), got %d", n)
	}

	scope.Formatter = true
	n, err = r.CountJobs(scope)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("expected 2 jobs for the formatter command too, got %d", n)
	}
}

func TestRunner_Run_PreflightFailureReturnsSyncError(t *testing.T) {
	// No Download/PackageInstall on the definition: tool.EnsureOneProject
	// has no install mechanism to try — a failure every job would also
	// hit, so it must surface as Run's synchronous error, not a per-job
	// RunError event.
	def := Definition{
		Linter:     config.LinterDefinition{Name: "nope", Files: []config.GlobPattern{"ALL"}},
		LintersDir: t.TempDir(),
	}
	r := NewRunner(def, nil)
	ch, err := r.Run(RunScope{Root: t.TempDir(), Targets: []string{"a.go"}})
	if err == nil {
		t.Fatal("expected a preflight error, got nil")
	}
	if ch != nil {
		t.Error("expected a nil channel alongside a preflight error")
	}
}

func TestRunner_Run_NoMatchingTargetsSkipsPreflight(t *testing.T) {
	// Same unresolvable definition as above, but no target matches its
	// Files glob: Run must return a closed, empty channel and a nil error
	// without ever attempting tool.EnsureOneProject.
	def := Definition{
		Linter:     config.LinterDefinition{Name: "nope", Files: []config.GlobPattern{"*.go"}},
		LintersDir: t.TempDir(),
	}
	r := NewRunner(def, nil)
	ch, err := r.Run(RunScope{Root: t.TempDir(), Targets: []string{"a.txt"}})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	got := 0
	for range ch {
		got++
	}
	if got != 0 {
		t.Errorf("expected zero events, got %d", got)
	}
}

func TestRunner_Dispatch_JobFailureDoesNotStopOthers(t *testing.T) {
	def := Definition{Linter: config.LinterDefinition{Name: "x"}}
	r := NewRunner(def, nil)
	ch := make(chan Event, 16)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		r.dispatch(t.TempDir(), shim.Shim{}, config.Command{Name: "lint-ok", Run: "true", Target: "${file}",
			Output: "regex", ParseRegex: config.RegexPattern(`(?P<path>nomatch)`)}, []string{"a.go"}, ch)
	}()
	go func() {
		defer wg.Done()
		r.dispatch(t.TempDir(), shim.Shim{}, config.Command{Name: "lint-fail", Run: "false", Target: "${file}"},
			[]string{"a.go"}, ch)
	}()
	wg.Wait()
	close(ch)

	var started, done, runErrors int
	for e := range ch {
		switch e.(type) {
		case JobStarted:
			started++
		case JobDone:
			done++
		case RunError:
			runErrors++
		}
	}
	if started != 2 || done != 2 {
		t.Errorf("expected both commands' jobs to start and finish (started=%d done=%d) — one command's failure must not stop the other's", started, done)
	}
	if runErrors != 1 {
		t.Errorf("expected exactly 1 RunError (from lint-fail's non-zero exit), got %d", runErrors)
	}
}
```

Add `"sync"` and `"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/shim"` to this test file's imports.

- [ ] **Step 2: Run the tests to confirm they fail**

```bash
go test ./pkg/worker/... -run 'TestRunner_' -v
```

Expected: FAIL — `undefined: Definition`, `undefined: NewRunner`, `undefined: RunScope`, and (once those compile) `r.dispatch` undefined.

- [ ] **Step 3: Write `worker.go`**

```go
// pkg/worker/worker.go
package worker

import (
	"context"
	"fmt"
	"sync"

	"golang.org/x/sync/semaphore"

	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/config"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/plugin"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/shim"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/tool"
)

// Definition describes what to run and how — the static "recipe" for one
// linter (equivalent to the old pkg/linter.Linter struct, minus the
// Sem/Progress fields: those are run-time concerns, now Runner's and
// RunScope's job respectively).
type Definition struct {
	Linter config.LinterDefinition
	// LintersDir is pkg/cache's Cache.Linters() result: this linter's own
	// binary lives under LintersDir/<name>/...
	LintersDir string
	// Runtimes are every already-Ensure'd runtime (pkg/runtime.Ensure's own
	// result) a package-manager-installed linter binary might need.
	Runtimes map[string]shim.Shim
	// ProjectCacheDir, if set, is symlinked into (pkg/cache.Cache.Workspace
	// for the current repo) via pkg/tool.EnsureOneProject instead of a plain
	// EnsureOne — "" skips that (no project symlink, e.g. a one-shot use).
	ProjectCacheDir string
}

// RunScope describes what to run a Runner against: the repo root, the
// candidate targets (already git-diff/--all resolved by the caller), the
// config-level lint.ignore rules, and whether to run this Definition's
// Formatter commands (fmt) or its non-Formatter ones (check).
type RunScope struct {
	Root        string
	Targets     []string
	IgnoreRules []config.LintIgnore
	Formatter   bool
}

// Runner runs one Definition's commands against a RunScope, streaming
// results as Events. sem is shared across every Runner in a single rtunk
// run (design doc §5) — nil means unbounded.
type Runner struct {
	def Definition
	sem *semaphore.Weighted
}

func NewRunner(def Definition, sem *semaphore.Weighted) *Runner {
	return &Runner{def: def, sem: sem}
}

// CountJobs reports how many subprocess invocations scope would launch
// across this Definition's commands matching scope.Formatter — matching
// and target-grouping only, no execution. Used to size a progress bar's
// total before Run starts.
func (r *Runner) CountJobs(scope RunScope) (int, error) {
	targets, err := filterPaths(scope.Targets, r.def.Linter.Name, scope.IgnoreRules)
	if err != nil {
		return 0, err
	}
	matched := filterMatches(r.def.Linter, targets)
	if len(matched) == 0 {
		return 0, nil
	}
	total := 0
	for _, c := range r.def.Linter.Commands {
		if c.Formatter != scope.Formatter {
			continue
		}
		groups, err := groupTargets(c.Target, matched)
		if err != nil {
			continue
		}
		total += len(groups)
	}
	return total, nil
}

// Run filters scope.Targets through lint.ignore and this Definition's own
// Files globs, then — for every scope.Formatter-matching command — runs its
// groups concurrently, streaming one Event per job as it completes. The
// channel closes once every job is done.
//
// The synchronous error return covers only preflight failures predictable
// to affect every job alike (e.g. this linter's own binary can't be
// installed at all) — a single job's own failure (a process crash,
// unparsable output, an unsupported target template) becomes a RunError
// event instead, and every other job keeps running: if it's foreseeable
// that a failure would also doom every other job, it's checked before the
// channel opens; otherwise it's local to that one job.
func (r *Runner) Run(scope RunScope) (<-chan Event, error) {
	targets, err := filterPaths(scope.Targets, r.def.Linter.Name, scope.IgnoreRules)
	if err != nil {
		return nil, err
	}
	matched := filterMatches(r.def.Linter, targets)
	if len(matched) == 0 {
		ch := make(chan Event)
		close(ch)
		return ch, nil
	}

	s, err := tool.EnsureOneProject(r.def.LintersDir, plugin.ToolFor(r.def.Linter), r.def.Runtimes, r.def.ProjectCacheDir)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", r.def.Linter.Name, err)
	}

	ch := make(chan Event)
	go func() {
		defer close(ch)
		var wg sync.WaitGroup
		for _, c := range r.def.Linter.Commands {
			if c.Formatter != scope.Formatter {
				continue
			}
			c := c
			wg.Add(1)
			go func() {
				defer wg.Done()
				r.dispatch(scope.Root, s, c, matched, ch)
			}()
		}
		wg.Wait()
	}()
	return ch, nil
}

// dispatch runs one Command's groups against matched (already scoped to
// r.def.Linter.Files and scope.IgnoreRules), sending a JobStarted/JobDone
// pair plus whichever Event each group's outcome produces.
func (r *Runner) dispatch(root string, s shim.Shim, c config.Command, matched []string, ch chan<- Event) {
	groups, err := groupTargets(c.Target, matched)
	if err != nil {
		ch <- RunError{Target: c.Name, Err: fmt.Errorf("%s/%s: %w", r.def.Linter.Name, c.Name, err)}
		return
	}
	configArgs := resolveConfigArgs(root, r.def.Linter)

	// max_concurrency further restricts this specific command's own
	// groups, on top of (never beyond) the shared global pool (r.sem).
	var localSem *semaphore.Weighted
	if c.MaxConcurrency > 0 {
		localSem = semaphore.NewWeighted(int64(c.MaxConcurrency))
	}

	var wg sync.WaitGroup
	for _, g := range groups {
		g := g
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.runGroup(root, s, configArgs, c, g, localSem, ch)
		}()
	}
	wg.Wait()
}

// runGroup runs one group of c, sending a JobStarted/JobDone pair around
// whichever Event c's kind produces. A failure here becomes a RunError on
// ch rather than stopping any other group or command.
func (r *Runner) runGroup(root string, s shim.Shim, configArgs []string, c config.Command, g group, localSem *semaphore.Weighted, ch chan<- Event) {
	name := r.def.Linter.Name + "/" + c.Name + ": " + g.resolved
	if err := r.acquire(context.Background(), localSem); err != nil {
		ch <- RunError{Target: name, Err: err}
		return
	}
	defer r.release(localSem)

	ch <- JobStarted{Target: name}
	defer func() { ch <- JobDone{Target: name} }()

	switch {
	case c.InPlace:
		changed, err := runInPlace(root, s, configArgs, c, g)
		if err != nil {
			ch <- RunError{Target: name, Err: fmt.Errorf("%s/%s: %w", r.def.Linter.Name, c.Name, err)}
			return
		}
		for _, f := range changed {
			ch <- FileChanged{Path: f}
		}
	case c.Output == "rewrite":
		fix, err := runRewrite(root, s, configArgs, c, g, r.def.Linter.Name)
		if err != nil {
			ch <- RunError{Target: name, Err: fmt.Errorf("%s/%s: %w", r.def.Linter.Name, c.Name, err)}
			return
		}
		if fix != nil {
			ch <- FixProposed{Fix: *fix}
		}
	default:
		r.runLint(root, s, configArgs, c, g, name, ch)
	}
}

func (r *Runner) runLint(root string, s shim.Shim, configArgs []string, c config.Command, g group, name string, ch chan<- Event) {
	severity, err := severityOf(c.Severity)
	if err != nil {
		ch <- RunError{Target: name, Err: fmt.Errorf("%s/%s: %w", r.def.Linter.Name, c.Name, err)}
		return
	}
	successCodes := c.SuccessCodes
	if len(successCodes) == 0 {
		successCodes = []int{0}
	}
	out, code, err := runCommand(root, s, configArgs, c, g.resolved)
	if err != nil {
		ch <- RunError{Target: name, Err: err}
		return
	}
	if !containsInt(successCodes, code) {
		ch <- RunError{Target: name, Err: fmt.Errorf("exit code %d not in success_codes %v", code, successCodes)}
		return
	}
	if c.Parser != nil {
		out, err = runParser(root, r.def.Linter.PluginDir, *c.Parser, out)
		if err != nil {
			ch <- RunError{Target: name, Err: err}
			return
		}
	}
	diags, err := parseOutput(root, c, out, r.def.Linter.Name)
	if err != nil {
		ch <- RunError{Target: name, Err: err}
		return
	}
	// sarif carries its own per-result severity; every other output type
	// is uniform per-command, from Command.Severity.
	if c.Output != "sarif" {
		for i := range diags {
			diags[i].Severity = severity
		}
	}
	for _, d := range diags {
		ch <- DiagnosticFound{Diagnostic: d}
	}
}

// acquire blocks until both the optional per-command local cap
// (Command.MaxConcurrency) and the shared global pool admit one more
// concurrent subprocess.
func (r *Runner) acquire(ctx context.Context, local *semaphore.Weighted) error {
	if local != nil {
		if err := local.Acquire(ctx, 1); err != nil {
			return err
		}
	}
	if r.sem != nil {
		if err := r.sem.Acquire(ctx, 1); err != nil {
			if local != nil {
				local.Release(1)
			}
			return err
		}
	}
	return nil
}

func (r *Runner) release(local *semaphore.Weighted) {
	if r.sem != nil {
		r.sem.Release(1)
	}
	if local != nil {
		local.Release(1)
	}
}
```

- [ ] **Step 4: Run the tests to confirm they pass**

```bash
go test ./pkg/worker/... -run 'TestRunner_' -v
```

Expected: PASS on all four.

- [ ] **Step 5: Run the whole `pkg/worker` suite**

```bash
go test ./pkg/worker/... -v
```

Expected: every test in the package (Tasks 2–8's moved and new tests) passes.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "feat(worker): add Definition/RunScope/Runner and the streaming Run API"
```

---

### Task 9: Rewire `cmd/rtunk/main.go` onto `pkg/worker`

**Files:**
- Modify: `cmd/rtunk/main.go` (the `linterWork`/`resolveWork`/`countJobs`/`newLinter` helpers, `checkCmd`, `fmtCmd`)
- Modify: `pkg/workspace/workspace.go` (drops its own `*progress.Tracker` parameter and `pkg/linter` import)

**Interfaces:**
- Consumes: `worker.Definition`/`worker.RunScope`/`worker.NewRunner`/`worker.Runner.CountJobs`/`worker.Runner.Run`/`worker.Event`/`worker.JobStarted`/`worker.JobDone`/`worker.DiagnosticFound`/`worker.FixProposed`/`worker.FileChanged`/`worker.RunError`/`worker.FilterDirectives` (Tasks 5, 7, 8)

- [ ] **Step 1: Fix `pkg/workspace/workspace.go`'s leaked `*progress.Tracker` parameter**

Read `pkg/workspace/workspace.go`. Its `NewLinter(name string, sem *semaphore.Weighted, prog *progress.Tracker) (*linter.Linter, bool)` method (the second confirmed leak from the spec's §1) returned a `*pkg/linter.Linter` — a type that no longer exists after Task 6. Since `cmd/rtunk/main.go`'s new `newRunners` helper (Step 3 below) builds `worker.Runner`s directly from a `*Workspace`'s already-public fields (`LintersDir`, `Runtimes`, `ProjectCacheDir`) instead of asking the `Workspace` to construct them, this method has no remaining purpose — delete it entirely, and delete the now-unused `internal/progress` and `pkg/linter` imports from `workspace.go`. Confirm nothing else in the repo calls `Workspace.NewLinter`:

```bash
grep -rn "\.NewLinter(" --include='*.go' .
```

Expected: no matches once this method is deleted and `cmd/rtunk/main.go` is updated in Step 3.

- [ ] **Step 2: Run `pkg/workspace`'s own tests**

```bash
go build ./pkg/workspace/... && go test ./pkg/workspace/...
```

Expected: builds and passes (its other tests don't exercise the deleted method).

- [ ] **Step 3: Replace `cmd/rtunk/main.go`'s helpers and update imports**

Read `cmd/rtunk/main.go` in full (it was read during this plan's design phase — re-read now to get exact current line numbers, since Tasks 1–8 may have shifted nothing in this file yet). Replace the import block, `linterWork`/`resolveWork`/`countJobs`/`newLinter` (today's lines 16–27 and 29–72), and the bodies of `checkCmd`/`fmtCmd` (today's lines 88–258) as follows.

New import block:

```go
import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/goccy/go-yaml"
	"github.com/spf13/cobra"
	"golang.org/x/sync/semaphore"

	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/internal/autofix"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/internal/gitutil"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/internal/progress"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/internal/report"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/cache"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/config"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/diagnostic"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/plugin"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/workspace"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/worker"
)
```

(Dropped: `"golang.org/x/sync/errgroup"` — no longer used, event consumption uses `sync.WaitGroup`; `internal/ignore` — its config-ignore filtering moved inside `worker.Runner` itself, addressed via `RunScope.IgnoreRules`; `pkg/linter` — replaced by `pkg/worker`.)

Replace `linterWork`/`resolveWork`/`countJobs`/`newLinter` with a single helper:

```go
// newRunners builds one worker.Runner per def, bound to ws's resolved
// LintersDir/Runtimes/ProjectCacheDir (SPECS.md §2.2/§7.2) and sharing sem
// across every linter in this run (SPECS.md §7.4).
func newRunners(defs []config.LinterDefinition, ws *workspace.Workspace, sem *semaphore.Weighted) []*worker.Runner {
	runners := make([]*worker.Runner, len(defs))
	for i, def := range defs {
		runners[i] = worker.NewRunner(worker.Definition{
			Linter:          def,
			LintersDir:      ws.LintersDir,
			Runtimes:        ws.Runtimes,
			ProjectCacheDir: ws.ProjectCacheDir,
		}, sem)
	}
	return runners
}
```

Replace `checkCmd`'s body:

```go
func checkCmd(cacheDirFlag *string, jobs *int, noProgress *bool) *cobra.Command {
	var failOn string
	var all bool
	cmd := &cobra.Command{
		Use:   "check [path]",
		Short: "Run linters on changed files (or the given path)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, cfg, targets, err := setup(args, all)
			if err != nil {
				return err
			}
			c, err := cfg.ResolveCache(*cacheDirFlag)
			if err != nil {
				return err
			}
			defs, ws, err := loadDefinitions(cmd, cfg, c, root)
			if err != nil {
				return err
			}
			severity, err := diagnostic.ParseSeverity(failOn)
			if err != nil {
				return err
			}

			scope := worker.RunScope{Root: root, Targets: targets, IgnoreRules: cfg.Lint.Ignore, Formatter: false}
			sem := semaphore.NewWeighted(int64(*jobs))
			runners := newRunners(defs, ws, sem)

			total := 0
			if !*noProgress {
				for _, r := range runners {
					n, err := r.CountJobs(scope)
					if err != nil {
						return err
					}
					total += n
				}
			}
			prog := progress.New(cmd.OutOrStdout(), "Checking", total)

			var mu sync.Mutex
			var diags []diagnostic.Diagnostic
			var wg sync.WaitGroup
			for _, r := range runners {
				ch, err := r.Run(scope)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: %v\n", err)
					continue
				}
				wg.Add(1)
				go func() {
					defer wg.Done()
					for ev := range ch {
						switch e := ev.(type) {
						case worker.JobStarted:
							prog.Start(e.Target)
						case worker.JobDone:
							prog.Done(e.Target)
						case worker.DiagnosticFound:
							mu.Lock()
							diags = append(diags, e.Diagnostic)
							mu.Unlock()
						case worker.RunError:
							mu.Lock()
							fmt.Fprintf(cmd.ErrOrStderr(), "warning: %v\n", e.Err)
							mu.Unlock()
						}
					}
				}()
			}
			wg.Wait()
			prog.Finish()

			diags, err = worker.FilterDirectives(root, targets, diags)
			if err != nil {
				return err
			}

			exit := report.Print(cmd.OutOrStdout(), diags, severity)
			if exit != 0 {
				os.Exit(exit)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&failOn, "fail-on", "warning", "minimum severity that fails the command (note|warning|error)")
	cmd.Flags().BoolVar(&all, "all", false, "scan the whole repo instead of git-diff-aware targeting")
	return cmd
}
```

Replace `fmtCmd`'s body:

```go
func fmtCmd(cacheDirFlag *string, jobs *int, noProgress *bool) *cobra.Command {
	var all, autoYes, autoNo bool
	cmd := &cobra.Command{
		Use:   "fmt [path]",
		Short: "Run formatters on changed files (or the given path)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, cfg, targets, err := setup(args, all)
			if err != nil {
				return err
			}
			c, err := cfg.ResolveCache(*cacheDirFlag)
			if err != nil {
				return err
			}
			defs, ws, err := loadDefinitions(cmd, cfg, c, root)
			if err != nil {
				return err
			}

			scope := worker.RunScope{Root: root, Targets: targets, IgnoreRules: cfg.Lint.Ignore, Formatter: true}
			sem := semaphore.NewWeighted(int64(*jobs))
			runners := newRunners(defs, ws, sem)

			total := 0
			if !*noProgress {
				for _, r := range runners {
					n, err := r.CountJobs(scope)
					if err != nil {
						return err
					}
					total += n
				}
			}
			prog := progress.New(cmd.OutOrStdout(), "Formatting", total)

			var mu sync.Mutex
			var changed []string
			var fixes []diagnostic.Fix
			var wg sync.WaitGroup
			for _, r := range runners {
				ch, err := r.Run(scope)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: %v\n", err)
					continue
				}
				wg.Add(1)
				go func() {
					defer wg.Done()
					for ev := range ch {
						switch e := ev.(type) {
						case worker.JobStarted:
							prog.Start(e.Target)
						case worker.JobDone:
							prog.Done(e.Target)
						case worker.FixProposed:
							mu.Lock()
							fixes = append(fixes, e.Fix)
							mu.Unlock()
						case worker.FileChanged:
							mu.Lock()
							changed = append(changed, e.Path)
							mu.Unlock()
						case worker.RunError:
							mu.Lock()
							fmt.Fprintf(cmd.ErrOrStderr(), "warning: %v\n", e.Err)
							mu.Unlock()
						}
					}
				}()
			}
			wg.Wait()
			prog.Finish()

			// Fixes (output: rewrite) are previewed and applied one at a
			// time, in the trunk AUTOFIXES style (diff + Y/n/all/none) —
			// unlike in_place commands (already applied above by the tool
			// itself, reported via FileChanged), rtunk decides here
			// whether to write them.
			applied, err := applyFixes(cmd, root, fixes, autoYes, autoNo)
			if err != nil {
				return err
			}
			changed = append(changed, applied...)

			for _, f := range changed {
				fmt.Fprintf(cmd.OutOrStdout(), "reformatted %s\n", f)
			}
			if len(changed) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "nothing to format")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "scan the whole repo instead of git-diff-aware targeting")
	cmd.Flags().BoolVarP(&autoYes, "fix", "y", false, "apply all proposed fixes without prompting")
	cmd.Flags().BoolVarP(&autoNo, "no-fix", "n", false, "show proposed fixes without applying any of them")
	return cmd
}
```

`applyFixes`, `cacheCmd`, `configCmd`, `loadDefinitions`, `setup`, `resolvePath` are all untouched — none of them reference `pkg/linter`, `internal/ignore`, or the old `diagnostic` import path (the `internal/diagnostic` → `pkg/diagnostic` rewrite in Task 1 already updated this file's import statement; only the code added since then needs the new `pkg/worker`/`pkg/diagnostic` names).

- [ ] **Step 4: Build and test the whole repo**

```bash
go build ./... && go vet ./... && go test ./...
```

Expected: clean build, `go vet` clean, every test across every package passes. This is the point where the whole tree is green again after Task 6's expected interim breakage.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor(cmd/rtunk): consume pkg/worker's streaming Run instead of pkg/linter's synchronous one"
```

---

### Task 10: Full-repo verification and dogfood check

**Files:** none (verification only)

- [ ] **Step 1: Confirm no dangling references to removed packages/types remain**

```bash
grep -rn "pkg/linter\|internal/ignore\|internal/output\|internal/diagnostic\|linter\.Linter\|linter\.NewLinter" --include='*.go' .
```

Expected: no matches.

- [ ] **Step 2: Confirm the deleted directories are gone**

```bash
ls pkg/linter internal/ignore internal/output internal/diagnostic 2>&1
```

Expected: `No such file or directory` for all four.

- [ ] **Step 3: Full build, vet, and test**

```bash
go build ./... && go vet ./... && go test ./... -count=1
```

Expected: clean on all three (`-count=1` disables Go's test cache so this is a genuine re-run, not a cached pass from Task 9).

- [ ] **Step 4: Confirm the two spec-identified leaks are actually closed**

```bash
grep -rn "diagnostic\.\|progress\.Tracker" --include='*.go' pkg/*/*.go | grep -v "_test.go" | grep -v "^pkg/worker/\|^pkg/diagnostic/"
```

Expected: no matches outside `pkg/worker`/`pkg/diagnostic` themselves — no other `pkg/` package's exported signature references `diagnostic` (fine, `pkg/diagnostic` is now itself a `pkg/` package, that's the point) or `progress.Tracker` anymore.

- [ ] **Step 5 (best-effort, requires network for plugins.sources — skip if unavailable rather than blocking): dogfood `rtunk check`/`rtunk fmt` against this repo**

```bash
go build -o /tmp/rtunk-dogfood ./cmd/rtunk
cd /Volumes/Spaces/Lab/rtunk && /tmp/rtunk-dogfood check --all --no-progress
/tmp/rtunk-dogfood fmt --all --no-progress --no-fix
```

Expected: both commands run to completion (network permitting) with output that reads the same as before this refactor (issue lines, "reformatted %s"/"nothing to format" — this is the manual confirmation that `JobStarted`/`JobDone`/`DiagnosticFound`/`FixProposed`/`FileChanged`/`RunError` really do reconstruct the old synchronous behavior end-to-end, which Tasks 1–9's unit tests cover piecewise but never together against real plugin-resolved linters).

- [ ] **Step 6: No commit — this task is verification-only; if Step 5 surfaces a real behavioral gap, fix it as a new small task appended to this plan (not folded silently into Task 9's already-committed diff) before considering the plan complete.**

---

## Out of scope (per spec §8, unchanged by this plan)

- No new library consumer (server, second binary) — only `cmd/rtunk` migrates.
- `SPECS.md` §5/§5.1's architecture diagram stays stale; updating it is a separate documentation pass.
