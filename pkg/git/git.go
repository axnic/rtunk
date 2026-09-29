// Package git wraps the git CLI operations rtunk's file-selection and repo-discovery logic
// needs: locating the repository root, and listing files by tracked/untracked/changed status.
// Every listing function returns absolute paths.
package git

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// emptyTree is git's well-known empty-tree object hash (constant across every repository):
// diffing against it lists a repo with no commits yet as if every tracked file were newly added.
const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// Output runs `git -C dir <args...>` and returns its trimmed stdout split by line (nil when
// empty).
func Output(dir string, args ...string) ([]string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return nil, nil
	}
	return strings.Split(s, "\n"), nil
}

// IsRepo reports whether dir is inside a git working tree.
func IsRepo(dir string) bool {
	_, err := Output(dir, "rev-parse", "--is-inside-work-tree")
	return err == nil
}

// RepoRoot resolves the git repository's top-level directory containing dir, via
// `git rev-parse --show-toplevel`. dir doesn't need to be exactly the git root -- git itself
// resolves upward -- so this stays correct in nested layouts (e.g. .trunk/trunk.yaml not directly
// at the repo root), unlike a naive filepath.Dir(filepath.Dir(configPath)) guess.
func RepoRoot(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return "", errors.New(strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", fmt.Errorf("resolve git repo root: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func absAll(dir string, rel []string) []string {
	out := make([]string, len(rel))
	for i, r := range rel {
		out[i] = filepath.Join(dir, r)
	}
	return out
}

// Files lists every file git knows about under paths (dir itself if paths is empty): tracked or
// untracked-but-not-ignored (`git ls-files -co --exclude-standard`). Absolute paths.
func Files(dir string, paths ...string) ([]string, error) {
	args := []string{"ls-files", "-co", "--exclude-standard"}
	if len(paths) > 0 {
		args = append(append(args, "--"), paths...)
	}
	out, err := Output(dir, args...)
	if err != nil {
		return nil, err
	}
	return absAll(dir, out), nil
}

// ChangedFiles lists the files changed (added/modified/renamed, never deleted) between from and
// the working tree, plus untracked non-ignored files -- both as absolute paths. from resolves as:
// an explicit ref if given, else the branch's upstream, else HEAD directly (or, in a repo with no
// commits yet, the empty tree, so every file counts as new).
func ChangedFiles(dir, from string) (changed, untracked []string, err error) {
	base := from
	if base == "" {
		if up, err := Output(dir, "rev-parse", "--abbrev-ref", "@{upstream}"); err == nil && len(up) == 1 {
			base = up[0]
		}
	}

	diffBase := "HEAD"
	if base != "" {
		mb, err := Output(dir, "merge-base", base, "HEAD")
		if err != nil || len(mb) != 1 {
			return nil, nil, fmt.Errorf("cannot resolve diff base %q: %v", base, err)
		}
		diffBase = mb[0]
	} else if _, err := Output(dir, "rev-parse", "--verify", "-q", "HEAD"); err != nil {
		diffBase = emptyTree
	}

	changedRel, err := Output(dir, "diff", "--relative", "--name-only", "--diff-filter=d", diffBase)
	if err != nil {
		return nil, nil, err
	}
	untrackedRel, err := Output(dir, "ls-files", "-o", "--exclude-standard")
	if err != nil {
		return nil, nil, err
	}
	return absAll(dir, changedRel), absAll(dir, untrackedRel), nil
}

// DiffNames lists the files with a diff, as absolute paths: unstaged (cached=false) or staged
// (cached=true).
func DiffNames(dir string, cached bool) ([]string, error) {
	args := []string{"diff", "--relative", "--name-only"}
	if cached {
		args = append(args, "--cached")
	}
	out, err := Output(dir, args...)
	if err != nil {
		return nil, err
	}
	return absAll(dir, out), nil
}
