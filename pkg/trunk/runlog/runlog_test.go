package runlog

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/trunk/output"
)

func start(t *testing.T, cacheDir, repoRoot, cmd string) (*Writer, *bytes.Buffer) {
	t.Helper()
	var warn bytes.Buffer
	w := Start(StartOpts{CacheDir: cacheDir, RepoRoot: repoRoot, Cmd: cmd, Version: "9.9.9", Argv: []string{"rtunk", cmd}, Warn: &warn})
	return w, &warn
}

func TestNilWriterIsANoOp(t *testing.T) {
	var w *Writer
	w.Emit(Event{T: KindOutput})
	w.Output(1, "stdout", "x")
	w.End(true)
	assert.Equal(t, 0, w.NextID())
	var buf bytes.Buffer
	assert.Same(t, &buf, w.Tee(1, "stdout", &buf), "a nil writer's Tee must hand back dst itself")
}

func TestRedactEnv(t *testing.T) {
	got := RedactEnv([]string{"HOME=/h", "GITHUB_TOKEN=abc", "aws_secret_access_key=z", "DB_PASSWORD=p", "URL=a=b", "noequals"})
	assert.Equal(t, map[string]string{
		"HOME": "/h", "GITHUB_TOKEN": "<redacted>", "aws_secret_access_key": "<redacted>",
		"DB_PASSWORD": "<redacted>", "URL": "a=b",
	}, got, "a value containing '=' must survive whole; an entry without '=' is dropped")
}

func TestStartWritesRunStartAndEndOK(t *testing.T) {
	cache, repo := t.TempDir(), t.TempDir()
	t.Setenv("MY_API_TOKEN", "hunter2")
	w, warn := start(t, cache, repo, "check")
	require.NotNil(t, w)
	w.End(false)
	assert.Empty(t, warn.String())

	runs, err := List(cache, repo)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, "check", runs[0].Cmd)
	assert.Equal(t, "ok", runs[0].Status)

	events, err := Load(runs[0].Path)
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, KindRunStart, events[0].T)
	assert.Equal(t, "9.9.9", events[0].Rtunk)
	assert.Equal(t, "<redacted>", events[0].Env["MY_API_TOKEN"])
	assert.Equal(t, KindRunEnd, events[1].T)
	assert.Equal(t, "ok", events[1].Status)
}

func TestEndFailedAndEmitAfterEndIsIgnored(t *testing.T) {
	cache, repo := t.TempDir(), t.TempDir()
	w, _ := start(t, cache, repo, "fmt")
	w.End(true)
	w.Emit(Event{T: KindOutput, Data: "late"})
	runs, err := List(cache, repo)
	require.NoError(t, err)
	assert.Equal(t, "failed", runs[0].Status)
	events, err := Load(runs[0].Path)
	require.NoError(t, err)
	assert.Len(t, events, 2, "nothing may be appended after run_end")
}

func TestOutputCapsAtOneMiB(t *testing.T) {
	cache, repo := t.TempDir(), t.TempDir()
	w, _ := start(t, cache, repo, "check")
	w.Output(1, "stdout", strings.Repeat("x", maxOutput+10))
	w.Output(1, "stderr", "")
	w.End(false)
	runs, _ := List(cache, repo)
	events, err := Load(runs[0].Path)
	require.NoError(t, err)
	require.Len(t, events, 3, "run_start, one output (the empty stderr logs nothing), run_end")
	assert.Len(t, events[1].Data, maxOutput)
	assert.True(t, events[1].Truncated)
}

func TestTeeForwardsEverythingButLogsOnlyUpToTheCap(t *testing.T) {
	cache, repo := t.TempDir(), t.TempDir()
	w, _ := start(t, cache, repo, "actions-run")
	var dst bytes.Buffer
	tee := w.Tee(3, "stdout", &dst)
	_, _ = tee.Write([]byte("hello "))
	_, _ = tee.Write([]byte(strings.Repeat("y", maxOutput)))
	_, _ = tee.Write([]byte("never logged"))
	w.End(false)

	assert.Equal(t, "hello "+strings.Repeat("y", maxOutput)+"never logged", dst.String(), "dst must receive every byte")
	runs, _ := List(cache, repo)
	events, err := Load(runs[0].Path)
	require.NoError(t, err)
	require.Len(t, events, 4, "run_start, two output chunks, run_end")
	assert.Equal(t, "hello ", events[1].Data)
	assert.False(t, events[1].Truncated)
	assert.Len(t, events[2].Data, maxOutput-len("hello "))
	assert.True(t, events[2].Truncated)
	assert.Equal(t, 3, events[2].ID)
}

func TestConcurrentEmitYieldsOnlyValidLines(t *testing.T) {
	cache, repo := t.TempDir(), t.TempDir()
	w, _ := start(t, cache, repo, "check")
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 50 {
				w.Emit(Event{T: KindOutput, ID: w.NextID(), Stream: "stdout", Data: fmt.Sprintf("g%d-%d", g, i)})
			}
		})
	}
	wg.Wait()
	w.End(false)
	runs, _ := List(cache, repo)
	events, err := Load(runs[0].Path)
	require.NoError(t, err)
	assert.Len(t, events, 402)
	ids := map[int]bool{}
	for _, ev := range events[1:401] {
		ids[ev.ID] = true
	}
	assert.Len(t, ids, 400, "NextID must hand out 400 distinct ids under concurrency")
}

func TestPruneKeepsNewest50(t *testing.T) {
	cache, repo := t.TempDir(), t.TempDir()
	root, err := logsRoot(cache)
	require.NoError(t, err)
	dir := filepath.Join(root, repoKey(repo))
	require.NoError(t, os.MkdirAll(dir, 0o750))
	for i := range 60 {
		name := fmt.Sprintf("2020010%dT000000.%09dZ-check.jsonl", 1+i/100, i)
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("{}\n"), 0o600))
	}
	w, _ := start(t, cache, repo, "check")
	w.End(false)
	names := runNames(dir)
	assert.Len(t, names, keepRuns)
	assert.NotContains(t, names, "20200101T000000.000000000Z-check.jsonl", "the oldest must be pruned")
	assert.Contains(t, names, "20200101T000000.000000059Z-check.jsonl", "the newest fixture must survive")
}

func TestUnwritableCacheWarnsOnceAndReturnsNilWriter(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(blocker, nil, 0o600))
	w, warn := start(t, filepath.Join(blocker, "cache"), t.TempDir(), "check")
	assert.Nil(t, w)
	assert.Equal(t, 1, strings.Count(warn.String(), "run log disabled"))
	w.End(false) // must not panic
}

func TestListReportsInterruptedAndFindResolvesRefs(t *testing.T) {
	cache, repo := t.TempDir(), t.TempDir()
	w1, _ := start(t, cache, repo, "check")
	w1.End(false)
	w2, _ := start(t, cache, repo, "fmt") // never ended: an interrupted run
	require.NotNil(t, w2)

	runs, err := List(cache, repo)
	require.NoError(t, err)
	require.Len(t, runs, 2)
	assert.Equal(t, "fmt", runs[0].Cmd, "newest first")
	assert.Equal(t, "interrupted", runs[0].Status)
	assert.Equal(t, "ok", runs[1].Status)

	latest, err := Find(cache, repo, "latest")
	require.NoError(t, err)
	assert.Equal(t, runs[0].Name, latest.Name)
	byPrefix, err := Find(cache, repo, runs[1].Name)
	require.NoError(t, err)
	assert.Equal(t, runs[1].Name, byPrefix.Name)

	_, err = Find(cache, repo, "2")
	assert.ErrorContains(t, err, "matches 2 runs")
	_, err = Find(cache, repo, "nope")
	assert.ErrorContains(t, err, `no run matches "nope"`)
	_, err = Find(cache, t.TempDir(), "")
	assert.ErrorContains(t, err, "no runs logged")
}

func TestLoadToleratesATruncatedFinalLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(`{"t":"run_start"}`+"\n"+`{"t":"output","data":"cut off mid-wr`), 0o600))
	events, err := Load(path)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, KindRunStart, events[0].T)
}

func TestCleanScopesToRepoUnlessAll(t *testing.T) {
	cache, repoA, repoB := t.TempDir(), t.TempDir(), t.TempDir()
	for _, r := range []string{repoA, repoB} {
		w, _ := start(t, cache, r, "check")
		w.End(false)
	}
	require.NoError(t, Clean(cache, repoA))
	a, _ := List(cache, repoA)
	b, _ := List(cache, repoB)
	assert.Empty(t, a)
	assert.Len(t, b, 1, "another repository's logs must survive a scoped clean")

	require.NoError(t, Clean(cache, ""))
	b, _ = List(cache, repoB)
	assert.Empty(t, b)
	require.NoError(t, Clean(cache, ""), "cleaning nothing is not an error")
}

func TestRender(t *testing.T) {
	zero, one := 0, 1
	var buf bytes.Buffer
	Render(&buf, []Event{
		{T: KindRunStart, TS: "T0", Rtunk: "1.2.3", Argv: []string{"rtunk", "check"}, Cwd: "/repo", RepoRoot: "/repo", Concurrency: 2, Env: map[string]string{"B": "2", "A": "1"}},
		{T: KindInvocation, ID: 1, Linter: "shellcheck", Argv: []string{"sh", "-c", "shellcheck -f json 'a.sh'"}, Template: "shellcheck -f json ${target}", Cwd: "/repo", PathPrefix: "/cache/shims", ToolVersions: map[string]string{"shellcheck": "0.10.0"}, Files: []string{"a.sh"}},
		{T: KindOutput, ID: 1, Stream: "stdout", Data: "[{}]\n"},
		{T: KindExit, ID: 1, Code: &one, Ms: 12, ParsedFrom: "stdout"},
		{T: KindParser, ID: 1, Argv: []string{"sh", "-c", "python3 conv.py"}, StdinFrom: "stdout", Code: &zero, Ms: 3, Data: "sarif"},
		{T: KindFindings, ID: 1, Findings: []output.Finding{{File: "a.sh", Line: 3, Severity: "error", RuleID: "SC1", Message: "bad"}}},
		{T: KindLinterEnd, Linter: "shellcheck", Phase: "Done"},
		{T: KindRunEnd, Status: "ok", Ms: 50},
	})
	want := `run T0  (rtunk 1.2.3)
  argv: rtunk check
  cwd: /repo
  repo: /repo
  concurrency: 2
  env:
    A=1
    B=2

#1 shellcheck
  $ shellcheck -f json 'a.sh'
  template: shellcheck -f json ${target}
  cwd: /repo
  PATH prefix: /cache/shims
  tools: shellcheck@0.10.0
  files: a.sh
  stdout:
    [{}]
  exit 1 in 12ms (parsing stdout)
  parser: python3 conv.py
    reads stdout, exit 0 in 3ms
    output:
      sarif
  findings: 1
    a.sh:3 error [SC1] bad
  -> shellcheck Done

status: ok in 50ms
`
	assert.Equal(t, want, buf.String())
}
