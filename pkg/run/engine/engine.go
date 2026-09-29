package engine

import (
	"cmp"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xunleii/rtunk/pkg/cache/download"
	"github.com/xunleii/rtunk/pkg/run/engine/security"
	"github.com/xunleii/rtunk/pkg/run/runlog"
	"github.com/xunleii/rtunk/pkg/trunk/config"
	"github.com/xunleii/rtunk/pkg/trunk/output"
)

// Env is every piece of shared configuration a job needs to run.
type Env struct {
	Cfg         config.Config
	RepoRoot    string // always absolute, see Run
	CacheDir    string
	Concurrency int // workers, at least 1 -- Run clamps a lower value up to 1
	// DryRun forces every InPlace command in this run to execute against a throwaway sandbox copy
	// of its own targets (reusing security.StageSandbox's "copy_targets" mechanism) instead of the
	// real files -- Event.ChangedFiles still reports what WOULD change, but nothing on disk is
	// ever modified. Independent of Command.SandboxType (which real catalog data never sets on an
	// InPlace command anyway, since a sandboxed write would otherwise be silently lost -- see the
	// InPlace+SandboxType skip in buildJobs).
	DryRun bool
	// Log, when non-nil, records every command this run launches (see pkg/run/runlog). A nil
	// Log records nothing. The caller owns it: Run neither opens nor ends it, so one Log can span
	// several Run calls (`check --fix`, `fmt --verify-stable`).
	Log *runlog.Writer
}

// Phase is one linter's point in the run lifecycle.
type Phase int

// The phases a linter run reports: Running is a progress update, the rest are terminal.
const (
	Running Phase = iota // File is set; a progress update, not a terminal outcome
	Done                 // Findings is set (possibly empty -- the command ran clean)
	Skipped              // an unsupported feature; Note says which -- never an error
	Failed               // the command itself errored; Err is set
	// The phases below are non-terminal progress for the live view; every consumer that only
	// cares about outcomes ignores them.
	Planned         // once per linter, after buildJobs, when it queued at least one job (Total, Batch, Files)
	JobDone         // one job finished, any outcome, after runBatch (File is the same string as its Running)
	InstallStart    // a download began for Item
	InstallProgress // Bytes/BytesTotal update for Item
	InstallDone     // the download for Item ended (success or failure)
	InstallPlanned  // once, before the first InstallStart, when installs are needed (Total = number of items)
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
	Files        []string         // Done, Skipped, Failed and Planned only (once files are matched) -- repoRoot-relative: every file the linter matched
	Total        int              // Planned only -- number of jobs queued for the linter
	Batch        bool             // Planned only -- true when any of the linter's jobs has more than one file in its batch
	Item         string           // Install* only -- "category/id", e.g. "tools/shfmt"
	Bytes        int64            // InstallProgress only -- bytes received so far
	BytesTotal   int64            // InstallStart|InstallProgress -- Content-Length, -1 when unknown
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
	toolVersions  map[string]string // linter.Tools resolved to versions, for the run log only
	files         []string          // the linter's whole matched file set, repoRoot-relative (for terminal events)
	dryRun        bool              // set uniformly from Env.DryRun for every job in a run -- a run-level
	// setting, not a per-command one; see runBatch's own use of it.
}

// prepareRunState pairs a sync.Once with the error the one call it actually runs produces --
// sync.Once.Do itself has no return value, so every caller blocked on the same Do call (not just
// the one that triggered it) reads err after Do returns to know whether the command's PrepareRun
// invocation it was waiting on actually succeeded. Safe with no extra locking: sync.Once.Do
// already establishes happens-before between the closure's writes and every Do call's return, the
// blocked ones included.
type prepareRunState struct {
	once sync.Once
	err  error
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

	concurrency := max(env.Concurrency, 1)

	_ = download.RecordUsage(env.CacheDir, repoRoot, env.Cfg) // best-effort; see RecordUsage's own doc comment

	events := make(chan Event)
	go func() {
		defer close(events)

		names := make([]string, 0, len(env.Cfg.Lint.Definitions))
		for name := range env.Cfg.Lint.Definitions {
			names = append(names, name)
		}
		sort.Strings(names)

		// Match every linter's files first: the tools they need are then known up front and
		// downloaded together, and their number is announced (InstallPlanned) before any starts.
		filesByName := map[string][]string{}
		var refs []download.Ref
		for _, name := range names {
			linter := env.Cfg.Lint.Definitions[name]
			files, err := Files(env.Cfg, linter, repoRoot, paths)
			if err != nil {
				events <- Event{Linter: name, Phase: Failed, Note: "matching files", Err: err}
				continue
			}
			if len(files) == 0 {
				continue
			}
			filesByName[name] = files
			refs = append(refs, requiredRefs(linter, include)...)
		}
		failed := prefetch(env.Cfg, root, env.CacheDir, repoRoot, refs, func(ev Event) { events <- ev })

		states := make(map[string]*linterState, len(names))
		var jobs []job
		// healthChecked caches each tool id's Tool.HealthChecks outcome (nil on success) across
		// every buildJobs call below -- all made serially, one at a time, in this same goroutine,
		// strictly before the worker-pool loop starts, so a plain map (no mutex, no sync.Once) is
		// enough: two linters sharing a tool must only run its health check once per Run call.
		healthChecked := map[string]error{}
		for _, name := range names {
			files, ok := filesByName[name]
			if !ok {
				continue
			}
			linterJobs := buildJobs(ctx, env.Cfg, root, env.CacheDir, repoRoot, name, env.Cfg.Lint.Definitions[name], files, failed, healthChecked, include, env.DryRun, events, env.Log)
			if len(linterJobs) == 0 {
				continue
			}
			states[name] = &linterState{remaining: len(linterJobs)}
			jobs = append(jobs, linterJobs...)
			batch := false
			for _, j := range linterJobs {
				batch = batch || len(j.batch) > 1
			}
			events <- Event{Linter: name, Phase: Planned, Total: len(linterJobs), Batch: batch, Files: linterJobs[0].files}
		}

		jobCh := make(chan job, len(jobs))
		for _, j := range jobs {
			jobCh <- j
		}
		close(jobCh)

		// prepareRunOnce holds one entry per (linter,command) pair whose Command.PrepareRun is set
		// -- built once, from the already-fully-built jobs slice, before any worker goroutine
		// starts; a plain map is safe here for the same reason states is: read-only for the rest
		// of this run. Each entry pairs a sync.Once with its own error slot (see runBatch's use of
		// it) rather than relying on sync.Once.Do's return value, which carries no success/failure
		// signal of its own -- every job blocked on the same Do call must see the triggering call's
		// own error, not silently proceed as if setup had succeeded.
		prepareRunOnce := make(map[string]*prepareRunState, len(jobs))
		for _, j := range jobs {
			if j.cmd.PrepareRun == "" {
				continue
			}
			key := j.linterName + "/" + j.cmd.Name
			if _, ok := prepareRunOnce[key]; !ok {
				prepareRunOnce[key] = &prepareRunState{}
			}
		}

		// cmdSems holds one buffered channel (used as a counting semaphore) per (linter,command)
		// pair whose Command.MaxConcurrency is positive -- built once, up front, for the same
		// reason prepareRunOnce is: a plain map is safe to read concurrently once every worker
		// goroutine has started, since nothing adds or removes keys after this point. Keyed by
		// linter+command (not linter alone) so two different commands on the same linter, each
		// capped, are never blocked by each other's cap -- only by their own.
		cmdSems := make(map[string]chan struct{}, len(jobs))
		for _, j := range jobs {
			if j.cmd.MaxConcurrency <= 0 {
				continue
			}
			key := j.linterName + "/" + j.cmd.Name
			if _, ok := cmdSems[key]; !ok {
				cmdSems[key] = make(chan struct{}, j.cmd.MaxConcurrency)
			}
		}

		var inPlaceMu sync.Mutex
		var wg sync.WaitGroup
		for i := 0; i < concurrency; i++ {
			wg.Go(func() {
				for j := range jobCh {
					if ctx.Err() != nil {
						return
					}
					runJob(ctx, j, states[j.linterName], repoRoot, &inPlaceMu, prepareRunOnce, cmdSems, events, env.Log)
				}
			})
		}
		wg.Wait()
	}()
	if env.Log == nil {
		return events, nil
	}
	// Every terminal event -- including the ones buildJobs sends directly -- passes through here
	// on its way out, so logging them needs no change at each of those send sites.
	logged := make(chan Event)
	go func() {
		defer close(logged)
		for ev := range events {
			logLinterEnd(env.Log, ev)
			logged <- ev
		}
	}()
	return logged, nil
}

// buildJobs resolves name's matched files and queues one job per runnable command invocation
// (include selects which commands are runnable), emitting a Skipped event immediately for every
// command an unsupported feature rules out (var, output format, an unresolvable Parser.Runtime,
// SandboxType/RunFrom attempt real resolution instead of a blanket skip too, per v0.3.2) and a
// Failed event (returning no jobs) if matching files or resolving tools errors outright. Shim
// resolution -- which may download a tool, or (separately) a Command.Parser's own runtime -- runs
// at most once per linter (per distinct Parser.Runtime, for the parser case), lazily, on the first
// command that needs it.
func buildJobs(ctx context.Context, cfg config.Config, root, cacheDir, repoRoot, name string, linter config.Linter, files []string, failed map[string]error, healthChecked map[string]error, include func(config.Command) bool, dryRun bool, events chan<- Event, log *runlog.Writer) []job {
	relFiles := make([]string, len(files))
	for i, f := range files {
		if rel, err := filepath.Rel(repoRoot, f); err == nil {
			relFiles[i] = rel
		} else {
			relFiles[i] = f
		}
	}

	var jobs []job
	var pathEnv string
	pathEnvResolved := false
	versions := toolVersions(cfg, linter.Tools)
	emit := func(ev Event) { events <- ev }
	parserPathEnvByRuntime := map[string]string{}

	for _, cmd := range selectApplicableCommands(cfg, linter) {
		if !include(cmd) {
			continue
		}
		if cmd.Enabled != nil && !*cmd.Enabled {
			events <- Event{Linter: name, Phase: Skipped, Note: "disabled by its own plugin source", Files: relFiles}
			continue
		}
		if v, ok := findUnsupportedVar(cmd.Run); ok {
			events <- Event{Linter: name, Phase: Skipped, Note: fmt.Sprintf("unsupported template var %q", v), Files: relFiles}
			continue
		}
		if !supportedOutputFormats[cmd.Output] {
			events <- Event{Linter: name, Phase: Skipped, Note: fmt.Sprintf("unsupported output format %q", cmd.Output), Files: relFiles}
			continue
		}
		if cmd.Formatter && !cmd.InPlace && (cmd.Output == "rewrite" || cmd.Output == "shfmt") && cmd.SandboxType != "" {
			events <- Event{Linter: name, Phase: Skipped, Note: "stdin/stdout formatter combined with sandbox_type is not supported (the declared sandbox would be silently ignored)", Files: relFiles}
			continue
		}

		var parserPathEnv string
		if cmd.Parser != nil {
			if v, ok := findUnsupportedParserVar(cmd.Parser.Run); ok {
				events <- Event{Linter: name, Phase: Skipped, Note: fmt.Sprintf("unsupported template var %q in parser", v), Files: relFiles}
				continue
			}
			dir, cached := parserPathEnvByRuntime[cmd.Parser.Runtime]
			if !cached {
				resolved, err := resolveRuntimeShimDir(cfg, root, cacheDir, repoRoot, cmd.Parser.Runtime, failed, emit)
				if err != nil {
					events <- Event{Linter: name, Phase: Skipped, Note: fmt.Sprintf("parser runtime %q unavailable: %v", cmd.Parser.Runtime, err), Files: relFiles}
					continue
				}
				dir = resolved
				parserPathEnvByRuntime[cmd.Parser.Runtime] = dir
			}
			parserPathEnv = dir
		}

		if cmd.InPlace && cmd.SandboxType != "" {
			events <- Event{Linter: name, Phase: Skipped, Note: "in_place command combined with sandbox_type is unsupported (writes would be lost)", Files: relFiles}
			continue
		}
		if cmd.SandboxType != "" && cmd.SandboxType != "copy_targets" && cmd.SandboxType != "expanded" {
			events <- Event{Linter: name, Phase: Skipped, Note: fmt.Sprintf("unsupported sandbox_type %q", cmd.SandboxType), Files: relFiles}
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
			events <- Event{Linter: name, Phase: Skipped, Note: fmt.Sprintf("unsupported run_from %q", cmd.RunFrom), Files: relFiles}
			continue
		}

		if cmd.Target != "" && cmd.Target != "${file}" && cmd.Target != "${parent}" {
			events <- Event{Linter: name, Phase: Skipped, Note: fmt.Sprintf("unsupported target %q", cmd.Target), Files: relFiles}
			continue
		}

		if !pathEnvResolved {
			shimDirs, err := resolveShimDirs(ctx, cfg, root, cacheDir, repoRoot, linter.Tools, failed, healthChecked, emit, log)
			if err != nil {
				events <- Event{Linter: name, Phase: Failed, Note: "resolving tools", Err: err, Files: relFiles}
				return nil
			}
			pathEnv = strings.Join(shimDirs, string(os.PathListSeparator))
			pathEnvResolved = true
		}

		for _, dir := range sortedKeys(groups) {
			relFiles := groups[dir]
			targets := relFiles
			if cmd.Target == "${parent}" {
				// Tools like golangci-lint must see a whole package: handed a lone file they
				// compile it without its siblings and report bogus "undefined:" errors.
				targets = parentDirs(relFiles)
			}
			var batches [][]string
			if cmd.Batch || !strings.Contains(cmd.Run, "${target}") {
				// A Run string with no ${target} placeholder can't distinguish between files --
				// running it once per matched file (Batch: false's default) would just repeat
				// the exact same invocation N times, reporting the exact same findings N times
				// (real catalog examples: tflint's and brakeman's first commands). One invocation
				// per resolved directory is what such a command can actually tell apart.
				batches = [][]string{targets}
			} else {
				for _, f := range targets {
					batches = append(batches, []string{f})
				}
			}
			for _, batch := range batches {
				jobs = append(jobs, job{
					linterName: name, linter: linter, cmd: cmd, batch: batch,
					pathEnv: pathEnv, parserPathEnv: parserPathEnv, resolvedDir: dir, toolVersions: versions, files: relFiles, dryRun: dryRun,
				})
			}
		}
	}
	return jobs
}

// parentDirs maps files (relative to one resolved directory) to their sorted, deduplicated
// parent directories, "." standing for the resolved directory itself.
func parentDirs(files []string) []string {
	seen := map[string]bool{}
	var dirs []string
	for _, f := range files {
		if d := filepath.Dir(f); !seen[d] {
			seen[d] = true
			dirs = append(dirs, d)
		}
	}
	sort.Strings(dirs)
	return dirs
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
// cmd.Run itself. ${exit_code} is the one var valid ONLY here, never in cmd.Run: real prettier's
// own parser.run is `python3 ${plugin}/linters/prettier/prettier_to_sarif.py ${exit_code}`,
// passing the real command's own exit code so the converter script can distinguish "ran clean"
// (0) from "reformatted" (prettier's own success_codes: [0, 2]) from a genuine tool error.
func findUnsupportedParserVar(run string) (string, bool) {
	for _, v := range templateVarRE.FindAllString(run, -1) {
		switch v {
		case "${target}", "${plugin}", "${cwd}", "${exit_code}":
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
func runJob(ctx context.Context, j job, state *linterState, repoRoot string, inPlaceMu *sync.Mutex, prepareRunOnce map[string]*prepareRunState, cmdSems map[string]chan struct{}, events chan<- Event, log *runlog.Writer) {
	state.mu.Lock()
	if state.failed {
		state.mu.Unlock()
		return
	}
	state.mu.Unlock()

	events <- Event{Linter: j.linterName, Phase: Running, File: strings.Join(j.batch, ", ")}
	id := log.NextID()
	findings, changedFiles, err := runBatch(ctx, j, repoRoot, inPlaceMu, prepareRunOnce, cmdSems, log, id)
	events <- Event{Linter: j.linterName, Phase: JobDone, File: strings.Join(j.batch, ", ")}
	if err == nil && len(findings) > 0 {
		log.Emit(runlog.Event{T: runlog.KindFindings, ID: id, Linter: j.linterName, Findings: findings})
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	state.remaining--
	if state.terminalSent {
		return
	}

	if err != nil {
		state.failed = true
		state.terminalSent = true
		events <- Event{Linter: j.linterName, Phase: Failed, Note: j.cmd.Name, Err: err, Files: j.files}
		return
	}

	state.findings = append(state.findings, findings...)
	state.changedFiles = dedupeStrings(append(state.changedFiles, changedFiles...))
	if state.remaining == 0 {
		state.terminalSent = true
		events <- Event{Linter: j.linterName, Phase: Done, Findings: state.findings, ChangedFiles: state.changedFiles, Files: j.files}
	}
}

// runBatch runs one job's invocation and parses its output per cmd.Output, remapping every
// finding's File back to repoRoot-relative before returning. If j.cmd.SandboxType is set, the
// invocation actually runs against a temporary staged copy (see security.StageSandbox); the
// parser only ever sees paths relative to j.resolvedDir, exactly as when no sandboxing is
// involved -- security.RemapFindings is what turns those back into repoRoot-relative paths either
// way. The second return value is the repoRoot-relative subset of j.batch this command actually
// changed on disk: for an InPlace command, via hashFiles' before/after comparison; for a
// stdin/stdout formatter (Formatter && !InPlace && rewrite/shfmt), via runStdinFormatter's own
// before/after string comparison instead (delegated to it below, before any of the InPlace-only
// machinery here runs) -- nil for every other command shape. inPlaceMu serializes every InPlace
// invocation, and runStdinFormatter's own writes, across the whole run
// (see the lock acquired below) so two of them can never interleave their before-hash/invoke/
// after-hash cycle over the same file. A dry run (job.dryRun) never reaches the real file at all
// -- see the sandboxType computation below. If j.cmd.PrepareRun is set, it runs -- exactly once
// per (linter,command), across every job/batch this command's own Run ever gets split into (see
// prepareRunOnce/prepareRunState) -- before anything else here: a killed/failed PrepareRun fails
// every job for this command, not just whichever one happened to trigger it. If j.cmd
// declares a positive MaxConcurrency, cmdSems holds a counting semaphore (keyed by
// linter+command, see Run) this call acquires before its own real invocation and releases on
// return -- capping how many of this specific command's invocations run at once, independent of
// (and never blocking) any other command's own cap.
func runBatch(ctx context.Context, j job, repoRoot string, inPlaceMu *sync.Mutex, prepareRunOnce map[string]*prepareRunState, cmdSems map[string]chan struct{}, log *runlog.Writer, id int) ([]output.Finding, []string, error) {
	// run_timeout is only parsed here, NOT yet applied to ctx: wrapping ctx this early would charge
	// a job's own timeout budget for time spent waiting on prepare_run (a separate, shared setup
	// step, see below) or queued on max_concurrency's semaphore (see below) -- neither is the
	// command's own real invocation, which is the only thing run_timeout is meant to bound. The
	// actual wrap happens right before that real invocation, further down.
	var runTimeout time.Duration
	hasRunTimeout := false
	if j.linter.RunTimeout != "" {
		if d, err := time.ParseDuration(j.linter.RunTimeout); err == nil {
			runTimeout, hasRunTimeout = d, true
		}
		// An unparseable run_timeout value is silently ignored here, matching how a malformed
		// value in the real catalog would already have been accepted by config.Resolve (no
		// existing validation rejects it) -- this task adds consumption of the field, not new
		// validation of it. If a future task adds config-time validation, revisit this.
	}

	if j.cmd.PrepareRun != "" {
		state := prepareRunOnce[j.linterName+"/"+j.cmd.Name]
		state.once.Do(func() {
			// prepare_run gets its own independent run_timeout-bounded context, not a wrap shared
			// with (and so eaten into by) the triggering job's own real invocation below -- a slow
			// setup step should not shrink the budget the command itself gets to run in.
			prepareCtx := ctx
			if hasRunTimeout {
				var cancel context.CancelFunc
				prepareCtx, cancel = context.WithTimeout(ctx, runTimeout)
				defer cancel()
			}
			pluginDir := j.linter.SourceRoot
			cwdDir := filepath.Join(j.linter.SourceRoot, j.linter.SourceDir)
			setupCmd := j.cmd
			setupCmd.Run = j.cmd.PrepareRun
			setupID := log.NextID()
			inv := runlog.Event{T: runlog.KindInvocation, ID: setupID, Linter: j.linterName, Sandbox: ""}
			_, stderrOut, exitCode, err := runOneInvocation(prepareCtx, setupCmd, j.resolvedDir, j.pathEnv, nil, pluginDir, cwdDir, log, inv, "")
			switch {
			case err != nil:
				state.err = err
			case exitCode < 0:
				// Mirrors the exitCode < 0 guard on the command's own real invocation below: a
				// negative exit code means the process was killed (e.g. prepareCtx's own timeout,
				// set up above) or crashed, not a normal exit.
				state.err = fmt.Errorf("engine: %s: %s: prepare_run process did not exit cleanly (killed or crashed)", j.linterName, j.cmd.Name)
			case exitCode != 0:
				// PrepareRun has no output to parse -- exit-code only: any non-zero exit fails
				// every job for this command, not just this one.
				msg := strings.TrimSpace(stderrOut)
				state.err = fmt.Errorf("engine: %s: %s: prepare_run exited %d: %s", j.linterName, j.cmd.Name, exitCode, msg)
			}
		})
		if state.err != nil {
			return nil, nil, state.err
		}
	}

	// max_concurrency's semaphore is acquired on the PARENT ctx -- still unwrapped by run_timeout
	// at this point -- so a job can wait for a free slot as long as the overall run itself isn't
	// canceled, without burning its own per-invocation timeout budget while merely queued. Only the
	// overall ctx being canceled (or its own real invocation's later timeout, once applied below)
	// can end this wait.
	if sem, ok := cmdSems[j.linterName+"/"+j.cmd.Name]; ok {
		select {
		case sem <- struct{}{}:
			defer func() { <-sem }()
		case <-ctx.Done():
			return nil, nil, fmt.Errorf("engine: %s: %s: %w", j.linterName, j.cmd.Name, ctx.Err())
		}
	}

	// Only now -- after prepare_run and after the semaphore acquire, so the timeout budget starts
	// counting from when the command actually gets to run, not from when it started queuing -- does
	// run_timeout wrap ctx for the command's own real invocation below.
	if hasRunTimeout {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, runTimeout)
		defer cancel()
	}

	// A stdin/stdout formatter (Formatter, not InPlace, rewrite/shfmt output) never touches its
	// own target directly -- it reads content on stdin and writes the reformatted result to its
	// own stdout, so none of the InPlace hashing/sandboxing/mutex machinery below applies; it gets
	// its own dedicated per-file loop instead.
	if j.cmd.Formatter && !j.cmd.InPlace && (j.cmd.Output == "rewrite" || j.cmd.Output == "shfmt") {
		pluginDir := j.linter.SourceRoot
		cwdDir := filepath.Join(j.linter.SourceRoot, j.linter.SourceDir)
		return runStdinFormatter(ctx, j, repoRoot, pluginDir, cwdDir, inPlaceMu, log, id)
	}

	workDir := j.resolvedDir
	// A dry run stages InPlace commands into a throwaway sandbox copy regardless of the command's
	// own SandboxType (always empty in practice for InPlace commands -- see the InPlace+SandboxType
	// skip in buildJobs) so the real file is never touched, while ChangedFiles (computed below via
	// the exact same before/after hash comparison a real run already uses) still reports what
	// would have changed.
	//
	sandboxType := j.cmd.SandboxType
	tmpBase := ""
	if j.dryRun && j.cmd.InPlace {
		sandboxType = "copy_targets"
		// Stage inside repoRoot (not the OS default temp dir) so a config-driven formatter's own
		// ancestor-directory config walk (e.g. real prettier's own algorithm, confirmed via direct
		// reproduction with the real prettier binary) still finds real project config living at
		// repoRoot -- staging in /tmp has no path back to the real repo tree at all, so ANY
		// formatter whose config differs from its own built-in defaults would see a phantom diff
		// forever, on every dry-run check, even for an already-correctly-formatted file. This does
		// NOT recover a config file living strictly between the file's own directory and repoRoot
		// (a narrower, real, but much less common case) -- repoRoot-level config is the standard,
		// near-universal convention for essentially every real formatter.
		//
		// Known limitation: copy_targets stages only the batch's own target files, not any
		// intermediate-directory config file living strictly between the file's own directory and
		// repoRoot (e.g. a nested per-package .prettierrc override) -- staging inside repoRoot
		// (tmpBase, above) recovers repoRoot-level config via a genuine ancestor walk, but a config
		// file living in some directory between the target and repoRoot is still invisible to the
		// sandbox. This is a narrower residual case than before; repoRoot-level config is the
		// standard, near-universal convention for essentially every real formatter, so this is now
		// expected to be rare in practice rather than the common case it was before this fix.
		tmpBase = repoRoot
	}
	if sandboxType != "" {
		sandboxDir, cleanup, err := security.StageSandbox(sandboxType, j.resolvedDir, j.batch, tmpBase)
		if cleanup != nil {
			defer cleanup()
		}
		if err != nil {
			return nil, nil, err
		}
		workDir = sandboxDir
	}
	defer linkDirectConfigs(repoRoot, workDir, j.linter.DirectConfigs)()

	// Two InPlace commands (same or different linters) touching overlapping files must not
	// interleave their before-hash/invoke/after-hash cycle, or one's write can silently clobber
	// or be clobbered by the other's -- confirmed by real reproduction (25 runs, 5 lost a
	// formatter's write entirely) with two concurrent InPlace commands over the same file.
	// Serializing every InPlace invocation (not just per-file) is the simplest correct fix; a
	// per-path lock is the natural upgrade if this measurably limits throughput on a large
	// in_place run.
	if j.cmd.InPlace {
		inPlaceMu.Lock()
		defer inPlaceMu.Unlock()
	}

	pluginDir := j.linter.SourceRoot
	cwdDir := filepath.Join(j.linter.SourceRoot, j.linter.SourceDir)

	var beforeHashes map[string][32]byte
	if j.cmd.InPlace {
		beforeHashes = hashFiles(workDir, j.batch)
	}

	readFrom := outputSource(j.cmd)
	inv := runlog.Event{T: runlog.KindInvocation, ID: id, Linter: j.linterName, ToolVersions: j.toolVersions, Sandbox: sandboxType}
	out, stderr, exitCode, err := runOneInvocation(ctx, j.cmd, workDir, j.pathEnv, j.batch, pluginDir, cwdDir, log, inv, "")
	if err != nil {
		return nil, nil, err
	}
	// A negative exitCode means the process was killed or crashed rather than exiting normally
	// (Go's ExitCode() reports -1 for a signal-terminated process, e.g. ctx.WithTimeout's kill
	// above, or a top-level ctx cancellation) -- mirrors the same guard runStdinFormatterFile
	// already has. Without this, a killed pass_fail command's exitCode!=0 would fall through to
	// ParsePassFail below and get silently reported as "found violations" instead of the tool
	// having been killed, and a killed JSON-output command's empty stdout would fall through to
	// the isJSONFormat empty-output guard and get silently reported as a clean run -- both would
	// make RunTimeout effectively invisible for every non-formatter command shape.
	if exitCode < 0 {
		return nil, nil, fmt.Errorf("engine: %s: %s: process did not exit cleanly (killed or crashed)", j.linterName, j.cmd.Name)
	}
	if msg, failed := commandFailed(j.cmd, out, stderr, exitCode); failed {
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
		// changedFiles entries are j.batch's own resolvedDir-relative names (see the job doc
		// comment), not workDir-relative -- remap against j.resolvedDir, not workDir, exactly like
		// the finding-path remap below does. Before DryRun, an InPlace command's workDir was always
		// j.resolvedDir itself (SandboxType+InPlace is skipped in buildJobs), so this was a no-op
		// distinction; DryRun is the first case where workDir is a sandbox unrelated to repoRoot,
		// where joining it with a resolvedDir-relative name would produce a bogus path escaping
		// repoRoot entirely.
		changedFiles = remapPaths(changedFiles, j.resolvedDir, repoRoot)
	}

	// A Parser converts the real command's raw output into cmd.Output's expected shape (almost
	// always SARIF) before any of the dispatch below runs -- everything from here on parses out
	// exactly as if the real tool had produced it directly, whether or not a parser was involved.
	// Skipped when out is empty: a genuinely clean run (or an OS-gated command variant producing
	// nothing) must fall through to the isJSONFormat empty-output guard below unparsed, not feed
	// empty stdin to a converter script that may not tolerate it (e.g. Python's
	// json.load(sys.stdin) raises on empty input).
	if j.cmd.Parser != nil && strings.TrimSpace(out) != "" {
		converted, err := runParser(ctx, j.cmd.Parser, workDir, j.parserPathEnv, out, j.batch, pluginDir, cwdDir, exitCode, log, runlog.Event{T: runlog.KindParser, ID: id, StdinFrom: readFrom})
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
		output.ApplyIsSecurity(findings, j.cmd.IsSecurity)
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
	output.ApplyIsSecurity(findings, j.cmd.IsSecurity)
	return findings, changedFiles, nil
}

// commandFailed reports whether exitCode is a genuine failure for cmd: a declared ErrorCodes
// match, or -- for rewrite/shfmt output specifically -- an exit code outside a declared
// SuccessCodes list (real catalog formatters specify SuccessCodes, not ErrorCodes; without this
// second check, an exit code outside SuccessCodes fell through as a clean success with the
// failure completely swallowed, stderr included). Other Output formats already have their own
// separate failure signal (pass_fail turns a nonzero exit into a finding; sarif/json formats fail
// to parse on garbage output), so the second check is scoped to rewrite/shfmt only. msg is
// out/stderrOut combined and trimmed, for the caller's own error text.
func commandFailed(cmd config.Command, out, stderrOut string, exitCode int) (msg string, failed bool) {
	successMismatch := (cmd.Output == "rewrite" || cmd.Output == "shfmt") &&
		len(cmd.SuccessCodes) > 0 && !slices.Contains(cmd.SuccessCodes, exitCode)
	if !slices.Contains(cmd.ErrorCodes, exitCode) && !successMismatch {
		return "", false
	}
	msg = strings.TrimSpace(out)
	if errText := strings.TrimSpace(stderrOut); errText != "" {
		if msg == "" {
			msg = errText
		} else {
			msg += "\n" + errText
		}
	}
	return msg, true
}

// runStdinFormatter runs a stdin/stdout-only formatter (Formatter && !InPlace, Output
// rewrite/shfmt): the tool reads a file's content from stdin (or, for a command whose Run string
// reads ${target} itself instead -- real catalog examples: opa fmt, perltidy -se, pragma-once's
// fix.sh -- reads the file directly) and writes the reformatted result to its own stdout, rather
// than rewriting the file directly. One invocation per file in j.batch: stdin only ever carries
// one file's content, so a batch grouped by buildJobs into more than one file (real catalog shape:
// a Run string with no ${target} at all, e.g. terraform fmt's `terraform fmt -no-color -`, groups
// every matched file in a directory into one job) still gets one invocation per file here, each
// its own runlog id.
//
// Known limitation: unlike the InPlace dry-run path, there is no sandbox for this shape (buildJobs
// already skips SandboxType combined with it) -- a command whose Run reads ${target} itself still
// reads/writes side effects (e.g. perltidy's own .LOG file) against the real resolvedDir even
// during a dry run. Narrower than it sounds: rtunk itself never writes the target file's own
// content during a dry run either way (that part is always safe); only a tool's own side files, if
// it has any, would land for real. Add a sandbox for this shape too if that turns out to matter.
func runStdinFormatter(ctx context.Context, j job, repoRoot, pluginDir, cwdDir string, inPlaceMu *sync.Mutex, log *runlog.Writer, id int) ([]output.Finding, []string, error) {
	var changedFiles []string
	for i, f := range j.batch {
		if i > 0 {
			id = log.NextID() // the caller reserved one id for this job; each further invocation needs its own
		}
		changed, err := runStdinFormatterFile(ctx, j, f, pluginDir, cwdDir, inPlaceMu, log, id)
		if err != nil {
			return nil, nil, err
		}
		if changed {
			changedFiles = append(changedFiles, f)
		}
	}
	return nil, remapPaths(changedFiles, j.resolvedDir, repoRoot), nil
}

// runStdinFormatterFile is runStdinFormatter's own per-file body: read, invoke, validate, write.
// Guarded by inPlaceMu (skipped for j.dryRun, which never writes) for the file's own
// read-invoke-write span, exactly like the InPlace path's own whole-invocation lock -- without it,
// a concurrent InPlace command on the same file (real catalog overlap: an in-place formatter and a
// stdin one both enabled for the same file type) could write in the gap between this function's
// own read and its later write, and get silently clobbered by this function writing back content
// computed from what is now stale data (the same lost-update bug the InPlace-vs-InPlace mutex
// already prevents for that path).
//
// Two failure modes specific to this shape, neither caught by commandFailed's existing
// ErrorCodes/SuccessCodes checks alone, get their own explicit guard before anything is written:
// a negative exit code (the process was killed, e.g. by ctx's own cancellation/timeout -- its
// stdout is a truncated partial read, never the real result) and empty stdout for a non-empty
// file (almost never a real, intentional "delete everything" from a formatter; far more likely the
// tool wrote its result somewhere else, or to a stream this shape doesn't read).
func runStdinFormatterFile(ctx context.Context, j job, f, pluginDir, cwdDir string, inPlaceMu *sync.Mutex, log *runlog.Writer, id int) (changed bool, err error) {
	if !j.dryRun {
		inPlaceMu.Lock()
		defer inPlaceMu.Unlock()
	}
	path := filepath.Join(j.resolvedDir, f)
	before, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	inv := runlog.Event{T: runlog.KindInvocation, ID: id, Linter: j.linterName, ToolVersions: j.toolVersions}
	out, stderrOut, exitCode, err := runOneInvocation(ctx, j.cmd, j.resolvedDir, j.pathEnv, []string{f}, pluginDir, cwdDir, log, inv, string(before))
	if err != nil {
		return false, err
	}
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if exitCode < 0 {
		return false, fmt.Errorf("engine: %s: %s on %s: process did not exit cleanly (killed or crashed)", j.linterName, j.cmd.Name, f)
	}
	if msg, failed := commandFailed(j.cmd, out, stderrOut, exitCode); failed {
		return false, fmt.Errorf("engine: %s: %s on %s exited %d: %s", j.linterName, j.cmd.Name, f, exitCode, msg)
	}
	if out == "" && len(before) > 0 {
		return false, fmt.Errorf("engine: %s: %s on %s: empty output for a non-empty file, refusing to write", j.linterName, j.cmd.Name, f)
	}
	if out == string(before) {
		return false, nil
	}
	if !j.dryRun {
		// 0600: os.WriteFile only applies this mode when creating a new file -- an existing target
		// (the normal case) keeps its own permissions untouched.
		if err := os.WriteFile(path, []byte(out), 0o600); err != nil {
			return false, err
		}
	}
	return true, nil
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

// forwardInstall translates a download's events into engine Install* events through emit and
// returns the first failure's error (as the callers always did). Cached refs emit nothing, and a
// Done or Failed only closes an item whose InstallStart was emitted (a tool whose runtime failed
// first gets a Failed for a ref that never started), so starts and dones stay balanced.
func forwardInstall(evs <-chan download.Event, emit func(Event)) error {
	var first error
	for _, err := range forwardInstallAll(evs, emit, false) {
		if first == nil {
			first = err
		}
	}
	return first
}

// forwardInstallAll drains evs, emitting Install* events, and returns each failed item's error
// keyed by "category/id". With closeUnstarted a failed item that never started still gets its
// InstallDone, for callers that announced it with InstallPlanned.
func forwardInstallAll(evs <-chan download.Event, emit func(Event), closeUnstarted bool) map[string]error {
	started := map[string]bool{}
	failed := map[string]error{}
	for ev := range evs {
		item := ev.Ref.Category + "/" + ev.Ref.ID
		switch ev.Phase {
		case download.Started:
			started[item] = true
			emit(Event{Phase: InstallStart, Item: item, BytesTotal: -1})
		case download.Progress:
			emit(Event{Phase: InstallProgress, Item: item, Bytes: ev.Bytes, BytesTotal: ev.Total})
		case download.Done:
			if started[item] {
				emit(Event{Phase: InstallDone, Item: item})
			}
		case download.Failed:
			if started[item] || closeUnstarted {
				emit(Event{Phase: InstallDone, Item: item})
			}
			if failed[item] == nil {
				failed[item] = ev.Err
			}
		}
	}
	return failed
}

// requiredRefs is the tool and parser-runtime refs linter needs to run any of its commands
// selected by include -- nothing when none is runnable, so a `fmt` run fetches no check-only tool.
func requiredRefs(linter config.Linter, include func(config.Command) bool) []download.Ref {
	var refs []download.Ref
	runnable := false
	for _, cmd := range linter.Commands {
		if !include(cmd) || (cmd.Enabled != nil && !*cmd.Enabled) {
			continue
		}
		runnable = true
		if cmd.Parser != nil {
			refs = append(refs, download.Ref{Category: "runtimes", ID: cmd.Parser.Runtime})
		}
	}
	if !runnable {
		return nil
	}
	for _, id := range linter.Tools {
		refs = append(refs, download.Ref{Category: "tools", ID: id})
	}
	return refs
}

// prefetch downloads every missing ref in parallel (download.Download's own bound), announcing the
// item count first so the live view's total is fixed before the first install starts. It returns
// each failed item's error keyed by "category/id"; those linters then fail in resolveShimDirs
// without a second attempt.
func prefetch(cfg config.Config, root, cacheDir, repoRoot string, refs []download.Ref, emit func(Event)) map[string]error {
	// Same "is it there" test as resolveShimDirs/resolveRuntimeShimDir: a shim on disk wins.
	var missing []download.Ref
	for _, r := range refs {
		var shim string
		switch tool, rt := cfg.Tools[r.ID], cfg.Runtimes.Definitions[r.ID]; r.Category {
		case "tools":
			shim = download.ShimPath(root, "tools", r.ID, download.ResolveVersion(cfg.Lint.Enabled, r.ID, tool.KnownGoodVersion), r.ID)
		case "runtimes":
			if len(rt.Shims) > 0 {
				shim = download.ShimPath(root, "runtimes", r.ID, download.ResolveVersion(cfg.Runtimes.Enabled, r.ID, rt.KnownGoodVersion), rt.Shims[0])
			}
		}
		if _, err := os.Stat(shim); shim == "" || err != nil {
			missing = append(missing, r)
		}
	}
	pending := download.Pending(cfg, root, missing...)
	if len(pending) == 0 {
		return nil
	}
	emit(Event{Phase: InstallPlanned, Total: len(pending)})
	evs, err := download.Download(cfg, cacheDir, repoRoot, pending...)
	if err != nil {
		failed := map[string]error{}
		for _, r := range pending {
			failed[r.Category+"/"+r.ID] = err
			emit(Event{Phase: InstallDone, Item: r.Category + "/" + r.ID})
		}
		return failed
	}
	return forwardInstallAll(evs, emit, true)
}

// resolveShimDirs resolves (downloading first if not already cached) every tool id's shim, and
// returns the directory each shim lives in -- a Command.Run string references its tool(s) by bare
// name, so those directories become the PATH prefix that lets `sh -c` find them.
func resolveShimDirs(ctx context.Context, cfg config.Config, root, cacheDir, repoRoot string, toolIDs []string, failed map[string]error, healthChecked map[string]error, emit func(Event), log *runlog.Writer) ([]string, error) {
	dirs := make([]string, 0, len(toolIDs))
	for _, id := range toolIDs {
		tool, ok := cfg.Tools[id]
		if !ok {
			return nil, fmt.Errorf("engine: tool %q referenced but not found in resolved config", id)
		}
		version := download.ResolveVersion(cfg.Lint.Enabled, id, tool.KnownGoodVersion)
		shimPath := download.ShimPath(root, "tools", id, version, id)
		if err := failed["tools/"+id]; err != nil {
			return nil, err // already attempted (and reported) by prefetch
		}
		if _, statErr := os.Stat(shimPath); statErr != nil {
			evs, err := download.Download(cfg, cacheDir, repoRoot, download.Ref{Category: "tools", ID: id, Version: version})
			if err != nil {
				return nil, err
			}
			if err := forwardInstall(evs, emit); err != nil {
				return nil, err
			}
		}
		download.Touch(root, "tools", id, version)

		shimDir := filepath.Dir(shimPath)
		if len(tool.HealthChecks) > 0 {
			if err, checked := healthChecked[id]; checked {
				if err != nil {
					return nil, err
				}
			} else {
				err := runToolHealthChecks(ctx, tool, id, shimDir, repoRoot, log)
				healthChecked[id] = err
				if err != nil {
					return nil, err
				}
			}
		}

		dirs = append(dirs, shimDir)
	}
	return dirs, nil
}

// runToolHealthChecks runs every one of tool's declared HealthChecks (exit-code-only, no output
// parsing -- see Tool.HealthChecks's own doc comment), using shimDir as the PATH prefix so the
// check invokes the tool this install just resolved, not whatever else might be on PATH. A tool
// has no plugin-source directory of its own (unlike a Linter/Action), so pluginDir/cwdDir are
// empty and repoRoot is used as the invocation's own working directory.
func runToolHealthChecks(ctx context.Context, tool config.Tool, toolID, shimDir, repoRoot string, log *runlog.Writer) error {
	for _, check := range tool.HealthChecks {
		checkCmd := config.Command{Name: "health_check", Run: check.Command}
		id := log.NextID()
		inv := runlog.Event{T: runlog.KindInvocation, ID: id, Linter: toolID, Sandbox: ""}
		_, stderrOut, exitCode, err := runOneInvocation(ctx, checkCmd, repoRoot, shimDir, nil, "", "", log, inv, "")
		if err != nil {
			return err
		}
		if exitCode != 0 {
			msg := strings.TrimSpace(stderrOut)
			return fmt.Errorf("engine: tool %q: health check failed (exit %d): %s", toolID, exitCode, msg)
		}
	}
	return nil
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
func resolveRuntimeShimDir(cfg config.Config, root, cacheDir, repoRoot, runtimeID string, failed map[string]error, emit func(Event)) (string, error) {
	if err := failed["runtimes/"+runtimeID]; err != nil {
		return "", err // already attempted (and reported) by prefetch
	}
	started := false
	return download.ResolveRuntimeShimDir(cfg, root, cacheDir, repoRoot, runtimeID, func(ev download.Event) {
		// Matches forwardInstallAll's own Phase-translation exactly (as called via forwardInstall,
		// i.e. closeUnstarted=false): Done and Failed both only emit InstallDone for an item whose
		// InstallStart already went out, so a Cached event (which never starts) never produces a
		// stray InstallDone with no matching InstallStart.
		item := ev.Ref.Category + "/" + ev.Ref.ID
		switch ev.Phase {
		case download.Started:
			started = true
			emit(Event{Phase: InstallStart, Item: item, BytesTotal: -1})
		case download.Progress:
			emit(Event{Phase: InstallProgress, Item: item, Bytes: ev.Bytes, BytesTotal: ev.Total})
		case download.Done, download.Failed:
			if started {
				emit(Event{Phase: InstallDone, Item: item})
			}
		}
	})
}

// baseEnvAllow are the glob patterns (path.Match, matched against the upper-cased name) of the
// only variables of rtunk's own environment a linter or its parser sees: HOME, temp files, locale,
// proxy, TLS roots, and the tools' cache/config locations and toolchain switches (dropping those
// sends every tool back to a cold cache, which reads as a hung run). PATH is rebuilt by the caller.
var baseEnvAllow = []string{
	"HOME", "USER", "LOGNAME", "SHELL", "TMPDIR", "TERM", "TZ", "LANG", "LC_*",
	"*PROXY", "SSL_CERT_*",
	"XDG_*", "*CACHE*", "GO*", "CARGO_HOME", "RUSTUP_HOME",
}

// baseEnvDeny wins over baseEnvAllow: a broad allow pattern (GO* also matches GOAUTH) must never
// let a credential through. The keywords are gitleaks' generic-api-key rule
// (cmd/generate/config/rules/generic.go, MIT) plus detect-secrets' "pwd"
// (detect_secrets/plugins/keyword.py, MIT), each matched as a substring of the upper-cased name.
var baseEnvDeny = func() []string {
	keywords := []string{"ACCESS", "API", "AUTH", "KEY", "CREDENTIAL", "CREDS", "PASSWD", "PASSWORD", "PWD", "SECRET", "TOKEN"}
	globs := make([]string, len(keywords))
	for i, k := range keywords {
		globs[i] = "*" + k + "*"
	}
	return globs
}()

func globAny(patterns []string, name string) bool {
	for _, p := range patterns {
		if ok, _ := path.Match(p, name); ok {
			return true
		}
	}
	return false
}

// baseEnv is the part of os.Environ that baseEnvAllow lets through and baseEnvDeny does not.
func baseEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if upper := strings.ToUpper(name); globAny(baseEnvAllow, upper) && !globAny(baseEnvDeny, upper) {
			env = append(env, kv)
		}
	}
	return env
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
//
// A non-nil log records the invocation (inv arrives pre-filled by the caller with its id, linter,
// tool versions and sandbox, and is completed here with the command line actually run), both raw
// output streams, and the exit; a nil log records nothing.
func runOneInvocation(ctx context.Context, cmd config.Command, workDir, pathEnv string, files []string, pluginDir, cwdDir string, log *runlog.Writer, inv runlog.Event, stdin string) (out, stderrOut string, exitCode int, err error) {
	target := strings.Join(download.QuoteAll(files), " ")

	var tmpfile string
	if strings.Contains(cmd.Run, "${tmpfile}") {
		f, err := os.CreateTemp("", "rtunk-check-*")
		if err != nil {
			return "", "", 0, err
		}
		tmpfile = f.Name()
		_ = f.Close() // only the name is used; the child process writes the contents
		defer func() { _ = os.Remove(tmpfile) }()
	}

	run := strings.NewReplacer(
		"${target}", target, "${tmpfile}", tmpfile,
		"${plugin}", download.QuoteOne(pluginDir), "${cwd}", download.QuoteOne(cwdDir),
	).Replace(cmd.Run)

	c := exec.CommandContext(ctx, "sh", "-c", run)
	c.Dir = workDir
	if stdin != "" {
		// Every other caller passes "" and must keep today's stdin (nil -- /dev/null, a character
		// device), not a pipe: a tool that behaves differently when it detects a FIFO on stdin
		// (some auto-read-from-stdin-if-piped tools do) must not see one it was never given.
		c.Stdin = strings.NewReader(stdin)
	}
	path := os.Getenv("PATH")
	if pathEnv != "" {
		path = pathEnv + string(os.PathListSeparator) + path
	}
	c.Env = append(baseEnv(), "PATH="+path)

	inv.Template, inv.Files, inv.Cwd, inv.PathPrefix, inv.Argv = cmd.Run, files, workDir, pathEnv, c.Args
	log.Emit(inv)

	var stdout, stderr strings.Builder
	c.Stdout = &stdout
	c.Stderr = &stderr

	started := time.Now()
	runErr := c.Run()
	code := 0
	if runErr != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](runErr); ok {
			code = exitErr.ExitCode()
		} else {
			return "", "", 0, runErr
		}
	}
	log.Output(inv.ID, "stdout", stdout.String())
	log.Output(inv.ID, "stderr", stderr.String())
	log.Emit(runlog.Event{T: runlog.KindExit, ID: inv.ID, Code: &code, Ms: time.Since(started).Milliseconds(), ParsedFrom: outputSource(cmd)})

	switch outputSource(cmd) {
	case "stderr":
		out = stderr.String()
	case "tmp_file":
		data, readErr := os.ReadFile(tmpfile)
		if readErr != nil {
			return "", stderr.String(), code, readErr
		}
		log.Output(inv.ID, "tmp_file", string(data))
		out = string(data)
	default: // "" or "stdout"
		out = stdout.String()
	}
	return out, stderr.String(), code, nil
}

// outputSource names the stream a command's output is parsed from. markdownlint --json writes
// its report to stderr and its plugin definition (trunk's own parser is built in) never says so,
// so stdout would be empty and every file would look clean.
func outputSource(cmd config.Command) string {
	if cmd.Output == "markdownlint" {
		return cmp.Or(cmd.ReadOutputFrom, "stderr")
	}
	return cmp.Or(cmd.ReadOutputFrom, "stdout")
}

// runParser converts a real command's raw native output into the shape cmd.Output expects, by
// piping it through parser.Run: stdin is stdin (the real command's own raw output, exactly what
// runOneInvocation returned), and the script's own stdout is the result -- the universal contract
// every real trunk-io Command.Parser script uses (confirmed by reading trufflehog_to_sarif.py,
// tfsec/parse.py, and ruff_to_sarif.py in full during this feature's design). ${target}/${plugin}/
// ${cwd} substitute into parser.Run exactly as they do into cmd.Run; workDir is the same directory
// (or sandbox) the real command itself just ran in. parserPathEnv is the parser's own runtime's
// shim directory (e.g. wherever python3 lives), entirely separate from the linter's own pathEnv --
// a parser's runtime need not be any tool the linter itself uses. exitCode is the real command's
// own exit code, substituted for ${exit_code} -- real prettier's own parser.run passes it as a
// bare positional argument (never quoted: it's always digits from strconv.Itoa, and prettier's
// real script parses it as a Python int) so the converter script can tell "ran clean" from
// "reformatted" from a genuine tool error, all of which are non-error exit codes for prettier.
func runParser(ctx context.Context, parser *config.Parser, workDir, parserPathEnv, stdin string, batch []string, pluginDir, cwdDir string, exitCode int, log *runlog.Writer, ev runlog.Event) (string, error) {
	target := strings.Join(download.QuoteAll(batch), " ")
	run := strings.NewReplacer(
		"${target}", target, "${plugin}", download.QuoteOne(pluginDir), "${cwd}", download.QuoteOne(cwdDir),
		"${exit_code}", strconv.Itoa(exitCode),
	).Replace(parser.Run)

	c := exec.CommandContext(ctx, "sh", "-c", run)
	c.Dir = workDir
	c.Stdin = strings.NewReader(stdin)
	path := os.Getenv("PATH")
	if parserPathEnv != "" {
		path = parserPathEnv + string(os.PathListSeparator) + path
	}
	c.Env = append(baseEnv(), "PATH="+path)

	var stdout, stderr strings.Builder
	c.Stdout = &stdout
	c.Stderr = &stderr

	started := time.Now()
	runErr := c.Run()
	var errText string
	if runErr != nil {
		errText = strings.TrimSpace(stderr.String())
		if errText == "" {
			errText = runErr.Error()
		}
	}
	// ev arrives pre-filled by the caller with its id and stdin source; a nil log records nothing.
	if log != nil {
		code := 0
		if exitErr, ok := errors.AsType[*exec.ExitError](runErr); ok {
			code = exitErr.ExitCode()
		} else if runErr != nil {
			code = -1 // the parser could not even be launched
		}
		ev.Template, ev.Argv, ev.Code, ev.Ms, ev.Err = parser.Run, c.Args, &code, time.Since(started).Milliseconds(), errText
		ev.Data, ev.Truncated = runlog.Cap(stdout.String())
		log.Emit(ev)
	}
	if runErr != nil {
		return "", fmt.Errorf("engine: parser: %s", errText)
	}
	return stdout.String(), nil
}

// logLinterEnd records a linter's terminal event (Done, Skipped or Failed) in log. A Running event
// is progress, not an outcome, and the invocation events already cover it.
func logLinterEnd(log *runlog.Writer, ev Event) {
	var phase string
	switch ev.Phase {
	case Done:
		phase = "Done"
	case Skipped:
		phase = "Skipped"
	case Failed:
		phase = "Failed"
	default:
		return
	}
	e := runlog.Event{T: runlog.KindLinterEnd, Linter: ev.Linter, Phase: phase, Note: ev.Note, Changed: ev.ChangedFiles}
	if ev.Err != nil {
		e.Err = ev.Err.Error()
	}
	log.Emit(e)
}

// toolVersions resolves each of ids to the version resolveShimDirs will install and run (an id
// missing from cfg.Tools is left out; resolveShimDirs reports that as its own error).
func toolVersions(cfg config.Config, ids []string) map[string]string {
	if len(ids) == 0 {
		return nil
	}
	out := make(map[string]string, len(ids))
	for _, id := range ids {
		if tool, ok := cfg.Tools[id]; ok {
			out[id] = download.ResolveVersion(cfg.Lint.Enabled, id, tool.KnownGoodVersion)
		}
	}
	return out
}

// selectApplicableCommands narrows linter.Commands to the variant that applies on this host and
// to the linter's already-resolved tool version -- the real catalog commonly declares several
// same-named Command variants gated by Platforms or Version (often several open-ended ">=" ranges
// plus an unversioned fallback, ordered newest-first, where more than one range admits the pinned
// version), so at most one variant per Name survives: the first declared one whose Platforms/
// Version both admit this run, matching MatchEntry's own first-match-per-entry semantics for
// Download variants. Runs before buildJobs' own per-command skip checks, which are for genuine
// misconfigurations (an unsupported var, a disabled command): a platform/version mismatch here is
// normal, expected filtering, not a Skipped-worthy surprise.
func selectApplicableCommands(cfg config.Config, linter config.Linter) []config.Command {
	host, hostOK := download.HostOSName()
	toolVersion := gatingToolVersion(cfg, linter)
	seen := map[string]bool{}
	out := make([]config.Command, 0, len(linter.Commands))
	for _, cmd := range linter.Commands {
		if len(cmd.Platforms) > 0 && (!hostOK || !slices.Contains(cmd.Platforms, host)) {
			continue
		}
		if cmd.Version != "" && toolVersion != "" && !download.VersionSatisfies(cmd.Version, toolVersion) {
			continue
		}
		if seen[cmd.Name] {
			continue
		}
		seen[cmd.Name] = true
		out = append(out, cmd)
	}
	return out
}

// gatingToolVersion is the resolved tool version a Command.Version range is checked against: the
// linter's MainTool if it declares one, else its sole tool when there's exactly one -- the only
// shapes the real catalog uses this field with. Multiple tools with no MainTool have no single
// version to gate on, so Version is left unevaluated (every variant kept) rather than guessed.
func gatingToolVersion(cfg config.Config, linter config.Linter) string {
	id := linter.MainTool
	if id == "" {
		if len(linter.Tools) != 1 {
			return ""
		}
		id = linter.Tools[0]
	}
	tool, ok := cfg.Tools[id]
	if !ok {
		return ""
	}
	return download.ResolveVersion(cfg.Lint.Enabled, id, tool.KnownGoodVersion)
}
