package actions_test

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/trunk/actions"
	"github.com/xunleii/rtunk/pkg/trunk/config"
)

func TestRun_SubstitutesHookAndArgsAndCwd(t *testing.T) {
	repoRoot := t.TempDir()
	action := config.Action{ID: "echo-test", Run: `echo "${hook}" "${1}" "${@}"`}
	var stdout, stderr bytes.Buffer

	res, err := actions.Run(context.Background(), config.Config{}, action,
		actions.RunOptions{CacheDir: t.TempDir(), RepoRoot: repoRoot, Hook: "pre-commit", Args: []string{"a", "b"}},
		&stdout, &stderr)
	require.NoError(t, err, "stderr: %s", stderr.String())
	assert.Equal(t, 0, res.ExitCode)
	assert.False(t, res.Skipped)
	assert.Equal(t, "pre-commit a a b\n", stdout.String())
}

func TestRun_UnsupportedTemplateVar_IsAnError(t *testing.T) {
	action := config.Action{ID: "bad", Run: "echo ${bogus}"}
	var stdout, stderr bytes.Buffer
	_, err := actions.Run(context.Background(), config.Config{}, action,
		actions.RunOptions{CacheDir: t.TempDir(), RepoRoot: t.TempDir()}, &stdout, &stderr)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "${bogus}")
}

func TestRun_NonZeroExit_ReturnsExitCodeAndError(t *testing.T) {
	action := config.Action{ID: "failer", Run: "exit 3"}
	var stdout, stderr bytes.Buffer
	res, err := actions.Run(context.Background(), config.Config{}, action,
		actions.RunOptions{CacheDir: t.TempDir(), RepoRoot: t.TempDir()}, &stdout, &stderr)
	require.Error(t, err)
	assert.Equal(t, 3, res.ExitCode)
}

func TestRun_InteractiveTrue_SkipsNonTTYStdin(t *testing.T) {
	action := config.Action{ID: "interactive-only", Run: "echo should-not-run", Interactive: "true"}
	var stdout, stderr bytes.Buffer
	oldStdin := os.Stdin
	r, w, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { os.Stdin = oldStdin })
	os.Stdin = r
	w.Close()

	res, err := actions.Run(context.Background(), config.Config{}, action,
		actions.RunOptions{CacheDir: t.TempDir(), RepoRoot: t.TempDir()}, &stdout, &stderr)
	require.NoError(t, err)
	assert.True(t, res.Skipped)
	assert.Empty(t, stdout.String(), "a skipped action must never actually run")
}

func TestRun_HookStdinPath_ReceivesStdinContent(t *testing.T) {
	action := config.Action{ID: "read-stdin", Run: "cat ${hook_stdin_path}"}
	var stdout, stderr bytes.Buffer
	res, err := actions.Run(context.Background(), config.Config{}, action,
		actions.RunOptions{CacheDir: t.TempDir(), RepoRoot: t.TempDir(), Stdin: strings.NewReader("ref-data\n")},
		&stdout, &stderr)
	require.NoError(t, err, "stderr: %s", stderr.String())
	assert.Equal(t, 0, res.ExitCode)
	assert.Equal(t, "ref-data\n", stdout.String())
}

func TestRun_EnvironmentEntries_AreInjected(t *testing.T) {
	action := config.Action{ID: "env-test", Run: "echo $MY_VAR", Environment: []config.EnvironmentEntry{{Name: "MY_VAR", Value: "hello"}}}
	var stdout, stderr bytes.Buffer
	_, err := actions.Run(context.Background(), config.Config{}, action,
		actions.RunOptions{CacheDir: t.TempDir(), RepoRoot: t.TempDir()}, &stdout, &stderr)
	require.NoError(t, err, "stderr: %s", stderr.String())
	assert.Equal(t, "hello\n", stdout.String())
}
