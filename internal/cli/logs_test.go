package cli

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/trunk/runlog"
)

func TestLogsCmd_ListShowAndClean(t *testing.T) {
	cfgPath, _, work := checkFixture(t)
	cacheDir := t.TempDir()
	_, _, _ = run2(t, "--config", cfgPath, "--cache-dir", cacheDir, "check", work)

	list, stderr, err := run2(t, "--config", cfgPath, "--cache-dir", cacheDir, "logs")
	require.NoError(t, err, "stderr: %s", stderr)
	assert.Equal(t, 1, strings.Count(list, "\n"), "one run, one line")
	assert.Contains(t, list, "check")
	assert.Contains(t, list, "ok")

	text, stderr, err := run2(t, "--config", cfgPath, "--cache-dir", cacheDir, "logs", "show")
	require.NoError(t, err, "stderr: %s", stderr)
	assert.Contains(t, text, "$ false")
	assert.Contains(t, text, "findings: 1")
	assert.Contains(t, text, "status: ok")

	raw, stderr, err := run2(t, "--config", cfgPath, "--cache-dir", cacheDir, "logs", "show", "latest", "--json")
	require.NoError(t, err, "stderr: %s", stderr)
	assert.True(t, strings.HasPrefix(raw, `{"t":"run_start"`), "--json must print the file as-is: %.60s", raw)

	_, _, err = run2(t, "--config", cfgPath, "--cache-dir", cacheDir, "logs", "show", "no-such-run")
	assert.ErrorContains(t, err, `no run matches "no-such-run"`)

	_, stderr, err = run2(t, "--config", cfgPath, "--cache-dir", cacheDir, "logs", "clean")
	require.NoError(t, err, "stderr: %s", stderr)
	list, _, err = run2(t, "--config", cfgPath, "--cache-dir", cacheDir, "logs", "list")
	require.NoError(t, err)
	assert.Empty(t, list)
	_, _, err = run2(t, "--config", cfgPath, "--cache-dir", cacheDir, "logs", "show")
	assert.ErrorContains(t, err, "no runs logged")
}

func TestLogsCleanAll_RemovesOtherReposToo(t *testing.T) {
	cacheDir := t.TempDir()
	cfgA, repoA, workA := checkFixture(t)
	cfgB, repoB, workB := checkFixture(t)
	_, _, _ = run2(t, "--config", cfgA, "--cache-dir", cacheDir, "check", workA)
	_, _, _ = run2(t, "--config", cfgB, "--cache-dir", cacheDir, "check", workB)

	_, _, err := run2(t, "--config", cfgA, "--cache-dir", cacheDir, "logs", "clean")
	require.NoError(t, err)
	a, _ := runlog.List(cacheDir, repoA)
	b, _ := runlog.List(cacheDir, repoB)
	assert.Empty(t, a)
	assert.Len(t, b, 1, "a plain clean must leave the other repository alone")

	_, _, err = run2(t, "--cache-dir", cacheDir, "logs", "clean", "--all")
	require.NoError(t, err, "--all needs no config")
	b, _ = runlog.List(cacheDir, repoB)
	assert.Empty(t, b)
}

func TestCacheClean_RemovesRunLogsToo(t *testing.T) {
	cfgPath, repoRoot, work := checkFixture(t)
	cacheDir := t.TempDir()
	_, _, _ = run2(t, "--config", cfgPath, "--cache-dir", cacheDir, "check", work)

	_, stderr, err := run2(t, "--config", cfgPath, "--cache-dir", cacheDir, "cache", "clean")
	require.NoError(t, err, "stderr: %s", stderr)

	runs, err := runlog.List(cacheDir, repoRoot)
	require.NoError(t, err)
	assert.Empty(t, runs, "cache clean removes the whole cache root, logs/ included")
}
