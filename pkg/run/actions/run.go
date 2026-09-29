package actions

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/0xAX/notificator"

	"github.com/xunleii/rtunk/pkg/cache/download"
	"github.com/xunleii/rtunk/pkg/run/runlog"
	"github.com/xunleii/rtunk/pkg/trunk/config"
)

// RunOptions carries everything Run needs beyond the action/config themselves.
type RunOptions struct {
	CacheDir string    // "" -> OS default, same convention as pkg/cache/download
	RepoRoot string    // the repo trunk.yaml lives in; exec cwd fallback + history key
	Hook     string    // git hook name for ${hook}; "" for a manual `rtunk actions run <id>`
	Args     []string  // positional args forwarded from the git hook's own argv; ${1}.., ${@}
	Stdin    io.Reader // hook's stdin, read lazily only if ${hook_stdin_path} appears in Run
	// Log, when non-nil, records this run's command, output and exit (see pkg/run/runlog). The
	// caller owns it and may share one across every action of a `--hook` run.
	Log *runlog.Writer
}

// Result is one action run's outcome.
type Result struct {
	ActionID  string
	Hook      string
	StartedAt time.Time
	Duration  time.Duration
	ExitCode  int
	Skipped   bool   // true when interactive:true skipped a non-interactive context
	Err       string // non-empty on a launch failure (before/instead of a real exit code)
}

var templateVarRE = regexp.MustCompile(`\$\{[^}]+\}`)
var envVarRE = regexp.MustCompile(`\$\{env\.([A-Za-z_][A-Za-z0-9_]*)\}`)

// findUnsupportedActionVar returns the first ${...} in run that isn't one of the vars this
// package substitutes -- a typo'd or not-yet-supported var must be a hard error before exec, not
// silently passed through to the shell literally.
func findUnsupportedActionVar(run string) (string, bool) {
	for _, v := range templateVarRE.FindAllString(run, -1) {
		switch v {
		case "${cwd}", "${plugin}", "${hook}", "${hook_stdin_path}", "${@}":
			continue
		}
		if envVarRE.MatchString(v) {
			continue
		}
		if len(v) == 4 && v[1] == '{' && v[2] >= '1' && v[2] <= '9' && v[3] == '}' {
			continue
		}
		return v, true
	}
	return "", false
}

// substituteVars quotes every substituted value at substitution time (download.QuoteOne/QuoteAll) and
// leaves the corresponding ${...} bare in the Run string template -- the same convention
// pkg/run/engine.go's Command.Run/${target} substitution uses. args (opts.Args) is untrusted
// git-hook argv (branch/ref names an attacker controls), so ${1}..${9}/${@} MUST be quoted here:
// an unquoted substitution lets a value like `a" ; id ; "b` break out of the Run string and
// execute arbitrary shell.
//
// ${env.*} is expanded FIRST, against the original (trusted, plugin-authored) run string, before
// any arg/positional substitution runs. Doing it in the other order would re-scan the fully
// substituted string -- including whatever text got substituted in from args -- so an untrusted
// arg containing the literal text "${env.NAME}" would get expanded into that env var's real
// value, unquoted, after the fact.
func substituteVars(run, hook, cwd, plugin, hookStdinPath string, args []string) string {
	run = envVarRE.ReplaceAllStringFunc(run, func(m string) string {
		name := envVarRE.FindStringSubmatch(m)[1]
		return os.Getenv(name)
	})

	pairs := []string{
		"${cwd}", download.QuoteOne(cwd), "${plugin}", download.QuoteOne(plugin),
		"${hook}", hook, "${hook_stdin_path}", hookStdinPath,
		"${@}", strings.Join(download.QuoteAll(args), " "),
	}
	for i, a := range args {
		if i >= 9 {
			break
		}
		pairs = append(pairs, fmt.Sprintf("${%d}", i+1), download.QuoteOne(a))
	}
	return strings.NewReplacer(pairs...).Replace(run)
}

// IsInteractive reports whether os.Stdin is a real terminal -- the standard Go idiom (a pipe/file
// redirect never sets the character-device mode bit). Windows console detection differs and is
// deliberately not handled: this project has already ruled out full Windows shim support.
func IsInteractive() bool {
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// resolvePackagesFileBinDir installs actionID's packages_file manifest through download.Download
// (the same Ref/claimInstall/registry path tools and runtimes already use -- Task 2, closing the
// gap where this used to call download.InstallPackagesFile directly and was invisible to `cache
// prune`) and returns its node_modules/.bin dir for the PATH.
func resolvePackagesFileBinDir(cfg config.Config, cacheDir, repoRoot, root, actionID string) (string, error) {
	evs, err := download.Download(cfg, cacheDir, repoRoot, download.Ref{Category: "action-packages", ID: actionID})
	if err != nil {
		return "", err
	}
	for ev := range evs {
		if ev.Phase == download.Failed {
			return "", ev.Err
		}
	}
	return download.ActionPackagesBinDir(cfg, root, actionID)
}

func notifyOnError(action config.Action) bool {
	return action.NotifyOnError == nil || *action.NotifyOnError
}

// notifier sends the native OS notification -- darwin (osascript/terminal-notifier), linux
// (notify-send), and windows (SnoreToast) are each handled by the notificator package itself.
var notifier = notificator.New(notificator.Options{AppName: "rtunk"})

// notify is a best-effort native notification -- failures are silently swallowed, since a
// notification that can't be shown must never fail the action run that triggered it.
func notify(title, body string) {
	_ = notifier.Push(title, body, "", notificator.UR_NORMAL)
}

// Run executes action's Run string once: substituting template vars, resolving its Runtime/
// PackagesFile if any, and exec'ing through sh -c. Persists a history entry via AppendHistory on
// every exit path (success, non-zero exit, or launch failure) through its own finish() closure.
func Run(ctx context.Context, cfg config.Config, action config.Action, opts RunOptions, stdout, stderr io.Writer) (Result, error) {
	result := Result{ActionID: action.ID, Hook: opts.Hook, StartedAt: time.Now()}
	finish := func() Result {
		result.Duration = time.Since(result.StartedAt)
		_ = AppendHistory(opts.CacheDir, opts.RepoRoot, result) // best-effort: history must never fail the run it's recording
		return result
	}
	fail := func(err error) (Result, error) {
		result.Err = err.Error()
		opts.Log.Emit(runlog.Event{T: runlog.KindLinterEnd, Linter: action.ID, Phase: "Failed", Err: err.Error()})
		return finish(), err
	}

	if action.Interactive == "true" && !IsInteractive() {
		result.Skipped = true
		opts.Log.Emit(runlog.Event{T: runlog.KindLinterEnd, Linter: action.ID, Phase: "Skipped", Note: "non-interactive context"})
		return finish(), nil
	}

	if v, ok := findUnsupportedActionVar(action.Run); ok {
		return fail(fmt.Errorf("actions: %s: unsupported template var %s", action.ID, v))
	}

	root, err := download.Root(opts.CacheDir)
	if err != nil {
		return fail(err)
	}

	var pathDirs []string
	if action.Runtime != "" {
		if _, ok := cfg.Runtimes.Definitions[action.Runtime]; !ok {
			return fail(fmt.Errorf("actions: %s: runtime %q not found in resolved config", action.ID, action.Runtime))
		}
		shimDir, err := download.ResolveRuntimeShimDir(cfg, root, opts.CacheDir, opts.RepoRoot, action.Runtime, nil)
		if err != nil {
			return fail(err)
		}
		pathDirs = append(pathDirs, shimDir)
	}

	if action.PackagesFile != "" {
		if action.Runtime == "" {
			return fail(fmt.Errorf("actions: %s: packages_file set with no runtime", action.ID))
		}
		binDir, err := resolvePackagesFileBinDir(cfg, opts.CacheDir, opts.RepoRoot, root, action.ID)
		if err != nil {
			return fail(err)
		}
		pathDirs = append([]string{binDir}, pathDirs...)
	}

	var hookStdinPath string
	if strings.Contains(action.Run, "${hook_stdin_path}") && opts.Stdin != nil {
		f, err := os.CreateTemp("", "rtunk-action-stdin-*")
		if err != nil {
			return fail(err)
		}
		defer func() { _ = os.Remove(f.Name()) }()
		if _, err := io.Copy(f, opts.Stdin); err != nil {
			_ = f.Close()
			return fail(err)
		}
		// Close error matters: the child process reads this file back, so a dropped flush
		// would silently hand it truncated stdin.
		if err := f.Close(); err != nil {
			return fail(err)
		}
		hookStdinPath = f.Name()
	}

	cwd := ""
	if action.SourceRoot != "" {
		cwd = filepath.Join(action.SourceRoot, action.SourceDir)
	}

	run := substituteVars(action.Run, opts.Hook, cwd, action.SourceRoot, hookStdinPath, opts.Args)

	c := exec.CommandContext(ctx, "sh", "-c", run)
	// The process's own cwd is always the repo root: git hook argv (e.g. commit-msg's ${1}, a
	// path to COMMIT_EDITMSG) is repo-root-relative, never relative to a plugin's own source dir.
	// ${cwd}/${plugin} above still substitute to the plugin's absolute path for Run strings that
	// need it explicitly (e.g. `bash ${cwd}/update_config.sh`).
	c.Dir = opts.RepoRoot
	path := os.Getenv("PATH")
	if len(pathDirs) > 0 {
		path = strings.Join(pathDirs, string(os.PathListSeparator)) + string(os.PathListSeparator) + path
	}
	extraEnv := download.BuildEnv(action.Environment, nil)
	c.Env = append(append(os.Environ(), "PATH="+path), extraEnv...)

	// stdout/stderr stay live streams (an action may be interactive); Tee copies what passes
	// through into the log. With a non-nil Log the child therefore sees pipes, not the terminal.
	id := opts.Log.NextID()
	opts.Log.Emit(runlog.Event{
		T: runlog.KindInvocation, ID: id, Linter: action.ID, Template: action.Run, Argv: c.Args, Cwd: c.Dir,
		PathPrefix: strings.Join(pathDirs, string(os.PathListSeparator)),
	})
	c.Stdout = opts.Log.Tee(id, "stdout", stdout)
	c.Stderr = opts.Log.Tee(id, "stderr", stderr)
	c.Stdin = os.Stdin
	if opts.Log != nil {
		// Wrapped writers make exec wait for its copy goroutines, which end only when every holder
		// of the pipe (a `cmd &` left behind) closes it. Without a log, os.File needs no goroutine.
		c.WaitDelay = 2 * time.Second
	}

	execStart := time.Now()
	runErr := c.Run()
	if errors.Is(runErr, exec.ErrWaitDelay) {
		runErr = nil // the process itself exited 0; only a stray child kept the pipes open
	}
	if runErr != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](runErr); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			return fail(runErr)
		}
	}
	opts.Log.Emit(runlog.Event{T: runlog.KindExit, ID: id, Code: &result.ExitCode, Ms: time.Since(execStart).Milliseconds()})

	res := finish()
	if result.ExitCode != 0 {
		if notifyOnError(action) {
			notify(fmt.Sprintf("rtunk action %s failed", action.ID), fmt.Sprintf("exit code %d", result.ExitCode))
		}
		return res, fmt.Errorf("actions: %s: exit code %d", action.ID, result.ExitCode)
	}
	return res, nil
}
