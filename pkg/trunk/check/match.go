// Package check implements ROADMAP.md's v0.3 milestone: running enabled linters against source
// files and reporting findings, read-only. See
// docs/superpowers/specs/2026-09-12-check-v0.3-design.md for the full design.
package check

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/xunleii/rtunk/pkg/trunk/config"
	"gopkg.in/yaml.v3"
)

// Files resolves which files under paths match linter's Files (ids into cfg.Lint.Files),
// walking directories recursively (skipping .git). A directory entry that is itself a file (not
// a directory) is taken as-is, matched or not, without a walk.
//
// ponytail: walks the filesystem once per linter call rather than combining every enabled
// linter's file set into one shared walk -- simpler, correct, and fine at v0.3's scale; combine
// them if `rtunk check` on a large repo with many enabled linters gets slow.
func Files(cfg config.Config, linter config.Linter, paths []string) ([]string, error) {
	if len(linter.Files) == 0 {
		return nil, nil
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
	return out, nil
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
	for _, n := range filenames {
		if base == n {
			return true
		}
	}
	return false
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
	defer f.Close()

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
	for _, sb := range shebangs {
		if last == sb {
			return true
		}
	}
	return false
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
