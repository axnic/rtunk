package actions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/xunleii/rtunk/pkg/trunk/config"
	"github.com/xunleii/rtunk/pkg/trunk/download"
)

// RunOptions carries everything Run needs beyond the action/config themselves.
type RunOptions struct {
	CacheDir string    // "" -> OS default, same convention as pkg/trunk/download
	RepoRoot string    // the repo trunk.yaml lives in; exec cwd fallback + history key
	Hook     string    // git hook name for ${hook}; "" for a manual `rtunk actions run <id>`
	Args     []string  // positional args forwarded from the git hook's own argv; ${1}.., ${@}
	Stdin    io.Reader // hook's stdin, read lazily only if ${hook_stdin_path} appears in Run
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
		if strings.HasPrefix(v, "${env.") {
			continue
		}
		if len(v) == 4 && v[1] == '{' && v[2] >= '1' && v[2] <= '9' && v[3] == '}' {
			continue
		}
		return v, true
	}
	return "", false
}

func quoteOne(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func substituteVars(run, hook, cwd, plugin, hookStdinPath string, args []string) string {
	pairs := []string{
		"${cwd}", quoteOne(cwd), "${plugin}", quoteOne(plugin),
		"${hook}", hook, "${hook_stdin_path}", hookStdinPath,
		// ${@} is joined unquoted -- Run strings that need word-splitting semantics wrap it in
		// their own quotes (e.g. `"${@}"`, as in the real catalog and this package's own test),
		// so quoting here would double-quote and break that convention.
		"${@}", strings.Join(args, " "),
	}
	for i, a := range args {
		if i >= 9 {
			break
		}
		pairs = append(pairs, fmt.Sprintf("${%d}", i+1), a)
	}
	run = strings.NewReplacer(pairs...).Replace(run)
	return envVarRE.ReplaceAllStringFunc(run, func(m string) string {
		name := envVarRE.FindStringSubmatch(m)[1]
		return os.Getenv(name)
	})
}

// isInteractive reports whether os.Stdin is a real terminal -- the standard Go idiom (a pipe/file
// redirect never sets the character-device mode bit). Windows console detection differs and is
// deliberately not handled: this project has already ruled out full Windows shim support.
func isInteractive() bool {
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// resolveRuntimeShimDir is pkg/trunk/engine's own resolveRuntimeShimDir, copied rather than
// exported across the package boundary for this one call site (same reasoning as this package's
// local quoteOne/quoteAll: a ~15 line helper isn't worth a cross-package export).
func resolveRuntimeShimDir(cfg config.Config, root, cacheDir, runtimeID string) (string, error) {
	rt, ok := cfg.Runtimes.Definitions[runtimeID]
	if !ok {
		return "", fmt.Errorf("actions: runtime %q referenced but not found in resolved config", runtimeID)
	}
	if len(rt.Shims) == 0 {
		return "", fmt.Errorf("actions: runtime %q has no shims declared", runtimeID)
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

// resolvePackagesFileBinDir installs action.PackagesFile (if not already cached, keyed by its own
// content hash so identical manifests across actions/runs share one install) and returns its
// node_modules/.bin dir for the PATH.
func resolvePackagesFileBinDir(root string, rt config.Runtime, runtimeInstallDir, actionID, packagesFilePath string) (string, error) {
	data, err := os.ReadFile(packagesFilePath)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	installDir := download.InstallDir(root, "action-packages", actionID, hex.EncodeToString(sum[:]))
	if _, statErr := os.Stat(installDir); statErr != nil {
		if err := download.InstallPackagesFile(rt, runtimeInstallDir, installDir, packagesFilePath); err != nil {
			return "", err
		}
	}
	return filepath.Join(installDir, "node_modules", ".bin"), nil
}

func notifyOnError(action config.Action) bool {
	return action.NotifyOnError == nil || *action.NotifyOnError
}

// notify is a best-effort native notification -- failures are silently swallowed, since a
// notification that can't be shown must never fail the action run that triggered it.
func notify(title, body string) {
	switch goruntime.GOOS {
	case "darwin":
		script := fmt.Sprintf("display notification %q with title %q", body, title)
		_ = exec.Command("osascript", "-e", script).Run()
	case "linux":
		if path, err := exec.LookPath("notify-send"); err == nil {
			_ = exec.Command(path, title, body).Run()
		}
	}
}

// Run executes action's Run string once: substituting template vars, resolving its Runtime/
// PackagesFile if any, and exec'ing through sh -c. Does not persist history (see pkg/trunk/actions'
// history.go, wired in by a later task) -- callers that need history call actions.AppendHistory
// themselves with the returned Result.
func Run(ctx context.Context, cfg config.Config, action config.Action, opts RunOptions, stdout, stderr io.Writer) (Result, error) {
	result := Result{ActionID: action.ID, Hook: opts.Hook, StartedAt: time.Now()}
	fail := func(err error) (Result, error) {
		result.Err = err.Error()
		result.Duration = time.Since(result.StartedAt)
		return result, err
	}

	if action.Interactive == "true" && !isInteractive() {
		result.Skipped = true
		result.Duration = time.Since(result.StartedAt)
		return result, nil
	}

	if v, ok := findUnsupportedActionVar(action.Run); ok {
		return fail(fmt.Errorf("actions: %s: unsupported template var %s", action.ID, v))
	}

	root, err := download.Root(opts.CacheDir)
	if err != nil {
		return fail(err)
	}

	var pathDirs []string
	var rt config.Runtime
	var runtimeInstallDir string
	if action.Runtime != "" {
		var ok bool
		rt, ok = cfg.Runtimes.Definitions[action.Runtime]
		if !ok {
			return fail(fmt.Errorf("actions: %s: runtime %q not found in resolved config", action.ID, action.Runtime))
		}
		shimDir, err := resolveRuntimeShimDir(cfg, root, opts.CacheDir, action.Runtime)
		if err != nil {
			return fail(err)
		}
		pathDirs = append(pathDirs, shimDir)
		version := download.ResolveVersion(cfg.Runtimes.Enabled, action.Runtime, rt.KnownGoodVersion)
		runtimeInstallDir = download.InstallDir(root, "runtimes", action.Runtime, version)
	}

	if action.PackagesFile != "" {
		if action.Runtime == "" {
			return fail(fmt.Errorf("actions: %s: packages_file set with no runtime", action.ID))
		}
		packagesFilePath := action.PackagesFile
		if action.SourceRoot != "" {
			packagesFilePath = filepath.Join(action.SourceRoot, action.SourceDir, action.PackagesFile)
		}
		binDir, err := resolvePackagesFileBinDir(root, rt, runtimeInstallDir, action.ID, packagesFilePath)
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
		defer os.Remove(f.Name())
		if _, err := io.Copy(f, opts.Stdin); err != nil {
			f.Close()
			return fail(err)
		}
		f.Close()
		hookStdinPath = f.Name()
	}

	cwd := ""
	if action.SourceRoot != "" {
		cwd = filepath.Join(action.SourceRoot, action.SourceDir)
	}
	workDir := opts.RepoRoot
	if cwd != "" {
		workDir = cwd
	}

	run := substituteVars(action.Run, opts.Hook, cwd, action.SourceRoot, hookStdinPath, opts.Args)

	c := exec.CommandContext(ctx, "sh", "-c", run)
	c.Dir = workDir
	path := os.Getenv("PATH")
	if len(pathDirs) > 0 {
		path = strings.Join(pathDirs, string(os.PathListSeparator)) + string(os.PathListSeparator) + path
	}
	env := append(os.Environ(), "PATH="+path)
	env = append(env, download.BuildEnv(action.Environment, nil)...)
	c.Env = env
	c.Stdout = stdout
	c.Stderr = stderr
	c.Stdin = os.Stdin

	runErr := c.Run()
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
		} else {
			return fail(runErr)
		}
	}

	result.Duration = time.Since(result.StartedAt)
	if result.ExitCode != 0 {
		if notifyOnError(action) {
			notify(fmt.Sprintf("rtunk action %s failed", action.ID), fmt.Sprintf("exit code %d", result.ExitCode))
		}
		return result, fmt.Errorf("actions: %s: exit code %d", action.ID, result.ExitCode)
	}
	return result, nil
}
