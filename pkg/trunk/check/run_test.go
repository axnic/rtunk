package check

import (
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
)

// fakeToolSrc is a real compiled Go program standing in for a linter's tool binary, not a shell
// script -- avoids ENOEXEC on some shebang chains (the same lesson the python-runtime work in
// this repo's history already paid for). "sarif" prints a fake SARIF result per arg; "passfail"
// exits 1 if any given file's content is exactly "FAIL\n"; "crash" always exits 42.
const fakeToolSrc = `package main

import (
	"fmt"
	"os"
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
					"fakeskipparser": {
						Name: "fakeskipparser", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{{
							Name: "unsupported", Run: "faketool sarif ${target}", Output: "sarif",
							Parser: &config.Parser{Runtime: "python", Run: "convert.py"},
						}},
					},
					"fakeskiprunfrom": {
						Name: "fakeskiprunfrom", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{{Name: "unsupported", Run: "faketool sarif ${target}", Output: "sarif", RunFrom: "${parent}"}},
					},
					"fakeskipsandbox": {
						Name: "fakeskipsandbox", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{{Name: "unsupported", Run: "faketool sarif ${target}", Output: "sarif", SandboxType: "copy_targets"}},
					},
					"fakeformatteronly": {
						Name: "fakeformatteronly", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{{Name: "fmt", Run: "faketool sarif ${target}", Output: "rewrite", Formatter: true, InPlace: true}},
					},
				},
			},
		},
	}

	events, err := Run(cfg, cacheDir, repoRoot, nil, 1)
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
	assert.EqualError(t, errEv.Err, "check: fakeerror: boom exited 42: ")

	errStderrEv, ok := byLinter["fakeerrorstderr"]
	require.True(t, ok, "expected an event for fakeerrorstderr")
	assert.Equal(t, Failed, errStderrEv.Phase)
	assert.EqualError(t, errStderrEv.Err, "check: fakeerrorstderr: boom exited 43: boom: disk on fire",
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

	skipParserEv, ok := byLinter["fakeskipparser"]
	require.True(t, ok)
	assert.Equal(t, Skipped, skipParserEv.Phase)
	assert.Equal(t, "unsupported parser (native output requires a converter script)", skipParserEv.Note)

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
	events, err := Run(cfg, cacheDir, repoRoot, []string{"."}, 1)
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
		events, err := Run(newCfg(), cacheDir, repoRoot, nil, concurrency)
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

	events, err := Run(cfg, cacheDir, repoRoot, nil, 1)
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

	out, stderr, exitCode, err := runOneInvocation(cmd, repoRoot, "", nil)
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
						Commands: []config.Command{{Name: "lint", Run: "faketool perlcritic ${target}", Output: "regex"}},
					},
					"genericregex-e2e": {
						Name: "genericregex-e2e", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{{Name: "lint", Run: "faketool genericregex ${target}", Output: "regex"}},
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

	events, err := Run(cfg, cacheDir, repoRoot, nil, 1)
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
	assert.Equal(t, "SomePolicy", perlcriticEv.Findings[0].RuleID, "the \"regex\" output for linter name \"perlcritic\" must dispatch to ParsePerlCritic, not the generic parser")

	genericEv, ok := byLinter["genericregex-e2e"]
	require.True(t, ok)
	assert.Equal(t, Done, genericEv.Phase)
	require.Len(t, genericEv.Findings, 1)
	assert.Equal(t, "warning", genericEv.Findings[0].Severity, "a \"regex\" output for any other linter name must fall through to ParseGenericRegex")

	taploEv, ok := byLinter["taplo-e2e"]
	require.True(t, ok, "expected an event for taplo-e2e")
	assert.Equal(t, Done, taploEv.Phase, `Output: "taplo" must be dispatched, not Skipped as an unsupported output format`)
	require.Len(t, taploEv.Findings, 1)
	assert.Equal(t, Finding{
		Linter: "taplo-e2e", File: "target.txt", Line: 2, Column: 8,
		Severity: "error", Message: "invalid TOML",
	}, taploEv.Findings[0], "the finding's fields must come from ParseTaplo, proving the \"taplo\" case actually ran")

	emptyJSONEv, ok := byLinter["emptyjson-e2e"]
	require.True(t, ok, "expected an event for emptyjson-e2e")
	assert.Equal(t, Done, emptyJSONEv.Phase, "empty output on a JSON-shaped format must not Fail the whole run")
	assert.Empty(t, emptyJSONEv.Findings)
}
