package engine

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/cache/download"
	"github.com/xunleii/rtunk/pkg/run/runlog"
	"github.com/xunleii/rtunk/pkg/trunk/config"
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
	case "stdinfmt":
		// A stdin/stdout formatter: reads the target's content from stdin, writes the
		// reformatted result to stdout -- never touches the file itself.
		data, _ := io.ReadAll(os.Stdin)
		fmt.Print(strings.ToUpper(string(data)))
	case "emptyout":
		// Exits 0 with no stdout at all -- stands in for a tool that wrote its result
		// somewhere else (a side file) or misread its own invocation.
	case "partialthenkilled":
		// Prints a partial result immediately, then hangs -- stands in for a process a
		// context timeout/cancellation kills mid-run, whose already-flushed stdout is only
		// ever a truncated fragment of the real result.
		fmt.Print("PART")
		os.Stdout.Sync()
		time.Sleep(5 * time.Second)
	case "slowstdinfmt":
		// Like "stdinfmt", but sleeps between reading stdin and printing the result --
		// widens the window a concurrent InPlace write to the same file could land in, to
		// prove the read-invoke-write cycle is race-safe, not just "usually fast enough."
		data, _ := io.ReadAll(os.Stdin)
		time.Sleep(300 * time.Millisecond)
		fmt.Print(strings.ToUpper(string(data)))
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
	case "failonemptystdin":
		// Stands in for a real converter script that can't handle empty input (e.g. Python's
		// json.load(sys.stdin) raising on an empty stream) -- proves the parser is never invoked
		// at all when the real command's own output was empty.
		data, _ := io.ReadAll(os.Stdin)
		if len(data) == 0 {
			os.Exit(1)
		}
		fmt.Print("{\"runs\":[{\"results\":[]}]}")
	case "sleep":
		time.Sleep(250 * time.Millisecond)
	case "sleepwrite":
		// Stands in for two concurrent in_place formatters racing the same file: records an
		// "enter"/"exit" pair (with a deliberate sleep between them) to args[1] (a shared probe
		// log file), so a test can assert no two invocations' [enter,exit] windows overlap --
		// proving real serialization, not just "the final file content happens to look right."
		probePath := args[1]
		target := args[2]
		f, _ := os.OpenFile(probePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		f.WriteString("enter\n")
		f.Close()
		time.Sleep(100 * time.Millisecond)
		os.WriteFile(target, []byte("done\n"), 0o644)
		f2, _ := os.OpenFile(probePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		f2.WriteString("exit\n")
		f2.Close()
	case "labeledsleep":
		// Like "sleepwrite", but every line carries a caller-chosen label (args[1]) -- lets two
		// different commands share one probe file and still be told apart, so a test can check
		// both "this label's own windows never overlap with themselves" (max_concurrency: 1
		// honored) and "two different labels' windows DO overlap with each other" (their caps
		// don't block one another) from one merged, real-time-ordered log.
		label := args[1]
		probePath := args[2]
		target := args[3]
		f, _ := os.OpenFile(probePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		f.WriteString(label + ":enter\n")
		f.Close()
		time.Sleep(150 * time.Millisecond)
		os.WriteFile(target, []byte("done\n"), 0o644)
		f2, _ := os.OpenFile(probePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		f2.WriteString(label + ":exit\n")
		f2.Close()
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
	case "rewrite":
		// Simulates a real in-place formatter (gofmt -w): overwrites each target file with fixed
		// content, so a test can prove InPlace hashing detects a genuine content change on one
		// file while correctly excluding another whose content was already identical.
		for _, f := range args[1:] {
			os.WriteFile(f, []byte("formatted\n"), 0o644)
		}
	case "findancestor":
		// Walks up from its own cwd looking for a marker file named args[1], writing "v2\n" to
		// args[2] if found (config discovered) or "default\n" if not (silently fell back to
		// defaults) -- stands in for a real config-driven formatter's own ancestor-directory
		// config resolution (e.g. real prettier looking for .prettierrc/.editorconfig).
		markerName := args[1]
		target := args[2]
		dir, _ := os.Getwd()
		found := false
		for {
			if _, err := os.Stat(filepath.Join(dir, markerName)); err == nil {
				found = true
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
		if found {
			os.WriteFile(target, []byte("v2\n"), 0o644)
		} else {
			os.WriteFile(target, []byte("default\n"), 0o644)
		}
	case "rewrite2":
		// Same idea as "rewrite" but writes different content -- lets a test give two separate
		// commands on the same linter each genuinely change the same file (rather than one
		// command's rewrite being a no-op on top of the other's), so ChangedFiles' dedup logic
		// has a real duplicate to collapse instead of a vacuous one.
		for _, f := range args[1:] {
			os.WriteFile(f, []byte("formatted2\n"), 0o644)
		}
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
	case "rawtextexit2":
		// Like "rawtext" but exits 2 -- stands in for real prettier's own success_codes: [0, 2]
		// (2 meaning "reformatted"), letting a test prove ${exit_code} carries the real command's
		// own exit code through to the parser stage, exactly as real prettier's parser.run does.
		fmt.Print("RAWFINDING:" + strings.Join(args[1:], ","))
		os.Exit(2)
	case "sarifconvertexitcode":
		// Stands in for real prettier's own converter script contract: parser.run passes the real
		// command's exit code as a bare positional argument (args[1]), which the script uses to
		// decide what to report -- proven here by embedding it directly into the finding.
		data, _ := io.ReadAll(os.Stdin)
		raw := strings.TrimPrefix(strings.TrimSpace(string(data)), "RAWFINDING:")
		exitCode := args[1]
		var results []string
		for _, f := range strings.Split(raw, ",") {
			results = append(results, "{\"ruleId\":\"exit-code-"+exitCode+"\",\"level\":\"error\",\"message\":{\"text\":\"exit was "+exitCode+"\"},\"locations\":[{\"physicalLocation\":{\"artifactLocation\":{\"uri\":\""+f+"\"},\"region\":{\"startLine\":1}}}]}")
		}
		fmt.Print("{\"runs\":[{\"results\":[" + strings.Join(results, ",") + "]}]}")
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
					"fakeskipparservar": {
						Name: "fakeskipparservar", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{{
							Name: "unsupported", Run: "faketool sarif ${target}", Output: "sarif",
							Parser: &config.Parser{Runtime: "python", Run: "faketool sarifconvert ${target,}"},
						}},
					},
					"fakeskipparsertmpfile": {
						Name: "fakeskipparsertmpfile", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{{
							Name: "unsupported", Run: "faketool sarif ${target}", Output: "sarif",
							Parser: &config.Parser{Runtime: "python", Run: "faketool sarifconvert ${tmpfile}"},
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

	skipParserVarEv, ok := byLinter["fakeskipparservar"]
	require.True(t, ok)
	assert.Equal(t, Skipped, skipParserVarEv.Phase)
	assert.Contains(t, skipParserVarEv.Note, `${target,}`)
	assert.Contains(t, skipParserVarEv.Note, "in parser")

	skipParserTmpfileEv, ok := byLinter["fakeskipparsertmpfile"]
	require.True(t, ok)
	assert.Equal(t, Skipped, skipParserTmpfileEv.Phase)
	assert.Contains(t, skipParserTmpfileEv.Note, "${tmpfile}")
	assert.Contains(t, skipParserTmpfileEv.Note, "in parser")

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

// TestRun_RecordsUsageRegistry covers Run wiring download.RecordUsage (see registry.go): every
// call must leave behind a per-repository registry entry reflecting the just-resolved cfg, so a
// concurrently-running `cache prune` (Task 5) never mistakes this repo's in-use items for
// unreferenced ones.
func TestRun_RecordsUsageRegistry(t *testing.T) {
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

	env := Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: cacheDir, Concurrency: 1}
	events, err := Run(context.Background(), env, nil, notFormatter)
	require.NoError(t, err)
	for range events { //nolint:revive // draining the channel so Run's own goroutines finish before the asserts below
	}

	entries, err := os.ReadDir(filepath.Join(filepath.Dir(root), "registry"))
	require.NoError(t, err)
	assert.NotEmpty(t, entries, "Run must record a registry entry for env.RepoRoot")
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
		for range events { //nolint:revive // draining the channel is the whole point; there is nothing to do per event
		}
		return time.Since(start)
	}

	sequential := drain(1)
	parallel := drain(2)

	assert.Greater(t, sequential, 400*time.Millisecond, "two 250ms jobs one worker at a time must take close to 500ms")
	assert.Less(t, parallel, 400*time.Millisecond, "two 250ms jobs on two workers must take close to 250ms, not ~500ms")
}

// TestRun_MaxConcurrency_CapsParallelInvocationsOfOneCommand proves Command.MaxConcurrency caps
// how many invocations of that command run at once, independent of the run's own overall worker
// count: three files, Concurrency: 3 (so the run's own workers would happily run all three at
// once absent this cap), MaxConcurrency: 1. Reuses the "sleepwrite" probe technique
// TestRun_ConcurrentInPlaceCommandsAreSerialized already uses (enter/exit pairs logged to a
// shared file) to prove -- deterministically, not by timing -- that at most one invocation is
// ever mid-flight.
func TestRun_MaxConcurrency_CapsParallelInvocationsOfOneCommand(t *testing.T) {
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
	probe := filepath.Join(t.TempDir(), "probe.log")

	cfg := config.Config{
		Tools: map[string]config.Tool{"faketool": {Name: "faketool", KnownGoodVersion: "1.0.0"}},
		Lint: config.LintConfig{
			Files: map[string]config.FileType{},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"capped": {
						Name: "capped", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{{
							Name: "lint", Run: "faketool sleepwrite " + probe + " ${target}", Output: "pass_fail",
							MaxConcurrency: 1,
						}},
					},
				},
			},
		},
	}

	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: cacheDir, Concurrency: 3}, nil, notFormatter)
	require.NoError(t, err)
	for range events { //nolint:revive // draining the channel is the whole point; there is nothing to do per event
	}

	data, err := os.ReadFile(probe)
	require.NoError(t, err)
	lines := strings.Fields(strings.TrimSpace(string(data)))
	require.Len(t, lines, 6, "three invocations, each logging enter+exit")

	depth := 0
	for _, l := range lines {
		switch l {
		case "enter":
			depth++
			require.LessOrEqual(t, depth, 1, "a second invocation of this command entered before the first exited -- max_concurrency: 1 not honored")
		case "exit":
			depth--
		}
	}
}

// TestRun_MaxConcurrency_DifferentCommandsCappedIndependently proves the semaphore is keyed by
// linter+command, not linter alone: two commands on the same linter, each MaxConcurrency: 1, must
// each be capped at 1 among their own invocations while never blocking on the other command's own
// cap. Both commands log into one shared, label-tagged probe (the "labeledsleep" fake-tool case),
// so the merged log is real-time ordered across both: per-label depth must never exceed 1 (each
// command's own cap honored), and -- this is the case most likely to regress silently if the
// semaphore were keyed by linter name alone -- both labels must be "inside" (between their own
// enter/exit) at the same time at least once, proving neither command's cap blocks the other's.
func TestRun_MaxConcurrency_DifferentCommandsCappedIndependently(t *testing.T) {
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
	probe := filepath.Join(t.TempDir(), "probe.log")

	cfg := config.Config{
		Tools: map[string]config.Tool{"faketool": {Name: "faketool", KnownGoodVersion: "1.0.0"}},
		Lint: config.LintConfig{
			Files: map[string]config.FileType{},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"capped": {
						Name: "capped", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{
							{Name: "cmdA", Run: "faketool labeledsleep A " + probe + " ${target}", Output: "pass_fail", MaxConcurrency: 1},
							{Name: "cmdB", Run: "faketool labeledsleep B " + probe + " ${target}", Output: "pass_fail", MaxConcurrency: 1},
						},
					},
				},
			},
		},
	}

	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: cacheDir, Concurrency: 6}, nil, notFormatter)
	require.NoError(t, err)
	for range events { //nolint:revive // draining the channel is the whole point; there is nothing to do per event
	}

	data, err := os.ReadFile(probe)
	require.NoError(t, err)
	lines := strings.Fields(strings.TrimSpace(string(data)))
	require.Len(t, lines, 12, "six invocations (3 files x 2 commands), each logging enter+exit")

	depth := map[string]int{}
	open := map[string]bool{}
	sawBothOpen := false
	for _, l := range lines {
		label, evt, ok := strings.Cut(l, ":")
		require.True(t, ok, "malformed probe line %q", l)
		switch evt {
		case "enter":
			depth[label]++
			require.LessOrEqual(t, depth[label], 1, "a second invocation of command %s entered before the first exited -- max_concurrency: 1 not honored", label)
			open[label] = true
			if open["A"] && open["B"] {
				sawBothOpen = true
			}
		case "exit":
			depth[label]--
			open[label] = false
		default:
			t.Fatalf("unexpected probe event %q", l)
		}
	}
	assert.True(t, sawBothOpen, "cmdA and cmdB never ran concurrently with each other -- they should not block on each other's cap")
}

// TestRun_MaxConcurrency_DoesNotChargeRunTimeoutForQueueWait covers this task's second finding: a
// job queued behind a full max_concurrency semaphore must not have that wait counted against its
// own run_timeout. Three files share one MaxConcurrency: 1 command whose real invocation
// ("faketool sleep") reliably takes several hundred ms (250ms sleep plus real process
// spawn/shim-resolution overhead); with Concurrency: 3 all three jobs queue for the same single
// slot, so the third one waits through two full run cycles before it ever gets to run at all.
// RunTimeout (900ms) comfortably covers one job's own real run in isolation, but not two other
// jobs' worth of queuing on top of it. Before the fix, run_timeout wrapped ctx before the
// semaphore acquire, so the third job's deadline started ticking the moment it was scheduled
// (t=0) rather than when it actually got a slot -- its deadline would already be exceeded while
// still waiting in the semaphore select, well before its own real invocation ever started,
// producing exactly the bare, unhelpful "context deadline exceeded" the review flagged. After the
// fix, the semaphore is acquired on the unwrapped parent ctx and run_timeout only wraps ctx right
// before the real invocation, so every job gets its own full 900ms budget starting from when it
// actually begins running, regardless of how long it spent queuing first.
func TestRun_MaxConcurrency_DoesNotChargeRunTimeoutForQueueWait(t *testing.T) {
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
					"capped": {
						Name: "capped", Files: []string{"ALL"}, Tools: []string{"faketool"}, RunTimeout: "900ms",
						Commands: []config.Command{{
							Name: "lint", Run: "faketool sleep ${target}", Output: "pass_fail",
							MaxConcurrency: 1,
						}},
					},
				},
			},
		},
	}

	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: cacheDir, Concurrency: 3}, nil, notFormatter)
	require.NoError(t, err)

	var failed int
	for ev := range events {
		if ev.Phase == Failed {
			failed++
			t.Logf("unexpected Failed event: linter=%s command=%s err=%v", ev.Linter, ev.Note, ev.Err)
		}
	}
	assert.Equal(t, 0, failed, "a job's run_timeout must start from when it actually got a semaphore slot, not from when it started queuing")
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

	out, stderr, exitCode, err := runOneInvocation(context.Background(), cmd, repoRoot, "", nil, "", "", nil, runlog.Event{}, "")
	require.NoError(t, err)
	assert.Equal(t, 0, exitCode)
	assert.Empty(t, stderr)

	gotPath := strings.TrimSuffix(out, "\n")
	assert.Equal(t, os.Getenv("PATH"), gotPath, "empty pathEnv must leave PATH untouched")
	assert.False(t, strings.HasPrefix(gotPath, ":"), "PATH must not start with an empty (cwd) component: %q", gotPath)
	assert.False(t, strings.Contains(gotPath, "::"), "PATH must not contain an empty component: %q", gotPath)
}

func TestRunOneInvocation_MarkdownlintReadsStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh -c only on POSIX")
	}

	cmd := config.Command{Name: "lint", Run: "echo '[]' >&2", Output: "markdownlint"}

	out, _, _, err := runOneInvocation(context.Background(), cmd, t.TempDir(), "", nil, "", "", nil, runlog.Event{}, "")
	require.NoError(t, err)
	assert.Equal(t, "[]\n", out, "markdownlint --json reports on stderr")
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

// TestRun_TargetParentRunsOncePerDirectory covers target: ${parent} (golangci-lint's own
// definition): the tool must get each matched file's directory, not the file itself, and files
// sharing a directory must collapse into a single invocation.
func TestRun_TargetParentRunsOncePerDirectory(t *testing.T) {
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
	for _, f := range []string{"pa/one.tp", "pa/two.tp", "pb/three.tp"} {
		require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, filepath.Dir(f)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(repoRoot, f), []byte("x\n"), 0o644))
	}

	cfg := config.Config{
		Tools: map[string]config.Tool{"faketool": {Name: "faketool", KnownGoodVersion: "1.0.0"}},
		Lint: config.LintConfig{
			Files: map[string]config.FileType{"tp": {Name: "tp", Extensions: []string{"tp"}}},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"parent-linter": {
						Name: "parent-linter", Files: []string{"tp"}, Tools: []string{"faketool"},
						Commands: []config.Command{{
							Name: "lint", Run: "faketool sarif ${target}", Output: "sarif", Target: "${parent}",
						}},
					},
				},
			},
		},
	}

	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: cacheDir, Concurrency: 1}, nil, notFormatter)
	require.NoError(t, err)

	var running int
	var done Event
	for ev := range events {
		switch ev.Phase {
		case Running:
			running++
		case Done:
			done = ev
		}
	}
	assert.Equal(t, 2, running, "pa/ holds two files but must be linted once, pb/ once")
	require.Len(t, done.Findings, 2)
	got := []string{done.Findings[0].File, done.Findings[1].File}
	assert.ElementsMatch(t, []string{"pa", "pb"}, got)
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
	for range events { //nolint:revive // empty on purpose
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

// TestRun_LinterRunTimeout_FailsSlowCommand covers config.Linter.RunTimeout actually being
// enforced: a linter declaring a run_timeout shorter than its command's real runtime must have
// that invocation killed and reported Failed, not left to run forever.
func TestRun_LinterRunTimeout_FailsSlowCommand(t *testing.T) {
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
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "a.txt"), []byte("x\n"), 0o644))

	cfg := config.Config{
		Tools: map[string]config.Tool{"faketool": {Name: "faketool", KnownGoodVersion: "1.0.0"}},
		Lint: config.LintConfig{
			Files: map[string]config.FileType{},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"slow": {
						Name: "slow", Files: []string{"ALL"}, Tools: []string{"faketool"}, RunTimeout: "50ms",
						Commands: []config.Command{{Name: "lint", Run: "faketool sleep ${target}", Output: "pass_fail"}},
					},
				},
			},
		},
	}

	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: cacheDir, Concurrency: 1}, nil, notFormatter)
	require.NoError(t, err)

	var failed int
	for ev := range events {
		if ev.Phase == Failed {
			failed++
		}
	}
	assert.Equal(t, 1, failed, "a command exceeding its linter's run_timeout must report Failed")
}

// TestRun_LinterNoRunTimeout_SlowCommandStillSucceeds proves the RunTimeout addition is a true
// no-op for the overwhelming majority of linters that never declare one: the same slow command,
// with RunTimeout left at its default "", must still complete normally.
func TestRun_LinterNoRunTimeout_SlowCommandStillSucceeds(t *testing.T) {
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
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "a.txt"), []byte("x\n"), 0o644))

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

	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: cacheDir, Concurrency: 1}, nil, notFormatter)
	require.NoError(t, err)

	var done, failed int
	for ev := range events {
		switch ev.Phase {
		case Done:
			done++
		case Failed:
			failed++
		}
	}
	assert.Equal(t, 1, done, "no run_timeout declared must not change the slow command's normal success")
	assert.Equal(t, 0, failed)
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

	pluginRoot := t.TempDir()
	sourceDir := filepath.Join("linters", "fakeparsed")
	cwdDir := filepath.Join(pluginRoot, sourceDir)
	require.NoError(t, os.MkdirAll(cwdDir, 0o755))
	// Stand in for a real converter script living right next to its own plugin.yaml (e.g. real
	// trufflehog's trufflehog_to_sarif.py sits beside linters/trufflehog/plugin.yaml) -- a symlink
	// to the already-built fake tool binary is enough to prove ${cwd} resolved to a real,
	// invokable location, without building a second binary.
	require.NoError(t, os.Symlink(binPath, filepath.Join(cwdDir, "faketool")))

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
						SourceRoot: pluginRoot, SourceDir: sourceDir,
						Commands: []config.Command{{
							Name: "lint", Run: "faketool rawtext ${target}", Output: "sarif", Batch: true,
							Parser: &config.Parser{Runtime: "python", Run: "${cwd}/faketool sarifconvert"},
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

// TestRun_ParserExitCodeTemplateVarSubstitutes proves ${exit_code} substitutes into
// Command.Parser.Run as the real command's own exit code -- real prettier's own parser.run is
// `python3 ${plugin}/linters/prettier/prettier_to_sarif.py ${exit_code}`, passing prettier's own
// exit status (0 clean, 2 reformatted, both non-error per prettier's own success_codes: [0, 2])
// so the converter script can tell them apart. Before this test existed, ${exit_code} wasn't in
// findUnsupportedParserVar's allowlist at all, so any command using it (prettier included) was
// unconditionally Skipped as "unsupported template var".
func TestRun_ParserExitCodeTemplateVarSubstitutes(t *testing.T) {
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
					"fakeprettier": {
						Name: "fakeprettier", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{{
							Name: "format", Run: "faketool rawtextexit2 ${target}", Output: "sarif", Batch: true,
							Parser: &config.Parser{Runtime: "python", Run: "faketool sarifconvertexitcode ${exit_code}"},
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
		if ev.Linter == "fakeprettier" && ev.Phase == Done {
			got = ev
		}
	}
	require.Len(t, got.Findings, 1)
	assert.Equal(t, "exit-code-2", got.Findings[0].RuleID,
		"${exit_code} must substitute the real command's own exit code (2), not be left unsubstituted or blank")
	assert.Equal(t, "exit was 2", got.Findings[0].Message)
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

	pluginRoot := filepath.Join(t.TempDir(), "plugin source")

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

// TestRun_ParserNotInvokedOnEmptyOutput proves the parser stage is skipped entirely when the real
// command's own output is empty (a clean run) -- if it ran anyway, a converter script that can't
// handle empty stdin (very real: json.load(sys.stdin) raises on empty input) would turn a clean
// run into a spurious failure.
func TestRun_ParserNotInvokedOnEmptyOutput(t *testing.T) {
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
					"fakeemptyparsed": {
						Name: "fakeemptyparsed", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{{
							Name: "lint", Run: "faketool emptyjson ${target}", Output: "sarif", Batch: true,
							Parser: &config.Parser{Runtime: "python", Run: "faketool failonemptystdin"},
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
		if ev.Linter == "fakeemptyparsed" {
			got = ev
		}
	}
	assert.Equal(t, Done, got.Phase, "the parser must never run on empty output, or a script that rejects empty stdin would spuriously fail a clean run")
	assert.Empty(t, got.Findings)
}

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

// TestRun_PrepareRunExecutesOnceBeforeFirstUse covers Command.PrepareRun: a setup invocation
// declared on a command must run exactly once per (linter,command), before that command's own
// Run, no matter how many files/jobs/batches the command's own Run ends up split into. Both
// PrepareRun and Run are plain shell commands (no faketool binary needed) that append a marker
// line to the same file, so this needs no Tools/shim machinery at all -- mirrors
// TestRunOneInvocation_EmptyPathEnvHasNoCwdComponent's use of a Tools-less config.Command with a
// literal shell Run string.
func TestRun_PrepareRunExecutesOnceBeforeFirstUse(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh -c only on POSIX")
	}

	repoRoot := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(repoRoot, name), []byte("x\n"), 0o644))
	}
	marker := filepath.Join(t.TempDir(), "marker")

	cfg := config.Config{
		Lint: config.LintConfig{
			Files: map[string]config.FileType{},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"prep": {
						Name: "prep", Files: []string{"ALL"},
						Commands: []config.Command{{
							// Run references ${target} so buildJobs splits it into one job per
							// file (a Run string with no ${target} at all auto-collapses into a
							// single job regardless of the Batch flag -- see
							// TestRun_NoTargetCommandBatchesEvenWithoutBatchFlag -- which would
							// defeat this test's whole point of proving PrepareRun runs once
							// across MULTIPLE jobs/batches).
							Name: "check", Output: "pass_fail",
							PrepareRun: "echo prep >> " + marker,
							Run:        "true ${target}; echo run >> " + marker,
						}},
					},
				},
			},
		},
	}

	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: t.TempDir(), Concurrency: 3}, nil, notFormatter)
	require.NoError(t, err)
	for range events { //nolint:revive // draining the channel is the whole point; there is nothing to do per event
	}

	data, err := os.ReadFile(marker)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")

	var prepCount, runCount int
	for _, l := range lines {
		switch l {
		case "prep":
			prepCount++
		case "run":
			runCount++
		}
	}
	assert.Equal(t, 1, prepCount, "PrepareRun must execute exactly once, not once per job/batch, got lines: %v", lines)
	assert.Equal(t, 3, runCount, "the command's own Run must still execute once per file, unaffected by PrepareRun")
}

// TestRun_PrepareRunFailurePropagatesToAllConcurrentSiblingJobs covers the failure side of
// Command.PrepareRun that TestRun_PrepareRunExecutesOnceBeforeFirstUse doesn't: when the setup
// invocation exits non-zero while MULTIPLE jobs for the same (linter,command) are genuinely
// blocked on the same sync.Once.Do call (not just the one that happens to trigger it), every one
// of them must see the failure and skip its own real Run -- not just the triggering job. The
// mechanism (prepareRunState{once, err} in engine.go) relies on sync.Once.Do's happens-before
// guarantee to hand the triggering call's state.err to every blocked caller; if a sibling job ever
// raced past Do without observing state.err, it would slip through and actually invoke the real
// command below, which would show up as a line in runMarker.
//
// Same shape as TestRun_PrepareRunExecutesOnceBeforeFirstUse (3 files, a Run string containing
// ${target} so buildJobs splits into one job per file, Concurrency: 3 so all 3 are queued to
// workers at once) plus a deliberate short sleep in PrepareRun itself: the sleep buys the other 2
// workers time to reach their own state.once.Do call (a channel receive + mutex check, orders of
// magnitude faster than spawning a subprocess) before the first one's prepare_run returns, so all
// 3 are reliably blocked on the same Do call rather than racing to see whether 2 of them happen to
// start late enough to already observe state.failed.
func TestRun_PrepareRunFailurePropagatesToAllConcurrentSiblingJobs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh -c only on POSIX")
	}

	repoRoot := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(repoRoot, name), []byte("x\n"), 0o644))
	}
	prepMarker := filepath.Join(t.TempDir(), "prep-marker")
	runMarker := filepath.Join(t.TempDir(), "run-marker")

	cfg := config.Config{
		Lint: config.LintConfig{
			Files: map[string]config.FileType{},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"prepfail": {
						Name: "prepfail", Files: []string{"ALL"},
						Commands: []config.Command{{
							Name: "check", Output: "pass_fail",
							PrepareRun: "sleep 0.2; echo prep >> " + prepMarker + "; exit 7",
							Run:        "echo run >> " + runMarker + "; true ${target}",
						}},
					},
				},
			},
		},
	}

	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: t.TempDir(), Concurrency: 3}, nil, notFormatter)
	require.NoError(t, err)

	var jobDoneCount, doneCount int
	var failedEvents []Event
	for ev := range events {
		switch ev.Phase {
		case JobDone:
			jobDoneCount++
		case Done:
			doneCount++
		case Failed:
			failedEvents = append(failedEvents, ev)
		}
	}

	assert.Equal(t, 3, jobDoneCount,
		"all 3 jobs must run to completion (released by Do, not stuck or skipped) for this to be a real test of concurrent blocking")
	assert.Zero(t, doneCount, "a linter with a failed job must never also report Done")
	require.Len(t, failedEvents, 1, "one linter reports its terminal event exactly once, no matter how many of its jobs failed")

	got := failedEvents[0]
	assert.Equal(t, "check", got.Note)
	require.Error(t, got.Err)
	assert.Contains(t, got.Err.Error(), "prepare_run exited 7",
		"the failure must be attributed to prepare_run, not the command's own Run")
	assert.ElementsMatch(t, []string{"a.txt", "b.txt", "c.txt"}, got.Files)

	prepData, err := os.ReadFile(prepMarker)
	require.NoError(t, err)
	prepLines := strings.Split(strings.TrimSpace(string(prepData)), "\n")
	assert.Equal(t, []string{"prep"}, prepLines,
		"prepare_run must still execute exactly once, even though it fails and 3 jobs are blocked on it")

	_, statErr := os.Stat(runMarker)
	assert.True(t, os.IsNotExist(statErr),
		"the real command's Run must never execute for ANY of the 3 blocked jobs when their shared prepare_run failed, not just skip the triggering one")
}

// TestRun_NoPrepareRun_Unaffected mirrors TestRun_TargetParentRunsOncePerDirectory (no
// Command.PrepareRun set) to prove the new field is a true no-op for every command that doesn't
// declare one -- the overwhelming majority of the real catalog.
func TestRun_NoPrepareRun_Unaffected(t *testing.T) {
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
	for _, f := range []string{"pa/one.tp", "pa/two.tp", "pb/three.tp"} {
		require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, filepath.Dir(f)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(repoRoot, f), []byte("x\n"), 0o644))
	}

	cfg := config.Config{
		Tools: map[string]config.Tool{"faketool": {Name: "faketool", KnownGoodVersion: "1.0.0"}},
		Lint: config.LintConfig{
			Files: map[string]config.FileType{"tp": {Name: "tp", Extensions: []string{"tp"}}},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"parent-linter": {
						Name: "parent-linter", Files: []string{"tp"}, Tools: []string{"faketool"},
						Commands: []config.Command{{
							Name: "lint", Run: "faketool sarif ${target}", Output: "sarif", Target: "${parent}",
						}},
					},
				},
			},
		},
	}

	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: cacheDir, Concurrency: 1}, nil, notFormatter)
	require.NoError(t, err)

	var running int
	var done Event
	for ev := range events {
		switch ev.Phase {
		case Running:
			running++
		case Done:
			done = ev
		}
	}
	assert.Equal(t, 2, running, "pa/ holds two files but must be linted once, pb/ once")
	require.Len(t, done.Findings, 2)
	got := []string{done.Findings[0].File, done.Findings[1].File}
	assert.ElementsMatch(t, []string{"pa", "pb"}, got)
}

// TestRun_ToolHealthCheck_FailsResolutionOnNonZeroExit covers Tool.HealthChecks's whole point: a
// broken install must fail resolution for every linter referencing that tool, before the linter's
// own command ever runs -- not just surface later when the command itself happens to fail. The
// health check here ("exit 1") never invokes faketool at all; only its non-zero exit matters.
func TestRun_ToolHealthCheck_FailsResolutionOnNonZeroExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh -c only on POSIX")
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
	runMarker := filepath.Join(t.TempDir(), "run-marker")

	cfg := config.Config{
		Tools: map[string]config.Tool{"faketool": {
			Name: "faketool", KnownGoodVersion: "1.0.0", HealthChecks: []config.HealthCheck{{Command: "exit 1"}},
		}},
		Lint: config.LintConfig{
			Files: map[string]config.FileType{},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"healthfail": {
						Name: "healthfail", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{{
							Name: "lint", Output: "pass_fail",
							Run: "echo run >> " + runMarker + "; true ${target}",
						}},
					},
				},
			},
		},
	}

	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: cacheDir, Concurrency: 1}, nil, notFormatter)
	require.NoError(t, err)

	var failedEvents []Event
	for ev := range events {
		if ev.Phase == Failed {
			failedEvents = append(failedEvents, ev)
		}
	}
	require.Len(t, failedEvents, 1, "the linter must report exactly one Failed event")
	require.Error(t, failedEvents[0].Err)
	assert.Contains(t, failedEvents[0].Err.Error(), "health check failed")

	_, statErr := os.Stat(runMarker)
	assert.True(t, os.IsNotExist(statErr),
		"the linter's own command must never run when its tool's health check fails")
}

// TestRun_ToolHealthCheck_RunsOncePerRunAcrossMultipleLinters covers the caching side: two
// unrelated linters that happen to reference the same Tool must trigger its HealthChecks exactly
// once for the whole Run call, not once per linter that references it.
func TestRun_ToolHealthCheck_RunsOncePerRunAcrossMultipleLinters(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh -c only on POSIX")
	}

	binPath := buildFakeToolBinary(t)
	cacheDir := t.TempDir()
	root, err := download.Root(cacheDir)
	require.NoError(t, err)
	shimPath := download.ShimPath(root, "tools", "sharedtool", "1.0.0", "sharedtool")
	require.NoError(t, os.MkdirAll(filepath.Dir(shimPath), 0o755))
	require.NoError(t, download.WriteShim(shimPath, binPath))

	repoRoot := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(repoRoot, name), []byte("x\n"), 0o644))
	}
	marker := filepath.Join(t.TempDir(), "health-marker")

	cfg := config.Config{
		Tools: map[string]config.Tool{"sharedtool": {
			Name: "sharedtool", KnownGoodVersion: "1.0.0",
			HealthChecks: []config.HealthCheck{{Command: "echo check >> " + marker}},
		}},
		Lint: config.LintConfig{
			Files: map[string]config.FileType{},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"linterA": {
						Name: "linterA", Files: []string{"ALL"}, Tools: []string{"sharedtool"},
						Commands: []config.Command{{Name: "lint", Output: "pass_fail", Run: "true ${target}"}},
					},
					"linterB": {
						Name: "linterB", Files: []string{"ALL"}, Tools: []string{"sharedtool"},
						Commands: []config.Command{{Name: "lint", Output: "pass_fail", Run: "true ${target}"}},
					},
				},
			},
		},
	}

	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: cacheDir, Concurrency: 2}, nil, notFormatter)
	require.NoError(t, err)

	var doneCount int
	for ev := range events {
		if ev.Phase == Done {
			doneCount++
		}
	}
	assert.Equal(t, 2, doneCount, "both linters must complete successfully for this to be a real test of the caching, not the failure path")

	data, err := os.ReadFile(marker)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	assert.Equal(t, []string{"check"}, lines,
		"the shared tool's health check must run exactly once across both linters, got lines: %v", lines)
}

// TestRun_ToolNoHealthChecks_Unaffected mirrors TestRun_NoPrepareRun_Unaffected: same fixture as
// TestRun_NoPrepareRun_Unaffected, whose Tool declares no HealthChecks, proving the new field is a
// true no-op for every tool that doesn't declare one -- the overwhelming majority of the real
// catalog.
func TestRun_ToolNoHealthChecks_Unaffected(t *testing.T) {
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
	for _, f := range []string{"pa/one.tp", "pa/two.tp", "pb/three.tp"} {
		require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, filepath.Dir(f)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(repoRoot, f), []byte("x\n"), 0o644))
	}

	cfg := config.Config{
		Tools: map[string]config.Tool{"faketool": {Name: "faketool", KnownGoodVersion: "1.0.0"}},
		Lint: config.LintConfig{
			Files: map[string]config.FileType{"tp": {Name: "tp", Extensions: []string{"tp"}}},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"parent-linter": {
						Name: "parent-linter", Files: []string{"tp"}, Tools: []string{"faketool"},
						Commands: []config.Command{{
							Name: "lint", Run: "faketool sarif ${target}", Output: "sarif", Target: "${parent}",
						}},
					},
				},
			},
		},
	}

	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: cacheDir, Concurrency: 1}, nil, notFormatter)
	require.NoError(t, err)

	var running int
	var done Event
	for ev := range events {
		switch ev.Phase {
		case Running:
			running++
		case Done:
			done = ev
		}
	}
	assert.Equal(t, 2, running, "pa/ holds two files but must be linted once, pb/ once")
	require.Len(t, done.Findings, 2)
	got := []string{done.Findings[0].File, done.Findings[1].File}
	assert.ElementsMatch(t, []string{"pa", "pb"}, got)
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

	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: t.TempDir(), Concurrency: 1}, nil, func(_ config.Command) bool { return true })
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

	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: t.TempDir(), Concurrency: 1}, nil, func(_ config.Command) bool { return true })
	require.NoError(t, err)

	var got Event
	for ev := range events {
		got = ev
	}
	assert.Equal(t, Skipped, got.Phase)
	assert.Equal(t, "disabled by its own plugin source", got.Note)
}

func TestRun_VersionGatedCommand_OnlyMatchingVariantRuns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("faketool invoked via sh -c")
	}
	binPath := buildFakeToolBinary(t)
	cacheDir := t.TempDir()
	root, err := download.Root(cacheDir)
	require.NoError(t, err)
	shimPath := download.ShimPath(root, "tools", "faketool", "9.0.0", "faketool")
	require.NoError(t, os.MkdirAll(filepath.Dir(shimPath), 0o755))
	require.NoError(t, download.WriteShim(shimPath, binPath))

	cfg := config.Config{
		Tools: map[string]config.Tool{"faketool": {Name: "faketool", KnownGoodVersion: "9.0.0"}},
		Lint: config.LintConfig{
			Files: map[string]config.FileType{},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"fakegated": {
						Name: "fakegated", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{
							{Name: "lint", Version: ">=9.0.0", Run: "faketool sarifone", Output: "sarif"},
							{Name: "lint", Version: "<9.0.0", Run: "faketool crash", Output: "sarif"},
						},
					},
				},
			},
		},
	}
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "a.txt"), []byte("x"), 0o644))

	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: cacheDir, Concurrency: 1}, nil, notFormatter)
	require.NoError(t, err)

	var got []Event
	for ev := range events {
		got = append(got, ev)
	}
	for _, ev := range got {
		require.NotEqual(t, Skipped, ev.Phase, "an out-of-range variant must be silently absent, not skipped: %+v", ev)
	}
	require.NotEmpty(t, got)
	assert.Equal(t, Done, got[len(got)-1].Phase, "the in-range variant must have actually run")
}

func TestRun_PlatformRestrictedCommand_UnrestrictedSiblingStillRuns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("faketool invoked via sh -c")
	}
	host, ok := download.HostOSName()
	require.True(t, ok, "test host must map to a known trunk os name")
	other := "windows"
	if host == "windows" {
		other = "linux"
	}
	binPath := buildFakeToolBinary(t)
	t.Setenv("PATH", filepath.Dir(binPath)+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg := config.Config{
		Lint: config.LintConfig{
			Files: map[string]config.FileType{},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"fakeplatform": {
						Name: "fakeplatform", Files: []string{"ALL"},
						Commands: []config.Command{
							{Name: "lint", Platforms: []string{other}, Run: "faketool crash", Output: "sarif"},
							{Name: "lint", Run: "faketool sarifone", Output: "sarif"},
						},
					},
				},
			},
		},
	}
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "a.txt"), []byte("x"), 0o644))

	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: t.TempDir(), Concurrency: 1}, nil, notFormatter)
	require.NoError(t, err)

	var got []Event
	for ev := range events {
		got = append(got, ev)
	}
	for _, ev := range got {
		require.NotEqual(t, Skipped, ev.Phase, "a platform mismatch must be silently absent, not skipped: %+v", ev)
	}
	require.NotEmpty(t, got)
	assert.Equal(t, Done, got[len(got)-1].Phase, "the matching-platform sibling must have actually run")
}

func TestRun_CommandIsSecurity_TagsFindings(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("faketool invoked via sh -c")
	}
	binPath := buildFakeToolBinary(t)
	t.Setenv("PATH", filepath.Dir(binPath)+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg := config.Config{
		Lint: config.LintConfig{
			Files: map[string]config.FileType{},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"fakesecurity": {
						Name: "fakesecurity", Files: []string{"ALL"},
						Commands: []config.Command{{Name: "lint", Run: "faketool sarifone", Output: "sarif", IsSecurity: true}},
					},
				},
			},
		},
	}
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "a.txt"), []byte("x"), 0o644))

	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: t.TempDir(), Concurrency: 1}, nil, notFormatter)
	require.NoError(t, err)

	var got Event
	for ev := range events {
		if ev.Phase == Done {
			got = ev
		}
	}
	require.NotEmpty(t, got.Findings)
	for _, f := range got.Findings {
		assert.True(t, f.IsSecurity, "every finding from an IsSecurity command must be tagged")
	}
}

func TestRun_CommandNotIsSecurity_FindingsUntagged(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("faketool invoked via sh -c")
	}
	binPath := buildFakeToolBinary(t)
	t.Setenv("PATH", filepath.Dir(binPath)+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg := config.Config{
		Lint: config.LintConfig{
			Files: map[string]config.FileType{},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"fakenotsecurity": {
						Name: "fakenotsecurity", Files: []string{"ALL"},
						Commands: []config.Command{{Name: "lint", Run: "faketool sarifone", Output: "sarif"}},
					},
				},
			},
		},
	}
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "a.txt"), []byte("x"), 0o644))

	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: t.TempDir(), Concurrency: 1}, nil, notFormatter)
	require.NoError(t, err)

	var got Event
	for ev := range events {
		if ev.Phase == Done {
			got = ev
		}
	}
	require.NotEmpty(t, got.Findings)
	for _, f := range got.Findings {
		assert.False(t, f.IsSecurity)
	}
}

func TestSelectApplicableCommands_NoRestrictions_KeepsEverything(t *testing.T) {
	linter := config.Linter{Commands: []config.Command{{Name: "lint"}, {Name: "fmt"}}}
	got := selectApplicableCommands(config.Config{}, linter)
	assert.Equal(t, linter.Commands, got)
}

func TestSelectApplicableCommands_TwoToolsNoMainTool_KeepsVersionGatedVariant(t *testing.T) {
	linter := config.Linter{
		Tools:    []string{"a", "b"},
		Commands: []config.Command{{Name: "lint", Version: ">=1.0.0"}},
	}
	got := selectApplicableCommands(config.Config{}, linter)
	assert.Equal(t, linter.Commands, got, "no single tool version to gate on -- nothing guessed away")
}

// TestSelectApplicableCommands_OpenEndedRanges_FirstMatchWins reproduces the real catalog's ruff
// shape: several open-ended ">=" ranges plus an unversioned fallback, ordered newest-first, where
// more than one range admits the pinned version (unlike eslint's mutually-exclusive
// ">=9.0.0"/"<=8.57.0" pair) -- only the first one declared for a given tool version may ever be
// selected, matching MatchEntry's own first-match-per-entry semantics for Download variants.
func TestSelectApplicableCommands_OpenEndedRanges_FirstMatchWins(t *testing.T) {
	cfg := config.Config{Tools: map[string]config.Tool{"ruff": {KnownGoodVersion: "0.14.3"}}}
	linter := config.Linter{
		Tools: []string{"ruff"},
		Commands: []config.Command{
			{Name: "lint", Version: ">=0.6.0", Run: "newest"},
			{Name: "lint", Version: ">=0.1.0", Run: "middle"},
			{Name: "lint", Version: ">=0.0.266", Run: "oldest"},
			{Name: "lint", Run: "fallback"},
		},
	}
	got := selectApplicableCommands(cfg, linter)
	require.Len(t, got, 1, "exactly one lint variant must run, not every range that happens to admit 0.14.3")
	assert.Equal(t, "newest", got[0].Run, "the first declared variant whose range admits the pinned version wins")
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

// TestRun_FormatterWithoutInPlaceHasNoChangedFiles proves ChangedFiles is gated on
// Command.InPlace specifically, not Formatter, for a command shape the stdin/stdout formatter
// path doesn't claim: a Formatter: true, InPlace: false command whose Output is "sarif" (not
// "rewrite"/"shfmt") is a plain checking command as far as ChangedFiles goes -- it must report
// none, via the same hashFiles-is-InPlace-gated logic runStdinFormatter's own rewrite/shfmt path
// doesn't touch.
func TestRun_FormatterWithoutInPlaceHasNoChangedFiles(t *testing.T) {
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
	messy := filepath.Join(repoRoot, "messy.txt")
	require.NoError(t, os.WriteFile(messy, []byte("messy\n"), 0o644))

	cfg := config.Config{
		Tools: map[string]config.Tool{
			"faketool": {Name: "faketool", KnownGoodVersion: "1.0.0"},
		},
		Lint: config.LintConfig{
			Files: map[string]config.FileType{},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"fakefmtnoinplace": {
						Name: "fakefmtnoinplace", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{{
							Name: "format", Run: "faketool sarif ${target}", Output: "sarif",
							Batch: true, InPlace: false, Formatter: true,
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
		if ev.Linter == "fakefmtnoinplace" && ev.Phase == Done {
			got = ev
		}
	}
	assert.Nil(t, got.ChangedFiles, "InPlace: false must produce no ChangedFiles, regardless of Formatter")
}

// TestRun_ChangedFilesDeduplicatedWithinOneLinter proves a linter with two InPlace commands both
// touching the same file in the same batch reports it once, not twice, in its own single Done
// event -- real and reachable, not theoretical: this repo's own .trunk/trunk.yaml enables both
// prettier and markdownlint, both realistic InPlace candidates over the same .md files (a
// cross-linter case Task 2 handles separately; this is the same-linter case).
func TestRun_ChangedFilesDeduplicatedWithinOneLinter(t *testing.T) {
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
	target := filepath.Join(repoRoot, "a.txt")
	require.NoError(t, os.WriteFile(target, []byte("messy\n"), 0o644))

	cfg := config.Config{
		Tools: map[string]config.Tool{
			"faketool": {Name: "faketool", KnownGoodVersion: "1.0.0"},
		},
		Lint: config.LintConfig{
			Files: map[string]config.FileType{},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"fakedoublefmt": {
						Name: "fakedoublefmt", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{
							{
								Name: "format1", Run: "faketool rewrite ${target}", Output: "rewrite",
								SuccessCodes: []int{0}, Batch: true, InPlace: true, Formatter: true,
							},
							{
								Name: "format2", Run: "faketool rewrite2 ${target}", Output: "rewrite",
								SuccessCodes: []int{0}, Batch: true, InPlace: true, Formatter: true,
							},
						},
					},
				},
			},
		},
	}

	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: cacheDir, Concurrency: 1}, nil, func(c config.Command) bool { return c.Formatter })
	require.NoError(t, err)

	var got Event
	for ev := range events {
		if ev.Linter == "fakedoublefmt" && ev.Phase == Done {
			got = ev
		}
	}
	assert.Equal(t, []string{"a.txt"}, got.ChangedFiles,
		"two commands both changing the same file must report it once, not twice")
}

// TestRun_ConcurrentInPlaceCommandsAreSerialized proves two InPlace commands (here, two
// different linters) touching the same file never interleave their invocation: real
// reproduction (final whole-branch review) found 5/25 runs lost one formatter's write entirely
// when two InPlace commands raced the same file under Concurrency > 1. A probe log records
// "enter"/"exit" pairs around each invocation's own sleep-then-write; the two pairs must never
// interleave (enter,enter,exit,exit), only nest cleanly (enter,exit,enter,exit).
func TestRun_ConcurrentInPlaceCommandsAreSerialized(t *testing.T) {
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
	target := filepath.Join(repoRoot, "a.txt")
	require.NoError(t, os.WriteFile(target, []byte("x\n"), 0o644))
	probe := filepath.Join(t.TempDir(), "probe.log")

	cfg := config.Config{
		Tools: map[string]config.Tool{
			"faketool": {Name: "faketool", KnownGoodVersion: "1.0.0"},
		},
		Lint: config.LintConfig{
			Files: map[string]config.FileType{},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"fakeraceA": {
						Name: "fakeraceA", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{{
							Name: "format", Run: "faketool sleepwrite " + probe + " ${target}", Output: "rewrite",
							SuccessCodes: []int{0}, Batch: true, InPlace: true, Formatter: true,
						}},
					},
					"fakeraceB": {
						Name: "fakeraceB", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{{
							Name: "format", Run: "faketool sleepwrite " + probe + " ${target}", Output: "rewrite",
							SuccessCodes: []int{0}, Batch: true, InPlace: true, Formatter: true,
						}},
					},
				},
			},
		},
	}

	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: cacheDir, Concurrency: 2}, nil, func(c config.Command) bool { return c.Formatter })
	require.NoError(t, err)
	for range events { //nolint:revive // draining the channel is the whole point; there is nothing to do per event
	}

	data, err := os.ReadFile(probe)
	require.NoError(t, err)
	lines := strings.Fields(strings.TrimSpace(string(data)))
	require.Len(t, lines, 4, "two invocations, each logging enter+exit")

	depth := 0
	for _, l := range lines {
		switch l {
		case "enter":
			depth++
			require.LessOrEqual(t, depth, 1, "a second invocation entered before the first exited -- not serialized")
		case "exit":
			depth--
		}
	}
}

// TestRun_StdinFormatterAndInPlaceFormatter_SerializedOnSameFile proves a stdin/stdout formatter
// and a concurrent InPlace formatter, both enabled and both targeting the same file, never lose
// either one's write: without inPlaceMu held across the stdin formatter's own read-invoke-write
// span, the slower one can write back content it computed from a since-superseded read, silently
// discarding the other's change (the same lost-update inPlaceMu already prevents between two
// InPlace commands). "orig" -> InPlace appends "B" -> stdin-formatter uppercases whatever it
// actually read -- regardless of which one the mutex lets run first, full serialization means the
// second one always sees the first one's result, so the only possible final content is "ORIGB".
func TestRun_StdinFormatterAndInPlaceFormatter_SerializedOnSameFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("faketool invoked via sh -c")
	}
	binPath := buildFakeToolBinary(t)
	repoRoot := t.TempDir()
	target := filepath.Join(repoRoot, "a.txt")
	require.NoError(t, os.WriteFile(target, []byte("orig"), 0o644))
	t.Setenv("PATH", filepath.Dir(binPath)+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg := config.Config{
		Lint: config.LintConfig{
			Files: map[string]config.FileType{},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"inplacer": {
						Name: "inplacer", Files: []string{"ALL"},
						Commands: []config.Command{{
							Name: "format", Run: "sh -c 'sleep 0.1; printf B >> ${target}'",
							Output: "rewrite", SuccessCodes: []int{0}, InPlace: true, Formatter: true,
						}},
					},
					"stdiner": {
						Name: "stdiner", Files: []string{"ALL"},
						Commands: []config.Command{{
							Name: "format", Run: "faketool slowstdinfmt", Output: "rewrite",
							Formatter: true, InPlace: false,
						}},
					},
				},
			},
		},
	}

	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: t.TempDir(), Concurrency: 2}, nil, func(c config.Command) bool { return c.Formatter })
	require.NoError(t, err)
	for range events { //nolint:revive // draining the channel is the whole point; there is nothing to do per event
	}

	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "ORIGB", string(data), "full serialization means whichever ran second always saw the first one's write")
}

// TestRun_RewriteCommandFailingSuccessCodesReportsFailed proves a rewrite/shfmt command that
// exits outside its own SuccessCodes is reported Failed, not silently treated as a clean success
// -- real catalog formatters specify SuccessCodes (not ErrorCodes), and before this fix,
// rewrite/shfmt had no fallback failure signal at all (unlike pass_fail/sarif/etc., which have
// their own separate way of surfacing a non-zero exit).
func TestRun_RewriteCommandFailingSuccessCodesReportsFailed(t *testing.T) {
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
	target := filepath.Join(repoRoot, "a.txt")
	require.NoError(t, os.WriteFile(target, []byte("x\n"), 0o644))

	cfg := config.Config{
		Tools: map[string]config.Tool{
			"faketool": {Name: "faketool", KnownGoodVersion: "1.0.0"},
		},
		Lint: config.LintConfig{
			Files: map[string]config.FileType{},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"fakebadfmt": {
						Name: "fakebadfmt", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{{
							Name: "format", Run: "faketool crashstderr", Output: "rewrite",
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
		if ev.Linter == "fakebadfmt" {
			got = ev
		}
	}
	assert.Equal(t, Failed, got.Phase, "an exit code outside SuccessCodes must be reported Failed for rewrite/shfmt, not silently succeed")
	assert.ErrorContains(t, got.Err, "boom: disk on fire", "stderr must be surfaced in the error")
}

// TestRun_StdinStdoutFormatter_RewritesFile covers real catalog examples (terraform fmt, tofu
// fmt, stylua, opa, perltidy, sql-formatter, pragma-once, markdown-table-prettify): Formatter:
// true, Output: rewrite/shfmt, but no InPlace -- the tool reads a file's content from stdin and
// writes the reformatted result to its own stdout, rather than rewriting the file directly.
func TestRun_StdinStdoutFormatter_RewritesFile(t *testing.T) {
	binPath := buildFakeToolBinary(t)
	cfg := config.Config{
		Lint: config.LintConfig{
			Files: map[string]config.FileType{},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"fakestdoutfmt": {
						Name: "fakestdoutfmt", Files: []string{"ALL"},
						Commands: []config.Command{{
							Name: "format", Run: "faketool stdinfmt", Output: "rewrite",
							Formatter: true, InPlace: false,
						}},
					},
				},
			},
		},
	}
	repoRoot := t.TempDir()
	target := filepath.Join(repoRoot, "a.txt")
	require.NoError(t, os.WriteFile(target, []byte("messy"), 0o644))
	t.Setenv("PATH", filepath.Dir(binPath)+string(os.PathListSeparator)+os.Getenv("PATH"))

	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: t.TempDir(), Concurrency: 1}, nil, func(c config.Command) bool { return c.Formatter })
	require.NoError(t, err)

	var got Event
	for ev := range events {
		if ev.Phase == Done {
			got = ev
		}
	}
	assert.Equal(t, Done, got.Phase)
	assert.Equal(t, []string{"a.txt"}, got.ChangedFiles)
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "MESSY", string(data))
}

// TestRun_StdinStdoutFormatter_AlreadyFormattedIsNotChanged: the tool's own stdout, when it
// matches the file's current content byte for byte, means nothing to write and nothing changed --
// same "only report a real change" contract InPlace formatters already have via hashFiles.
func TestRun_StdinStdoutFormatter_AlreadyFormattedIsNotChanged(t *testing.T) {
	binPath := buildFakeToolBinary(t)
	cfg := config.Config{
		Lint: config.LintConfig{
			Files: map[string]config.FileType{},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"fakestdoutfmt": {
						Name: "fakestdoutfmt", Files: []string{"ALL"},
						Commands: []config.Command{{
							Name: "format", Run: "faketool stdinfmt", Output: "rewrite",
							Formatter: true, InPlace: false,
						}},
					},
				},
			},
		},
	}
	repoRoot := t.TempDir()
	target := filepath.Join(repoRoot, "a.txt")
	require.NoError(t, os.WriteFile(target, []byte("ALREADY-UPPER"), 0o644))
	t.Setenv("PATH", filepath.Dir(binPath)+string(os.PathListSeparator)+os.Getenv("PATH"))

	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: t.TempDir(), Concurrency: 1}, nil, func(c config.Command) bool { return c.Formatter })
	require.NoError(t, err)

	var got Event
	for ev := range events {
		if ev.Phase == Done {
			got = ev
		}
	}
	assert.Equal(t, Done, got.Phase)
	assert.Empty(t, got.ChangedFiles)
}

// TestRun_StdinStdoutFormatter_DryRunReportsWithoutWriting: dry-run (check --fix/fmt --check's own
// probe mode) must still detect the change, but never touch the real file.
func TestRun_StdinStdoutFormatter_DryRunReportsWithoutWriting(t *testing.T) {
	binPath := buildFakeToolBinary(t)
	cfg := config.Config{
		Lint: config.LintConfig{
			Files: map[string]config.FileType{},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"fakestdoutfmt": {
						Name: "fakestdoutfmt", Files: []string{"ALL"},
						Commands: []config.Command{{
							Name: "format", Run: "faketool stdinfmt", Output: "rewrite",
							Formatter: true, InPlace: false,
						}},
					},
				},
			},
		},
	}
	repoRoot := t.TempDir()
	target := filepath.Join(repoRoot, "a.txt")
	require.NoError(t, os.WriteFile(target, []byte("messy"), 0o644))
	t.Setenv("PATH", filepath.Dir(binPath)+string(os.PathListSeparator)+os.Getenv("PATH"))

	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: t.TempDir(), Concurrency: 1, DryRun: true}, nil, func(c config.Command) bool { return c.Formatter })
	require.NoError(t, err)

	var got Event
	for ev := range events {
		if ev.Phase == Done {
			got = ev
		}
	}
	assert.Equal(t, Done, got.Phase)
	assert.Equal(t, []string{"a.txt"}, got.ChangedFiles, "dry-run must still report the file as would-change")
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "messy", string(data), "dry-run must never write the real file")
}

// TestRun_StdinStdoutFormatter_SuccessCodesMismatchIsFailed: mirrors the single-invocation path's
// own SuccessCodes check for rewrite/shfmt output (real catalog formatters declare SuccessCodes,
// not ErrorCodes).
func TestRun_StdinStdoutFormatter_SuccessCodesMismatchIsFailed(t *testing.T) {
	binPath := buildFakeToolBinary(t)
	cfg := config.Config{
		Lint: config.LintConfig{
			Files: map[string]config.FileType{},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"fakestdoutfmt": {
						Name: "fakestdoutfmt", Files: []string{"ALL"},
						Commands: []config.Command{{
							Name: "format", Run: "faketool crash", Output: "rewrite",
							Formatter: true, InPlace: false, SuccessCodes: []int{0},
						}},
					},
				},
			},
		},
	}
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "a.txt"), []byte("x"), 0o644))
	t.Setenv("PATH", filepath.Dir(binPath)+string(os.PathListSeparator)+os.Getenv("PATH"))

	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: t.TempDir(), Concurrency: 1}, nil, func(c config.Command) bool { return c.Formatter })
	require.NoError(t, err)

	var got Event
	for ev := range events {
		if ev.Phase == Failed {
			got = ev
		}
	}
	assert.Equal(t, Failed, got.Phase)
}

// TestRun_StdinStdoutFormatterWithSandboxTypeIsSkipped: a stdin/stdout formatter never touches the
// real file directly -- writing its own stdout into a sandbox copy would rewrite a file nobody
// ever reads, silently discarding the result, so this combination is refused up front instead of
// running and doing nothing (mirrors the pre-existing InPlace+SandboxType skip).
func TestRun_StdinStdoutFormatterWithSandboxTypeIsSkipped(t *testing.T) {
	cfg := config.Config{
		Lint: config.LintConfig{
			Files: map[string]config.FileType{},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"fakestdoutfmt": {
						Name: "fakestdoutfmt", Files: []string{"ALL"},
						Commands: []config.Command{{
							Name: "format", Run: "faketool stdinfmt", Output: "rewrite",
							Formatter: true, InPlace: false, SandboxType: "copy_targets",
						}},
					},
				},
			},
		},
	}
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "a.txt"), []byte("x"), 0o644))

	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: t.TempDir(), Concurrency: 1}, nil, func(c config.Command) bool { return c.Formatter })
	require.NoError(t, err)

	var got Event
	for ev := range events {
		got = ev
	}
	assert.Equal(t, Skipped, got.Phase)
	assert.Equal(t, "stdin/stdout formatter combined with sandbox_type is not supported (the declared sandbox would be silently ignored)", got.Note)
}

// TestRun_StdinStdoutFormatter_EmptyOutputRefusesToWrite: a tool that exits 0 with no stdout at
// all almost never means "delete the whole file" -- far more likely it wrote its result somewhere
// else, or the invocation is simply misconfigured. Refusing to write (instead of silently emptying
// the file) is the only safe default.
func TestRun_StdinStdoutFormatter_EmptyOutputRefusesToWrite(t *testing.T) {
	binPath := buildFakeToolBinary(t)
	cfg := config.Config{
		Lint: config.LintConfig{
			Files: map[string]config.FileType{},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"fakestdoutfmt": {
						Name: "fakestdoutfmt", Files: []string{"ALL"},
						Commands: []config.Command{{
							Name: "format", Run: "faketool emptyout", Output: "rewrite",
							Formatter: true, InPlace: false,
						}},
					},
				},
			},
		},
	}
	repoRoot := t.TempDir()
	target := filepath.Join(repoRoot, "a.txt")
	require.NoError(t, os.WriteFile(target, []byte("precious"), 0o644))
	t.Setenv("PATH", filepath.Dir(binPath)+string(os.PathListSeparator)+os.Getenv("PATH"))

	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: t.TempDir(), Concurrency: 1}, nil, func(c config.Command) bool { return c.Formatter })
	require.NoError(t, err)

	var got Event
	for ev := range events {
		if ev.Phase == Failed {
			got = ev
		}
	}
	assert.Equal(t, Failed, got.Phase)
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "precious", string(data), "empty stdout for a non-empty file must never be written")
}

// TestRun_StdinStdoutFormatter_KilledProcessDoesNotWritePartialOutput: a process killed mid-run
// (ctx cancellation/timeout) may have already flushed a partial fragment of its real output --
// writing that fragment back to the file would silently corrupt it.
func TestRun_StdinStdoutFormatter_KilledProcessDoesNotWritePartialOutput(t *testing.T) {
	binPath := buildFakeToolBinary(t)
	cfg := config.Config{
		Lint: config.LintConfig{
			Files: map[string]config.FileType{},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"fakestdoutfmt": {
						Name: "fakestdoutfmt", Files: []string{"ALL"},
						Commands: []config.Command{{
							Name: "format", Run: "faketool partialthenkilled", Output: "rewrite",
							Formatter: true, InPlace: false,
						}},
					},
				},
			},
		},
	}
	repoRoot := t.TempDir()
	target := filepath.Join(repoRoot, "a.txt")
	require.NoError(t, os.WriteFile(target, []byte("precious"), 0o644))
	t.Setenv("PATH", filepath.Dir(binPath)+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	events, err := Run(ctx, Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: t.TempDir(), Concurrency: 1}, nil, func(c config.Command) bool { return c.Formatter })
	require.NoError(t, err)

	var got Event
	for ev := range events {
		if ev.Phase == Failed {
			got = ev
		}
	}
	assert.Equal(t, Failed, got.Phase)
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "precious", string(data), "a killed process's partial stdout must never be written")
}

// TestRun_StdinStdoutFormatter_MultiFileBatch_EachFileGetsOwnInvocation covers the real catalog
// shape (terraform fmt, tofu fmt, sql-formatter, markdown-table-prettify): a Run string with no
// ${target} placeholder groups every matched file in a directory into one job's batch (buildJobs'
// own "can't tell files apart" rule) -- runStdinFormatter must still invoke once per file, each
// getting its own stdin content, its own result, and its own runlog id.
func TestRun_StdinStdoutFormatter_MultiFileBatch_EachFileGetsOwnInvocation(t *testing.T) {
	binPath := buildFakeToolBinary(t)
	cfg := config.Config{
		Lint: config.LintConfig{
			Files: map[string]config.FileType{},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"fakestdoutfmt": {
						Name: "fakestdoutfmt", Files: []string{"ALL"},
						Commands: []config.Command{{
							// No ${target}: buildJobs groups every matched file into one batch.
							Name: "format", Run: "faketool stdinfmt", Output: "rewrite",
							Formatter: true, InPlace: false,
						}},
					},
				},
			},
		},
	}
	repoRoot := t.TempDir()
	a, b := filepath.Join(repoRoot, "a.txt"), filepath.Join(repoRoot, "b.txt")
	require.NoError(t, os.WriteFile(a, []byte("aaa"), 0o644))
	require.NoError(t, os.WriteFile(b, []byte("bbb"), 0o644))
	t.Setenv("PATH", filepath.Dir(binPath)+string(os.PathListSeparator)+os.Getenv("PATH"))

	cacheDir := t.TempDir()
	log := runlog.Start(runlog.StartOpts{CacheDir: cacheDir, RepoRoot: repoRoot, Cmd: "check"})
	require.NotNil(t, log, "log must open cleanly in a fresh temp cache dir")
	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: cacheDir, Concurrency: 1, Log: log}, nil, func(c config.Command) bool { return c.Formatter })
	require.NoError(t, err)

	var got Event
	for ev := range events {
		if ev.Phase == Done {
			got = ev
		}
	}
	log.End(false)
	assert.Equal(t, Done, got.Phase)
	assert.ElementsMatch(t, []string{"a.txt", "b.txt"}, got.ChangedFiles)
	dataA, _ := os.ReadFile(a)
	dataB, _ := os.ReadFile(b)
	assert.Equal(t, "AAA", string(dataA))
	assert.Equal(t, "BBB", string(dataB))

	runs, err := runlog.List(cacheDir, repoRoot)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	logEvents, err := runlog.Load(runs[0].Path)
	require.NoError(t, err)
	var invocationIDs []int
	for _, ev := range logEvents {
		if ev.T == runlog.KindInvocation {
			invocationIDs = append(invocationIDs, ev.ID)
		}
	}
	require.Len(t, invocationIDs, 2, "one invocation per file, not one for the whole batch")
	assert.NotEqual(t, invocationIDs[0], invocationIDs[1], "each invocation must have its own runlog id")
}

// TestRun_StdinStdoutFormatter_TargetBasedShape covers the real catalog's other stdin/stdout
// formatter shape (opa fmt, perltidy, pragma-once's fix.sh): the command reads ${target} itself
// (a path argument), rather than expecting content piped on stdin, but still writes its result to
// stdout instead of rewriting the file in place -- this engine still feeds it stdin defensively
// (harmless: the tool never reads it) and still captures its stdout as the new content.
func TestRun_StdinStdoutFormatter_TargetBasedShape(t *testing.T) {
	cfg := config.Config{
		Lint: config.LintConfig{
			Files: map[string]config.FileType{},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"fakestdoutfmt": {
						Name: "fakestdoutfmt", Files: []string{"ALL"},
						Commands: []config.Command{{
							// Reads ${target} itself (via cat), ignores stdin entirely.
							Name: "format", Run: "cat ${target}", Output: "rewrite",
							Formatter: true, InPlace: false,
						}},
					},
				},
			},
		},
	}
	repoRoot := t.TempDir()
	target := filepath.Join(repoRoot, "a.txt")
	require.NoError(t, os.WriteFile(target, []byte("same\n"), 0o644))

	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: t.TempDir(), Concurrency: 1}, nil, func(c config.Command) bool { return c.Formatter })
	require.NoError(t, err)

	var got Event
	for ev := range events {
		if ev.Phase == Done {
			got = ev
		}
	}
	assert.Equal(t, Done, got.Phase)
	assert.Empty(t, got.ChangedFiles, "cat echoes the file back unchanged -- no real reformatting happened")
}

// TestRun_DryRunNeverWritesRealFile is the load-bearing test for this whole feature: a DryRun
// run of a real content-changing InPlace command must report ChangedFiles correctly (it WOULD
// change the file) while leaving the real file's content completely untouched -- proven by
// reading the real file's bytes after the run, not just trusting the reported Event.
func TestRun_DryRunNeverWritesRealFile(t *testing.T) {
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
	target := filepath.Join(repoRoot, "messy.txt")
	require.NoError(t, os.WriteFile(target, []byte("messy\n"), 0o644))

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

	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: cacheDir, Concurrency: 1, DryRun: true}, nil, func(c config.Command) bool { return c.Formatter })
	require.NoError(t, err)

	var got Event
	for ev := range events {
		if ev.Linter == "fakefmt" && ev.Phase == Done {
			got = ev
		}
	}
	assert.Equal(t, []string{"messy.txt"}, got.ChangedFiles, "DryRun must still correctly report what WOULD change")

	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "messy\n", string(data), "DryRun must NEVER write to the real file, even though ChangedFiles reports a change")
}

// TestRun_NonDryRunInPlaceStillWritesRealFile is TestRun_DryRunNeverWritesRealFile's own
// counterpart: it proves the "j.dryRun &&" half of runBatch's sandboxType guard actually matters
// -- without it, every InPlace command would always run sandboxed and rtunk fmt would silently
// become a permanent no-op that never writes anything real. DryRun is left at its zero value
// (false) deliberately, to also cover the case where a caller simply omits the field.
func TestRun_NonDryRunInPlaceStillWritesRealFile(t *testing.T) {
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
	target := filepath.Join(repoRoot, "messy.txt")
	require.NoError(t, os.WriteFile(target, []byte("messy\n"), 0o644))

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
	assert.Equal(t, []string{"messy.txt"}, got.ChangedFiles, "a real run must still correctly report what changed")

	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "formatted\n", string(data), "a non-DryRun InPlace command must actually write the real file")
}

// TestRun_DryRunSandboxStagedInsideRepoRoot proves the dry-run sandbox for an InPlace command is
// created inside repoRoot, not the OS default temp directory -- the fix for a real Critical found
// by this feature's own final review: a config-driven formatter (real prettier, reproduced with
// the actual binary) resolves its own config by walking UP from the file it's formatting, and a
// sandbox with no path back to the real repo (the OS default /tmp) means that walk can never find
// real project config, silently falling back to defaults and producing a phantom "still needs
// reformatting" diff forever, even on an already-correctly-formatted file. This test proves the
// mechanism (sandbox path is genuinely under repoRoot), not prettier's own specific behavior --
// see the fake tool's "findancestor" case, which walks up from its own cwd looking for a marker
// file the same way a real formatter would look for a config file.
//
// The real file starts already at "v2\n" -- the content a config-aware formatter would already
// have produced -- specifically so the two branches diverge in ChangedFiles, not just in some
// side channel: with the sandbox staged inside repoRoot (the fix), the ancestor walk from the
// sandbox finds config.marker at repoRoot and writes "v2\n" back, matching the already-correct
// real content, so NO change is reported. With the old OS-default-tmp staging (the bug), the walk
// never finds config.marker, faketool falls back to "default\n", which differs from the real
// "v2\n" -- a phantom change reported on an already-correctly-formatted file, exactly the bug
// this fix closes. (Confirmed by temporarily reverting to unconditional tmpBase="" during
// development: this assertion flips from empty to []string{"work/a.txt"}.)
func TestRun_DryRunSandboxStagedInsideRepoRoot(t *testing.T) {
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
	// A real repo-root-level "config" file -- stands in for real prettier's own .prettierrc or
	// .editorconfig living at the repo root.
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "config.marker"), []byte("v2\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "work"), 0o755))
	target := filepath.Join(repoRoot, "work", "a.txt")
	// Already correctly formatted per the real config (config-aware output is "v2\n") -- a real
	// run has nothing left to do here, so a correct dry-run check must report no change either.
	require.NoError(t, os.WriteFile(target, []byte("v2\n"), 0o644))

	cfg := config.Config{
		Tools: map[string]config.Tool{
			"faketool": {Name: "faketool", KnownGoodVersion: "1.0.0"},
		},
		Lint: config.LintConfig{
			// Scoped to "txt" specifically (not "ALL") so config.marker itself is never picked up
			// as a lint target and copied into the sandbox as a batch file -- that would let the
			// sandbox "find" it trivially at its own top level regardless of staging location,
			// defeating the point of this test (proving discovery via a genuine ancestor walk).
			Files: map[string]config.FileType{"txt": {Name: "txt", Extensions: []string{"txt"}}},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"fakecfgfmt": {
						Name: "fakecfgfmt", Files: []string{"txt"}, Tools: []string{"faketool"},
						Commands: []config.Command{{
							// Walks up from its own cwd looking for config.marker, the same way a
							// real formatter's own config-resolution algorithm would look for a
							// real config file -- writes "v2\n" if found (config-aware behavior),
							// "default\n" if not (silent fallback to built-in defaults).
							Name: "format", Run: "faketool findancestor config.marker ${target}", Output: "rewrite",
							SuccessCodes: []int{0}, Batch: true, InPlace: true, Formatter: true,
						}},
					},
				},
			},
		},
	}

	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: cacheDir, Concurrency: 1, DryRun: true}, nil, func(c config.Command) bool { return c.Formatter })
	require.NoError(t, err)

	var got Event
	for ev := range events {
		if ev.Linter == "fakecfgfmt" && ev.Phase == Done {
			got = ev
		}
	}
	assert.Empty(t, got.ChangedFiles,
		"the sandbox's ancestor walk must find repoRoot's real config.marker and reproduce the "+
			"already-correct content, not silently fall back to defaults and report a phantom change")

	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "v2\n", string(data), "DryRun must never write the real file")
}

// fakeToolCache installs the fake tool shim under a fresh cache dir and returns that dir.
func fakeToolCache(t *testing.T) string {
	t.Helper()
	binPath := buildFakeToolBinary(t)
	cacheDir := t.TempDir()
	root, err := download.Root(cacheDir)
	require.NoError(t, err)
	shimPath := download.ShimPath(root, "tools", "faketool", "1.0.0", "faketool")
	require.NoError(t, os.MkdirAll(filepath.Dir(shimPath), 0o755))
	require.NoError(t, download.WriteShim(shimPath, binPath))
	return cacheDir
}

func TestRun_TerminalEventsCarryFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("faketool invoked via sh -c")
	}
	cacheDir := fakeToolCache(t)
	repoRoot := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(repoRoot, name), []byte("x\n"), 0o644))
	}
	off := false
	cfg := config.Config{
		Tools: map[string]config.Tool{"faketool": {Name: "faketool", KnownGoodVersion: "1.0.0"}},
		Lint: config.LintConfig{
			Files: map[string]config.FileType{},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"clean": {
						Name: "clean", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{{Name: "lint", Run: "faketool passfail ${target}", Output: "pass_fail", ErrorCodes: []int{1}}},
					},
					"off": {
						Name: "off", Files: []string{"ALL"},
						Commands: []config.Command{{Name: "lint", Run: "faketool passfail ${target}", Output: "pass_fail", Enabled: &off}},
					},
				},
			},
		},
	}

	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: cacheDir, Concurrency: 1}, nil, notFormatter)
	require.NoError(t, err)

	got := map[string]Event{}
	for ev := range events {
		if ev.Phase == Running {
			assert.Empty(t, ev.Files, "Running events carry no Files")
			continue
		}
		got[ev.Linter] = ev
	}
	assert.Equal(t, Done, got["clean"].Phase)
	assert.ElementsMatch(t, []string{"a.txt", "b.txt"}, got["clean"].Files)
	assert.Equal(t, Skipped, got["off"].Phase)
	assert.ElementsMatch(t, []string{"a.txt", "b.txt"}, got["off"].Files)
}

func TestRun_FailedEventCarriesTheWholeFileSet(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("faketool invoked via sh -c")
	}
	cacheDir := fakeToolCache(t)
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
						Commands: []config.Command{{Name: "lint", Run: "faketool alwaysfail ${target}", Output: "pass_fail", ErrorCodes: []int{1}}},
					},
				},
			},
		},
	}
	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: cacheDir, Concurrency: 1}, nil, notFormatter)
	require.NoError(t, err)

	var failed Event
	for ev := range events {
		if ev.Phase == Failed {
			failed = ev
		}
	}
	assert.ElementsMatch(t, []string{"a.txt", "b.txt", "c.txt"}, failed.Files, "a failed linter's files still count as checked")
}

func TestRun_PlannedAndJobDoneEvents(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("faketool invoked via sh -c")
	}
	cacheDir := fakeToolCache(t)
	repoRoot := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(repoRoot, name), []byte("x\n"), 0o644))
	}
	off := false
	cfg := config.Config{
		Tools: map[string]config.Tool{"faketool": {Name: "faketool", KnownGoodVersion: "1.0.0"}},
		Lint: config.LintConfig{
			Files: map[string]config.FileType{},
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"perfile": {
						Name: "perfile", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{{Name: "lint", Run: "faketool passfail ${target}", Output: "pass_fail", ErrorCodes: []int{1}}},
					},
					"batched": {
						Name: "batched", Files: []string{"ALL"}, Tools: []string{"faketool"},
						Commands: []config.Command{{Name: "lint", Run: "faketool passfail ${target}", Output: "pass_fail", ErrorCodes: []int{1}, Batch: true}},
					},
					"off": {
						Name: "off", Files: []string{"ALL"},
						Commands: []config.Command{{Name: "lint", Run: "faketool passfail ${target}", Output: "pass_fail", Enabled: &off}},
					},
				},
			},
		},
	}
	events, err := Run(context.Background(), Env{Cfg: cfg, RepoRoot: repoRoot, CacheDir: cacheDir, Concurrency: 1}, nil, notFormatter)
	require.NoError(t, err)

	planned := map[string]Event{}
	running := map[string][]string{}
	jobDone := map[string][]string{}
	terminalSeen := map[string]bool{}
	for ev := range events {
		switch ev.Phase {
		case Planned:
			planned[ev.Linter] = ev
		case Running:
			running[ev.Linter] = append(running[ev.Linter], ev.File)
		case JobDone:
			assert.False(t, terminalSeen[ev.Linter], "a JobDone never follows its linter's terminal event")
			jobDone[ev.Linter] = append(jobDone[ev.Linter], ev.File)
		case Done, Skipped, Failed:
			terminalSeen[ev.Linter] = true
		}
	}

	assert.Equal(t, 3, planned["perfile"].Total)
	assert.False(t, planned["perfile"].Batch)
	assert.ElementsMatch(t, []string{"a.txt", "b.txt", "c.txt"}, planned["perfile"].Files)
	assert.Equal(t, 1, planned["batched"].Total)
	assert.True(t, planned["batched"].Batch)
	assert.NotContains(t, planned, "off", "a linter that only got Skipped is never planned")
	assert.ElementsMatch(t, running["perfile"], jobDone["perfile"], "every Running is paired with a JobDone")
	assert.ElementsMatch(t, running["batched"], jobDone["batched"])
}

func TestForwardInstall_TranslatesAndBalances(t *testing.T) {
	ref := func(id string) download.Ref { return download.Ref{Category: "tools", ID: id} }
	evs := make(chan download.Event, 8)
	evs <- download.Event{Ref: ref("shfmt"), Phase: download.Started}
	evs <- download.Event{Ref: ref("shfmt"), Phase: download.Progress, Bytes: 5, Total: 10}
	evs <- download.Event{Ref: ref("shfmt"), Phase: download.Done}
	evs <- download.Event{Ref: ref("cached"), Phase: download.Cached}
	close(evs)

	var got []Event
	require.NoError(t, forwardInstall(evs, func(ev Event) { got = append(got, ev) }))
	require.Len(t, got, 3, "Cached emits nothing")
	assert.Equal(t, Event{Phase: InstallStart, Item: "tools/shfmt", BytesTotal: -1}, got[0])
	assert.Equal(t, Event{Phase: InstallProgress, Item: "tools/shfmt", Bytes: 5, BytesTotal: 10}, got[1])
	assert.Equal(t, Event{Phase: InstallDone, Item: "tools/shfmt"}, got[2])

	// A Failed for an item that never started (a tool whose runtime failed first) closes no row,
	// and still returns the error; a started one is closed.
	evs = make(chan download.Event, 4)
	evs <- download.Event{Ref: ref("started"), Phase: download.Started}
	evs <- download.Event{Ref: ref("started"), Phase: download.Failed, Err: errors.New("boom")}
	close(evs)
	got = nil
	err := forwardInstall(evs, func(ev Event) { got = append(got, ev) })
	assert.EqualError(t, err, "boom")
	require.Len(t, got, 2)
	assert.Equal(t, InstallDone, got[1].Phase)

	evs = make(chan download.Event, 2)
	evs <- download.Event{Ref: ref("never"), Phase: download.Failed, Err: errors.New("boom2")}
	close(evs)
	got = nil
	assert.EqualError(t, forwardInstall(evs, func(ev Event) { got = append(got, ev) }), "boom2")
	assert.Empty(t, got, "no InstallDone for an item that never started")
}

func TestLogLinterEnd_IgnoresTheNewPhases(t *testing.T) {
	cache, repo := t.TempDir(), t.TempDir()
	log := runlog.Start(runlog.StartOpts{CacheDir: cache, RepoRoot: repo, Cmd: "check", Version: "t", Argv: []string{"rtunk"}})
	require.NotNil(t, log)
	for _, ph := range []Phase{Planned, JobDone, InstallStart, InstallProgress, InstallDone} {
		logLinterEnd(log, Event{Linter: "x", Phase: ph})
	}
	log.End(false)
	runs, err := runlog.List(cache, repo)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	events, err := runlog.Load(runs[0].Path)
	require.NoError(t, err)
	for _, ev := range events {
		assert.NotEqual(t, runlog.KindLinterEnd, ev.T)
	}
}
