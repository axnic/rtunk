package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/xunleii/rtunk/pkg/run/engine"
	"github.com/xunleii/rtunk/pkg/trunk/config"
)

// listItem is one row of `rtunk linters list` / `rtunk actions list`. Files is nil for actions
// (they have no notion of matching files).
type listItem struct {
	ID          string `json:"id"`
	Version     string `json:"version"`
	Files       *int   `json:"files,omitempty"`
	Description string `json:"description"`
	label       string // human text after the name: "2 go files", or the action's description
}

func (i listItem) name() string {
	if i.Version != "" {
		return i.ID + "@" + i.Version
	}
	return i.ID
}

// listing is the grouped result: enabled (with pinned version), available (not enabled, and for
// linters matching at least one file here), and other (linters only: the rest, shown with --all).
type listing struct {
	Enabled   []listItem `json:"enabled"`
	Available []listItem `json:"available"`
	Other     []listItem `json:"other"`
}

// enabledVersions maps bare id -> pinned version ("" when unpinned) for an enabled: list.
func enabledVersions(enabled []string) map[string]string {
	out := map[string]string{}
	for _, e := range enabled {
		id, version, _ := cutVersion(e)
		out[id] = version
	}
	return out
}

// repoFiles is every file of the repository (absolute paths): `git ls-files -co
// --exclude-standard` in git, a plain walk (skipping .git) otherwise.
// ponytail: the caller matches every file against every linter (O(files x linters)); index by
// extension if `linters list` gets slow on a huge repo.
func repoFiles(repoRoot string) ([]string, error) {
	if inGit(repoRoot) {
		return expandPaths(repoRoot, []string{repoRoot})
	}
	var files []string
	err := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		files = append(files, path)
		return nil
	})
	return files, err
}

// countLabel is "N <type> files" for a linter with a single (non-ALL) file type, "N files"
// otherwise, singular for 1.
func countLabel(n int, ids []string) string {
	noun := "files"
	if n == 1 {
		noun = "file"
	}
	if len(ids) == 1 && ids[0] != "ALL" {
		return fmt.Sprintf("%d %s %s", n, ids[0], noun)
	}
	return fmt.Sprintf("%d %s", n, noun)
}

func buildLintersList(cfg config.Config, repoRoot string, files []string) listing {
	pinned := enabledVersions(cfg.Lint.Enabled)
	present := directConfigFiles(repoRoot, files)
	names := make([]string, 0, len(cfg.Lint.Definitions))
	for name := range cfg.Lint.Definitions {
		names = append(names, name)
	}
	sort.Strings(names)

	l := listing{Enabled: []listItem{}, Available: []listItem{}, Other: []listItem{}}
	for _, name := range names {
		linter := cfg.Lint.Definitions[name]
		n := 0
		for _, f := range files {
			if engine.Matches(cfg, linter, f) {
				n++
			}
		}
		item := listItem{ID: name, Files: &n, Description: linter.Description, label: countLabel(n, linter.Files)}
		if version, ok := pinned[name]; ok {
			item.Version = version
			l.Enabled = append(l.Enabled, item)
		} else if suggested(linter, n, present) {
			l.Available = append(l.Available, item)
		} else {
			l.Other = append(l.Other, item)
		}
	}
	return l
}

// suggested applies suggest_if's real 3 values (files_present, config_present, never) to decide
// whether a not-yet-enabled linter belongs in the Available (suggested) bucket. Unset SuggestIf
// keeps this project's own pre-existing default (files_present's own behavior: n > 0), for every
// linter that doesn't declare the field.
func suggested(linter config.Linter, n int, present map[string]bool) bool {
	switch linter.SuggestIf {
	case "never":
		return false
	case "config_present":
		return configPresent(linter.DirectConfigs, present)
	default: // "files_present", or unset -- today's own pre-existing default
		return n > 0
	}
}

// configPresent reports whether any of directConfigs (repoRoot-relative paths, e.g.
// ".github/actionlint.yaml") names a real file this repository actually has, per present (see
// directConfigFiles).
func configPresent(directConfigs []string, present map[string]bool) bool {
	for _, c := range directConfigs {
		if present[c] {
			return true
		}
	}
	return false
}

// directConfigFiles reports which repoRoot-relative paths this repository actually has, from
// files -- buildLintersList's own full repository file list (absolute paths, see repoFiles), not
// any per-linter matched subset.
func directConfigFiles(repoRoot string, files []string) map[string]bool {
	present := map[string]bool{}
	for _, f := range files {
		if rel, err := filepath.Rel(repoRoot, f); err == nil {
			present[rel] = true
		}
	}
	return present
}

func buildActionsList(cfg config.Config) listing {
	pinned := enabledVersions(cfg.Actions.Enabled)
	names := make([]string, 0, len(cfg.Actions.Definitions))
	for name := range cfg.Actions.Definitions {
		names = append(names, name)
	}
	sort.Strings(names)

	l := listing{Enabled: []listItem{}, Available: []listItem{}, Other: []listItem{}}
	for _, name := range names {
		desc := cfg.Actions.Definitions[name].Description
		item := listItem{ID: name, Description: desc, label: desc}
		if version, ok := pinned[name]; ok {
			item.Version = version
			l.Enabled = append(l.Enabled, item)
		} else {
			l.Available = append(l.Available, item)
		}
	}
	return l
}

// writeListing renders l as human text or JSON. noun/hintCmd name the kind ("linter", "linters
// enable"); showOther prints the Other group (--all), otherwise it is only counted.
func writeListing(w io.Writer, l listing, format string, noun, hintCmd string, showOther bool) error {
	if format == "json" {
		if !showOther {
			l.Other = []listItem{}
		}
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		return enc.Encode(l)
	}

	width := 0
	groups := []struct {
		title string
		mark  string
		items []listItem
	}{
		{"Enabled", "✔", l.Enabled},
		{"Available for this repo (not enabled)", "◯", l.Available},
	}
	if noun != "linter" {
		groups[1].title = "Available (not enabled)"
	}
	if showOther {
		groups = append(groups, struct {
			title string
			mark  string
			items []listItem
		}{"Other (no matching file)", "◯", l.Other})
	}
	for _, g := range groups {
		for _, it := range g.items {
			width = max(width, len(it.name()))
		}
	}

	var b strings.Builder
	for _, g := range groups {
		if len(g.items) == 0 {
			continue
		}
		b.WriteString(g.title + "\n")
		for _, it := range g.items {
			_, _ = fmt.Fprintf(&b, "  %s %-*s  %s\n", g.mark, width, it.name(), it.label)
		}
	}
	if !showOther && len(l.Other) > 0 {
		n := len(l.Other)
		verb := "linters don't"
		if n == 1 {
			verb = "linter doesn't"
		}
		_, _ = fmt.Fprintf(&b, "(%d other %s match any file here — rtunk linters list --all)\n", n, verb)
	}
	_, _ = fmt.Fprintf(&b, "\nEnable one with: rtunk %s <id>\n", hintCmd)
	_, err := io.WriteString(w, b.String())
	return err
}
