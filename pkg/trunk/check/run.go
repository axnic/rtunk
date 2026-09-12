package check

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/xunleii/rtunk/pkg/trunk/config"
	"github.com/xunleii/rtunk/pkg/trunk/download"
)

// Phase is one linter's point in check's run lifecycle.
type Phase int

const (
	Running Phase = iota // File is set; a progress update, not a terminal outcome
	Done                 // Findings is set (possibly empty -- the linter ran clean)
	Skipped              // an unsupported feature; Note says which -- never an error
	Failed               // the linter's own command errored; Err is set
)

// Event reports one linter's check progress or outcome, streamed on the channel Run returns. A
// linter with no matching files, or whose only commands are formatters (Formatter: true),
// produces no event at all -- there is nothing to report. A linter that does run produces one
// Running event per command invocation (per batch, or per file for a non-Batch command), followed
// by exactly one terminal event (Done, Skipped, or Failed).
type Event struct {
	Linter   string
	Phase    Phase
	Findings []Finding // Done only
	Note     string    // Skipped (why) or Failed (which command)
	Err      error     // Failed only
	File     string    // Running only -- the file (or comma-joined batch) about to be checked
}

// templateVarRE matches every ${...} placeholder in a Command.Run string.
var templateVarRE = regexp.MustCompile(`\$\{[^}]*\}`)

// supportedOutputFormats is every Command.Output value this package knows how to parse -- anything
// else is Skipped. "taplo" gets its own top-level dispatch (its real Command.Output value, not a
// "regex" special case). "regex" covers every other free-text linter dispatched by name in
// runBatch's switch: perlcritic gets its own parser (see docs/superpowers/specs/
// 2026-09-12-check-v0.3.1-output-formats-design.md), everything else falls through to the
// best-effort ParseGenericRegex.
var supportedOutputFormats = map[string]bool{
	"sarif": true, "sarif_uri": true, "pass_fail": true,
	"actionlint": true, "bandit": true, "buildifier": true, "cfnlint": true,
	"eslint": true, "hadolint": true, "haml_lint": true, "markdownlint": true,
	"pylint": true, "rubocop": true, "stylelint": true, "taplo": true, "regex": true,
}

// job is one command invocation queued for a worker: one batch (all matched files, for a Batch
// command; one file otherwise) of one linter's one command, with that linter's tool shims already
// resolved onto pathEnv. resolvedDir is the directory Command.RunFrom resolved to for every file
// in batch (repoRoot when RunFrom is empty) -- batch's entries are paths relative to resolvedDir,
// not repoRoot, ready for ${target} substitution once the invocation's cwd becomes resolvedDir (or
// a sandbox mirroring it). Resolving shims (which may download a tool) happens once per linter
// before any worker starts -- never inside a worker -- so two jobs never race downloading the
// same tool.
type job struct {
	linterName  string
	linter      config.Linter
	cmd         config.Command
	batch       []string
	pathEnv     string
	resolvedDir string
}

// linterState accumulates one linter's concurrently-completing jobs into the single terminal
// event (Done or Failed) a sequential run would send once its last command finished. Jobs for the
// same linter can now finish on different workers in any order, so "is this linter done" is
// tracked by a remaining-jobs counter guarded by mu, not by loop position.
type linterState struct {
	mu           sync.Mutex
	remaining    int
	findings     []Finding
	terminalSent bool
	failed       bool // once true, workers skip any not-yet-started job for this linter
}

// Run executes every enabled linter's non-formatter commands against the files matched under
// paths (repoRoot is the default walk root when paths is empty, and every command's default
// working directory), downloading any missing tool shim first, and streams one Event per linter
// that had something to report. cfg is expected already enabled+used-trimmed (config.Resolve's
// output).
//
// concurrency workers (at least 1) run the queued command invocations in parallel; the queue
// itself is built sequentially and in a fixed order -- linters sorted by name, each linter's own
// batches sorted by resolved directory then file -- so which job a worker happens to pick up next
// is the only source of nondeterminism, never the queue's own order.
func Run(cfg config.Config, cacheDir, repoRoot string, paths []string, concurrency int) (<-chan Event, error) {
	root, err := download.Root(cacheDir)
	if err != nil {
		return nil, err
	}

	// Every match Files() returns is later relativized against repoRoot (here, for gitignore
	// lookups; below, for RunFrom resolution) via filepath.Rel, which errors outright if one side
	// is absolute and the other relative -- e.g. `rtunk check .` passes paths=["."], a relative
	// walk root, while repoRoot is always absolute. Absolutizing both up front means every path
	// Files() walks and returns is comparable to repoRoot.
	repoRoot, err = filepath.Abs(repoRoot)
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		paths = []string{repoRoot}
	} else {
		for i, p := range paths {
			abs, err := filepath.Abs(p)
			if err != nil {
				return nil, err
			}
			paths[i] = abs
		}
	}

	if concurrency < 1 {
		concurrency = 1
	}

	events := make(chan Event)
	go func() {
		defer close(events)

		names := make([]string, 0, len(cfg.Lint.Definitions))
		for name := range cfg.Lint.Definitions {
			names = append(names, name)
		}
		sort.Strings(names)

		states := make(map[string]*linterState, len(names))
		var jobs []job
		for _, name := range names {
			linterJobs := buildJobs(cfg, root, cacheDir, repoRoot, name, cfg.Lint.Definitions[name], paths, events)
			if len(linterJobs) == 0 {
				continue
			}
			states[name] = &linterState{remaining: len(linterJobs)}
			jobs = append(jobs, linterJobs...)
		}

		jobCh := make(chan job, len(jobs))
		for _, j := range jobs {
			jobCh <- j
		}
		close(jobCh)

		var wg sync.WaitGroup
		for i := 0; i < concurrency; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := range jobCh {
					runJob(j, states[j.linterName], repoRoot, events)
				}
			}()
		}
		wg.Wait()
	}()
	return events, nil
}

// buildJobs resolves name's matched files and queues one job per runnable command invocation,
// emitting a Skipped event immediately for every command an unsupported feature rules out (var,
// output format, parser -- unchanged from v0.3.1; SandboxType/RunFrom now attempt real resolution
// instead of a blanket skip) and a Failed event (returning no jobs) if matching files or resolving
// tools errors outright. Shim resolution -- which may download a tool -- runs at most once per
// linter, lazily, on the first command that needs it.
func buildJobs(cfg config.Config, root, cacheDir, repoRoot, name string, linter config.Linter, paths []string, events chan<- Event) []job {
	files, err := Files(cfg, linter, repoRoot, paths)
	if err != nil {
		events <- Event{Linter: name, Phase: Failed, Note: "matching files", Err: err}
		return nil
	}
	if len(files) == 0 {
		return nil
	}

	var jobs []job
	var pathEnv string
	pathEnvResolved := false

	for _, cmd := range linter.Commands {
		if cmd.Formatter {
			continue
		}
		if v, ok := findUnsupportedVar(cmd.Run); ok {
			events <- Event{Linter: name, Phase: Skipped, Note: fmt.Sprintf("unsupported template var %q", v)}
			continue
		}
		if !supportedOutputFormats[cmd.Output] {
			events <- Event{Linter: name, Phase: Skipped, Note: fmt.Sprintf("unsupported output format %q", cmd.Output)}
			continue
		}
		if cmd.Parser != nil {
			events <- Event{Linter: name, Phase: Skipped, Note: "unsupported parser (native output requires a converter script)"}
			continue
		}
		if cmd.SandboxType != "" && cmd.SandboxType != "copy_targets" && cmd.SandboxType != "expanded" {
			events <- Event{Linter: name, Phase: Skipped, Note: fmt.Sprintf("unsupported sandbox_type %q", cmd.SandboxType)}
			continue
		}

		// "expanded" without an explicit RunFrom needs to default to the target's own directory,
		// not repoRoot: it exists to give a tool sibling-file context (a whole Go package, a
		// whole Terraform module), and real catalog data confirms this matters -- gokart's real
		// command is exactly SandboxType: expanded with RunFrom left empty, and it needs its
		// target's own directory, not repoRoot, to expand meaningfully. tflint's own "expanded"
		// command instead sets RunFrom: ${target_directory} explicitly, so this default only ever
		// fires when RunFrom really is empty -- an explicit RunFrom (of any form) is untouched.
		effectiveRunFrom := cmd.RunFrom
		if cmd.SandboxType == "expanded" && effectiveRunFrom == "" {
			effectiveRunFrom = "${target_directory}"
		}

		groups, ok := groupByRunFrom(effectiveRunFrom, files, repoRoot, linter.DirectConfigs)
		if !ok {
			events <- Event{Linter: name, Phase: Skipped, Note: fmt.Sprintf("unsupported run_from %q", cmd.RunFrom)}
			continue
		}

		if !pathEnvResolved {
			shimDirs, err := resolveShimDirs(cfg, root, cacheDir, linter.Tools)
			if err != nil {
				events <- Event{Linter: name, Phase: Failed, Note: "resolving tools", Err: err}
				return nil
			}
			pathEnv = strings.Join(shimDirs, string(os.PathListSeparator))
			pathEnvResolved = true
		}

		for _, dir := range sortedKeys(groups) {
			relFiles := groups[dir]
			var batches [][]string
			if cmd.Batch {
				batches = [][]string{relFiles}
			} else {
				for _, f := range relFiles {
					batches = append(batches, []string{f})
				}
			}
			for _, batch := range batches {
				jobs = append(jobs, job{
					linterName: name, linter: linter, cmd: cmd, batch: batch,
					pathEnv: pathEnv, resolvedDir: dir,
				})
			}
		}
	}
	return jobs
}

// groupByRunFrom resolves runFrom for every file in files (absolute paths, already matched under
// repoRoot), grouping them by resolved directory -- each group's files are returned relative to
// that directory (sorted), ready for ${target} substitution once the invocation's cwd becomes that
// directory (or a sandbox mirroring it). ok is false if runFrom isn't a recognized form.
func groupByRunFrom(runFrom string, files []string, repoRoot string, directConfigs []string) (map[string][]string, bool) {
	groups := map[string][]string{}
	for _, f := range files {
		dir, ok := resolveRunFrom(runFrom, f, repoRoot, directConfigs)
		if !ok {
			return nil, false
		}
		rel, err := filepath.Rel(dir, f)
		if err != nil {
			return nil, false
		}
		groups[dir] = append(groups[dir], rel)
	}
	for dir := range groups {
		sort.Strings(groups[dir])
	}
	return groups, true
}

func sortedKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// findUnsupportedVar reports the first ${...} placeholder in run that isn't ${target} or
// ${tmpfile} -- the only two this plan substitutes. Everything else (e.g. ${target,},
// ${upstream-ref}) is unsupported: left unsubstituted, it either breaks the shell (bad
// substitution) or gets silently reinterpreted by sh itself (${upstream-ref} -> ${upstream:-ref}).
func findUnsupportedVar(run string) (string, bool) {
	for _, v := range templateVarRE.FindAllString(run, -1) {
		if v != "${target}" && v != "${tmpfile}" {
			return v, true
		}
	}
	return "", false
}

// runJob executes one queued job: sends a Running event, runs the invocation, then folds the
// result into state -- findings accumulate across every job of the same linter, the first failure
// marks the linter failed (any of its not-yet-started jobs are then skipped, best-effort: a job
// already picked up by a worker still runs to completion), and the linter's single terminal event
// fires exactly once, the moment its last job finishes.
func runJob(j job, state *linterState, repoRoot string, events chan<- Event) {
	state.mu.Lock()
	if state.failed {
		state.mu.Unlock()
		return
	}
	state.mu.Unlock()

	events <- Event{Linter: j.linterName, Phase: Running, File: strings.Join(j.batch, ", ")}
	findings, err := runBatch(j, repoRoot)

	state.mu.Lock()
	defer state.mu.Unlock()
	state.remaining--
	if state.terminalSent {
		return
	}

	if err != nil {
		state.failed = true
		state.terminalSent = true
		events <- Event{Linter: j.linterName, Phase: Failed, Note: j.cmd.Name, Err: err}
		return
	}

	state.findings = append(state.findings, findings...)
	if state.remaining == 0 {
		state.terminalSent = true
		events <- Event{Linter: j.linterName, Phase: Done, Findings: state.findings}
	}
}

// runBatch runs one job's invocation and parses its output per cmd.Output, remapping every
// finding's File back to repoRoot-relative before returning. If j.cmd.SandboxType is set, the
// invocation actually runs against a temporary staged copy (see stageSandbox); the parser only
// ever sees paths relative to j.resolvedDir, exactly as when no sandboxing is involved --
// remapFindings is what turns those back into repoRoot-relative paths either way.
func runBatch(j job, repoRoot string) ([]Finding, error) {
	workDir := j.resolvedDir
	if j.cmd.SandboxType != "" {
		sandboxDir, cleanup, err := stageSandbox(j.cmd.SandboxType, j.resolvedDir, j.batch)
		if cleanup != nil {
			defer cleanup()
		}
		if err != nil {
			return nil, err
		}
		workDir = sandboxDir
	}

	out, stderr, exitCode, err := runOneInvocation(j.cmd, workDir, j.pathEnv, j.batch)
	if err != nil {
		return nil, err
	}
	if containsInt(j.cmd.ErrorCodes, exitCode) {
		msg := strings.TrimSpace(out)
		if errText := strings.TrimSpace(stderr); errText != "" {
			if msg == "" {
				msg = errText
			} else {
				msg += "\n" + errText
			}
		}
		return nil, fmt.Errorf("check: %s: %s exited %d: %s", j.linterName, j.cmd.Name, exitCode, msg)
	}

	// Real plugin data confirms SuccessCodes already enumerates every "ran fine, here's the
	// verdict" code, "found issues" included (e.g. ansible-lint sarif: [0,2,5]) -- there is no
	// third bucket. Every exit code not in ErrorCodes parses normally.
	var findings []Finding

	// Every JSON-shaped Output format's parser fails to unmarshal empty/whitespace-only input
	// ("unexpected end of JSON input"), which would otherwise abort this whole Run for every
	// linter, not just this one. A real linter can produce genuinely empty output on a clean run
	// (e.g. markdownlint), or when an OS/version-gated command variant of the same linter
	// silently produces nothing on this platform (a known, separate architectural gap -- out of
	// scope here) -- either way, empty output means zero findings, not a parse failure. "pass_fail"
	// never parses JSON (exit-code only) and "regex"'s parsers (ParsePerlCritic/ParseGenericRegex)
	// already tolerate empty input by iterating an empty line list, so both are excluded from this
	// guard.
	isJSONFormat := j.cmd.Output != "pass_fail" && j.cmd.Output != "regex"
	if isJSONFormat && strings.TrimSpace(out) == "" {
		remapFindings(findings, j.resolvedDir, repoRoot)
		ApplyIssueURL(findings, j.linter.IssueURLFormat)
		return findings, nil
	}

	switch j.cmd.Output {
	case "sarif", "sarif_uri":
		// sarif_uri (checkov): ReadOutputFrom "tmp_file" already resolved the real SARIF bytes
		// written to ${tmpfile} into out -- no separate parser needed.
		findings, err = ParseSARIF([]byte(out), j.linterName)
	case "pass_fail":
		if exitCode != 0 {
			findings = ParsePassFail(j.linterName, j.batch)
		}
	case "actionlint":
		findings, err = ParseActionlint([]byte(out), j.linterName)
	case "bandit":
		findings, err = ParseBandit([]byte(out), j.linterName)
	case "buildifier":
		findings, err = ParseBuildifier([]byte(out), j.linterName)
	case "cfnlint":
		findings, err = ParseCfnLint([]byte(out), j.linterName)
	case "eslint":
		findings, err = ParseESLint([]byte(out), j.linterName)
	case "hadolint":
		findings, err = ParseHadolint([]byte(out), j.linterName)
	case "haml_lint":
		findings, err = ParseHamlLint([]byte(out), j.linterName)
	case "markdownlint":
		findings, err = ParseMarkdownlint([]byte(out), j.linterName)
	case "pylint":
		findings, err = ParsePylint([]byte(out), j.linterName)
	case "rubocop":
		findings, err = ParseRubocop([]byte(out), j.linterName)
	case "stylelint":
		findings, err = ParseStylelint([]byte(out), j.linterName)
	case "taplo":
		findings, err = ParseTaplo([]byte(out), j.linterName)
	case "regex":
		switch j.linterName {
		case "perlcritic":
			findings, err = ParsePerlCritic([]byte(out), j.linterName)
		default:
			findings = ParseGenericRegex([]byte(out), j.linterName)
		}
	}
	if err != nil {
		return nil, err
	}
	remapFindings(findings, j.resolvedDir, repoRoot)
	ApplyIssueURL(findings, j.linter.IssueURLFormat)
	return findings, nil
}

func containsInt(codes []int, code int) bool {
	for _, c := range codes {
		if c == code {
			return true
		}
	}
	return false
}

// resolveShimDirs resolves (downloading first if not already cached) every tool id's shim, and
// returns the directory each shim lives in -- a Command.Run string references its tool(s) by bare
// name, so those directories become the PATH prefix that lets `sh -c` find them.
func resolveShimDirs(cfg config.Config, root, cacheDir string, toolIDs []string) ([]string, error) {
	dirs := make([]string, 0, len(toolIDs))
	for _, id := range toolIDs {
		tool, ok := cfg.Tools[id]
		if !ok {
			return nil, fmt.Errorf("check: tool %q referenced but not found in resolved config", id)
		}
		version := download.ResolveVersion(cfg.Lint.Enabled, id, tool.KnownGoodVersion)
		shimPath := download.ShimPath(root, "tools", id, version, id)
		if _, statErr := os.Stat(shimPath); statErr != nil {
			evs, err := download.Download(cfg, cacheDir, download.Ref{Category: "tools", ID: id, Version: version})
			if err != nil {
				return nil, err
			}
			for ev := range evs {
				if ev.Phase == download.Failed {
					return nil, ev.Err
				}
			}
		}
		dirs = append(dirs, filepath.Dir(shimPath))
	}
	return dirs, nil
}

// runOneInvocation substitutes ${target}/${tmpfile} into cmd.Run and executes it through a shell
// (a Command.Run string is a shell command line referencing its tool(s) by bare name, not a
// path), with pathEnv prefixed onto PATH (verbatim PATH when pathEnv is empty -- a leading empty
// PATH component means "current directory" on POSIX, which would let workDir's own files shadow
// real binaries) and workDir as the working directory. Returns the output named by
// cmd.ReadOutputFrom (default stdout), the process's raw stderr (always captured, regardless of
// ReadOutputFrom, so callers can surface it on a crash), and the exit code; err is only ever a
// launch failure (e.g. "sh" missing), never a non-zero exit -- callers read exitCode for that.
func runOneInvocation(cmd config.Command, workDir, pathEnv string, files []string) (output, stderrOut string, exitCode int, err error) {
	target := strings.Join(quoteAll(files), " ")

	var tmpfile string
	if strings.Contains(cmd.Run, "${tmpfile}") {
		f, err := os.CreateTemp("", "rtunk-check-*")
		if err != nil {
			return "", "", 0, err
		}
		tmpfile = f.Name()
		f.Close()
		defer os.Remove(tmpfile)
	}

	run := strings.NewReplacer("${target}", target, "${tmpfile}", tmpfile).Replace(cmd.Run)

	c := exec.Command("sh", "-c", run)
	c.Dir = workDir
	path := os.Getenv("PATH")
	if pathEnv != "" {
		path = pathEnv + string(os.PathListSeparator) + path
	}
	c.Env = append(os.Environ(), "PATH="+path)

	var stdout, stderr strings.Builder
	c.Stdout = &stdout
	c.Stderr = &stderr

	runErr := c.Run()
	code := 0
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			code = exitErr.ExitCode()
		} else {
			return "", "", 0, runErr
		}
	}

	switch cmd.ReadOutputFrom {
	case "stderr":
		output = stderr.String()
	case "tmp_file":
		data, readErr := os.ReadFile(tmpfile)
		if readErr != nil {
			return "", stderr.String(), code, readErr
		}
		output = string(data)
	default: // "" or "stdout"
		output = stdout.String()
	}
	return output, stderr.String(), code, nil
}

func quoteAll(files []string) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = "'" + strings.ReplaceAll(f, "'", `'\''`) + "'"
	}
	return out
}
