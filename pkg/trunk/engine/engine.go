package engine

import (
	"context"
	"crypto/sha256"
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
	"github.com/xunleii/rtunk/pkg/trunk/engine/security"
	"github.com/xunleii/rtunk/pkg/trunk/output"
)

// Env is every piece of shared configuration a job needs to run.
type Env struct {
	Cfg         config.Config
	RepoRoot    string // always absolute, see Run
	CacheDir    string
	Concurrency int // workers, at least 1 -- Run clamps a lower value up to 1
}

// Phase is one linter's point in the run lifecycle.
type Phase int

const (
	Running Phase = iota // File is set; a progress update, not a terminal outcome
	Done                 // Findings is set (possibly empty -- the command ran clean)
	Skipped              // an unsupported feature; Note says which -- never an error
	Failed               // the command itself errored; Err is set
)

// Event reports one linter's run progress or outcome, streamed on the channel Run returns. A
// linter with no matching files, or none of whose commands pass include, produces no event at all
// -- there is nothing to report. A linter that does run produces one Running event per command
// invocation (per batch, or per file for a non-Batch command), followed by exactly one terminal
// event (Done, Skipped, or Failed).
type Event struct {
	Linter       string
	Phase        Phase
	Findings     []output.Finding // Done only
	ChangedFiles []string         // Done only, InPlace commands only -- repoRoot-relative paths this linter actually rewrote (content differed before/after)
	Note         string           // Skipped (why) or Failed (which command)
	Err          error            // Failed only
	File         string           // Running only -- the file (or comma-joined batch) about to be checked
}

// templateVarRE matches every ${...} placeholder in a Command.Run string.
var templateVarRE = regexp.MustCompile(`\$\{[^}]*\}`)

// supportedOutputFormats is every Command.Output value this package knows how to parse -- anything
// else is Skipped. "taplo" gets its own top-level dispatch (its real Command.Output value, not a
// "regex" special case). "regex" is always dispatched through output.ParseFromRegex, using the
// command's own ParseRegex field.
var supportedOutputFormats = map[string]bool{
	"sarif": true, "sarif_uri": true, "pass_fail": true,
	"actionlint": true, "bandit": true, "buildifier": true, "cfnlint": true,
	"eslint": true, "hadolint": true, "haml_lint": true, "markdownlint": true,
	"pylint": true, "rubocop": true, "stylelint": true, "taplo": true, "regex": true,
	// rewrite/shfmt: real catalog formatter commands (gofmt, black, rustfmt, isort, autopep8,
	// rubocop's fix-layout, stylelint's fix) with nothing to parse -- success is decided purely
	// by ErrorCodes; the caller learns what changed via Event.ChangedFiles instead.
	"rewrite": true, "shfmt": true,
}

// job is one command invocation queued for a worker: one batch (all matched files, for a Batch
// command; one file otherwise) of one linter's one command, with that linter's tool shims already
// resolved onto pathEnv, and -- when cmd.Parser is set -- that parser's own runtime shim directory
// resolved onto parserPathEnv (a separate PATH prefix used only for the parser-stage invocation: a
// parser's runtime need not be any tool the linter itself uses). resolvedDir is the directory
// Command.RunFrom resolved to for every file in batch (repoRoot when RunFrom is empty) -- batch's
// entries are paths relative to resolvedDir, not repoRoot, ready for ${target} substitution once
// the invocation's cwd becomes resolvedDir (or a sandbox mirroring it). Resolving shims (which may
// download a tool or runtime) happens once per linter before any worker starts -- never inside a
// worker -- so two jobs never race downloading the same thing.
type job struct {
	linterName    string
	linter        config.Linter
	cmd           config.Command
	batch         []string
	pathEnv       string
	parserPathEnv string
	resolvedDir   string
}

// linterState accumulates one linter's concurrently-completing jobs into the single terminal
// event (Done or Failed) a sequential run would send once its last command finished. Jobs for the
// same linter can now finish on different workers in any order, so "is this linter done" is
// tracked by a remaining-jobs counter guarded by mu, not by loop position.
type linterState struct {
	mu           sync.Mutex
	remaining    int
	findings     []output.Finding
	changedFiles []string
	terminalSent bool
	failed       bool // once true, workers skip any not-yet-started job for this linter
}

// Run executes every command include selects, across every enabled linter, against the files
// matched under paths (env.RepoRoot is the default walk root when paths is empty, and every
// command's default working directory), downloading any missing tool shim first, and streams one
// Event per linter that had something to report. env.Cfg is expected already
// enabled+used-trimmed (config.Resolve's output).
//
// ctx cancellation stops the run: exec.CommandContext kills an in-flight linter subprocess the
// moment ctx is canceled, and each worker checks ctx.Err() before picking up its next queued job,
// so cancellation also stops new work from starting, not just kills whatever's already running.
// The caller must still drain events to completion after canceling -- a canceled invocation
// reports as a Failed event (its error is context.Canceled, not a real command failure), not a
// silently-closed channel, and every event send blocks until read; an abandoned, undrained
// channel leaks the producer goroutine and any worker still mid-send.
//
// include selects which of a linter's commands are runnable -- internal/cli's check command
// passes `func(c config.Command) bool { return !c.Formatter }`, its fmt command passes the
// complement. A Formatter command (Output: "rewrite" or "shfmt" in the real trunk-io catalog,
// InPlace: true) reports via Event.ChangedFiles (a before/after content-hash comparison per file,
// see runBatch) rather than Event.Findings -- there is nothing to parse; success is decided
// purely by ErrorCodes, same as any other command.
//
// env.Concurrency workers (at least 1) run the queued command invocations in parallel; the queue
// itself is built sequentially and in a fixed order -- linters sorted by name, each linter's own
// batches sorted by resolved directory then file -- so which job a worker happens to pick up next
// is the only source of nondeterminism, never the queue's own order.
func Run(ctx context.Context, env Env, paths []string, include func(config.Command) bool) (<-chan Event, error) {
	root, err := download.Root(env.CacheDir)
	if err != nil {
		return nil, err
	}

	// Every match Files() returns is later relativized against repoRoot (here, for gitignore
	// lookups; below, for RunFrom resolution) via filepath.Rel, which errors outright if one side
	// is absolute and the other relative -- e.g. `rtunk check .` passes paths=["."], a relative
	// walk root, while repoRoot is always absolute. Absolutizing both up front means every path
	// Files() walks and returns is comparable to repoRoot.
	repoRoot, err := filepath.Abs(env.RepoRoot)
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

	concurrency := env.Concurrency
	if concurrency < 1 {
		concurrency = 1
	}

	events := make(chan Event)
	go func() {
		defer close(events)

		names := make([]string, 0, len(env.Cfg.Lint.Definitions))
		for name := range env.Cfg.Lint.Definitions {
			names = append(names, name)
		}
		sort.Strings(names)

		states := make(map[string]*linterState, len(names))
		var jobs []job
		for _, name := range names {
			linterJobs := buildJobs(env.Cfg, root, env.CacheDir, repoRoot, name, env.Cfg.Lint.Definitions[name], paths, include, events)
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
					if ctx.Err() != nil {
						return
					}
					runJob(ctx, j, states[j.linterName], repoRoot, events)
				}
			}()
		}
		wg.Wait()
	}()
	return events, nil
}

// buildJobs resolves name's matched files and queues one job per runnable command invocation
// (include selects which commands are runnable), emitting a Skipped event immediately for every
// command an unsupported feature rules out (var, output format, an unresolvable Parser.Runtime,
// SandboxType/RunFrom attempt real resolution instead of a blanket skip too, per v0.3.2) and a
// Failed event (returning no jobs) if matching files or resolving tools errors outright. Shim
// resolution -- which may download a tool, or (separately) a Command.Parser's own runtime -- runs
// at most once per linter (per distinct Parser.Runtime, for the parser case), lazily, on the first
// command that needs it.
func buildJobs(cfg config.Config, root, cacheDir, repoRoot, name string, linter config.Linter, paths []string, include func(config.Command) bool, events chan<- Event) []job {
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
	parserPathEnvByRuntime := map[string]string{}

	for _, cmd := range linter.Commands {
		if !include(cmd) {
			continue
		}
		if cmd.Enabled != nil && !*cmd.Enabled {
			events <- Event{Linter: name, Phase: Skipped, Note: "disabled by its own plugin source"}
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

		var parserPathEnv string
		if cmd.Parser != nil {
			if v, ok := findUnsupportedParserVar(cmd.Parser.Run); ok {
				events <- Event{Linter: name, Phase: Skipped, Note: fmt.Sprintf("unsupported template var %q in parser", v)}
				continue
			}
			dir, cached := parserPathEnvByRuntime[cmd.Parser.Runtime]
			if !cached {
				resolved, err := resolveRuntimeShimDir(cfg, root, cacheDir, cmd.Parser.Runtime)
				if err != nil {
					events <- Event{Linter: name, Phase: Skipped, Note: fmt.Sprintf("parser runtime %q unavailable: %v", cmd.Parser.Runtime, err)}
					continue
				}
				dir = resolved
				parserPathEnvByRuntime[cmd.Parser.Runtime] = dir
			}
			parserPathEnv = dir
		}

		if cmd.InPlace && cmd.SandboxType != "" {
			events <- Event{Linter: name, Phase: Skipped, Note: "in_place command combined with sandbox_type is unsupported (writes would be lost)"}
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
			if cmd.Batch || !strings.Contains(cmd.Run, "${target}") {
				// A Run string with no ${target} placeholder can't distinguish between files --
				// running it once per matched file (Batch: false's default) would just repeat
				// the exact same invocation N times, reporting the exact same findings N times
				// (real catalog examples: tflint's and brakeman's first commands). One invocation
				// per resolved directory is what such a command can actually tell apart.
				batches = [][]string{relFiles}
			} else {
				for _, f := range relFiles {
					batches = append(batches, []string{f})
				}
			}
			for _, batch := range batches {
				jobs = append(jobs, job{
					linterName: name, linter: linter, cmd: cmd, batch: batch,
					pathEnv: pathEnv, parserPathEnv: parserPathEnv, resolvedDir: dir,
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
		dir, ok := security.ResolveRunFrom(runFrom, f, repoRoot, directConfigs)
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

// findUnsupportedVar reports the first ${...} placeholder in run that isn't one of the four this
// package substitutes: ${target}, ${tmpfile}, ${plugin} (a Linter's own plugin source's root
// directory, config.Linter.SourceRoot), or ${cwd} (that plugin source's own linter subdirectory,
// SourceRoot joined with SourceDir). Everything else (e.g. ${target,}, ${upstream-ref}) is
// unsupported: left unsubstituted, it either breaks the shell (bad substitution) or gets silently
// reinterpreted by sh itself (${upstream-ref} -> ${upstream:-ref}).
func findUnsupportedVar(run string) (string, bool) {
	for _, v := range templateVarRE.FindAllString(run, -1) {
		switch v {
		case "${target}", "${tmpfile}", "${plugin}", "${cwd}":
			continue
		}
		return v, true
	}
	return "", false
}

// findUnsupportedParserVar is findUnsupportedVar for a Command.Parser.Run string specifically --
// ${tmpfile} is valid in cmd.Run (findUnsupportedVar's own allowlist) but never substituted by
// runParser: a parser script gets its data on stdin, not via a tmpfile, and runOneInvocation's own
// `defer os.Remove(tmpfile)` has already fired by the time runParser starts, so even substituting
// it would point at a file that's already gone. Screened out here, even though it's valid for
// cmd.Run itself.
func findUnsupportedParserVar(run string) (string, bool) {
	for _, v := range templateVarRE.FindAllString(run, -1) {
		switch v {
		case "${target}", "${plugin}", "${cwd}":
			continue
		}
		return v, true
	}
	return "", false
}

// runJob executes one queued job: sends a Running event, runs the invocation, then folds the
// result into state -- findings accumulate across every job of the same linter, the first failure
// marks the linter failed (any of its not-yet-started jobs are then skipped, best-effort: a job
// already picked up by a worker still runs to completion), and the linter's single terminal event
// fires exactly once, the moment its last job finishes.
func runJob(ctx context.Context, j job, state *linterState, repoRoot string, events chan<- Event) {
	state.mu.Lock()
	if state.failed {
		state.mu.Unlock()
		return
	}
	state.mu.Unlock()

	events <- Event{Linter: j.linterName, Phase: Running, File: strings.Join(j.batch, ", ")}
	findings, changedFiles, err := runBatch(ctx, j, repoRoot)

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
	state.changedFiles = dedupeStrings(append(state.changedFiles, changedFiles...))
	if state.remaining == 0 {
		state.terminalSent = true
		events <- Event{Linter: j.linterName, Phase: Done, Findings: state.findings, ChangedFiles: state.changedFiles}
	}
}

// runBatch runs one job's invocation and parses its output per cmd.Output, remapping every
// finding's File back to repoRoot-relative before returning. If j.cmd.SandboxType is set, the
// invocation actually runs against a temporary staged copy (see security.StageSandbox); the
// parser only ever sees paths relative to j.resolvedDir, exactly as when no sandboxing is
// involved -- security.RemapFindings is what turns those back into repoRoot-relative paths either
// way. The second return value is the repoRoot-relative subset of j.batch this command actually
// changed on disk (InPlace commands only, via hashFiles' before/after comparison) -- always nil
// for a non-InPlace command.
func runBatch(ctx context.Context, j job, repoRoot string) ([]output.Finding, []string, error) {
	workDir := j.resolvedDir
	if j.cmd.SandboxType != "" {
		sandboxDir, cleanup, err := security.StageSandbox(j.cmd.SandboxType, j.resolvedDir, j.batch)
		if cleanup != nil {
			defer cleanup()
		}
		if err != nil {
			return nil, nil, err
		}
		workDir = sandboxDir
	}

	pluginDir := j.linter.SourceRoot
	cwdDir := filepath.Join(j.linter.SourceRoot, j.linter.SourceDir)

	var beforeHashes map[string][32]byte
	if j.cmd.InPlace {
		beforeHashes = hashFiles(workDir, j.batch)
	}

	out, stderr, exitCode, err := runOneInvocation(ctx, j.cmd, workDir, j.pathEnv, j.batch, pluginDir, cwdDir)
	if err != nil {
		return nil, nil, err
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
		return nil, nil, fmt.Errorf("engine: %s: %s exited %d: %s", j.linterName, j.cmd.Name, exitCode, msg)
	}

	var changedFiles []string
	if j.cmd.InPlace {
		afterHashes := hashFiles(workDir, j.batch)
		for _, f := range j.batch {
			if beforeHashes[f] != afterHashes[f] {
				changedFiles = append(changedFiles, f)
			}
		}
		changedFiles = remapPaths(changedFiles, workDir, repoRoot)
	}

	// A Parser converts the real command's raw output into cmd.Output's expected shape (almost
	// always SARIF) before any of the dispatch below runs -- everything from here on parses out
	// exactly as if the real tool had produced it directly, whether or not a parser was involved.
	// Skipped when out is empty: a genuinely clean run (or an OS-gated command variant producing
	// nothing) must fall through to the isJSONFormat empty-output guard below unparsed, not feed
	// empty stdin to a converter script that may not tolerate it (e.g. Python's
	// json.load(sys.stdin) raises on empty input).
	if j.cmd.Parser != nil && strings.TrimSpace(out) != "" {
		converted, err := runParser(ctx, j.cmd.Parser, workDir, j.parserPathEnv, out, j.batch, pluginDir, cwdDir)
		if err != nil {
			return nil, nil, err
		}
		out = converted
	}

	// Real plugin data confirms SuccessCodes already enumerates every "ran fine, here's the
	// verdict" code, "found issues" included (e.g. ansible-lint sarif: [0,2,5]) -- there is no
	// third bucket. Every exit code not in ErrorCodes parses normally.
	var findings []output.Finding

	// Every JSON-shaped Output format's parser fails to unmarshal empty/whitespace-only input
	// ("unexpected end of JSON input"), which would otherwise abort this whole Run for every
	// linter, not just this one. A real linter can produce genuinely empty output on a clean run
	// (e.g. markdownlint), or when an OS/version-gated command variant of the same linter
	// silently produces nothing on this platform (a known, separate architectural gap -- out of
	// scope here) -- either way, empty output means zero findings, not a parse failure. "pass_fail"
	// never parses JSON (exit-code only), "regex" (output.ParseFromRegex) already tolerates empty
	// input (zero regex matches), and "rewrite"/"shfmt" never parse anything at all -- all four are
	// excluded from this guard.
	isJSONFormat := j.cmd.Output != "pass_fail" && j.cmd.Output != "regex" &&
		j.cmd.Output != "rewrite" && j.cmd.Output != "shfmt"
	if isJSONFormat && strings.TrimSpace(out) == "" {
		security.RemapFindings(findings, j.resolvedDir, repoRoot)
		output.ApplyIssueURL(findings, j.linter.IssueURLFormat)
		return findings, changedFiles, nil
	}

	switch j.cmd.Output {
	case "sarif", "sarif_uri":
		// sarif_uri (checkov): ReadOutputFrom "tmp_file" already resolved the real SARIF bytes
		// written to ${tmpfile} into out -- no separate parser needed.
		findings, err = output.ParseSARIF([]byte(out), j.linterName)
	case "pass_fail":
		if exitCode != 0 {
			findings = output.ParsePassFail(j.linterName, j.batch)
		}
	case "actionlint":
		findings, err = output.ParseActionlint([]byte(out), j.linterName)
	case "bandit":
		findings, err = output.ParseBandit([]byte(out), j.linterName)
	case "buildifier":
		findings, err = output.ParseBuildifier([]byte(out), j.linterName)
	case "cfnlint":
		findings, err = output.ParseCfnLint([]byte(out), j.linterName)
	case "eslint":
		findings, err = output.ParseESLint([]byte(out), j.linterName)
	case "hadolint":
		findings, err = output.ParseHadolint([]byte(out), j.linterName)
	case "haml_lint":
		findings, err = output.ParseHamlLint([]byte(out), j.linterName)
	case "markdownlint":
		findings, err = output.ParseMarkdownlint([]byte(out), j.linterName)
	case "pylint":
		findings, err = output.ParsePylint([]byte(out), j.linterName)
	case "rubocop":
		findings, err = output.ParseRubocop([]byte(out), j.linterName)
	case "stylelint":
		findings, err = output.ParseStylelint([]byte(out), j.linterName)
	case "taplo":
		findings, err = output.ParseTaplo([]byte(out), j.linterName)
	case "regex":
		findings, err = output.ParseFromRegex(j.cmd.ParseRegex, []byte(out), j.linterName)
	case "rewrite", "shfmt":
		// No structured output to parse -- success is already decided by the ErrorCodes check
		// above; the caller learns what changed via changedFiles (populated above) instead.
	}
	if err != nil {
		return nil, nil, err
	}
	if workDir != j.resolvedDir {
		// workDir is a sandbox mirroring j.resolvedDir's structure. Most tools echo back the
		// relative path we substituted into ${target}, which is already correct as-is -- but a
		// tool that echoes an absolute path instead would otherwise leak the throwaway sandbox
		// directory into the final report. Rewrite only the absolute case; a relative one needs
		// no help, it's already resolvedDir-relative by construction.
		//
		// A tool that builds its own absolute path (e.g. via getcwd()) reports the OS's physical
		// path, which can differ from workDir's own literal string when a symlink sits somewhere
		// in the tempdir prefix (macOS: /tmp -> /private/tmp, /var -> /private/var, both live
		// under os.MkdirTemp's default root) -- resolve workDir the same way before comparing, so
		// this doesn't just work by coincidence on platforms with no such symlink.
		//
		// This is a real behavior security.RemapFindings does not perform on its own: it always
		// remaps relative to j.resolvedDir, so an absolute finding path pointing into the sandbox
		// (not j.resolvedDir) would otherwise pass through unrewritten whenever j.resolvedDir ==
		// repoRoot (RemapFindings' own no-op guard).
		base := workDir
		if resolved, err := filepath.EvalSymlinks(workDir); err == nil {
			base = resolved
		}
		for i, f := range findings {
			if filepath.IsAbs(f.File) {
				if rel, err := filepath.Rel(base, f.File); err == nil {
					findings[i].File = rel
				}
			}
		}
	}
	security.RemapFindings(findings, j.resolvedDir, repoRoot)
	output.ApplyIssueURL(findings, j.linter.IssueURLFormat)
	return findings, changedFiles, nil
}

func containsInt(codes []int, code int) bool {
	for _, c := range codes {
		if c == code {
			return true
		}
	}
	return false
}

// dedupeStrings returns ss with duplicates removed, preserving first-occurrence order -- used for
// Event.ChangedFiles, since a linter with more than one InPlace command touching the same file in
// the same batch would otherwise list it more than once in that linter's own single Done event.
func dedupeStrings(ss []string) []string {
	if len(ss) < 2 {
		return ss
	}
	seen := make(map[string]bool, len(ss))
	out := ss[:0]
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// hashFiles returns each file's (dir-joined) SHA-256 content hash, keyed by its own entry in
// files -- a missing file (deleted, or never existed) is simply absent from the result, so
// comparing a before/after pair naturally treats "existed then, gone now" (and vice versa) as
// changed, the same as any other content difference, with no special-casing needed.
func hashFiles(dir string, files []string) map[string][32]byte {
	hashes := make(map[string][32]byte, len(files))
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			continue
		}
		hashes[f] = sha256.Sum256(data)
	}
	return hashes
}

// remapPaths is security.RemapFindings' own base/repoRoot remap logic, for a plain list of
// dir-relative paths instead of []output.Finding -- used for Event.ChangedFiles, which has no
// Finding struct to carry a File field.
func remapPaths(paths []string, base, repoRoot string) []string {
	if base == repoRoot || len(paths) == 0 {
		return paths
	}
	out := make([]string, len(paths))
	for i, p := range paths {
		abs := p
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(base, p)
		}
		if rel, err := filepath.Rel(repoRoot, abs); err == nil {
			out[i] = rel
		} else {
			out[i] = p
		}
	}
	return out
}

// resolveShimDirs resolves (downloading first if not already cached) every tool id's shim, and
// returns the directory each shim lives in -- a Command.Run string references its tool(s) by bare
// name, so those directories become the PATH prefix that lets `sh -c` find them.
func resolveShimDirs(cfg config.Config, root, cacheDir string, toolIDs []string) ([]string, error) {
	dirs := make([]string, 0, len(toolIDs))
	for _, id := range toolIDs {
		tool, ok := cfg.Tools[id]
		if !ok {
			return nil, fmt.Errorf("engine: tool %q referenced but not found in resolved config", id)
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

// resolveRuntimeShimDir resolves (downloading first if not already cached) runtimeID's own shim
// directory -- mirrors resolveShimDirs, but for a single Command.Parser.Runtime rather than a
// linter's own Tools list: a parser script is invoked through its runtime's own interpreter shim
// (e.g. python3), not a tool binary, and that runtime need not be one any Tool in cfg references.
//
// Every real trunk-io Command.Parser this feature was designed against uses runtime: python or
// runtime: node, and both real Runtime definitions fetch via a download: recipe (confirmed by
// reading their real plugin.yaml files) -- so this always has a real shim to resolve in practice.
// A runtime whose SystemVersion is set instead (the "already installed on this machine" case)
// never gets a shim written for it at all (see fetchRuntimeRef), so resolving one here would
// return a directory that was never created; this is a known, real gap for that specific
// combination, left unhandled since no real catalog Command.Parser reaches it today.
func resolveRuntimeShimDir(cfg config.Config, root, cacheDir, runtimeID string) (string, error) {
	rt, ok := cfg.Runtimes.Definitions[runtimeID]
	if !ok {
		return "", fmt.Errorf("engine: parser runtime %q referenced but not found in resolved config", runtimeID)
	}
	if len(rt.Shims) == 0 {
		return "", fmt.Errorf("engine: parser runtime %q has no shims declared", runtimeID)
	}
	version := download.ResolveVersion(cfg.Runtimes.Enabled, runtimeID, rt.KnownGoodVersion)
	shimPath := download.ShimPath(root, "runtimes", runtimeID, version, rt.Shims[0])
	if _, statErr := os.Stat(shimPath); statErr != nil {
		evs, err := download.Download(cfg, cacheDir, download.Ref{Category: "runtimes", ID: runtimeID, Version: version})
		if err != nil {
			return "", err
		}
		for ev := range evs {
			if ev.Phase == download.Failed {
				return "", ev.Err
			}
		}
	}
	return filepath.Dir(shimPath), nil
}

// runOneInvocation substitutes ${target}/${tmpfile}/${plugin}/${cwd} into cmd.Run and executes it
// through a shell (a Command.Run string is a shell command line referencing its tool(s) by bare
// name, not a path), with pathEnv prefixed onto PATH (verbatim PATH when pathEnv is empty -- a
// leading empty PATH component means "current directory" on POSIX, which would let workDir's own
// files shadow real binaries) and workDir as the working directory. ctx cancellation kills the
// subprocess immediately via exec.CommandContext. Returns the output named by cmd.ReadOutputFrom
// (default stdout), the process's raw stderr (always captured, regardless of ReadOutputFrom, so
// callers can surface it on a crash), and the exit code; err is only ever a launch failure (e.g.
// "sh" missing), never a non-zero exit -- callers read exitCode for that.
func runOneInvocation(ctx context.Context, cmd config.Command, workDir, pathEnv string, files []string, pluginDir, cwdDir string) (out, stderrOut string, exitCode int, err error) {
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

	run := strings.NewReplacer(
		"${target}", target, "${tmpfile}", tmpfile,
		"${plugin}", quoteOne(pluginDir), "${cwd}", quoteOne(cwdDir),
	).Replace(cmd.Run)

	c := exec.CommandContext(ctx, "sh", "-c", run)
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
		out = stderr.String()
	case "tmp_file":
		data, readErr := os.ReadFile(tmpfile)
		if readErr != nil {
			return "", stderr.String(), code, readErr
		}
		out = string(data)
	default: // "" or "stdout"
		out = stdout.String()
	}
	return out, stderr.String(), code, nil
}

// runParser converts a real command's raw native output into the shape cmd.Output expects, by
// piping it through parser.Run: stdin is stdin (the real command's own raw output, exactly what
// runOneInvocation returned), and the script's own stdout is the result -- the universal contract
// every real trunk-io Command.Parser script uses (confirmed by reading trufflehog_to_sarif.py,
// tfsec/parse.py, and ruff_to_sarif.py in full during this feature's design). ${target}/${plugin}/
// ${cwd} substitute into parser.Run exactly as they do into cmd.Run; workDir is the same directory
// (or sandbox) the real command itself just ran in. parserPathEnv is the parser's own runtime's
// shim directory (e.g. wherever python3 lives), entirely separate from the linter's own pathEnv --
// a parser's runtime need not be any tool the linter itself uses.
func runParser(ctx context.Context, parser *config.Parser, workDir, parserPathEnv, stdin string, batch []string, pluginDir, cwdDir string) (string, error) {
	target := strings.Join(quoteAll(batch), " ")
	run := strings.NewReplacer(
		"${target}", target, "${plugin}", quoteOne(pluginDir), "${cwd}", quoteOne(cwdDir),
	).Replace(parser.Run)

	c := exec.CommandContext(ctx, "sh", "-c", run)
	c.Dir = workDir
	c.Stdin = strings.NewReader(stdin)
	path := os.Getenv("PATH")
	if parserPathEnv != "" {
		path = parserPathEnv + string(os.PathListSeparator) + path
	}
	c.Env = append(os.Environ(), "PATH="+path)

	var stdout, stderr strings.Builder
	c.Stdout = &stdout
	c.Stderr = &stderr

	if err := c.Run(); err != nil {
		errText := strings.TrimSpace(stderr.String())
		if errText == "" {
			errText = err.Error()
		}
		return "", fmt.Errorf("engine: parser: %s", errText)
	}
	return stdout.String(), nil
}

func quoteAll(files []string) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = "'" + strings.ReplaceAll(f, "'", `'\''`) + "'"
	}
	return out
}

// quoteOne is quoteAll for a single string -- used for ${plugin}/${cwd}, which (unlike ${target})
// are one path each, not a list.
func quoteOne(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
