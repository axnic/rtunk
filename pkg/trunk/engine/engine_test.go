package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/trunk/config"
	"github.com/xunleii/rtunk/pkg/trunk/download"
	"github.com/xunleii/rtunk/pkg/trunk/output"
)

// fakeToolSrc is a real compiled Go program standing in for a linter's tool binary, not a shell
// script -- avoids ENOEXEC on some shebang chains (the same lesson the python-runtime work in
// this repo's history already paid for). "sarif" prints a fake SARIF result per arg; "passfail"
// exits 1 if any given file's content is exactly "FAIL\n"; "crash" always exits 42.
const fakeToolSrc = `package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		os.Exit(1)
	}
	switch args[0] {
	case "sarif":
		var results []string
		for _, f := range args[1:] {
			results = append(results, "{\"ruleId\":\"fake-rule\",\"level\":\"error\",\"message\":{\"text\":\"fake finding\"},\"locations\":[{\"physicalLocation\":{\"artifactLocation\":{\"uri\":\""+f+"\"},\"region\":{\"startLine\":1}}}]}")
		}
		fmt.Print("{\"runs\":[{\"results\":[" + strings.Join(results, ",") + "]}]}")
	case "sarifone":
		// Emits exactly one SARIF result unconditionally, regardless of any file arguments --
		// stands in for a real command whose Run string never references ${target} (e.g.
		// tflint's, brakeman's first commands), which can't distinguish between the files it's
		// nominally checking.
		fmt.Print("{\"runs\":[{\"results\":[{\"ruleId\":\"fake-rule\",\"level\":\"error\",\"message\":{\"text\":\"fake finding\"},\"locations\":[{\"physicalLocation\":{\"artifactLocation\":{\"uri\":\"whatever\"},\"region\":{\"startLine\":1}}}]}]}]}")
	case "passfail":
		for _, f := range args[1:] {
			data, err := os.ReadFile(f)
			if err == nil && string(data) == "FAIL\n" {
				os.Exit(1)
			}
		}
	case "crash":
		os.Exit(42)
	case "crashstderr":
		fmt.Fprintln(os.Stderr, "boom: disk on fire")
		os.Exit(43)
	case "hadolint":
		fmt.Print("[{\"line\":1,\"code\":\"DL3006\",\"message\":\"pin a version\",\"column\":1,\"file\":\"" + args[1] + "\",\"level\":\"warning\"}]")
	case "sarifuri":
		// args[1] is the ${tmpfile} path checkov's real recipe writes SARIF to.
		os.WriteFile(args[1], []byte("{\"runs\":[{\"results\":[{\"ruleId\":\"CKV_1\",\"level\":\"error\",\"message\":{\"text\":\"finding\"},\"locations\":[{\"physicalLocation\":{\"artifactLocation\":{\"uri\":\""+args[2]+"\"},\"region\":{\"startLine\":1}}}]}]}]}"), 0o644)
	case "perlcritic":
		fmt.Print("path=" + args[1] + ",line=1,col=1,code=SomePolicy,message=a violation\n")
	case "genericregex":
		fmt.Print(args[1] + ":1:1: [warning] a generic finding\n")
	case "taplofake":
		// A real taplo codespan_reporting diagnostic block (shape captured in parse_test.go's
		// TestParseTaplo), referencing the actual target file this invocation was given.
		fmt.Print("error: invalid TOML\n  ┌─ " + args[1] + ":2:8\n  │\n2 │ name = \"test\n  │        ^ unexpected token\n")
	case "emptyjson":
		// Prints nothing: stands in for a clean run (or an OS-gated command variant) that
		// produces genuinely empty stdout on a JSON-shaped Output format.
	case "sleep":
		time.Sleep(250 * time.Millisecond)
	case "alwaysfail":
		os.Exit(1)
	case "pwdls":
		// Reports its own cwd and a recursive listing of every regular file under it (relative
		// paths, so a nested layout like "combo/target.combo" is visible, not just top-level
		// names) as a fake SARIF finding's message, one result per file argument (args[1:], same
		// pattern as the "sarif" case above) so a Batch invocation over N files still produces N
		// findings, not one -- lets a test assert on exactly which directory the process ran from
		// and which files it sees there (RunFrom resolution / SandboxType staging), while each
		// finding's own artifactLocation still round-trips through the normal ParseSARIF +
		// remapFindings pipeline.
		wd, _ := os.Getwd()
		var names []string
		filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() {
				names = append(names, path)
			}
			return nil
		})
		sort.Strings(names)
		msg := "pwd=" + wd + ";files=" + strings.Join(names, ",")
		var results []string
		for _, f := range args[1:] {
			results = append(results, "{\"ruleId\":\"pwd-check\",\"level\":\"error\",\"message\":{\"text\":\""+msg+"\"},\"locations\":[{\"physicalLocation\":{\"artifactLocation\":{\"uri\":\""+f+"\"},\"region\":{\"startLine\":1}}}]}")
		}
		fmt.Print("{\"runs\":[{\"results\":[" + strings.Join(results, ",") + "]}]}")
	case "abspath":
		// Emits a SARIF result whose artifactLocation.uri is an absolute path (built from its
		// own cwd, so it always points wherever the process actually ran -- the real sandbox
		// directory when sandboxed), unlike "sarif" which echoes back the relative path given on
		// the command line. Stands in for a real linter that reports its findings by absolute
		// path rather than relative to cwd -- proving the sandbox's own temp directory doesn't
		// leak into the final report.
		wd, _ := os.Getwd()
		var results []string
		for _, f := range args[1:] {
			abs := filepath.Join(wd, f)
			results = append(results, "{\"ruleId\":\"fake-rule\",\"level\":\"error\",\"message\":{\"text\":\"fake finding\"},\"locations\":[{\"physicalLocation\":{\"artifactLocation\":{\"uri\":\""+abs+"\"},\"region\":{\"startLine\":1}}}]}")
		}
		fmt.Print("{\"runs\":[{\"results\":[" + strings.Join(results, ",") + "]}]}")
	case "rawtext":
		// Stands in for a real tool's native (non-SARIF) output -- e.g. trufflehog's own NDJSON --
		// that Command.Parser.Run converts into SARIF via the stdin/stdout pipe (see "sarifconvert"
		// below).
		fmt.Print("RAWFINDING:" + strings.Join(args[1:], ","))
	case "sarifconvert":
		// Stands in for a real converter script (e.g. trufflehog_to_sarif.py): reads the tool's
		// raw output on stdin, writes SARIF on stdout. The files it reports come from what it
		// actually read off stdin, not its own argv -- proving the pipe, not argv, carries the
		// real data across the two stages.
		data, _ := io.ReadAll(os.Stdin)
		raw := strings.TrimPrefix(strings.TrimSpace(string(data)), "RAWFINDING:")
		var results []string
		for _, f := range strings.Split(raw, ",") {
			results = append(results, "{\"ruleId\":\"converted-rule\",\"level\":\"error\",\"message\":{\"text\":\"converted from raw\"},\"locations\":[{\"physicalLocation\":{\"artifactLocation\":{\"uri\":\""+f+"\"},\"region\":{\"startLine\":1}}}]}")
		}
		fmt.Print("{\"runs\":[{\"results\":[" + strings.Join(results, ",") + "]}]}")
	case "echoargs":
		// Emits its own argv (minus args[0]) joined by "|" as a single SARIF finding's message --
		// lets a test assert exactly what ${plugin}/${cwd} substituted into Command.Run, without
		// needing a real linter or real plugin source.
		fmt.Print("{\"runs\":[{\"results\":[{\"ruleId\":\"echo\",\"level\":\"error\",\"message\":{\"text\":\""+strings.Join(args[1:], "|")+"\"},\"locations\":[{\"physicalLocation\":{\"artifactLocation\":{\"uri\":\"whatever\"},\"region\":{\"startLine\":1}}}]}]}]}")
	}
}
`

func buildFakeToolBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "faketool.go")
	require.NoError(t, os.WriteFile(srcPath, []byte(fakeToolSrc), 0o644))
	binPath := filepath.Join(dir, "faketool")
	out, err := exec.Command("go", "build", "-o", binPath, srcPath).CombinedOutput()
	require.NoError(t, err, "building fake tool: %s", out)
	return binPath
}

// notFormatter is the include predicate every test in this file passes -- pkg/trunk/check's real
// predicate (see engine.go's Run doc comment), reproduced here since these tests' fixtures never
// set Formatter: true on a command they expect to run.
func notFormatter(c config.Command) bool { return !c.Formatter }

func TestRun(t *testing.T) {
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
	okFile := filepath.Join(repoRoot, "ok.txt")
	failFile := filepath.Join(repoRoot, "fail.txt")
	require.NoError(t, os.WriteFile(okFile, []byte("fine\n"), 0o644))
	require.NoError(t, os.WriteFile(failFile, []byte("FAIL\n"), 0o644))

	cfg := config.Config{
		Tools: map[string]config.Tool{
			"faketool": {Name: "faketool", KnownGoodVersion: "1.0.0"},
		},
		Lint: config.LintConfig{
			Files: map[string]config.FileType{},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"fakesarif": {
						Name: "fakesarif", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{{Name: "lint", Run: "faketool sarif ${target}", Output: "sarif", Batch: true}},
					},
					"fakepassfail": {
						Name: "fakepassfail", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{{Name: "check", Run: "faketool passfail ${target}", Output: "pass_fail"}},
					},
					"fakeerror": {
						Name: "fakeerror", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{{Name: "boom", Run: "faketool crash", Output: "pass_fail", ErrorCodes: []int{42}}},
					},
					"fakeerrorstderr": {
						Name: "fakeerrorstderr", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{{Name: "boom", Run: "faketool crashstderr", Output: "pass_fail", ErrorCodes: []int{43}}},
					},
					"fakeskipformat": {
						Name: "fakeskipformat", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{{Name: "unsupported", Run: "faketool sarif ${target}", Output: "xml"}},
					},
					"fakeskipvar": {
						Name: "fakeskipvar", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{{Name: "unsupported", Run: "faketool sarif ${target} ${workspace}", Output: "sarif"}},
					},
					"fakeskipvarcomma": {
						// ${target,} (a real catalog placeholder, comma-suffixed) is not on any
						// denylist -- only the allowlist scan (${target}/${tmpfile} exactly)
						// catches it.
						Name: "fakeskipvarcomma", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{{Name: "unsupported", Run: "faketool sarif ${target,}", Output: "sarif"}},
					},
					"fakeskipvarupstream": {
						// ${upstream-ref} would otherwise reach sh unsubstituted and get silently
						// reinterpreted as ${upstream:-ref} (the literal string "ref").
						Name: "fakeskipvarupstream", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{{Name: "unsupported", Run: "faketool sarif ${target} ${upstream-ref}", Output: "sarif"}},
					},
					"fakeskipparserruntime": {
						// cfg has no Runtimes.Definitions["python"] entry at all -- resolveRuntimeShimDir
						// must Skip this one command, not fail the whole linter, since a real unresolvable
						// parser runtime is a per-command config gap, not a run-wide error.
						Name: "fakeskipparserruntime", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{{
							Name: "unsupported", Run: "faketool sarif ${target}", Output: "sarif",
							Parser: &config.Parser{Runtime: "python", Run: "convert.py"},
						}},
					},
					"fakeskiprunfrom": {
						Name: "fakeskiprunfrom", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{{Name: "unsupported", Run: "faketool sarif ${target}", Output: "sarif", RunFrom: "apps"}},
					},
					"fakeskipsandbox": {
						Name: "fakeskipsandbox", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{{Name: "unsupported", Run: "faketool sarif ${target}", Output: "sarif", SandboxType: "unknown_type"}},
					},
					"fakeformatteronly": {
						Name: "fakeformatteronly", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{{Name: "fmt", Run: "faketool sarif ${target}", Output: "rewrite", Formatter: true, InPlace: true}},
					},
				},
			},
		},
	}

	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: cacheDir, Concurrency: 1}, nil, notFormatter)
	require.NoError(t, err)

	byLinter := map[string]Event{}
	var all []Event
	for ev := range events {
		all = append(all, ev)
		byLinter[ev.Linter] = ev
	}

	// fakesarif's command is Batch: true -- both matched files go in one invocation, so exactly
	// one Running event covers the whole batch, files comma-joined.
	var sarifRunning []Event
	for _, ev := range all {
		if ev.Linter == "fakesarif" && ev.Phase == Running {
			sarifRunning = append(sarifRunning, ev)
		}
	}
	require.Len(t, sarifRunning, 1, "a Batch command must emit exactly one Running event for the whole batch")
	assert.ElementsMatch(t, []string{"ok.txt", "fail.txt"}, strings.Split(sarifRunning[0].File, ", "))

	// fakepassfail's command is not Batch -- one invocation per file, so one Running event per
	// file, each naming just that file.
	var pfRunning []string
	for _, ev := range all {
		if ev.Linter == "fakepassfail" && ev.Phase == Running {
			pfRunning = append(pfRunning, ev.File)
		}
	}
	assert.ElementsMatch(t, []string{"ok.txt", "fail.txt"}, pfRunning, "a non-Batch command must emit one Running event per file")

	sarifEv, ok := byLinter["fakesarif"]
	require.True(t, ok, "expected an event for fakesarif")
	assert.Equal(t, Done, sarifEv.Phase)
	require.Len(t, sarifEv.Findings, 2, "one batched invocation, two files, two findings")
	// Findings must be repo-relative (item 7), not the absolute paths Files() matched --
	// repoRoot is every command's Dir, so the tool only ever sees/echoes relative paths.
	gotFiles := map[string]bool{sarifEv.Findings[0].File: true, sarifEv.Findings[1].File: true}
	assert.True(t, gotFiles["ok.txt"] && gotFiles["fail.txt"], "want relative ok.txt/fail.txt, got %v", gotFiles)

	pfEv, ok := byLinter["fakepassfail"]
	require.True(t, ok, "expected an event for fakepassfail")
	assert.Equal(t, Done, pfEv.Phase)
	require.Len(t, pfEv.Findings, 1, "only fail.txt should produce a finding")
	assert.Equal(t, "fail.txt", pfEv.Findings[0].File)

	errEv, ok := byLinter["fakeerror"]
	require.True(t, ok, "expected an event for fakeerror")
	assert.Equal(t, Failed, errEv.Phase)
	assert.EqualError(t, errEv.Err, "engine: fakeerror: boom exited 42: ")

	errStderrEv, ok := byLinter["fakeerrorstderr"]
	require.True(t, ok, "expected an event for fakeerrorstderr")
	assert.Equal(t, Failed, errStderrEv.Phase)
	assert.EqualError(t, errStderrEv.Err, "engine: fakeerrorstderr: boom exited 43: boom: disk on fire",
		"stderr must be included in the error message, not silently dropped")

	skipFormatEv, ok := byLinter["fakeskipformat"]
	require.True(t, ok)
	assert.Equal(t, Skipped, skipFormatEv.Phase)
	assert.Contains(t, skipFormatEv.Note, "xml")

	skipVarEv, ok := byLinter["fakeskipvar"]
	require.True(t, ok)
	assert.Equal(t, Skipped, skipVarEv.Phase)
	assert.Contains(t, skipVarEv.Note, "workspace")

	skipVarCommaEv, ok := byLinter["fakeskipvarcomma"]
	require.True(t, ok)
	assert.Equal(t, Skipped, skipVarCommaEv.Phase)
	assert.Equal(t, `unsupported template var "${target,}"`, skipVarCommaEv.Note)

	skipVarUpstreamEv, ok := byLinter["fakeskipvarupstream"]
	require.True(t, ok)
	assert.Equal(t, Skipped, skipVarUpstreamEv.Phase)
	assert.Equal(t, `unsupported template var "${upstream-ref}"`, skipVarUpstreamEv.Note)

	skipParserEv, ok := byLinter["fakeskipparserruntime"]
	require.True(t, ok)
	assert.Equal(t, Skipped, skipParserEv.Phase)
	assert.Contains(t, skipParserEv.Note, `parser runtime "python" unavailable`)
	assert.Contains(t, skipParserEv.Note, "not found in resolved config")

	skipRunFromEv, ok := byLinter["fakeskiprunfrom"]
	require.True(t, ok)
	assert.Equal(t, Skipped, skipRunFromEv.Phase)
	assert.Contains(t, skipRunFromEv.Note, "run_from")

	skipSandboxEv, ok := byLinter["fakeskipsandbox"]
	require.True(t, ok)
	assert.Equal(t, Skipped, skipSandboxEv.Phase)
	assert.Contains(t, skipSandboxEv.Note, "sandbox_type")

	_, formatterOnlySeen := byLinter["fakeformatteronly"]
	assert.False(t, formatterOnlySeen, "a linter with only Formatter commands must emit no event at all")
}

// TestRun_RelativePathArgument covers `rtunk check .`: paths passed to Run can be relative (a
// bare "." is the default CLI argument for "check the whole repo"), while repoRoot is always
// absolute. Every match Files() returns is later relativized against repoRoot via filepath.Rel,
// which errors outright ("Rel: can't make X relative to Y") if one side is absolute and the
// other relative -- a walk rooted at a relative path used to produce exactly that crash.
func TestRun_RelativePathArgument(t *testing.T) {
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
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "ok.txt"), []byte("fine\n"), 0o644))

	cfg := config.Config{
		Tools: map[string]config.Tool{"faketool": {Name: "faketool", KnownGoodVersion: "1.0.0"}},
		Lint: config.LintConfig{
			Files: map[string]config.FileType{},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"fakesarif": {
						Name: "fakesarif", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{{Name: "lint", Run: "faketool sarif ${target}", Output: "sarif", Batch: true}},
					},
				},
			},
		},
	}

	t.Chdir(repoRoot)
	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: cacheDir, Concurrency: 1}, []string{"."}, notFormatter)
	require.NoError(t, err)

	var done *Event
	for ev := range events {
		if ev.Phase == Done {
			e := ev
			done = &e
		}
	}
	require.NotNil(t, done, "expected a Done event, not a crash relativizing a relative path argument")
	require.Len(t, done.Findings, 1)
	assert.Equal(t, "ok.txt", done.Findings[0].File)
}

// TestRun_ParallelWorkersRunConcurrently covers the actual point of concurrency workers: two
// linters that each take ~250ms must finish in well under 2x that when concurrency lets both run
// at once, proving jobs for different linters really execute in parallel rather than queued
// behind each other one at a time.
func TestRun_ParallelWorkersRunConcurrently(t *testing.T) {
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
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "a.txt"), []byte("a\n"), 0o644))

	newCfg := func() config.Config {
		return config.Config{
			Tools: map[string]config.Tool{"faketool": {Name: "faketool", KnownGoodVersion: "1.0.0"}},
			Lint: config.LintConfig{
				Files: map[string]config.FileType{},
				CategoryConfig: config.CategoryConfig[config.Linter]{
					Definitions: map[string]config.Linter{
						"slow1": {
							Name: "slow1", Files: []string{"ALL"}, Tools: []string{"faketool"},
							Commands: []config.Command{{Name: "lint", Run: "faketool sleep ${target}", Output: "pass_fail", Batch: true}},
						},
						"slow2": {
							Name: "slow2", Files: []string{"ALL"}, Tools: []string{"faketool"},
							Commands: []config.Command{{Name: "lint", Run: "faketool sleep ${target}", Output: "pass_fail", Batch: true}},
						},
					},
				},
			},
		}
	}

	drain := func(concurrency int) time.Duration {
		start := time.Now()
		events, err := Run(context.Background(), Env{Cfg: newCfg(), RepoRoot: repoRoot, CacheDir: cacheDir, Concurrency: concurrency}, nil, notFormatter)
		require.NoError(t, err)
		for range events {
		}
		return time.Since(start)
	}

	sequential := drain(1)
	parallel := drain(2)

	assert.Greater(t, sequential, 400*time.Millisecond, "two 250ms jobs one worker at a time must take close to 500ms")
	assert.Less(t, parallel, 400*time.Millisecond, "two 250ms jobs on two workers must take close to 250ms, not ~500ms")
}

// TestRun_FailedLinterSkipsRemainingJobs covers the best-effort abort: once a linter's command
// fails on one file, its other, not-yet-started per-file jobs must be skipped rather than run.
// concurrency: 1 makes "not yet started" deterministic (jobs run strictly in queue order), so
// exactly one Running event (the file that failed) and one Failed event must appear -- never a
// second Running/Done for either of the other two files.
func TestRun_FailedLinterSkipsRemainingJobs(t *testing.T) {
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
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(repoRoot, name), []byte("x\n"), 0o644))
	}

	cfg := config.Config{
		Tools: map[string]config.Tool{"faketool": {Name: "faketool", KnownGoodVersion: "1.0.0"}},
		Lint: config.LintConfig{
			Files: map[string]config.FileType{},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"alwaysfail": {
						Name: "alwaysfail", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{{
							Name: "lint", Run: "faketool alwaysfail ${target}", Output: "pass_fail", ErrorCodes: []int{1},
						}},
					},
				},
			},
		},
	}

	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: cacheDir, Concurrency: 1}, nil, notFormatter)
	require.NoError(t, err)

	var running, failed int
	for ev := range events {
		switch ev.Phase {
		case Running:
			running++
		case Failed:
			failed++
		}
	}
	assert.Equal(t, 1, running, "only the first of 3 files' jobs must have started running")
	assert.Equal(t, 1, failed, "exactly one Failed event, not one per file")
}

// TestRunOneInvocation_EmptyPathEnvHasNoCwdComponent covers a linter with an empty Tools list (6
// real catalog linters have this shape): pathEnv is then "", and naively prefixing it plus a
// separator onto PATH used to produce a leading empty PATH component, which POSIX shells treat as
// "the current directory" -- letting the repo under check (cmd.Dir) shadow real system binaries.
func TestRunOneInvocation_EmptyPathEnvHasNoCwdComponent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh -c only on POSIX")
	}

	repoRoot := t.TempDir()
	cmd := config.Command{Name: "check", Run: "echo \"$PATH\"", Output: "pass_fail"}

	out, stderr, exitCode, err := runOneInvocation(context.Background(), cmd, repoRoot, "", nil, "", "")
	require.NoError(t, err)
	assert.Equal(t, 0, exitCode)
	assert.Empty(t, stderr)

	gotPath := strings.TrimSuffix(out, "\n")
	assert.Equal(t, os.Getenv("PATH"), gotPath, "empty pathEnv must leave PATH untouched")
	assert.False(t, strings.HasPrefix(gotPath, ":"), "PATH must not start with an empty (cwd) component: %q", gotPath)
	assert.False(t, strings.Contains(gotPath, "::"), "PATH must not contain an empty component: %q", gotPath)
}

func TestRun_NewOutputFormatDispatch(t *testing.T) {
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
	target := filepath.Join(repoRoot, "target.txt")
	require.NoError(t, os.WriteFile(target, []byte("content\n"), 0o644))

	cfg := config.Config{
		Tools: map[string]config.Tool{
			"faketool": {Name: "faketool", KnownGoodVersion: "1.0.0"},
		},
		Lint: config.LintConfig{
			Files: map[string]config.FileType{},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"hadolint-e2e": {
						Name: "hadolint-e2e", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{{Name: "lint", Run: "faketool hadolint ${target}", Output: "hadolint"}},
					},
					"sarifuri-e2e": {
						Name: "sarifuri-e2e", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{{Name: "lint", Run: "faketool sarifuri ${tmpfile} ${target}", Output: "sarif_uri", ReadOutputFrom: "tmp_file"}},
					},
					"perlcritic": {
						Name: "perlcritic", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{{Name: "lint", Run: "faketool perlcritic ${target}", Output: "regex", ParseRegex: `path=(?P<path>[^,]+),line=(?P<line>\d+),col=(?P<col>\d+),code=(?P<code>[^,]+),message=(?P<message>.+)`}},
					},
					"genericregex-e2e": {
						Name: "genericregex-e2e", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{{Name: "lint", Run: "faketool genericregex ${target}", Output: "regex", ParseRegex: `(?P<path>[^:]+):(?P<line>\d+):(?P<col>\d+): \[(?P<severity>\w+)\] (?P<message>.+)`}},
					},
					"taplo-e2e": {
						// Output: "taplo" (taplo's real Command.Output value) must dispatch
						// through the new top-level "taplo" case, not the nested "regex" switch.
						Name: "taplo-e2e", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{{Name: "lint", Run: "faketool taplofake ${target}", Output: "taplo"}},
					},
					"emptyjson-e2e": {
						// A JSON-shaped Output format (hadolint) whose invocation produces empty
						// stdout must not fail the whole Run -- zero findings, not a parse error.
						Name: "emptyjson-e2e", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{{Name: "lint", Run: "faketool emptyjson ${target}", Output: "hadolint"}},
					},
				},
			},
		},
	}

	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: cacheDir, Concurrency: 1}, nil, notFormatter)
	require.NoError(t, err)

	byLinter := map[string]Event{}
	for ev := range events {
		byLinter[ev.Linter] = ev
	}

	hadolintEv, ok := byLinter["hadolint-e2e"]
	require.True(t, ok)
	assert.Equal(t, Done, hadolintEv.Phase)
	require.Len(t, hadolintEv.Findings, 1)
	assert.Equal(t, "DL3006", hadolintEv.Findings[0].RuleID)

	sarifURIEv, ok := byLinter["sarifuri-e2e"]
	require.True(t, ok)
	assert.Equal(t, Done, sarifURIEv.Phase)
	require.Len(t, sarifURIEv.Findings, 1, "sarif_uri must dispatch through ParseSARIF via the tmp_file it names")
	assert.Equal(t, "CKV_1", sarifURIEv.Findings[0].RuleID)

	perlcriticEv, ok := byLinter["perlcritic"]
	require.True(t, ok)
	assert.Equal(t, Done, perlcriticEv.Phase)
	require.Len(t, perlcriticEv.Findings, 1)
	assert.Equal(t, "SomePolicy", perlcriticEv.Findings[0].RuleID, "the \"regex\" output must dispatch through output.ParseFromRegex using the command's own ParseRegex")

	genericEv, ok := byLinter["genericregex-e2e"]
	require.True(t, ok)
	assert.Equal(t, Done, genericEv.Phase)
	require.Len(t, genericEv.Findings, 1)
	assert.Equal(t, "warning", genericEv.Findings[0].Severity, "a \"regex\" output dispatches through output.ParseFromRegex using the command's own ParseRegex, regardless of linter name")

	taploEv, ok := byLinter["taplo-e2e"]
	require.True(t, ok, "expected an event for taplo-e2e")
	assert.Equal(t, Done, taploEv.Phase, `Output: "taplo" must be dispatched, not Skipped as an unsupported output format`)
	require.Len(t, taploEv.Findings, 1)
	assert.Equal(t, output.Finding{
		Linter: "taplo-e2e", File: "target.txt", Line: 2, Column: 8,
		Severity: "error", Message: "invalid TOML",
	}, taploEv.Findings[0], "the finding's fields must come from ParseTaplo, proving the \"taplo\" case actually ran")

	emptyJSONEv, ok := byLinter["emptyjson-e2e"]
	require.True(t, ok, "expected an event for emptyjson-e2e")
	assert.Equal(t, Done, emptyJSONEv.Phase, "empty output on a JSON-shaped format must not Fail the whole run")
	assert.Empty(t, emptyJSONEv.Findings)
}

// TestRun_RunFromAndSandbox covers v0.3.2's real engine wiring end to end: RunFrom resolution
// (${target_directory}), SandboxType staging (copy_targets, expanded -- including expanded's
// empty-RunFrom default to the target's own directory), the two combined (mirroring snyk's real
// ${parent}+copy_targets), and a Batch: true command whose matched files span two different
// resolved directories. Each fixture linter is scoped to its own dedicated FileType (a unique
// extension) so that walking every fixture directory in one Run() call never lets one linter
// accidentally match another's files.
func TestRun_RunFromAndSandbox(t *testing.T) {
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

	// target-directory-linter (.td): one file in a subdirectory. RunFrom must resolve to that
	// subdirectory, not repoRoot -- no sandboxing, so the process runs against the real directory.
	subDir := filepath.Join(repoRoot, "sub")
	require.NoError(t, os.MkdirAll(subDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(subDir, "file.td"), []byte("x\n"), 0o644))

	// copy-targets-linter (.ct): a matched target plus a real, unmatched sibling file (a
	// different, unregistered extension) in the same directory. RunFrom is left empty, so
	// resolvedDir defaults to repoRoot -- the sandbox must contain exactly "ct/target.ct",
	// never the sibling (which was never a Files() match at all, and copy_targets must not
	// independently discover it).
	ctDir := filepath.Join(repoRoot, "ct")
	require.NoError(t, os.MkdirAll(ctDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ctDir, "target.ct"), []byte("x\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(ctDir, "sibling.txt"), []byte("x\n"), 0o644))

	// expanded-linter (.exp): two matched files in the same directory, RunFrom left empty.
	// "expanded" must default to the target's own directory (this test's whole point -- see the
	// buildJobs comment on effectiveRunFrom) and stage both files, even though only one directory
	// is involved and Batch: true means a single invocation covers both.
	expDir := filepath.Join(repoRoot, "exp")
	require.NoError(t, os.MkdirAll(expDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(expDir, "a.exp"), []byte("x\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(expDir, "b.exp"), []byte("x\n"), 0o644))

	// combo-linter (.combo): mirrors snyk (${parent} -> repoRoot, + copy_targets). resolvedDir is
	// repoRoot, so the sandboxed copy must preserve the nested "combo/" prefix, not flatten it.
	comboDir := filepath.Join(repoRoot, "combo")
	require.NoError(t, os.MkdirAll(comboDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(comboDir, "target.combo"), []byte("x\n"), 0o644))

	// batch-split-linter (.bs): two files in two different subdirectories, Batch: true with
	// ${target_directory} -- must become two separate invocations, not one.
	batchDir1 := filepath.Join(repoRoot, "batch1")
	batchDir2 := filepath.Join(repoRoot, "batch2")
	require.NoError(t, os.MkdirAll(batchDir1, 0o755))
	require.NoError(t, os.MkdirAll(batchDir2, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(batchDir1, "one.bs"), []byte("x\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(batchDir2, "two.bs"), []byte("x\n"), 0o644))

	cfg := config.Config{
		Tools: map[string]config.Tool{"faketool": {Name: "faketool", KnownGoodVersion: "1.0.0"}},
		Lint: config.LintConfig{
			Files: map[string]config.FileType{
				"td":    {Name: "td", Extensions: []string{"td"}},
				"ct":    {Name: "ct", Extensions: []string{"ct"}},
				"exp":   {Name: "exp", Extensions: []string{"exp"}},
				"combo": {Name: "combo", Extensions: []string{"combo"}},
				"bs":    {Name: "bs", Extensions: []string{"bs"}},
			},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"target-directory-linter": {
						Name: "target-directory-linter", Files: []string{"td"}, Tools: []string{"faketool"},
						Commands: []config.Command{{
							Name: "lint", Run: "faketool pwdls ${target}", Output: "sarif",
							RunFrom: "${target_directory}",
						}},
					},
					"copy-targets-linter": {
						Name: "copy-targets-linter", Files: []string{"ct"}, Tools: []string{"faketool"},
						Commands: []config.Command{{
							Name: "lint", Run: "faketool pwdls ${target}", Output: "sarif",
							SandboxType: "copy_targets",
						}},
					},
					"expanded-linter": {
						Name: "expanded-linter", Files: []string{"exp"}, Tools: []string{"faketool"},
						Commands: []config.Command{{
							Name: "lint", Run: "faketool pwdls ${target}", Output: "sarif",
							SandboxType: "expanded", Batch: true,
						}},
					},
					"combo-linter": {
						Name: "combo-linter", Files: []string{"combo"}, Tools: []string{"faketool"},
						Commands: []config.Command{{
							Name: "lint", Run: "faketool pwdls ${target}", Output: "sarif",
							RunFrom: "${parent}", SandboxType: "copy_targets",
						}},
					},
					"batch-split-linter": {
						Name: "batch-split-linter", Files: []string{"bs"}, Tools: []string{"faketool"},
						Commands: []config.Command{{
							Name: "lint", Run: "faketool sarif ${target}", Output: "sarif",
							RunFrom: "${target_directory}", Batch: true,
						}},
					},
				},
			},
		},
	}

	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: cacheDir, Concurrency: 1}, nil, notFormatter)
	require.NoError(t, err)

	var all []Event
	byLinter := map[string]Event{}
	for ev := range events {
		all = append(all, ev)
		if ev.Phase == Done {
			byLinter[ev.Linter] = ev
		}
	}

	// target_directory: cwd must be subDir itself (no sandboxing), symlink-normalized -- t.TempDir()
	// on macOS can live under a symlinked /tmp -- and the finding's File must come back
	// repoRoot-relative.
	tdEv := byLinter["target-directory-linter"]
	require.Len(t, tdEv.Findings, 1)
	wantSubDir, err := filepath.EvalSymlinks(subDir)
	require.NoError(t, err)
	assert.Contains(t, tdEv.Findings[0].Message, "pwd="+wantSubDir)
	assert.Contains(t, tdEv.Findings[0].Message, "files=file.td")
	assert.Equal(t, filepath.Join("sub", "file.td"), tdEv.Findings[0].File)

	// copy_targets: resolvedDir is repoRoot (RunFrom left empty), so the sandbox must preserve the
	// nested "ct/" prefix -- and sibling.txt (a real file, but never a Files() match) must never
	// appear, proving copy_targets stages exactly the given targets, nothing it finds on its own.
	ctEv := byLinter["copy-targets-linter"]
	require.Len(t, ctEv.Findings, 1)
	assert.Contains(t, ctEv.Findings[0].Message, "files=ct/target.ct")
	assert.NotContains(t, ctEv.Findings[0].Message, "sibling")
	assert.Equal(t, filepath.Join("ct", "target.ct"), ctEv.Findings[0].File)

	// expanded: both a.exp and b.exp must appear -- proving the empty-RunFrom default resolved to
	// expDir itself (not repoRoot, which has no .exp files at all), and expanded staged the whole
	// directory, not just the matched targets.
	expEv := byLinter["expanded-linter"]
	require.Len(t, expEv.Findings, 2)
	for _, f := range expEv.Findings {
		assert.Contains(t, f.Message, "files=a.exp,b.exp")
	}

	// combo: ${parent} resolves to repoRoot, but copy_targets still sandboxes -- the sandbox's
	// recursive listing must show the nested "combo/target.combo" path (not a flattened
	// "target.combo"), and the finding's File must still come back repoRoot-relative.
	comboEv := byLinter["combo-linter"]
	require.Len(t, comboEv.Findings, 1)
	assert.Contains(t, comboEv.Findings[0].Message, "files=combo/target.combo")
	assert.Equal(t, filepath.Join("combo", "target.combo"), comboEv.Findings[0].File)

	// batch-split: 2 files in 2 different resolved directories must produce 2 Running events for
	// batch-split-linter, not 1 -- and 2 findings, each attributed to its own real file.
	var batchRunning int
	for _, ev := range all {
		if ev.Linter == "batch-split-linter" && ev.Phase == Running {
			batchRunning++
		}
	}
	assert.Equal(t, 2, batchRunning, "files in 2 different resolved directories must split into 2 invocations")
	batchEv := byLinter["batch-split-linter"]
	require.Len(t, batchEv.Findings, 2)
	gotFiles := map[string]bool{batchEv.Findings[0].File: true, batchEv.Findings[1].File: true}
	assert.True(t, gotFiles[filepath.Join("batch1", "one.bs")] && gotFiles[filepath.Join("batch2", "two.bs")],
		"want repoRoot-relative batch1/one.bs and batch2/two.bs, got %v", gotFiles)
}

// TestRun_SandboxAbsolutePathFindingIsRemapped covers a final-review finding: a sandboxed tool
// that echoes an absolute path (into the throwaway sandbox directory) for artifactLocation.uri,
// instead of the relative path substituted into ${target}, must not leak that temp directory
// into the report -- the finding's File must still come back as a clean repoRoot-relative path.
func TestRun_SandboxAbsolutePathFindingIsRemapped(t *testing.T) {
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
	ctDir := filepath.Join(repoRoot, "ct")
	require.NoError(t, os.MkdirAll(ctDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ctDir, "target.abs"), []byte("x\n"), 0o644))

	cfg := config.Config{
		Tools: map[string]config.Tool{"faketool": {Name: "faketool", KnownGoodVersion: "1.0.0"}},
		Lint: config.LintConfig{
			Files: map[string]config.FileType{
				"abs": {Name: "abs", Extensions: []string{"abs"}},
			},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"abspath-linter": {
						Name: "abspath-linter", Files: []string{"abs"}, Tools: []string{"faketool"},
						Commands: []config.Command{{
							Name: "lint", Run: "faketool abspath ${target}", Output: "sarif",
							SandboxType: "copy_targets",
						}},
					},
				},
			},
		},
	}

	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: cacheDir, Concurrency: 1}, nil, notFormatter)
	require.NoError(t, err)

	var done *Event
	for ev := range events {
		if ev.Phase == Done {
			e := ev
			done = &e
		}
	}
	require.NotNil(t, done, "expected a Done event for abspath-linter")
	require.Len(t, done.Findings, 1)
	assert.Equal(t, filepath.Join("ct", "target.abs"), done.Findings[0].File,
		"an absolute artifactLocation.uri pointing into the sandbox must be remapped back to a "+
			"clean repoRoot-relative path, not leak the sandbox's temp directory")
	assert.NotContains(t, done.Findings[0].File, os.TempDir())
}

// TestRun_NoTargetCommandBatchesEvenWithoutBatchFlag covers a final-review finding: a command
// with Batch: false whose Run string never references ${target} (real catalog examples:
// tflint's and brakeman's first commands) can't distinguish between the files it's nominally
// checking -- running it once per matched file (Batch: false's naive default) would repeat the
// exact same invocation, and so the exact same findings, once per file. Such a command must
// batch by resolved directory even though Batch is false, producing exactly one Running event
// and no duplicated findings for 3 matched files in the same directory.
func TestRun_NoTargetCommandBatchesEvenWithoutBatchFlag(t *testing.T) {
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
	for _, name := range []string{"a.nt", "b.nt", "c.nt"} {
		require.NoError(t, os.WriteFile(filepath.Join(repoRoot, name), []byte("x\n"), 0o644))
	}

	cfg := config.Config{
		Tools: map[string]config.Tool{"faketool": {Name: "faketool", KnownGoodVersion: "1.0.0"}},
		Lint: config.LintConfig{
			Files: map[string]config.FileType{
				"nt": {Name: "nt", Extensions: []string{"nt"}},
			},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"no-target-linter": {
						Name: "no-target-linter", Files: []string{"nt"}, Tools: []string{"faketool"},
						Commands: []config.Command{{
							Name: "lint", Run: "faketool sarifone", Output: "sarif", Batch: false,
						}},
					},
				},
			},
		},
	}

	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: cacheDir, Concurrency: 1}, nil, notFormatter)
	require.NoError(t, err)

	var running int
	var done *Event
	for ev := range events {
		if ev.Linter != "no-target-linter" {
			continue
		}
		if ev.Phase == Running {
			running++
		}
		if ev.Phase == Done {
			e := ev
			done = &e
		}
	}

	assert.Equal(t, 1, running,
		"a Run string with no ${target} can't distinguish files -- it must run once per resolved directory, not once per file")
	require.NotNil(t, done, "expected a Done event for no-target-linter")
	require.Len(t, done.Findings, 1,
		"the identical invocation must not be repeated once per file, duplicating its findings")
}

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
	events, err := Run(ctx, Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: cacheDir, Concurrency: 1}, nil, notFormatter)
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

// TestRun_ParserConvertsRawOutputThroughStdinStdout proves the real Command.Parser contract every
// trunk-io converter script this feature's research found actually uses (trufflehog_to_sarif.py,
// tfsec/parse.py, ruff_to_sarif.py, all read in full): the real command's raw stdout becomes the
// parser script's stdin, and the parser script's own stdout is what gets parsed per cmd.Output --
// not the real command's raw output directly.
func TestRun_ParserConvertsRawOutputThroughStdinStdout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("faketool invoked via sh -c")
	}

	binPath := buildFakeToolBinary(t)

	cacheDir := t.TempDir()
	root, err := download.Root(cacheDir)
	require.NoError(t, err)

	// The linter's own tool.
	toolShim := download.ShimPath(root, "tools", "faketool", "1.0.0", "faketool")
	require.NoError(t, os.MkdirAll(filepath.Dir(toolShim), 0o755))
	require.NoError(t, download.WriteShim(toolShim, binPath))

	// The parser's own runtime -- a real "python" would resolve to a python3 shim; standing in
	// with faketool itself is enough to prove the wiring (runtime lookup, PATH, stdin/stdout)
	// without needing a real Python interpreter in CI.
	runtimeShim := download.ShimPath(root, "runtimes", "python", "3.12.0", "faketool")
	require.NoError(t, os.MkdirAll(filepath.Dir(runtimeShim), 0o755))
	require.NoError(t, download.WriteShim(runtimeShim, binPath))

	repoRoot := t.TempDir()
	target := filepath.Join(repoRoot, "target.txt")
	require.NoError(t, os.WriteFile(target, []byte("content\n"), 0o644))

	cfg := config.Config{
		Tools: map[string]config.Tool{
			"faketool": {Name: "faketool", KnownGoodVersion: "1.0.0"},
		},
		Runtimes: config.CategoryConfig[config.Runtime]{
			Definitions: map[string]config.Runtime{
				"python": {Type: "python", KnownGoodVersion: "3.12.0", Shims: config.ShimList{"faketool"}},
			},
		},
		Lint: config.LintConfig{
			Files: map[string]config.FileType{},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"fakeparsed": {
						Name: "fakeparsed", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{{
							Name: "lint", Run: "faketool rawtext ${target}", Output: "sarif", Batch: true,
							Parser: &config.Parser{Runtime: "python", Run: "faketool sarifconvert"},
						}},
					},
				},
			},
		},
	}

	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: cacheDir, Concurrency: 1}, nil, notFormatter)
	require.NoError(t, err)

	var got Event
	for ev := range events {
		if ev.Linter == "fakeparsed" && ev.Phase == Done {
			got = ev
		}
	}
	require.Len(t, got.Findings, 1)
	assert.Equal(t, "converted-rule", got.Findings[0].RuleID,
		"the finding must come from sarifconvert's output, not rawtext's raw text")
	assert.Equal(t, "converted from raw", got.Findings[0].Message)
	assert.Equal(t, "target.txt", got.Findings[0].File)
}

// TestRun_PluginAndCwdTemplateVarsResolveFromLinterSource proves ${plugin} substitutes to the
// Linter's own SourceRoot and ${cwd} to SourceRoot joined with SourceDir -- the two template vars
// a real trunk-io Command.Run/Parser.Run references to reach its own plugin source's scripts
// (nancy's real run.sh is `sh ${plugin}/linters/nancy/run.sh`; tfsec's real parser.run is
// `python3 ${cwd}/parse.py`, both confirmed by reading the real catalog during this feature's
// design).
func TestRun_PluginAndCwdTemplateVarsResolveFromLinterSource(t *testing.T) {
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
	target := filepath.Join(repoRoot, "target.txt")
	require.NoError(t, os.WriteFile(target, []byte("content\n"), 0o644))

	pluginRoot := filepath.Join(t.TempDir(), "plugin-source")

	cfg := config.Config{
		Tools: map[string]config.Tool{
			"faketool": {Name: "faketool", KnownGoodVersion: "1.0.0"},
		},
		Lint: config.LintConfig{
			Files: map[string]config.FileType{},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"fakevars": {
						Name: "fakevars", Files: []string{"ALL"}, Tools: []string{"faketool"},
						SourceRoot: pluginRoot, SourceDir: filepath.Join("linters", "fakevars"),
						Commands: []config.Command{{
							Name: "lint", Run: "faketool echoargs ${plugin} ${cwd}", Output: "sarif", Batch: true,
						}},
					},
				},
			},
		},
	}

	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: cacheDir, Concurrency: 1}, nil, notFormatter)
	require.NoError(t, err)

	var got Event
	for ev := range events {
		if ev.Linter == "fakevars" && ev.Phase == Done {
			got = ev
		}
	}
	require.Len(t, got.Findings, 1)
	wantCwd := filepath.Join(pluginRoot, "linters", "fakevars")
	assert.Equal(t, pluginRoot+"|"+wantCwd, got.Findings[0].Message)
}
