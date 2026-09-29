package engine

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/cache/download"
	"github.com/xunleii/rtunk/pkg/run/runlog"
	"github.com/xunleii/rtunk/pkg/trunk/config"
)

// runLogged runs cfg through Run with a real run log attached, drains it, and returns the
// events the log recorded (run_start and run_end included) plus what Run itself streamed.
func runLogged(t *testing.T, cfg config.Config, repoRoot, cacheDir string) (logged []runlog.Event, streamed []Event) {
	t.Helper()
	w := runlog.Start(runlog.StartOpts{CacheDir: cacheDir, RepoRoot: repoRoot, Cmd: "check", Warn: os.Stderr})
	require.NotNil(t, w)
	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: cacheDir, Concurrency: 1, Log: w}, nil, notFormatter)
	require.NoError(t, err)
	for ev := range events {
		streamed = append(streamed, ev)
	}
	w.End(false)

	runs, err := runlog.List(cacheDir, repoRoot)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	logged, err = runlog.Load(runs[0].Path)
	require.NoError(t, err)
	return logged, streamed
}

func kinds(events []runlog.Event) []string {
	out := make([]string, len(events))
	for i, ev := range events {
		out[i] = ev.T
	}
	return out
}

func TestRun_LogsInvocationOutputsExitAndLinterEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh -c only on POSIX")
	}
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "a.txt"), []byte("x\n"), 0o644))
	cfg := config.Config{Lint: config.LintConfig{
		Files: map[string]config.FileType{},
		CategoryConfig: config.CategoryConfig[config.Linter]{Definitions: map[string]config.Linter{
			"echoer": {Name: "echoer", Files: []string{"ALL"}, Commands: []config.Command{
				{Name: "check", Run: "echo out; echo err >&2; echo ${target} >/dev/null", Output: "pass_fail", Batch: true},
			}},
		}},
	}}

	logged, streamed := runLogged(t, cfg, repoRoot, t.TempDir())

	assert.Equal(t, []string{"run_start", "invocation", "output", "output", "exit", "linter_end", "run_end"}, kinds(logged))
	inv := logged[1]
	assert.Equal(t, 1, inv.ID)
	assert.Equal(t, "echoer", inv.Linter)
	assert.Equal(t, "echo out; echo err >&2; echo ${target} >/dev/null", inv.Template)
	assert.Equal(t, []string{"sh", "-c", "echo out; echo err >&2; echo 'a.txt' >/dev/null"}, inv.Argv, "argv must be the substituted, quoted command line")
	assert.Equal(t, []string{"a.txt"}, inv.Files)
	assert.Equal(t, repoRoot, inv.Cwd)
	assert.Equal(t, "stdout", logged[2].Stream)
	assert.Equal(t, "out\n", logged[2].Data)
	assert.Equal(t, "stderr", logged[3].Stream)
	assert.Equal(t, "err\n", logged[3].Data)
	require.NotNil(t, logged[4].Code)
	assert.Equal(t, 0, *logged[4].Code)
	assert.Equal(t, "stdout", logged[4].ParsedFrom)
	assert.Equal(t, "Done", logged[5].Phase)
	assert.Equal(t, "echoer", logged[5].Linter)

	// The public stream is unchanged by logging: plan, one Running, its JobDone, the terminal Done.
	require.Len(t, streamed, 4)
	assert.Equal(t, Planned, streamed[0].Phase)
	assert.Equal(t, Running, streamed[1].Phase)
	assert.Equal(t, JobDone, streamed[2].Phase)
	assert.Equal(t, Done, streamed[3].Phase)
}

func TestRun_LogsFailedLinterEndWithError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh -c only on POSIX")
	}
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "a.txt"), []byte("x\n"), 0o644))
	cfg := config.Config{Lint: config.LintConfig{
		Files: map[string]config.FileType{},
		CategoryConfig: config.CategoryConfig[config.Linter]{Definitions: map[string]config.Linter{
			"broken": {Name: "broken", Files: []string{"ALL"}, Commands: []config.Command{
				{Name: "check", Run: "echo boom >&2; exit 1", Output: "pass_fail", Batch: true, ErrorCodes: []int{1}},
			}},
		}},
	}}

	logged, _ := runLogged(t, cfg, repoRoot, t.TempDir())

	require.Equal(t, []string{"run_start", "invocation", "output", "exit", "linter_end", "run_end"}, kinds(logged),
		"only stderr had content, so exactly one output event")
	assert.Equal(t, "stderr", logged[2].Stream)
	require.NotNil(t, logged[3].Code)
	assert.Equal(t, 1, *logged[3].Code, "the exit event must record the real exit code")
	assert.Equal(t, "Failed", logged[4].Phase)
	assert.Contains(t, logged[4].Err, "exited 1")
}

func TestRun_LogsSkippedLinterFromBuildJobs(t *testing.T) {
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "a.txt"), []byte("x\n"), 0o644))
	cfg := config.Config{Lint: config.LintConfig{
		Files: map[string]config.FileType{},
		CategoryConfig: config.CategoryConfig[config.Linter]{Definitions: map[string]config.Linter{
			"weird": {Name: "weird", Files: []string{"ALL"}, Commands: []config.Command{{Name: "check", Run: "true", Output: "xml"}}},
		}},
	}}

	logged, _ := runLogged(t, cfg, repoRoot, t.TempDir())

	require.Equal(t, []string{"run_start", "linter_end", "run_end"}, kinds(logged), "no job ran, but the skip must still be logged")
	assert.Equal(t, "Skipped", logged[1].Phase)
	assert.Contains(t, logged[1].Note, "unsupported output format")
}

func TestRun_LogsParserStepFindingsAndToolVersions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("faketool invoked via sh -c")
	}
	binPath := buildFakeToolBinary(t)
	cacheDir := t.TempDir()
	root, err := download.Root(cacheDir)
	require.NoError(t, err)

	toolShim := download.ShimPath(root, "tools", "faketool", "1.0.0", "faketool")
	require.NoError(t, os.MkdirAll(filepath.Dir(toolShim), 0o755))
	require.NoError(t, download.WriteShim(toolShim, binPath))
	runtimeShim := download.ShimPath(root, "runtimes", "python", "3.12.0", "faketool")
	require.NoError(t, os.MkdirAll(filepath.Dir(runtimeShim), 0o755))
	require.NoError(t, download.WriteShim(runtimeShim, binPath))

	pluginRoot := t.TempDir()
	sourceDir := filepath.Join("linters", "fakeparsed")
	cwdDir := filepath.Join(pluginRoot, sourceDir)
	require.NoError(t, os.MkdirAll(cwdDir, 0o755))
	require.NoError(t, os.Symlink(binPath, filepath.Join(cwdDir, "faketool")))

	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "target.txt"), []byte("content\n"), 0o644))

	cfg := config.Config{
		Tools: map[string]config.Tool{"faketool": {Name: "faketool", KnownGoodVersion: "1.0.0"}},
		Runtimes: config.CategoryConfig[config.Runtime]{Definitions: map[string]config.Runtime{
			"python": {Type: "python", KnownGoodVersion: "3.12.0", Shims: config.ShimList{"faketool"}},
		}},
		Lint: config.LintConfig{
			Files: map[string]config.FileType{},
			CategoryConfig: config.CategoryConfig[config.Linter]{Definitions: map[string]config.Linter{
				"fakeparsed": {
					Name: "fakeparsed", Files: []string{"ALL"}, Tools: []string{"faketool"},
					SourceRoot: pluginRoot, SourceDir: sourceDir,
					Commands: []config.Command{{
						Name: "lint", Run: "faketool rawtext ${target}", Output: "sarif", Batch: true,
						Parser: &config.Parser{Runtime: "python", Run: "${cwd}/faketool sarifconvert"},
					}},
				},
			}},
		},
	}

	logged, _ := runLogged(t, cfg, repoRoot, cacheDir)

	assert.Equal(t, []string{"run_start", "invocation", "output", "exit", "parser", "findings", "linter_end", "run_end"}, kinds(logged))
	inv, exit, parser, findings := logged[1], logged[3], logged[4], logged[5]
	assert.Equal(t, map[string]string{"faketool": "1.0.0"}, inv.ToolVersions)
	assert.Equal(t, filepath.Dir(toolShim), inv.PathPrefix)
	assert.Equal(t, "stdout", exit.ParsedFrom)
	assert.Equal(t, inv.ID, parser.ID)
	assert.Equal(t, "stdout", parser.StdinFrom)
	assert.Equal(t, "${cwd}/faketool sarifconvert", parser.Template)
	require.NotNil(t, parser.Code)
	assert.Equal(t, 0, *parser.Code)
	assert.Contains(t, parser.Data, "converted-rule", "the parser's own stdout must be logged")
	require.Len(t, findings.Findings, 1)
	assert.Equal(t, "converted-rule", findings.Findings[0].RuleID)
	assert.Equal(t, inv.ID, findings.ID)
}

func TestRun_NilLogChangesNothing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh -c only on POSIX")
	}
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "a.txt"), []byte("x\n"), 0o644))
	cfg := config.Config{Lint: config.LintConfig{
		Files: map[string]config.FileType{},
		CategoryConfig: config.CategoryConfig[config.Linter]{Definitions: map[string]config.Linter{
			"echoer": {Name: "echoer", Files: []string{"ALL"}, Commands: []config.Command{{Name: "check", Run: "true", Output: "pass_fail", Batch: true}}},
		}},
	}}
	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: t.TempDir(), Concurrency: 1}, nil, notFormatter)
	require.NoError(t, err)
	var n int
	for range events {
		n++
	}
	assert.Equal(t, 4, n, "Planned, Running, JobDone, Done")
}
