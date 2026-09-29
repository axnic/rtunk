// Package githooks installs/uninstalls the git hook shim scripts that trigger pkg/run/actions'
// enabled actions at the right git lifecycle points.
package githooks

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/xunleii/rtunk/pkg/run/actions"
	"github.com/xunleii/rtunk/pkg/trunk/config"
)

// marker identifies a hook file as rtunk's own -- present verbatim in every shim Install writes,
// checked by both a re-run of Install (skip-unless-force logic) and Uninstall (never remove a
// foreign hook).
const marker = "# Installed by rtunk git-hooks install -- do not edit by hand."

// Install writes a shim script into each git hook name referenced by any of cfg's currently
// enabled actions' git_hooks triggers. An existing hook file that isn't rtunk's own (missing
// marker) is left untouched and reported as skipped unless force is true.
func Install(repoRoot string, cfg config.Config, force bool) (installed, skipped []string, err error) {
	dir, err := hooksDir(repoRoot)
	if err != nil {
		return nil, nil, err
	}
	//nolint:gosec // .git/hooks keeps git's own conventional mode
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, err
	}

	// Resolve the running rtunk binary's own absolute path rather than embedding the bare
	// "rtunk" name: GUI git clients (Tower, SourceTree, VS Code, JetBrains) often run hooks under
	// a minimal PATH that excludes mise/asdf shims or Homebrew paths, so a bare `exec rtunk ...`
	// fails with "exec: rtunk: not found" there.
	self, err := os.Executable()
	if err != nil {
		return nil, nil, err
	}

	for _, name := range hookNamesFor(cfg) {
		path := filepath.Join(dir, name)
		foreign, statErr := isForeignHook(path)
		if statErr != nil {
			return installed, skipped, statErr
		}
		if foreign && !force {
			skipped = append(skipped, name)
			continue
		}
		script := "#!/bin/sh\n" + marker + "\n# Run `rtunk git-hooks uninstall` to remove.\n" +
			fmt.Sprintf("exec %s actions run --hook %s -- \"$@\"\n", self, name)
		//nolint:gosec // git only runs a hook that is executable
		if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
			return installed, skipped, err
		}
		installed = append(installed, name)
	}
	sort.Strings(installed)
	sort.Strings(skipped)
	return installed, skipped, nil
}

// Uninstall removes every hook file under repoRoot's hooks dir that carries rtunk's marker. A
// foreign hook is left untouched.
func Uninstall(repoRoot string) (removed []string, err error) {
	dir, err := hooksDir(repoRoot)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		foreign, statErr := isForeignHook(path)
		if statErr != nil || foreign {
			continue
		}
		if err := os.Remove(path); err != nil {
			return removed, err
		}
		removed = append(removed, e.Name())
	}
	sort.Strings(removed)
	return removed, nil
}

// hooksDir returns where git hooks actually live for repoRoot, via `git rev-parse --git-path
// hooks`: this one command correctly resolves the real hooks directory whether repoRoot is a
// normal repo, a worktree, a submodule (where ".git" is a file, not a directory -- a hand-rolled
// "<repoRoot>/.git/hooks" join breaks there), or has core.hooksPath set (relative or absolute).
func hooksDir(repoRoot string) (string, error) {
	out, err := exec.Command("git", "-C", repoRoot, "rev-parse", "--git-path", "hooks").Output()
	if err != nil {
		return "", err
	}
	p := strings.TrimSpace(string(out))
	if !filepath.IsAbs(p) {
		p = filepath.Join(repoRoot, p)
	}
	return p, nil
}

// isForeignHook reports whether path exists and lacks rtunk's marker. A missing file is not
// foreign (there is simply nothing there yet).
func isForeignHook(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return !strings.Contains(string(data), marker), nil
}

// hookNamesFor computes the git hook name set to install: every distinct Trigger.GitHooks value
// across cfg's currently enabled actions -- no fixed whitelist, so this stays correct as the
// enabled action set changes without githooks needing its own enumeration of every git hook name
// that exists.
func hookNamesFor(cfg config.Config) []string {
	seen := map[string]bool{}
	var names []string
	for _, a := range actions.Resolve(cfg, "") {
		for _, trig := range a.Triggers {
			for _, h := range trig.GitHooks {
				if !seen[h] {
					seen[h] = true
					names = append(names, h)
				}
			}
		}
	}
	sort.Strings(names)
	return names
}
