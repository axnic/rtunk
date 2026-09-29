package engine

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/xunleii/rtunk/pkg/trunk/config"
	"gopkg.in/yaml.v3"
)

// Files resolves which files under paths match linter's Files (ids into cfg.Lint.Files),
// walking directories recursively (skipping .git), then drops any match git considers ignored
// (see filterGitignored). repoRoot anchors the gitignore lookup -- it need not equal paths, e.g.
// paths can be a subset of repoRoot passed explicitly on the command line. A directory entry
// that is itself a file (not a directory) is taken as-is, matched or not, without a walk.
//
// Every path in paths must resolve inside repoRoot -- a path outside it is rejected outright
// (rather than silently matched and only failing later, deep inside sandbox staging, if any
// enabled linter's command happens to use SandboxType: copy_targets/expanded; see the design
// spec's "Security" section for the real gap this closes).
//
// ponytail: walks the filesystem once per linter call rather than combining every enabled
// linter's file set into one shared walk -- simpler, correct, and fine at v0.3's scale; combine
// them if `rtunk check` on a large repo with many enabled linters gets slow.
func Files(cfg config.Config, linter config.Linter, repoRoot string, paths []string) ([]string, error) {
	if len(linter.Files) == 0 {
		return nil, nil
	}

	for _, p := range paths {
		rel, err := filepath.Rel(repoRoot, p)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("engine: path %q is outside repository root %q", p, repoRoot)
		}
	}

	var out []string
	seen := make(map[string]bool)
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			if !seen[p] && matchesAny(cfg.Lint.Files, linter.Files, p) {
				seen[p] = true
				out = append(out, p)
			}
			continue
		}

		err = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == ".git" {
					return filepath.SkipDir
				}
				return nil
			}
			if seen[path] {
				return nil
			}
			if matchesAny(cfg.Lint.Files, linter.Files, path) {
				seen[path] = true
				out = append(out, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return filterGitignored(repoRoot, out), nil
}

// filterGitignored drops any path in files that git considers ignored, deferring to the real
// `git check-ignore` rather than a hand-rolled gitignore parser: it is the same matcher git
// itself uses, so nested .gitignore files, .git/info/exclude, and core.excludesFile all just
// work without reimplementing their precedence rules. Returns files unchanged (never an error)
// when repoRoot isn't inside a git repository or git isn't on PATH -- gitignore support is a
// courtesy, not a requirement for `rtunk check` to run.
func filterGitignored(repoRoot string, files []string) []string {
	if len(files) == 0 {
		return files
	}

	rel := make([]string, len(files))
	for i, f := range files {
		r, err := filepath.Rel(repoRoot, f)
		if err != nil {
			return files
		}
		rel[i] = r
	}

	cmd := exec.Command("git", "check-ignore", "--no-index", "-z", "--stdin")
	cmd.Dir = repoRoot
	cmd.Stdin = strings.NewReader(strings.Join(rel, "\x00") + "\x00")
	var out strings.Builder
	cmd.Stdout = &out

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		// exit 1 means "nothing ignored", not an error -- fall through and parse the (empty)
		// output normally. Anything else (128: not a git repo; a launch failure: git missing)
		// means we can't ask, so every file passes through unfiltered.
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			return files
		}
	}

	ignored := map[string]bool{}
	for p := range strings.SplitSeq(strings.TrimSuffix(out.String(), "\x00"), "\x00") {
		if p != "" {
			ignored[p] = true
		}
	}
	if len(ignored) == 0 {
		return files
	}

	kept := make([]string, 0, len(files))
	for i, f := range files {
		if !ignored[rel[i]] {
			kept = append(kept, f)
		}
	}
	return kept
}

// matchesAny reports whether path matches any of the FileType ids (looked up in registry, each
// resolved through its own Inherit chain). "ALL" (config.Linter.Files: ["ALL"], e.g. cspell)
// matches every file without a registry lookup.
func matchesAny(registry map[string]config.FileType, ids []string, path string) bool {
	for _, id := range ids {
		if id == "ALL" {
			return true
		}
		ft, ok := registry[id]
		if !ok {
			continue
		}
		if matchesFileType(registry, ft, path, map[string]bool{}) {
			return true
		}
	}
	return false
}

// matchesFileType reports whether path matches ft: a "structural" match (any of its own
// Extensions/Filenames/Regexes/Shebangs, or any FileType it Inherits from) that RequiredYAMLKeys
// (when set) then narrows further -- RequiredYAMLKeys distinguishes e.g. "cloudformation" from
// plain "yaml", it does not independently qualify a file no structural check already matched.
// seen guards against an Inherit cycle (a config error, not a hang).
func matchesFileType(registry map[string]config.FileType, ft config.FileType, path string, seen map[string]bool) bool {
	if seen[ft.Name] {
		return false
	}
	seen[ft.Name] = true

	structural := matchesExtension(ft.Extensions, path) ||
		matchesFilename(ft.Filenames, path) ||
		matchesRegex(ft.Regexes, path) ||
		matchesShebang(ft.Shebangs, path)

	for _, parentID := range ft.Inherit {
		if parent, ok := registry[parentID]; ok && matchesFileType(registry, parent, path, seen) {
			structural = true
			break
		}
	}

	if !structural {
		return false
	}
	if len(ft.RequiredYAMLKeys) > 0 {
		return matchesRequiredYAMLKeys(ft.RequiredYAMLKeys, path)
	}
	return true
}

func matchesExtension(extensions []string, path string) bool {
	ext := strings.TrimPrefix(filepath.Ext(path), ".")
	for _, e := range extensions {
		if strings.EqualFold(ext, e) {
			return true
		}
	}
	return false
}

func matchesFilename(filenames []string, path string) bool {
	base := filepath.Base(path)
	return slices.Contains(filenames, base)
}

func matchesRegex(patterns []string, path string) bool {
	slashPath := filepath.ToSlash(path)
	for _, p := range patterns {
		if re, err := regexp.Compile(p); err == nil && re.MatchString(slashPath) {
			return true
		}
	}
	return false
}

// matchesShebang only applies to extensionless files -- an extensioned file's language is already
// settled by its Extensions match, if any.
func matchesShebang(shebangs []string, path string) bool {
	if len(shebangs) == 0 || filepath.Ext(path) != "" {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return false
	}
	line := scanner.Text()
	if !strings.HasPrefix(line, "#!") {
		return false
	}
	fields := strings.Fields(strings.TrimPrefix(line, "#!"))
	if len(fields) == 0 {
		return false
	}
	// "/usr/bin/env bash" -> last field "bash"; "/bin/bash" -> one field, Base gives "bash".
	last := filepath.Base(fields[len(fields)-1])
	return slices.Contains(shebangs, last)
}

// matchesRequiredYAMLKeys reports whether path parses as YAML and its top-level mapping contains
// every key. A parse failure (the file isn't YAML-shaped) means "does not match", not an error --
// an unrelated file failing to parse as this FileType's shape isn't a check error.
func matchesRequiredYAMLKeys(keys []string, path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return false
	}
	for _, k := range keys {
		if _, ok := doc[k]; !ok {
			return false
		}
	}
	return true
}

// Matches reports whether path (absolute, so shebang files can be read) is one of linter's files.
// The same predicate Files applies, exported for `rtunk linters list`'s per-linter file counts.
func Matches(cfg config.Config, linter config.Linter, path string) bool {
	return len(linter.Files) > 0 && matchesAny(cfg.Lint.Files, linter.Files, path)
}
