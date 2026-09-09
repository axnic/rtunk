// Package gitutil resolves the repo root and the set of files changed
// relative to the trunk branch (see SPECS.md §7.3).
package gitutil

import (
	"os"
	"os/exec"
	"strings"
)

// Root returns the top-level directory of the git repo containing dir.
func Root(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// RemoteURL returns the "origin" remote URL, or "" if the repo has none
// (e.g. a fresh local-only repo) — used as a stable, portable cache key
// (SPECS.md §2.2) instead of the local clone path.
func RemoteURL(root string) string {
	out, err := exec.Command("git", "-C", root, "config", "--get", "remote.origin.url").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// ListFiles lists every file under pathspec (tracked or untracked) that
// isn't excluded by .gitignore — `--exclude-standard` already implements
// gitignore semantics correctly, so --all/<path> targeting gets "respect
// .gitignore" (SPECS.md §9.2) for free instead of reimplementing it.
func ListFiles(root, pathspec string) ([]string, error) {
	out, err := exec.Command("git", "-C", root, "ls-files", "--cached", "--others", "--exclude-standard", "--", pathspec).Output()
	if err != nil {
		return nil, err
	}
	var files []string
	for _, f := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files, nil
}

// ChangedFiles lists files modified (committed, staged, or unstaged)
// relative to trunkBranch, plus new untracked files not excluded by
// .gitignore — a new file you haven't committed yet is exactly the kind of
// thing you want linted, and `git diff` alone never lists untracked files
// (a plain "trunk_branch has nothing to diff against" would otherwise make
// a fresh repo's `check` see zero targets). Filtered to files still present
// on disk (skips deletions).
func ChangedFiles(root, trunkBranch string) ([]string, error) {
	tracked, err := exec.Command("git", "-C", root, "diff", "--name-only", trunkBranch).Output()
	if err != nil {
		return nil, err
	}
	untracked, err := exec.Command("git", "-C", root, "ls-files", "--others", "--exclude-standard").Output()
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	var files []string
	for _, out := range [][]byte{tracked, untracked} {
		for _, f := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if f == "" || seen[f] {
				continue
			}
			if _, err := os.Stat(root + "/" + f); err != nil {
				continue
			}
			seen[f] = true
			files = append(files, f)
		}
	}
	return files, nil
}
