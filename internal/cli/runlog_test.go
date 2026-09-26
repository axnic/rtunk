package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/trunk/runlog"
)

// lastRun loads the newest run log of repoRoot, failing the test if there is none.
func lastRun(t *testing.T, cacheDir, repoRoot string) (runlog.Summary, []runlog.Event) {
	t.Helper()
	runs, err := runlog.List(cacheDir, repoRoot)
	require.NoError(t, err)
	require.NotEmpty(t, runs, "the command must have written a run log")
	events, err := runlog.Load(runs[0].Path)
	require.NoError(t, err)
	return runs[0], events
}

// checkFixture is a repo with one linter whose command always "finds" one issue (pass_fail on a
// non-zero exit), plus a file for it to run on.
func checkFixture(t *testing.T) (cfgPath, repoRoot, work string) {
	t.Helper()
	cfgPath, repoRoot = writeLinterFixture(t, []string{"alpha"}, `    - name: alpha
      description: Alpha linter
      files: [ALL]
      commands:
        - name: check
          run: "false"
          output: pass_fail
`)
	work = filepath.Join(repoRoot, "work")
	require.NoError(t, os.MkdirAll(work, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(work, "file.txt"), []byte("hi\n"), 0o644))
	return cfgPath, repoRoot, work
}

func TestCheckRunCmd_WritesRunLog(t *testing.T) {
	cfgPath, repoRoot, work := checkFixture(t)
	cacheDir := t.TempDir()

	_, _, err := run2(t, "--config", cfgPath, "--cache-dir", cacheDir, "check", work)
	require.Error(t, err, "the linter's finding makes check exit non-zero")

	run, events := lastRun(t, cacheDir, repoRoot)
	assert.Equal(t, "check", run.Cmd)
	assert.Equal(t, "ok", run.Status, "a finding is a result, not a failed run")

	start := events[0]
	assert.Equal(t, "run_start", start.T)
	assert.Equal(t, []string{"rtunk", "--config", cfgPath, "--cache-dir", cacheDir, "check", work}, start.Argv)
	assert.Equal(t, cfgPath, start.Config)
	assert.Equal(t, repoRoot, start.RepoRoot)
	assert.True(t, filepath.IsAbs(start.RepoRoot))

	var inv, findings *runlog.Event
	for i := range events {
		switch events[i].T {
		case "invocation":
			inv = &events[i]
		case "findings":
			findings = &events[i]
		}
	}
	require.NotNil(t, inv)
	assert.Equal(t, []string{"sh", "-c", "false"}, inv.Argv)
	require.NotNil(t, findings)
	assert.Len(t, findings.Findings, 1)
}

func TestCheckRunCmd_FailedLinterMarksRunFailed(t *testing.T) {
	cfgPath, repoRoot := writeLinterFixture(t, []string{"beta"}, `    - name: beta
      description: Beta linter
      files: [ALL]
      commands:
        - name: check
          run: "false"
          output: pass_fail
          error_codes: [1]
`)
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "file.txt"), []byte("hi\n"), 0o644))
	cacheDir := t.TempDir()

	_, _, err := run2(t, "--config", cfgPath, "--cache-dir", cacheDir, "check", filepath.Dir(filepath.Dir(cfgPath)))
	require.Error(t, err)

	run, _ := lastRun(t, cacheDir, repoRoot)
	assert.Equal(t, "failed", run.Status)
}

func TestFmtCmd_WritesRunLog(t *testing.T) {
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
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "messy.txt"), []byte("messy\n"), 0o644))
	cacheDir := t.TempDir()

	_, stderr, err := run2(t, "--config", cfgPath, "--cache-dir", cacheDir, "fmt", filepath.Dir(filepath.Dir(cfgPath)))
	require.NoError(t, err, "stderr: %s", stderr)

	run, events := lastRun(t, cacheDir, repoRoot)
	assert.Equal(t, "fmt", run.Cmd)
	assert.Equal(t, "ok", run.Status)
	var end *runlog.Event
	for i := range events {
		if events[i].T == "linter_end" {
			end = &events[i]
		}
	}
	require.NotNil(t, end)
	assert.Contains(t, end.Changed, "messy.txt", "fmt's linter_end must record which files it rewrote")
}

// writeActionFixture is a git repo with one enabled action, "greet", running runLine.
func writeActionFixture(t *testing.T, runLine string) (cfgPath, repoRoot string) {
	t.Helper()
	repoRoot = t.TempDir()
	require.NoError(t, exec.Command("git", "-C", repoRoot, "init", "-q").Run())
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, ".trunk"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "pluginrepo", "actions", "greet"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, ".trunk", "trunk.yaml"), []byte(`version: "0.1"
plugins:
  sources:
    - id: local
      local: ../pluginrepo
actions:
  enabled:
    - greet
`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "pluginrepo", "actions", "greet", "plugin.yaml"),
		[]byte("actions:\n  definitions:\n    - id: greet\n      description: Says hello\n      run: "+runLine+"\n"), 0o644))
	return filepath.Join(repoRoot, ".trunk", "trunk.yaml"), repoRoot
}

// pinStdinTerminal fixes what `actions run` believes about its stdin for the duration of a test.
func pinStdinTerminal(t *testing.T, isTerminal bool) {
	t.Helper()
	prev := stdinIsTerminal
	stdinIsTerminal = func() bool { return isTerminal }
	t.Cleanup(func() { stdinIsTerminal = prev })
}

func TestActionsRunCmd_InteractiveWritesNoLog(t *testing.T) {
	pinStdinTerminal(t, true)
	cfgPath, repoRoot := writeActionFixture(t, `"echo hello"`)
	cacheDir := t.TempDir()

	stdout, stderr, err := run2(t, "--config", cfgPath, "--cache-dir", cacheDir, "actions", "run", "greet")
	require.NoError(t, err, "stderr: %s", stderr)
	assert.Equal(t, "hello\n", stdout, "the action itself must run exactly as before")

	runs, err := runlog.List(cacheDir, repoRoot)
	require.NoError(t, err)
	assert.Empty(t, runs, "a person running an action from a terminal keeps their terminal: no log")
}

func TestActionsRunCmd_WritesRunLog(t *testing.T) {
	pinStdinTerminal(t, false)
	cfgPath, repoRoot := writeActionFixture(t, `"echo hello"`)
	cacheDir := t.TempDir()

	stdout, stderr, err := run2(t, "--config", cfgPath, "--cache-dir", cacheDir, "actions", "run", "greet")
	require.NoError(t, err, "stderr: %s", stderr)
	assert.Equal(t, "hello\n", stdout)

	run, events := lastRun(t, cacheDir, repoRoot)
	assert.Equal(t, "actions-run", run.Cmd)
	assert.Equal(t, "ok", run.Status)
	var inv, out *runlog.Event
	for i := range events {
		switch events[i].T {
		case "invocation":
			inv = &events[i]
		case "output":
			out = &events[i]
		}
	}
	require.NotNil(t, inv)
	assert.Equal(t, "greet", inv.Linter)
	require.NotNil(t, out)
	assert.Equal(t, "hello\n", out.Data)
}

// A stale hook (its action disabled or gone) matches nothing; it must not push real check/fmt
// runs out of the 50-run retention with empty logs.
func TestActionsRunCmd_HookMatchingNothingWritesNoLog(t *testing.T) {
	pinStdinTerminal(t, false)
	cfgPath, repoRoot := writeActionFixture(t, `"echo hello"`)
	cacheDir := t.TempDir()

	_, stderr, err := run2(t, "--config", cfgPath, "--cache-dir", cacheDir, "actions", "run", "--hook", "pre-commit")
	require.NoError(t, err, "stderr: %s", stderr)

	runs, err := runlog.List(cacheDir, repoRoot)
	require.NoError(t, err)
	assert.Empty(t, runs)
}

func TestActionsRunCmd_NonZeroExitMarksRunFailed(t *testing.T) {
	pinStdinTerminal(t, false)
	cfgPath, repoRoot := writeActionFixture(t, `"exit 4"`)
	cacheDir := t.TempDir()

	_, _, err := run2(t, "--config", cfgPath, "--cache-dir", cacheDir, "actions", "run", "greet")
	require.Error(t, err)

	run, _ := lastRun(t, cacheDir, repoRoot)
	assert.Equal(t, "failed", run.Status)
}

func TestUnwritableCacheDir_WarnsOnceAndTheRunStillWorks(t *testing.T) {
	cfgPath, _, work := checkFixture(t)
	blocker := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(blocker, nil, 0o600))

	stdout, stderr, err := run2(t, "--config", cfgPath, "--cache-dir", filepath.Join(blocker, "cache"), "check", work)
	require.Error(t, err)
	assert.EqualError(t, err, "rtunk: check found 1 issue(s)", "the run itself must behave exactly as without a log")
	assert.Contains(t, stdout, "1 issue")
	assert.Equal(t, 1, strings.Count(stderr, "run log disabled"))
}

// TestCheckRunCmd_FixLogsBothPassesInOneFile: `check --fix` calls the engine twice (formatter pass,
// then checking pass) under one command; both must land in the same log, with ids that never
// collide across the two calls.
func TestCheckRunCmd_FixLogsBothPassesInOneFile(t *testing.T) {
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
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "work", "file.txt"), []byte("messy\n"), 0o644))
	cacheDir := t.TempDir()

	_, stderr, err := run2(t, "--config", cfgPath, "--cache-dir", cacheDir, "check", "--fix", filepath.Join(repoRoot, "work"))
	require.NoError(t, err, "stderr: %s", stderr)

	runs, err := runlog.List(cacheDir, repoRoot)
	require.NoError(t, err)
	require.Len(t, runs, 1, "both engine passes belong to one command, hence one log")
	_, events := lastRun(t, cacheDir, repoRoot)

	ids := map[int]string{}
	var starts, ends int
	for _, ev := range events {
		switch ev.T {
		case "run_start":
			starts++
		case "run_end":
			ends++
		case "invocation":
			ids[ev.ID] = ev.Template
		}
	}
	assert.Equal(t, 1, starts)
	assert.Equal(t, 1, ends)
	assert.Len(t, ids, 2, "one invocation per pass, each with its own id")
}

func TestFmtCmd_CheckLogsDryRun(t *testing.T) {
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
	messy := filepath.Join(repoRoot, "messy.txt")
	require.NoError(t, os.WriteFile(messy, []byte("messy\n"), 0o644))
	cacheDir := t.TempDir()

	_, _, err := run2(t, "--config", cfgPath, "--cache-dir", cacheDir, "fmt", "--check", filepath.Dir(filepath.Dir(cfgPath)))
	require.Error(t, err, "fmt --check exits non-zero when something would change")

	run, events := lastRun(t, cacheDir, repoRoot)
	assert.Equal(t, "ok", run.Status, "a would-change verdict is a result, not a failed run")
	assert.True(t, events[0].DryRun)
	data, err := os.ReadFile(messy)
	require.NoError(t, err)
	assert.Equal(t, "messy\n", string(data), "and the file itself must be untouched")
}

const fightingFormatters = `    - name: fmtA
      description: Always rewrites to AAA, undoing fmtB's own change
      files: [ALL]
      commands:
        - name: format
          run: printf 'AAA\n' > ${target}
          output: rewrite
          success_codes: [0]
          in_place: true
          formatter: true
    - name: fmtB
      description: Always rewrites to BBB, undoing fmtA's own change
      files: [ALL]
      commands:
        - name: format
          run: printf 'BBB\n' > ${target}
          output: rewrite
          success_codes: [0]
          in_place: true
          formatter: true
`

// An unstable --verify-stable is a verdict on the formatters (like `fmt --check` finding files
// to reformat), not a failed run: the command errors, the log's status stays ok.
func TestVerifyStableUnstable_LogsOkWhileCommandErrors(t *testing.T) {
	for _, args := range [][]string{
		{"fmt", "--verify-stable"},
		{"check", "--fix", "--verify-stable"},
	} {
		t.Run(args[0], func(t *testing.T) {
			cfgPath, repoRoot := writeLinterFixture(t, []string{"fmtA", "fmtB"}, fightingFormatters)
			require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "osc.txt"), []byte("original\n"), 0o644))
			cacheDir := t.TempDir()

			full := append([]string{"--config", cfgPath, "--cache-dir", cacheDir}, args...)
			_, _, err := run2(t, append(full, "-j", "1", repoRoot)...)
			require.ErrorContains(t, err, "did not converge")

			run, _ := lastRun(t, cacheDir, repoRoot)
			assert.Equal(t, "ok", run.Status)
		})
	}
}
