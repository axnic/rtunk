package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// errNoFiles is returned by resolvePaths when the selection is legitimately empty (nothing
// changed): the caller reports it and exits 0 rather than falling back to a walk of the whole
// repository, which is what an empty paths slice means to engine.Run.
var errNoFiles = errors.New("no files to process")

// errOutsideGitNoPaths is returned by selectFiles when there is no git repository to diff against
// and no explicit paths were given: unlike errNoFiles (a legitimately empty selection inside git),
// this is a caller mistake -- there is no default to fall back to -- so resolvePaths' callers must
// treat it as a real failure, not a "nothing to do" no-op.
var errOutsideGitNoPaths = errors.New("outside a git repository, explicit paths are required")

// resolvePaths turns the user's path arguments into the absolute file list engine.Run gets, per
// docs/cli.md "File selection": no paths -> changed files (selectFiles), explicit paths -> every
// file under them (expandPaths). errNoFiles means there is nothing to run.
func resolvePaths(repoRoot string, paths []string, from string) ([]string, error) {
	var files []string
	var err error
	if len(paths) == 0 {
		files, err = selectFiles(repoRoot, from)
	} else {
		files, err = expandPaths(repoRoot, paths)
	}
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, errNoFiles
	}
	return files, nil
}

// gitOut runs git in dir and returns its trimmed stdout split by line (nil when empty).
func gitOut(dir string, args ...string) ([]string, error) {
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

func inGit(dir string) bool {
	_, err := gitOut(dir, "rev-parse", "--is-inside-work-tree")
	return err == nil
}

func absAll(dir string, rel []string) []string {
	out := make([]string, len(rel))
	for i, r := range rel {
		out[i] = filepath.Join(dir, r)
	}
	return out
}

// gitEmptyTree is git's well-known empty-tree object hash (constant across every repository):
// diffing against it lists a repo with no commits yet as if every tracked file were newly added.
const gitEmptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// selectFiles is the no-path file selection: the diff from the diff base (from, else the branch's
// upstream, else HEAD) to the working tree, plus untracked non-ignored files. With an explicit
// base or an upstream, the diff base is merge-base(base, HEAD); with neither, it is HEAD directly,
// so the no-upstream case picks up every staged and unstaged change since the last commit, not
// staged changes only -- or, in a repo with no commits yet (no HEAD to diff against), everything.
// Outside git, errOutsideGitNoPaths. Deleted files are never selected. Paths are absolute.
func selectFiles(repoRoot, from string) ([]string, error) {
	if !inGit(repoRoot) {
		return nil, errOutsideGitNoPaths
	}
	base := from
	if base == "" {
		if up, err := gitOut(repoRoot, "rev-parse", "--abbrev-ref", "@{upstream}"); err == nil && len(up) == 1 {
			base = up[0]
		}
	}
	diffBase := "HEAD"
	if base != "" {
		mb, err := gitOut(repoRoot, "merge-base", base, "HEAD")
		if err != nil || len(mb) != 1 {
			return nil, fmt.Errorf("cannot resolve diff base %q: %v", base, err)
		}
		diffBase = mb[0]
	} else if _, err := gitOut(repoRoot, "rev-parse", "--verify", "-q", "HEAD"); err != nil {
		diffBase = gitEmptyTree
	}
	changed, err := gitOut(repoRoot, "diff", "--relative", "--name-only", "--diff-filter=d", diffBase)
	if err != nil {
		return nil, err
	}
	untracked, err := gitOut(repoRoot, "ls-files", "-o", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	return absAll(repoRoot, append(changed, untracked...)), nil
}

// expandPaths is the explicit-path selection: every file under paths, from
// `git ls-files -co --exclude-standard` in git (existing files only), the paths untouched
// otherwise (engine.Files walks them). A nonexistent path is an error either way.
func expandPaths(repoRoot string, paths []string) ([]string, error) {
	abs := make([]string, len(paths))
	for i, p := range paths {
		a, err := filepath.Abs(p)
		if err != nil {
			return nil, err
		}
		if _, err := os.Stat(a); err != nil {
			return nil, err
		}
		abs[i] = a
	}
	if !inGit(repoRoot) {
		return abs, nil
	}
	listed, err := gitOut(repoRoot, append([]string{"ls-files", "-co", "--exclude-standard", "--"}, abs...)...)
	if err != nil {
		return nil, err
	}
	// ls-files prints paths relative to repoRoot (its -C dir), for entries under it.
	var files []string
	for _, f := range absAll(repoRoot, listed) {
		if info, err := os.Stat(f); err == nil && !info.IsDir() { // -c lists index entries deleted on disk
			files = append(files, f)
		}
	}
	return files, nil
}

// partiallyStaged returns the absolute paths with both staged and unstaged changes: rewriting one
// in the working tree would leave the index out of sync with what the user staged.
func partiallyStaged(repoRoot string) map[string]bool {
	unstaged, _ := gitOut(repoRoot, "diff", "--relative", "--name-only")
	staged, _ := gitOut(repoRoot, "diff", "--cached", "--relative", "--name-only")
	in := map[string]bool{}
	for _, f := range staged {
		in[f] = true
	}
	out := map[string]bool{}
	for _, f := range unstaged {
		if in[f] {
			out[filepath.Join(repoRoot, f)] = true
		}
	}
	return out
}
