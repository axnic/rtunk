package check

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/xunleii/rtunk/pkg/trunk/config"
	"github.com/xunleii/rtunk/pkg/trunk/download"
)

// Phase is one linter's point in check's run lifecycle.
type Phase int

const (
	Done    Phase = iota // Findings is set (possibly empty -- the linter ran clean)
	Skipped              // an unsupported feature; Note says which -- never an error
	Failed               // the linter's own command errored; Err is set
)

// Event reports one linter's check outcome, streamed on the channel Run returns. A linter with no
// matching files, or whose only commands are formatters (Formatter: true), produces no event at
// all -- there is nothing to report.
type Event struct {
	Linter   string
	Phase    Phase
	Findings []Finding // Done only
	Note     string    // Skipped (why) or Failed (which command)
	Err      error     // Failed only
}

// templateVarRE matches every ${...} placeholder in a Command.Run string.
var templateVarRE = regexp.MustCompile(`\$\{[^}]*\}`)

// Run executes every enabled linter's non-formatter commands against the files matched under
// paths (repoRoot is the default walk root when paths is empty, and every command's working
// directory), downloading any missing tool shim first, and streams one Event per linter that had
// something to report. cfg is expected already enabled+used-trimmed (config.Resolve's output).
func Run(cfg config.Config, cacheDir, repoRoot string, paths []string) (<-chan Event, error) {
	root, err := download.Root(cacheDir)
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		paths = []string{repoRoot}
	}

	events := make(chan Event)
	go func() {
		defer close(events)
		for name, linter := range cfg.Lint.Definitions {
			runLinter(cfg, root, cacheDir, repoRoot, name, linter, paths, events)
		}
	}()
	return events, nil
}

func runLinter(cfg config.Config, root, cacheDir, repoRoot, name string, linter config.Linter, paths []string, events chan<- Event) {
	files, err := Files(cfg, linter, paths)
	if err != nil {
		events <- Event{Linter: name, Phase: Failed, Note: "matching files", Err: err}
		return
	}
	if len(files) == 0 {
		return
	}

	var findings []Finding
	ran := false
	for _, cmd := range linter.Commands {
		if cmd.Formatter {
			continue
		}
		if cmd.RunFrom != "" {
			events <- Event{Linter: name, Phase: Skipped, Note: fmt.Sprintf("unsupported run_from %q", cmd.RunFrom)}
			continue
		}
		if cmd.SandboxType != "" {
			events <- Event{Linter: name, Phase: Skipped, Note: fmt.Sprintf("unsupported sandbox_type %q", cmd.SandboxType)}
			continue
		}
		if v, ok := findUnsupportedVar(cmd.Run); ok {
			events <- Event{Linter: name, Phase: Skipped, Note: fmt.Sprintf("unsupported template var %q", v)}
			continue
		}
		if cmd.Output != "sarif" && cmd.Output != "pass_fail" {
			events <- Event{Linter: name, Phase: Skipped, Note: fmt.Sprintf("unsupported output format %q", cmd.Output)}
			continue
		}
		if cmd.Parser != nil {
			events <- Event{Linter: name, Phase: Skipped, Note: "unsupported parser (native output requires a converter script)"}
			continue
		}

		cmdFindings, err := runCommand(cfg, root, cacheDir, repoRoot, name, linter, cmd, files)
		if err != nil {
			events <- Event{Linter: name, Phase: Failed, Note: cmd.Name, Err: err}
			return
		}
		ran = true
		findings = append(findings, cmdFindings...)
	}

	if ran {
		events <- Event{Linter: name, Phase: Done, Findings: findings}
	}
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

// runCommand resolves every tool cmd's linter needs onto PATH, then runs cmd once (Batch) or once
// per matched file, parsing each invocation's output per cmd.Output.
func runCommand(cfg config.Config, root, cacheDir, repoRoot, linterName string, linter config.Linter, cmd config.Command, files []string) ([]Finding, error) {
	shimDirs, err := resolveShimDirs(cfg, root, cacheDir, linter.Tools)
	if err != nil {
		return nil, err
	}
	pathEnv := strings.Join(shimDirs, string(os.PathListSeparator))

	// files are relativized against repoRoot (every command's Dir) so both pass_fail's
	// Finding.File and sarif's echoed-back ${target} URI come out repo-relative, matching the
	// design spec's report format -- not absolute paths from the walk root.
	relFiles := make([]string, len(files))
	for i, f := range files {
		rel, err := filepath.Rel(repoRoot, f)
		if err != nil {
			return nil, err
		}
		relFiles[i] = rel
	}

	var batches [][]string
	if cmd.Batch {
		batches = [][]string{relFiles}
	} else {
		for _, f := range relFiles {
			batches = append(batches, []string{f})
		}
	}

	var findings []Finding
	for _, batch := range batches {
		out, stderr, exitCode, err := runOneInvocation(cmd, repoRoot, pathEnv, batch)
		if err != nil {
			return nil, err
		}
		if containsInt(cmd.ErrorCodes, exitCode) {
			msg := strings.TrimSpace(out)
			if errText := strings.TrimSpace(stderr); errText != "" {
				if msg == "" {
					msg = errText
				} else {
					msg += "\n" + errText
				}
			}
			return nil, fmt.Errorf("check: %s: %s exited %d: %s", linterName, cmd.Name, exitCode, msg)
		}

		// Real plugin data confirms SuccessCodes already enumerates every "ran fine, here's the
		// verdict" code, "found issues" included (e.g. ansible-lint sarif: [0,2,5]) -- there is
		// no third bucket. Every exit code not in ErrorCodes parses normally.
		var batchFindings []Finding
		switch cmd.Output {
		case "sarif":
			batchFindings, err = ParseSARIF([]byte(out), linterName)
			if err != nil {
				return nil, err
			}
		case "pass_fail":
			if exitCode != 0 {
				batchFindings = ParsePassFail(linterName, batch)
			}
		}
		ApplyIssueURL(batchFindings, linter.IssueURLFormat)
		findings = append(findings, batchFindings...)
	}
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
// PATH component means "current directory" on POSIX, which would let repoRoot's own files shadow
// real binaries) and repoRoot as the working directory. Returns the output named by
// cmd.ReadOutputFrom (default stdout), the process's raw stderr (always captured, regardless of
// ReadOutputFrom, so callers can surface it on a crash), and the exit code; err is only ever a
// launch failure (e.g. "sh" missing), never a non-zero exit -- callers read exitCode for that.
func runOneInvocation(cmd config.Command, repoRoot, pathEnv string, files []string) (output, stderrOut string, exitCode int, err error) {
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
	c.Dir = repoRoot
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
