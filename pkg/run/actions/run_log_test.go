package actions_test

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/run/actions"
	"github.com/xunleii/rtunk/pkg/run/runlog"
	"github.com/xunleii/rtunk/pkg/trunk/config"
)

// runLogged runs action with a real run log attached and returns what the log recorded, plus
// Run's own results and streams.
func runLogged(t *testing.T, action config.Action, repoRoot string) (logged []runlog.Event, res actions.Result, stdout, stderr string, runErr error) {
	t.Helper()
	cache := t.TempDir()
	w := runlog.Start(runlog.StartOpts{CacheDir: cache, RepoRoot: repoRoot, Cmd: "actions-run", Warn: os.Stderr})
	require.NotNil(t, w)
	var out, errOut bytes.Buffer
	res, runErr = actions.Run(context.Background(), config.Config{}, action,
		actions.RunOptions{CacheDir: cache, RepoRoot: repoRoot, Log: w}, &out, &errOut)
	w.End(runErr != nil)

	runs, err := runlog.List(cache, repoRoot)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	logged, err = runlog.Load(runs[0].Path)
	require.NoError(t, err)
	return logged, res, out.String(), errOut.String(), runErr
}

func kinds(events []runlog.Event) []string {
	out := make([]string, len(events))
	for i, ev := range events {
		out[i] = ev.T
	}
	return out
}

func TestRun_LogsInvocationOutputAndExit(t *testing.T) {
	repoRoot := t.TempDir()
	action := config.Action{ID: "greet", Run: "echo hi; echo oops >&2"}

	logged, res, stdout, stderr, err := runLogged(t, action, repoRoot)
	require.NoError(t, err)

	assert.Equal(t, "hi\n", stdout, "the live stream must still receive everything")
	assert.Equal(t, "oops\n", stderr)
	assert.Equal(t, 0, res.ExitCode)
	require.Equal(t, []string{"run_start", "invocation", "output", "output", "exit", "run_end"}, kinds(logged))
	inv := logged[1]
	assert.Equal(t, "greet", inv.Linter)
	assert.Equal(t, []string{"sh", "-c", "echo hi; echo oops >&2"}, inv.Argv)
	assert.Equal(t, repoRoot, inv.Cwd)
	// stdout and stderr are drained by separate goroutines, so their two events come in either order.
	streams := map[string]string{logged[2].Stream: logged[2].Data, logged[3].Stream: logged[3].Data}
	assert.Equal(t, map[string]string{"stdout": "hi\n", "stderr": "oops\n"}, streams)
	require.NotNil(t, logged[4].Code)
	assert.Equal(t, 0, *logged[4].Code)
	assert.Equal(t, inv.ID, logged[4].ID)
}

func TestRun_LogsNonZeroExit(t *testing.T) {
	logged, res, _, _, err := runLogged(t, config.Action{ID: "boom", Run: "exit 3"}, t.TempDir())
	require.Error(t, err)
	assert.Equal(t, 3, res.ExitCode)
	require.Equal(t, []string{"run_start", "invocation", "exit", "run_end"}, kinds(logged))
	require.NotNil(t, logged[2].Code)
	assert.Equal(t, 3, *logged[2].Code)
	assert.Equal(t, "failed", logged[3].Status)
}

func TestRun_LogsFailureBeforeExecAsLinterEnd(t *testing.T) {
	logged, _, _, _, err := runLogged(t, config.Action{ID: "bad", Run: "echo ${bogus}"}, t.TempDir())
	require.Error(t, err)
	require.Equal(t, []string{"run_start", "linter_end", "run_end"}, kinds(logged), "nothing was launched, so no invocation")
	assert.Equal(t, "bad", logged[1].Linter)
	assert.Equal(t, "Failed", logged[1].Phase)
	assert.Contains(t, logged[1].Err, "${bogus}")
}

func TestRun_NilLogStillWorks(t *testing.T) {
	var stdout, stderr bytes.Buffer
	_, err := actions.Run(context.Background(), config.Config{}, config.Action{ID: "x", Run: "echo ok"},
		actions.RunOptions{CacheDir: t.TempDir(), RepoRoot: t.TempDir()}, &stdout, &stderr)
	require.NoError(t, err)
	assert.Equal(t, "ok\n", stdout.String())
}

// A background child that keeps the inherited pipes open must not hold Run hostage: with a log
// attached stdout/stderr are wrapped writers, so exec waits on copy goroutines until WaitDelay.
func TestRun_LogBackgroundChildDoesNotBlock(t *testing.T) {
	start := time.Now()
	logged, res, stdout, _, err := runLogged(t, config.Action{ID: "bg", Run: "sleep 3 & echo hi"}, t.TempDir())
	require.NoError(t, err)
	assert.Less(t, time.Since(start), 2800*time.Millisecond)
	assert.Equal(t, 0, res.ExitCode)
	assert.Equal(t, "hi\n", stdout)
	require.Equal(t, []string{"run_start", "invocation", "output", "exit", "run_end"}, kinds(logged))
	assert.Equal(t, "hi\n", logged[2].Data)
	require.NotNil(t, logged[3].Code)
	assert.Equal(t, 0, *logged[3].Code)
}

func TestRun_NilLogBackgroundChildStillWorks(t *testing.T) {
	var stdout bytes.Buffer
	_, err := actions.Run(context.Background(), config.Config{}, config.Action{ID: "bg", Run: "sleep 1 & echo hi"},
		actions.RunOptions{CacheDir: t.TempDir(), RepoRoot: t.TempDir()}, &stdout, &bytes.Buffer{})
	require.NoError(t, err)
	assert.Equal(t, "hi\n", stdout.String())
}
