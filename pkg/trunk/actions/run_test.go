package actions_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/trunk/actions"
	"github.com/xunleii/rtunk/pkg/trunk/config"
)

func TestRun_SubstitutesHookAndArgsAndCwd(t *testing.T) {
	repoRoot := t.TempDir()
	// ${1}/${@} are quoted at substitution time (quoteOne/quoteAll), so the Run string
	// references them bare -- the same convention as ${cwd}/${plugin} and pkg/trunk/engine's
	// own ${target}. Double-quoting them again here would nest quotes incorrectly.
	action := config.Action{ID: "echo-test", Run: `echo ${hook} ${1} ${@}`}
	var stdout, stderr bytes.Buffer

	res, err := actions.Run(context.Background(), config.Config{}, action,
		actions.RunOptions{CacheDir: t.TempDir(), RepoRoot: repoRoot, Hook: "pre-commit", Args: []string{"a", "b"}},
		&stdout, &stderr)
	require.NoError(t, err, "stderr: %s", stderr.String())
	assert.Equal(t, 0, res.ExitCode)
	assert.False(t, res.Skipped)
	assert.Equal(t, "pre-commit a a b\n", stdout.String())
}

func TestRun_ArgsWithShellMetacharacters_AreNotInjected(t *testing.T) {
	// A git-hook arg (branch/ref name) is untrusted input. If ${1}/${@} substituted it bare
	// instead of quoted, this value would close the echo command's argument and execute a
	// second command -- proving the quoting fix actually closes that path, not just that the
	// happy-path test still passes.
	evil := `a" ; echo INJECTED ; "b`
	action := config.Action{ID: "injection-test", Run: `printf '%s\n' ${1} && printf '%s\n' ${@}`}
	var stdout, stderr bytes.Buffer

	res, err := actions.Run(context.Background(), config.Config{}, action,
		actions.RunOptions{CacheDir: t.TempDir(), RepoRoot: t.TempDir(), Args: []string{evil}},
		&stdout, &stderr)
	require.NoError(t, err, "stderr: %s", stderr.String())
	assert.Equal(t, 0, res.ExitCode)
	// The evil string itself contains the literal text "echo INJECTED", so this only proves
	// non-injection if the output is *exactly* the two round-tripped lines below -- an actual
	// injection would additionally run `echo INJECTED` as its own command, printing a bare
	// "INJECTED\n" line with no surrounding quotes/semicolons, which this exact match rules out.
	assert.Equal(t, evil+"\n"+evil+"\n", stdout.String())
}

func TestRun_ArgContainingEnvVarSyntax_IsNotReExpanded(t *testing.T) {
	// ${env.*} must expand against the original (trusted) action.Run string BEFORE args are
	// substituted in. If it ran afterwards over the fully-substituted string instead, an
	// untrusted arg containing the literal text "${env.EVIL}" would get expanded into EVIL's
	// real value post-quoting -- reopening injection even though ${1} itself was quoted.
	t.Setenv("EVIL", "'; echo INJECTED; '")
	action := config.Action{ID: "env-reexpand-test", Run: `printf '%s\n' ${1}`}
	var stdout, stderr bytes.Buffer

	res, err := actions.Run(context.Background(), config.Config{}, action,
		actions.RunOptions{CacheDir: t.TempDir(), RepoRoot: t.TempDir(), Args: []string{"${env.EVIL}"}},
		&stdout, &stderr)
	require.NoError(t, err, "stderr: %s", stderr.String())
	assert.Equal(t, 0, res.ExitCode)
	assert.Equal(t, "${env.EVIL}\n", stdout.String(), "the arg's literal text must round-trip verbatim, not get expanded into $EVIL's value")
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
	require.NoError(t, w.Close())

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

func TestRun_ExecCwdIsRepoRoot_NotPluginSourceRoot(t *testing.T) {
	// The Critical regression: c.Dir must always be opts.RepoRoot, even for a plugin-sourced
	// action (non-empty SourceRoot/SourceDir). Git hook argv (e.g. commit-msg's ${1}) is
	// repo-root-relative, never relative to the plugin's own cache dir. marker.txt exists only
	// under repoRoot, never under the plugin dir, so `cat ${1}` only succeeds if the process's
	// actual cwd is repoRoot.
	repoRoot := t.TempDir()
	pluginDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "marker.txt"), []byte("repo-root-content\n"), 0o644))

	action := config.Action{ID: "plugin-action", Run: "cat ${1}", SourceRoot: pluginDir}
	var stdout, stderr bytes.Buffer

	res, err := actions.Run(context.Background(), config.Config{}, action,
		actions.RunOptions{CacheDir: t.TempDir(), RepoRoot: repoRoot, Args: []string{"marker.txt"}},
		&stdout, &stderr)
	require.NoError(t, err, "stderr: %s", stderr.String())
	assert.Equal(t, 0, res.ExitCode)
	assert.Equal(t, "repo-root-content\n", stdout.String())
}

func TestRun_EnvironmentEntries_AreInjected(t *testing.T) {
	action := config.Action{ID: "env-test", Run: "echo $MY_VAR", Environment: []config.EnvironmentEntry{{Name: "MY_VAR", Value: "hello"}}}
	var stdout, stderr bytes.Buffer
	_, err := actions.Run(context.Background(), config.Config{}, action,
		actions.RunOptions{CacheDir: t.TempDir(), RepoRoot: t.TempDir()}, &stdout, &stderr)
	require.NoError(t, err, "stderr: %s", stderr.String())
	assert.Equal(t, "hello\n", stdout.String())
}
