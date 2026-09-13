# rtunk fmt / check --fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement `rtunk fmt [paths...]` and `rtunk check --fix [paths...]` (ROADMAP.md
v0.4), by teaching `pkg/trunk/engine` to run `Command.Formatter` commands (currently
unconditionally `Skipped`) and report which files they actually changed.

**Architecture:** `engine.Run` gains support for two unparsed `Output` values
(`rewrite`, `shfmt`) and a new `Event.ChangedFiles` field, populated by a SHA-256
before/after comparison gated on `Command.InPlace`. `internal/cli` calls `engine.Run`
directly with an inline predicate for both `check` (`!cmd.Formatter`) and the new `fmt`
command (`cmd.Formatter`); `check --fix` runs the formatter predicate first, then the
checking predicate, so fixed issues never appear in the final report. `pkg/trunk/check`
(a one-line predicate wrapper, made redundant by this same direct-call pattern) is
deleted.

**Tech Stack:** Go, `crypto/sha256` (stdlib, no new dependency), existing
`pkg/trunk/{config,engine}` and `internal/cli` packages.

**Spec:** `docs/superpowers/specs/2026-09-13-fmt-autofix-design.md`

## Global Constraints

- No new `go.mod` dependencies.
- Every test asserts exact values, never just "no error."
- `go build ./... && go vet ./... && go test ./... -count=1 -race` clean before every
  task is done.
- Commits signed (`-S`), commitlint conventions per `.agents/skills/git-commit/SKILL.md`
  (check `.commitlintrc.js`'s `scopes` list before picking a scope name -- e.g.
  `pkg/trunk/download` commits use scope `cache`, not `download`).
- `Command.InPlace` (not `Formatter`) gates the new file-hashing mechanism -- `prettier`'s
  real catalog command is `Formatter: true` with `Output: sarif`, so gating on the wrong
  flag would silently skip hashing for it.

---

### Task 1: engine -- rewrite/shfmt support, ChangedFiles, Command.Enabled

**Files:**

- Modify: `pkg/trunk/config/definitions.go` (add `Command.Enabled *bool`)
- Modify: `pkg/trunk/engine/engine.go`
- Test: `pkg/trunk/config/definitions_test.go`, `pkg/trunk/engine/engine_test.go`

**Interfaces:**

- Produces: `config.Command.Enabled *bool` (nil = on by default); `engine.Event.ChangedFiles
[]string` (Done only, repoRoot-relative, populated only for `InPlace` commands).
- Consumes: nothing new from elsewhere in this plan (Task 2 consumes `Event.ChangedFiles`
  and the direct `engine.Run` call pattern this task doesn't otherwise change).

- [ ] **Step 1: Add `Command.Enabled`**

In `pkg/trunk/config/definitions.go`, in the `Command` struct, add (after `Formatter`):

```go
	Formatter      bool    `yaml:"formatter,omitempty"`
	// Enabled defaults a command on (nil) or explicitly off (real catalog example: ruff's own
	// "format" command sets false, since ruff-format competes with black) -- distinct from
	// Linter-level enable/disable (trunk.yaml's lint.enabled: list), which this field does not
	// touch. rtunk has no trunk.yaml-level override for a single command's own Enabled today
	// (a real gap, deliberately out of scope); this field only ever reflects what the plugin
	// source's own catalog data says.
	Enabled        *bool   `yaml:"enabled,omitempty"`
```

- [ ] **Step 2: Test `Command.Enabled`'s YAML round trip**

Append to `pkg/trunk/config/definitions_test.go`:

```go
// TestCommand_Enabled checks that an absent yaml "enabled:" key decodes to nil (a command is on
// by default), while an explicit "enabled: false" decodes to a non-nil false -- real catalog data
// (ruff's own "format" command) relies on distinguishing "not specified" from "explicitly off".
func TestCommand_Enabled(t *testing.T) {
	var withDefault config.Command
	require.NoError(t, yaml.Unmarshal([]byte(`
name: format
run: echo hi
`), &withDefault))
	assert.Nil(t, withDefault.Enabled, "no enabled: key at all must default to on (nil), not false")

	var explicitOff config.Command
	require.NoError(t, yaml.Unmarshal([]byte(`
name: format
run: echo hi
enabled: false
`), &explicitOff))
	require.NotNil(t, explicitOff.Enabled)
	assert.False(t, *explicitOff.Enabled)
}
```

Run: `go test ./pkg/trunk/config/... -run TestCommand_Enabled -v`
Expected: PASS.

- [ ] **Step 3: Extend `supportedOutputFormats`**

In `pkg/trunk/engine/engine.go`, replace:

```go
var supportedOutputFormats = map[string]bool{
	"sarif": true, "sarif_uri": true, "pass_fail": true,
	"actionlint": true, "bandit": true, "buildifier": true, "cfnlint": true,
	"eslint": true, "hadolint": true, "haml_lint": true, "markdownlint": true,
	"pylint": true, "rubocop": true, "stylelint": true, "taplo": true, "regex": true,
}
```

with:

```go
var supportedOutputFormats = map[string]bool{
	"sarif": true, "sarif_uri": true, "pass_fail": true,
	"actionlint": true, "bandit": true, "buildifier": true, "cfnlint": true,
	"eslint": true, "hadolint": true, "haml_lint": true, "markdownlint": true,
	"pylint": true, "rubocop": true, "stylelint": true, "taplo": true, "regex": true,
	// rewrite/shfmt: real catalog formatter commands (gofmt, black, rustfmt, isort, autopep8,
	// rubocop's fix-layout, stylelint's fix) with nothing to parse -- success is decided purely
	// by ErrorCodes; the caller learns what changed via Event.ChangedFiles instead.
	"rewrite": true, "shfmt": true,
}
```

- [ ] **Step 4: Add `Event.ChangedFiles` and `linterState.changedFiles`**

Replace the `Event` struct:

```go
type Event struct {
	Linter   string
	Phase    Phase
	Findings []output.Finding // Done only
	Note     string           // Skipped (why) or Failed (which command)
	Err      error            // Failed only
	File     string           // Running only -- the file (or comma-joined batch) about to be checked
}
```

with:

```go
type Event struct {
	Linter       string
	Phase        Phase
	Findings     []output.Finding // Done only
	ChangedFiles []string         // Done only, InPlace commands only -- repoRoot-relative paths this linter actually rewrote (content differed before/after)
	Note         string           // Skipped (why) or Failed (which command)
	Err          error            // Failed only
	File         string           // Running only -- the file (or comma-joined batch) about to be checked
}
```

Replace `linterState`:

```go
type linterState struct {
	mu           sync.Mutex
	remaining    int
	findings     []output.Finding
	terminalSent bool
	failed       bool // once true, workers skip any not-yet-started job for this linter
}
```

with:

```go
type linterState struct {
	mu           sync.Mutex
	remaining    int
	findings     []output.Finding
	changedFiles []string
	terminalSent bool
	failed       bool // once true, workers skip any not-yet-started job for this linter
}
```

- [ ] **Step 5: Update `Run`'s doc comment (the stale "future fmt package" paragraph)**

Replace:

```go
// include selects which of a linter's commands are runnable -- pkg/trunk/check passes
// `func(c config.Command) bool { return !c.Formatter }`; a future pkg/trunk/fmt would pass the
// complement, but that alone is not enough to make fmt a thin sibling the way this doc used to
// imply: the predicate only decides which commands are considered, while everything downstream of
// it -- supportedOutputFormats, the Output-format dispatch switch, Event.Findings' []output.Finding
// shape -- is still shaped around "check" reporting. A real formatter command (Output: "rewrite" or
// "shfmt" in the real trunk-io catalog, InPlace: true) has neither a supported Output value here
// nor any notion of "reformatted successfully" to report. What this package genuinely gives a
// future fmt package is the job queue, RunFrom/SandboxType resolution, and file matching; the
// output/reporting half remains fmt's own work.
```

with:

```go
// include selects which of a linter's commands are runnable -- internal/cli's check command
// passes `func(c config.Command) bool { return !c.Formatter }`, its fmt command passes the
// complement. A Formatter command (Output: "rewrite" or "shfmt" in the real trunk-io catalog,
// InPlace: true) reports via Event.ChangedFiles (a before/after content-hash comparison per file,
// see runBatch) rather than Event.Findings -- there is nothing to parse; success is decided
// purely by ErrorCodes, same as any other command.
```

- [ ] **Step 6: Add the `Enabled` and `InPlace`+`SandboxType` skip checks to `buildJobs`**

In `buildJobs`, replace:

```go
	for _, cmd := range linter.Commands {
		if !include(cmd) {
			continue
		}
		if v, ok := findUnsupportedVar(cmd.Run); ok {
```

with:

```go
	for _, cmd := range linter.Commands {
		if !include(cmd) {
			continue
		}
		if cmd.Enabled != nil && !*cmd.Enabled {
			events <- Event{Linter: name, Phase: Skipped, Note: "disabled by its own plugin source"}
			continue
		}
		if v, ok := findUnsupportedVar(cmd.Run); ok {
```

Then, still in `buildJobs`, replace:

```go
		if cmd.SandboxType != "" && cmd.SandboxType != "copy_targets" && cmd.SandboxType != "expanded" {
			events <- Event{Linter: name, Phase: Skipped, Note: fmt.Sprintf("unsupported sandbox_type %q", cmd.SandboxType)}
			continue
		}
```

with:

```go
		if cmd.InPlace && cmd.SandboxType != "" {
			events <- Event{Linter: name, Phase: Skipped, Note: "in_place command combined with sandbox_type is unsupported (writes would be lost)"}
			continue
		}
		if cmd.SandboxType != "" && cmd.SandboxType != "copy_targets" && cmd.SandboxType != "expanded" {
			events <- Event{Linter: name, Phase: Skipped, Note: fmt.Sprintf("unsupported sandbox_type %q", cmd.SandboxType)}
			continue
		}
```

- [ ] **Step 7: Add `hashFiles` and `remapPaths`, import `crypto/sha256`**

Add `"crypto/sha256"` to `engine.go`'s import block (alphabetically after `"context"`):

```go
import (
	"context"
	"crypto/sha256"
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
```

Add these two functions right after `containsInt`:

```go
// hashFiles returns each file's (dir-joined) SHA-256 content hash, keyed by its own entry in
// files -- a missing file (deleted, or never existed) is simply absent from the result, so
// comparing a before/after pair naturally treats "existed then, gone now" (and vice versa) as
// changed, the same as any other content difference, with no special-casing needed.
func hashFiles(dir string, files []string) map[string][32]byte {
	hashes := make(map[string][32]byte, len(files))
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			continue
		}
		hashes[f] = sha256.Sum256(data)
	}
	return hashes
}

// remapPaths is security.RemapFindings' own base/repoRoot remap logic, for a plain list of
// dir-relative paths instead of []output.Finding -- used for Event.ChangedFiles, which has no
// Finding struct to carry a File field.
func remapPaths(paths []string, base, repoRoot string) []string {
	if base == repoRoot || len(paths) == 0 {
		return paths
	}
	out := make([]string, len(paths))
	for i, p := range paths {
		abs := p
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(base, p)
		}
		if rel, err := filepath.Rel(repoRoot, abs); err == nil {
			out[i] = rel
		} else {
			out[i] = p
		}
	}
	return out
}
```

- [ ] **Step 8: Wire hashing and the `rewrite`/`shfmt` no-op case into `runBatch`, update its signature and every caller**

Replace `runBatch` entirely:

```go
// runBatch runs one job's invocation and parses its output per cmd.Output, remapping every
// finding's File back to repoRoot-relative before returning. If j.cmd.SandboxType is set, the
// invocation actually runs against a temporary staged copy (see security.StageSandbox); the
// parser only ever sees paths relative to j.resolvedDir, exactly as when no sandboxing is
// involved -- security.RemapFindings is what turns those back into repoRoot-relative paths either
// way. The second return value is the repoRoot-relative subset of j.batch this command actually
// changed on disk (InPlace commands only, via hashFiles' before/after comparison) -- always nil
// for a non-InPlace command.
func runBatch(ctx context.Context, j job, repoRoot string) ([]output.Finding, []string, error) {
	workDir := j.resolvedDir
	if j.cmd.SandboxType != "" {
		sandboxDir, cleanup, err := security.StageSandbox(j.cmd.SandboxType, j.resolvedDir, j.batch)
		if cleanup != nil {
			defer cleanup()
		}
		if err != nil {
			return nil, nil, err
		}
		workDir = sandboxDir
	}

	pluginDir := j.linter.SourceRoot
	cwdDir := filepath.Join(j.linter.SourceRoot, j.linter.SourceDir)

	var beforeHashes map[string][32]byte
	if j.cmd.InPlace {
		beforeHashes = hashFiles(workDir, j.batch)
	}

	out, stderr, exitCode, err := runOneInvocation(ctx, j.cmd, workDir, j.pathEnv, j.batch, pluginDir, cwdDir)
	if err != nil {
		return nil, nil, err
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
		return nil, nil, fmt.Errorf("engine: %s: %s exited %d: %s", j.linterName, j.cmd.Name, exitCode, msg)
	}

	var changedFiles []string
	if j.cmd.InPlace {
		afterHashes := hashFiles(workDir, j.batch)
		for _, f := range j.batch {
			if beforeHashes[f] != afterHashes[f] {
				changedFiles = append(changedFiles, f)
			}
		}
		changedFiles = remapPaths(changedFiles, workDir, repoRoot)
	}

	// A Parser converts the real command's raw output into cmd.Output's expected shape (almost
	// always SARIF) before any of the dispatch below runs -- everything from here on parses out
	// exactly as if the real tool had produced it directly, whether or not a parser was involved.
	// Skipped when out is empty: a genuinely clean run (or an OS-gated command variant producing
	// nothing) must fall through to the isJSONFormat empty-output guard below unparsed, not feed
	// empty stdin to a converter script that may not tolerate it (e.g. Python's
	// json.load(sys.stdin) raises on empty input).
	if j.cmd.Parser != nil && strings.TrimSpace(out) != "" {
		converted, err := runParser(ctx, j.cmd.Parser, workDir, j.parserPathEnv, out, j.batch, pluginDir, cwdDir)
		if err != nil {
			return nil, nil, err
		}
		out = converted
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
	// never parses JSON (exit-code only), "regex" (output.ParseFromRegex) already tolerates empty
	// input (zero regex matches), and "rewrite"/"shfmt" never parse anything at all -- all four are
	// excluded from this guard.
	isJSONFormat := j.cmd.Output != "pass_fail" && j.cmd.Output != "regex" &&
		j.cmd.Output != "rewrite" && j.cmd.Output != "shfmt"
	if isJSONFormat && strings.TrimSpace(out) == "" {
		security.RemapFindings(findings, j.resolvedDir, repoRoot)
		output.ApplyIssueURL(findings, j.linter.IssueURLFormat)
		return findings, changedFiles, nil
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
	case "rewrite", "shfmt":
		// No structured output to parse -- success is already decided by the ErrorCodes check
		// above; the caller learns what changed via changedFiles (populated above) instead.
	}
	if err != nil {
		return nil, nil, err
	}
	if workDir != j.resolvedDir {
		// workDir is a sandbox mirroring j.resolvedDir's structure. Most tools echo back the
		// relative path we substituted into ${target}, which is already correct as-is -- but a
		// tool that echoes an absolute path instead would otherwise leak the throwaway sandbox
		// directory into the final report. Rewrite only the absolute case; a relative one needs
		// no help, it's already resolvedDir-relative by construction.
		//
		// A tool that builds its own absolute path (e.g. via getcwd()) reports the OS's physical
		// path, which can differ from workDir's own literal string when a symlink sits somewhere
		// in the tempdir prefix (macOS: /tmp -> /private/tmp, /var -> /private/var, both live
		// under os.MkdirTemp's default root) -- resolve workDir the same way before comparing, so
		// this doesn't just work by coincidence on platforms with no such symlink.
		//
		// This is a real behavior security.RemapFindings does not perform on its own: it always
		// remaps relative to j.resolvedDir, so an absolute finding path pointing into the sandbox
		// (not j.resolvedDir) would otherwise pass through unrewritten whenever j.resolvedDir ==
		// repoRoot (RemapFindings' own no-op guard).
		base := workDir
		if resolved, err := filepath.EvalSymlinks(workDir); err == nil {
			base = resolved
		}
		for i, f := range findings {
			if filepath.IsAbs(f.File) {
				if rel, err := filepath.Rel(base, f.File); err == nil {
					findings[i].File = rel
				}
			}
		}
	}
	security.RemapFindings(findings, j.resolvedDir, repoRoot)
	output.ApplyIssueURL(findings, j.linter.IssueURLFormat)
	return findings, changedFiles, nil
}
```

Then update `runJob` (its only caller) -- replace:

```go
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
```

with:

```go
	events <- Event{Linter: j.linterName, Phase: Running, File: strings.Join(j.batch, ", ")}
	findings, changedFiles, err := runBatch(ctx, j, repoRoot)

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
	state.changedFiles = append(state.changedFiles, changedFiles...)
	if state.remaining == 0 {
		state.terminalSent = true
		events <- Event{Linter: j.linterName, Phase: Done, Findings: state.findings, ChangedFiles: state.changedFiles}
	}
}
```

- [ ] **Step 9: Run the existing suite to confirm the signature change compiles cleanly everywhere**

Run: `go build ./... && go test ./pkg/trunk/engine/... -count=1 -v`
Expected: PASS -- `runBatch` has exactly one caller (`runJob`, just updated), so nothing else
should need touching. If anything else fails to compile, it is calling `runBatch` directly and
was missed above.

- [ ] **Step 10: Add the fake `"rewrite"` tool case**

In `pkg/trunk/engine/engine_test.go`, add this case to `fakeToolSrc`'s `switch args[0]` block,
right after the existing `"abspath"` case:

```go
	case "rewrite":
		// Simulates a real in-place formatter (gofmt -w): overwrites each target file with fixed
		// content, so a test can prove InPlace hashing detects a genuine content change on one
		// file while correctly excluding another whose content was already identical.
		for _, f := range args[1:] {
			os.WriteFile(f, []byte("formatted\n"), 0o644)
		}
```

- [ ] **Step 11: Write the new engine tests**

Append to `pkg/trunk/engine/engine_test.go`:

```go
// TestRun_InPlaceReportsOnlyGenuinelyChangedFiles proves ChangedFiles reflects a real content
// difference, not just "the command ran" -- one file already has the formatter's target content
// (untouched by the rewrite), the other doesn't (genuinely rewritten).
func TestRun_InPlaceReportsOnlyGenuinelyChangedFiles(t *testing.T) {
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
	alreadyFormatted := filepath.Join(repoRoot, "already.txt")
	messy := filepath.Join(repoRoot, "messy.txt")
	require.NoError(t, os.WriteFile(alreadyFormatted, []byte("formatted\n"), 0o644))
	require.NoError(t, os.WriteFile(messy, []byte("messy\n"), 0o644))

	cfg := config.Config{
		Tools: map[string]config.Tool{
			"faketool": {Name: "faketool", KnownGoodVersion: "1.0.0"},
		},
		Lint: config.LintConfig{
			Files: map[string]config.FileType{},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"fakefmt": {
						Name: "fakefmt", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{{
							Name: "format", Run: "faketool rewrite ${target}", Output: "rewrite",
							SuccessCodes: []int{0}, Batch: true, InPlace: true, Formatter: true,
						}},
					},
				},
			},
		},
	}

	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: cacheDir, Concurrency: 1}, nil, func(c config.Command) bool { return c.Formatter })
	require.NoError(t, err)

	var got Event
	for ev := range events {
		if ev.Linter == "fakefmt" && ev.Phase == Done {
			got = ev
		}
	}
	assert.Empty(t, got.Findings, "a rewrite/shfmt command has nothing to parse")
	assert.Equal(t, []string{"messy.txt"}, got.ChangedFiles,
		"only the file whose content genuinely differs before/after must be reported changed")
}

// TestRun_InPlaceWithSandboxIsSkipped covers the new explicit skip: no real catalog formatter
// combines InPlace with SandboxType (a sandboxed write would be silently lost), so this project
// rejects the combination outright rather than silently discarding a fix.
func TestRun_InPlaceWithSandboxIsSkipped(t *testing.T) {
	cfg := config.Config{
		Lint: config.LintConfig{
			Files: map[string]config.FileType{},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"fakebadfmt": {
						Name: "fakebadfmt", Files: []string{"ALL"},
						Commands: []config.Command{{
							Name: "format", Run: "faketool rewrite ${target}", Output: "rewrite",
							InPlace: true, SandboxType: "copy_targets",
						}},
					},
				},
			},
		},
	}
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "a.txt"), []byte("x"), 0o644))

	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, Concurrency: 1}, nil, func(c config.Command) bool { return true })
	require.NoError(t, err)

	var got Event
	for ev := range events {
		got = ev
	}
	assert.Equal(t, Skipped, got.Phase)
	assert.Contains(t, got.Note, "sandbox_type is unsupported")
}

// TestRun_DisabledCommandIsSkipped covers Command.Enabled: false -- real catalog example: ruff's
// own "format" command defaults off since ruff-format competes with black.
func TestRun_DisabledCommandIsSkipped(t *testing.T) {
	disabled := false
	cfg := config.Config{
		Lint: config.LintConfig{
			Files: map[string]config.FileType{},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"fakedisabled": {
						Name: "fakedisabled", Files: []string{"ALL"},
						Commands: []config.Command{{
							Name: "format", Run: "faketool rewrite ${target}", Output: "rewrite",
							InPlace: true, Enabled: &disabled,
						}},
					},
				},
			},
		},
	}
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "a.txt"), []byte("x"), 0o644))

	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, Concurrency: 1}, nil, func(c config.Command) bool { return true })
	require.NoError(t, err)

	var got Event
	for ev := range events {
		got = ev
	}
	assert.Equal(t, Skipped, got.Phase)
	assert.Equal(t, "disabled by its own plugin source", got.Note)
}

func TestHashFiles_DetectsContentChange(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one"), 0o644))
	before := hashFiles(dir, []string{"a.txt", "missing.txt"})
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("two"), 0o644))
	after := hashFiles(dir, []string{"a.txt", "missing.txt"})

	assert.NotEqual(t, before["a.txt"], after["a.txt"])
	_, missingBefore := before["missing.txt"]
	_, missingAfter := after["missing.txt"]
	assert.False(t, missingBefore, "a file that never existed must be absent from the result, not zero-valued")
	assert.False(t, missingAfter)
}

func TestRemapPaths(t *testing.T) {
	assert.Equal(t, []string{"sub/a.txt"}, remapPaths([]string{"a.txt"}, "/repo/sub", "/repo"))
	assert.Equal(t, []string{"a.txt"}, remapPaths([]string{"a.txt"}, "/repo", "/repo"),
		"same base and repoRoot is a no-op, matching security.RemapFindings' own guard")
}
```

- [ ] **Step 12: Run the new tests, then the full package**

Run: `go test ./pkg/trunk/engine/... -count=1 -race -v`
Expected: PASS, including every test above and every pre-existing test in the package.

- [ ] **Step 13: Full build/vet/test, then commit**

Run: `go build ./... && go vet ./... && go test ./... -count=1 -race`
Expected: PASS across every package.

```bash
git add pkg/trunk/config/definitions.go pkg/trunk/config/definitions_test.go \
        pkg/trunk/engine/engine.go pkg/trunk/engine/engine_test.go
git commit -S -m "$(cat <<'EOF'
+[engine]: Support in-place formatter commands (rewrite/shfmt, ChangedFiles)

ROADMAP.md v0.4 needs a way to run Command.Formatter commands and know
which files they actually touched. Adds the two Output values every real
catalog plain formatter uses (rewrite, shfmt -- both have nothing to
parse, success is decided by ErrorCodes alone) and Event.ChangedFiles, a
SHA-256 before/after comparison gated on Command.InPlace specifically
(not Formatter -- prettier's own real command is Formatter: true with
Output: sarif, so gating on the wrong flag would silently skip hashing
for it). Also adds Command.Enabled (ruff's own real "format" command sets
enabled: false to avoid competing with black; config.Command had no field
for it) and an explicit skip for InPlace+SandboxType, a combination no
real catalog command uses since a sandboxed write would be silently lost.
EOF
)"
```

---

### Task 2: internal/cli -- `rtunk fmt`, `check --fix`, delete pkg/trunk/check

**Files:**

- Delete: `pkg/trunk/check/check.go` (and the now-empty `pkg/trunk/check/` directory)
- Modify: `internal/cli/check.go`, `internal/cli/cli.go`
- Create: `internal/cli/fmt.go`
- Test: `internal/cli/check_run_test.go`, `internal/cli/fmt_test.go` (new)

**Interfaces:**

- Consumes: `engine.Run` (unchanged public signature), `engine.Event.ChangedFiles` (Task 1),
  `config.Command.Formatter` (pre-existing).
- Produces: `drainRunEvents(printFn func(engine.Event), events <-chan engine.Event)
(findings []output.Finding, changed []string, skipped []string, failed error)`,
  `printFmtEvent(w io.Writer, ev engine.Event)`, `printFmtReport(w io.Writer, changed,
skipped []string)` -- all in `check.go`, used by both `check.go`'s own `--fix` pass and
  `fmt.go`.

- [ ] **Step 1: Delete `pkg/trunk/check`**

```bash
rm -rf pkg/trunk/check
```

(Confirmed via `grep -rln "pkg/trunk/check" --include="*.go" .` before this plan was written:
its only real, non-doc-comment consumer is `internal/cli/check.go`'s one-line predicate wrapper,
updated in Step 2 below. The package has no test file of its own.)

- [ ] **Step 2: Rewrite `internal/cli/check.go`'s `checkRunCmd` to call `engine.Run` directly, add `--fix`, and factor `drainRunEvents`/`printFmtEvent`/`printFmtReport`**

Replace the whole file's import block:

```go
import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/xunleii/rtunk/pkg/trunk/config"
	"github.com/xunleii/rtunk/pkg/trunk/engine"
	"github.com/xunleii/rtunk/pkg/trunk/output"
)
```

(drops the `pkg/trunk/check` import -- everything else in the existing import block is
unchanged).

Replace `checkRunCmd` and its `Run` method entirely:

```go
// checkRunCmd is `rtunk check [paths...]`: given paths, or the whole repository if none.
type checkRunCmd struct {
	Paths []string `arg:"" optional:"" help:"Paths to check (default: whole repository)."`
	Jobs  int      `short:"j" help:"Number of parallel linter workers (default: number of CPUs)."`
	Fix   bool     `help:"Apply automatic fixes (formatter commands) before reporting."`
}

func (c *checkRunCmd) Run(cli *CLI, stdout io.Writer, stderr Stderr) error {
	configPath := cli.Config
	if configPath == "" {
		found, err := findTrunkYAML()
		if err != nil {
			return err
		}
		configPath = found
	}
	cfg, err := resolveConfig(configPath, cli.CacheDir, false)
	if err != nil {
		return err
	}
	// configPath is <repoRoot>/.trunk/trunk.yaml (findTrunkYAML's only supported layout) --
	// repoRoot is two directories up.
	repoRoot := filepath.Dir(filepath.Dir(configPath))

	jobs := c.Jobs
	if jobs <= 0 {
		jobs = runtime.NumCPU()
	}
	env := engine.Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: cli.CacheDir, Concurrency: jobs}

	// --fix runs every Formatter command first (the exact same selection `rtunk fmt` uses) and
	// lets it finish writing before the checking pass below ever reads the same files -- an
	// issue the formatter genuinely fixed is, by definition, no longer wrong by the time the
	// checking commands run, so it never appears in the findings this command reports. A Failed
	// event in this pass is recorded but does not abort: the checking pass below still runs,
	// matching this project's existing per-linter failure isolation (one linter's Failed event
	// has never stopped its siblings from running).
	var fixFailed error
	if c.Fix {
		fixEvents, err := engine.Run(context.Background(), env, c.Paths, func(cmd config.Command) bool { return cmd.Formatter })
		if err != nil {
			return err
		}
		_, changed, fixSkipped, ffErr := drainRunEvents(func(ev engine.Event) { printFmtEvent(stderr, ev) }, fixEvents)
		fixFailed = ffErr
		printFmtReport(stdout, changed, fixSkipped)
	}

	events, err := engine.Run(context.Background(), env, c.Paths, func(cmd config.Command) bool { return !cmd.Formatter })
	if err != nil {
		return err
	}
	findings, _, skipped, failed := drainRunEvents(func(ev engine.Event) { printEvent(stderr, ev) }, events)

	printReport(stdout, findings, skipped)
	if fixFailed != nil {
		return fixFailed
	}
	if failed != nil {
		return failed
	}
	if len(findings) > 0 {
		return fmt.Errorf("rtunk: check found %d issue(s)", len(findings))
	}
	return nil
}

// drainRunEvents streams every event from events through printFn as it arrives and accumulates
// its terminal Done/Skipped/Failed outcomes -- shared by check's own reporting pass and check
// --fix's earlier formatter pass, which differ only in which of findings/changed the caller goes
// on to use (the other is simply empty: a Formatter command has no Findings to report, and a
// non-Formatter one has no ChangedFiles).
func drainRunEvents(printFn func(engine.Event), events <-chan engine.Event) (findings []output.Finding, changed []string, skipped []string, failed error) {
	skippedLinters := map[string]bool{}
	for ev := range events {
		printFn(ev)
		switch ev.Phase {
		case engine.Done:
			findings = append(findings, ev.Findings...)
			changed = append(changed, ev.ChangedFiles...)
		case engine.Skipped:
			// Dedupe by linter: a linter with several unsupported commands emits one Skipped
			// event per command, but the report should name it once, not once per command.
			if !skippedLinters[ev.Linter] {
				skippedLinters[ev.Linter] = true
				skipped = append(skipped, fmt.Sprintf("%s [%s]", ev.Linter, ev.Note))
			}
		case engine.Failed:
			if failed == nil {
				failed = ev.Err
			}
		}
	}
	return findings, changed, skipped, failed
}
```

Then, right after the existing `printEvent` function (leave `printEvent` itself completely
unchanged), add:

```go
// printFmtEvent is printEvent's fmt/--fix-pass equivalent: a Done event here reports how many
// files a formatter actually changed (Event.ChangedFiles), not how many issues it found -- a
// Formatter command has nothing to report as a Finding.
func printFmtEvent(w io.Writer, ev engine.Event) {
	switch ev.Phase {
	case engine.Running:
		fmt.Fprintf(w, "running %s: %s\n", ev.Linter, ev.File)
	case engine.Done:
		fmt.Fprintf(w, "done %s: %d file(s) changed\n", ev.Linter, len(ev.ChangedFiles))
	case engine.Skipped:
		fmt.Fprintf(w, "skipped %s: %s\n", ev.Linter, ev.Note)
	case engine.Failed:
		fmt.Fprintf(w, "failed: %v\n", ev.Err)
	}
}
```

And right after the existing `printReport` function (leave `printReport` itself completely
unchanged), add:

```go
// printFmtReport is printReport's fmt/--fix-pass equivalent: lists which files were actually
// changed (sorted), then a trailing summary line, mirroring printReport's own shape (one line
// per item, then a blank line, then the summary) so the two report kinds read consistently.
func printFmtReport(w io.Writer, changed []string, skipped []string) {
	sorted := append([]string(nil), changed...)
	sort.Strings(sorted)
	for _, f := range sorted {
		fmt.Fprintln(w, f)
	}

	summary := fmt.Sprintf("%d file(s) reformatted", len(sorted))
	if len(skipped) > 0 {
		sortedSkipped := append([]string(nil), skipped...)
		sort.Strings(sortedSkipped)
		summary += fmt.Sprintf(" (%d linter(s) skipped: %s)", len(sortedSkipped), strings.Join(sortedSkipped, ", "))
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, summary)
}
```

Every other function in `check.go` (`checkListCmd`, `formatLintList`, `checkEnableCmd`,
`checkDisableCmd`, `editEnabled`, `detectIndentWidth`, `findOrCreateMapKey`, `addEnabled`,
`removeEnabled`, `formatFinding`) is untouched.

- [ ] **Step 3: Create `internal/cli/fmt.go`**

```go
package cli

import (
	"context"
	"io"
	"path/filepath"
	"runtime"

	"github.com/xunleii/rtunk/pkg/trunk/config"
	"github.com/xunleii/rtunk/pkg/trunk/engine"
)

// fmtCmd is `rtunk fmt [paths...]`: ROADMAP.md v0.4, running every enabled linter's Formatter
// command(s) -- gofmt, prettier, black, and the rest -- against the given paths, or the whole
// repository if none are given.
type fmtCmd struct {
	Paths []string `arg:"" optional:"" help:"Paths to format (default: whole repository)."`
	Jobs  int      `short:"j" help:"Number of parallel linter workers (default: number of CPUs)."`
}

func (c *fmtCmd) Run(cli *CLI, stdout io.Writer, stderr Stderr) error {
	configPath := cli.Config
	if configPath == "" {
		found, err := findTrunkYAML()
		if err != nil {
			return err
		}
		configPath = found
	}
	cfg, err := resolveConfig(configPath, cli.CacheDir, false)
	if err != nil {
		return err
	}
	repoRoot := filepath.Dir(filepath.Dir(configPath))

	jobs := c.Jobs
	if jobs <= 0 {
		jobs = runtime.NumCPU()
	}

	events, err := engine.Run(context.Background(), engine.Env{
		Cfg: cfg, RepoRoot: repoRoot, CacheDir: cli.CacheDir, Concurrency: jobs,
	}, c.Paths, func(cmd config.Command) bool { return cmd.Formatter })
	if err != nil {
		return err
	}

	_, changed, skipped, failed := drainRunEvents(func(ev engine.Event) { printFmtEvent(stderr, ev) }, events)
	printFmtReport(stdout, changed, skipped)
	return failed
}
```

- [ ] **Step 4: Register `fmt` on the CLI grammar**

In `internal/cli/cli.go`, replace:

```go
	CheckCmd    checkCmd    `cmd:"" name:"check" help:"Run enabled checks against source files (read-only)."`
}
```

with:

```go
	CheckCmd    checkCmd    `cmd:"" name:"check" help:"Run enabled checks against source files (read-only)."`
	FmtCmd      fmtCmd      `cmd:"" name:"fmt" help:"Run configured formatters against source files."`
}
```

- [ ] **Step 5: Run the existing CLI suite to confirm nothing broke**

Run: `go test ./internal/cli/... -count=1 -v`
Expected: PASS -- `TestCheckRunCmd_SkipsUnsupportedFormats`'s `formatter-only` fixture (a real
`output: rewrite, formatter: true, in_place: true` command) must still produce NO event at all
under plain `check` (its own test assertion), since `!cmd.Formatter` still excludes it before
Task 1's new `supportedOutputFormats` entries are ever consulted.

- [ ] **Step 6: Write the new `fmt` test**

Create `internal/cli/fmt_test.go`:

```go
package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFmtCmd_ReportsOnlyChangedFiles drives `rtunk fmt` end to end against a real fake formatter
// command (a plain shell one-liner, no compiled binary needed): one file already holds the
// formatter's target content (untouched), the other doesn't (genuinely rewritten) -- the report
// must name only the second.
func TestFmtCmd_ReportsOnlyChangedFiles(t *testing.T) {
	cfgPath, repoRoot := writeLinterFixture(t, []string{"fakefmt"}, `    - name: fakefmt
      description: A fake in-place formatter
      files: [ALL]
      commands:
        - name: format
          run: printf 'formatted\n' > ${target}
          output: rewrite
          success_codes: [0]
          in_place: true
          formatter: true
`)
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "work"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "work", "already.txt"), []byte("formatted\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "work", "messy.txt"), []byte("messy\n"), 0o644))

	cacheDir := t.TempDir()
	stdout, stderr, err := run2(t, "--config", cfgPath, "--cache-dir", cacheDir, "fmt", filepath.Join(repoRoot, "work"))
	require.NoError(t, err, "stderr: %s", stderr)

	want := "work/messy.txt\n\n1 file(s) reformatted\n"
	assert.Equal(t, want, stdout)
}
```

- [ ] **Step 7: Write the `check --fix` end-to-end test**

Append to `internal/cli/check_run_test.go`:

```go
// TestCheckRunCmd_Fix_AppliesFixesBeforeReporting proves the two-pass ordering for real: a linter
// with both a checking command (fails while the file's content isn't "formatted\n") and a
// formatter command (rewrites it to exactly that). Plain `check` must report the finding; `check
// --fix` must not, since the formatter pass fixes the file before the checking pass ever reads it.
func TestCheckRunCmd_Fix_AppliesFixesBeforeReporting(t *testing.T) {
	cfgPath, repoRoot := writeLinterFixture(t, []string{"fakefix"}, `    - name: fakefix
      description: A fake linter with both a checker and a formatter command
      files: [ALL]
      commands:
        - name: lint
          run: grep -qxF formatted ${target}
          output: pass_fail
        - name: format
          run: printf 'formatted\n' > ${target}
          output: rewrite
          success_codes: [0]
          in_place: true
          formatter: true
`)
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "work"), 0o755))
	target := filepath.Join(repoRoot, "work", "file.txt")
	require.NoError(t, os.WriteFile(target, []byte("messy\n"), 0o644))

	cacheDir := t.TempDir()

	// Without --fix: the checking command reports the file as failing.
	stdout, stderr, err := run2(t, "--config", cfgPath, "--cache-dir", cacheDir, "check", filepath.Join(repoRoot, "work"))
	require.Error(t, err, "stderr: %s", stderr)
	assert.Contains(t, stdout, "1 issue(s) in 1 file(s)")

	require.NoError(t, os.WriteFile(target, []byte("messy\n"), 0o644)) // reset for the --fix run

	// With --fix: the formatter pass rewrites the file before the checking pass ever reads it, so
	// the finding that showed up above must be absent here.
	stdout, stderr, err = run2(t, "--config", cfgPath, "--cache-dir", cacheDir, "check", "--fix", filepath.Join(repoRoot, "work"))
	require.NoError(t, err, "stderr: %s", stderr)
	assert.Contains(t, stdout, "1 file(s) reformatted")
	assert.Contains(t, stdout, "0 issue(s) in 0 file(s)")

	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "formatted\n", string(data))
}
```

- [ ] **Step 8: Run the new tests, then the full package**

Run: `go test ./internal/cli/... -count=1 -v`
Expected: PASS, including both new tests and every pre-existing one.

- [ ] **Step 9: Full build/vet/test, then commit**

Run: `go build ./... && go vet ./... && go test ./... -count=1 -race`
Expected: PASS across every package (confirms `pkg/trunk/check`'s removal broke nothing).

```bash
git add -A pkg/trunk/check internal/cli/check.go internal/cli/cli.go internal/cli/fmt.go \
        internal/cli/fmt_test.go internal/cli/check_run_test.go
git commit -S -m "$(cat <<'EOF'
+[cli]: Add rtunk fmt and check --fix

ROADMAP.md v0.4. Both call engine.Run directly with an inline predicate
(cmd.Formatter for fmt, its complement for check) now that Task 1 taught
engine.Run to actually run and report on Formatter commands -- pkg/trunk/
check, a one-line predicate wrapper around the exact same call check.go
could have made directly, is deleted as redundant now that fmt needs the
identical pattern anyway. check --fix runs the formatter pass first and
lets it finish writing before the checking pass reads the same files, so
an issue the formatter fixed never appears in the final report -- proven
end to end, not just asserted, via a fixture whose checking command only
passes once the formatter command has actually run.
EOF
)"
```

---

## Self-Review Notes (for whoever runs this plan)

- **Spec coverage:** `Command.Enabled` (Task 1, Steps 1-2), `rewrite`/`shfmt` support and
  `ChangedFiles` hashing (Task 1, Steps 3-13), the `InPlace`+`SandboxType` skip (Task 1,
  Step 6), `pkg/trunk/check` deletion (Task 2, Step 1), `rtunk fmt` (Task 2, Steps 3-4,
  6), `check --fix`'s fix-then-report ordering (Task 2, Steps 2, 7). The spec's Non-goals
  (`fmt --check` dry-run, a trunk.yaml-level `Command.Enabled` override, diff display) have
  no corresponding task -- nothing to do there, by design.
- **Task 1 -> Task 2 dependency:** Task 2's `printFmtEvent`/`drainRunEvents` read
  `Event.ChangedFiles`, and `fmtCmd`'s own predicate only produces useful jobs once
  `supportedOutputFormats` accepts `rewrite`/`shfmt` -- Task 1 must land first. Task 1 has
  no dependency on Task 2 and is independently testable on its own.
- **`runBatch`'s signature change is the one place a missed call site would silently break
  the build**, not silently misbehave -- Step 9 (Task 1) exists specifically to catch that
  immediately, before any test-writing, since the compiler itself is the fastest check here.
