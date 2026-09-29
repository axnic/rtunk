// shared.go holds the helpers used across more than one command family: config resolution,
// git-backed file selection, linter filtering, renderer construction, the run log, and the
// `linters list`/`actions list` rendering. Anything scoped to a single command tree lives next to
// that command's own file(s) instead.
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/xunleii/rtunk/internal/cli/render"
	"github.com/xunleii/rtunk/pkg/cache/download"
	"github.com/xunleii/rtunk/pkg/git"
	"github.com/xunleii/rtunk/pkg/run/actions"
	"github.com/xunleii/rtunk/pkg/run/engine"
	"github.com/xunleii/rtunk/pkg/run/runlog"
	"github.com/xunleii/rtunk/pkg/trunk/config"
)

// findConfig walks up from the working directory looking for .rtunk/rtunk.yaml (preferred) or
// .trunk/trunk.yaml (fallback, for compat with a repo that hasn't run `rtunk init` yet) at each
// level, the same way git locates .git -- the nearest directory that has either file wins, and
// .rtunk/rtunk.yaml is preferred over .trunk/trunk.yaml when a single directory has both (the
// common case once `rtunk init` has run in an existing trunk repo). The walk is bounded by the git
// repository root (if any): a miss must not fall through to an unrelated config file sitting
// further up the filesystem, e.g. in a parent repo or the home dir. Outside a git repo, it falls
// back to walking to the filesystem root.
func findConfig() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	start := dir

	var gitRoot string
	if out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output(); err == nil {
		gitRoot = strings.TrimSpace(string(out))
	}

	for {
		if candidate := filepath.Join(dir, ".rtunk", "rtunk.yaml"); fileExists(candidate) {
			return candidate, nil
		}
		if candidate := filepath.Join(dir, ".trunk", "trunk.yaml"); fileExists(candidate) {
			return candidate, nil
		}
		if dir == gitRoot {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("no .rtunk/rtunk.yaml or .trunk/trunk.yaml found (searched from %s upward); use --config to specify one", start)
}

// fileExists is findConfig's own os.Stat-based existence check, factored out since it's now
// called twice per directory level instead of once.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// resolveConfig finds (unless configPath is already set) and resolves the trunk.yaml in effect.
// all selects ResolveAll (the full merged catalog) over Resolve (enabled+used only) -- only
// `plugins print` and `linters list`/`actions list` set it.
func resolveConfig(configPath, cacheDir string, all bool) (config.Config, error) {
	if configPath == "" {
		found, err := findConfig()
		if err != nil {
			return config.Config{}, err
		}
		configPath = found
	}
	if all {
		return config.ResolveAll(configPath, cacheDir)
	}
	return config.Resolve(configPath, cacheDir)
}

// checkDeprecations hard-refuses cfg if it enables a legacy-shaped linter (entry #8), else prints
// a warning to stderr for every enabled linter/command carrying a deprecated: message (entry #9).
// Called by check/fmt's Run right after resolveConfig, "before any execution starts" -- read-only
// inspection commands (config print, linters list) call resolveConfig directly and skip this, so a
// broken configuration can still be inspected in order to fix it.
func checkDeprecations(cfg config.Config, stderr io.Writer) error {
	warnings, err := cfg.CheckDeprecations()
	for _, w := range warnings {
		_, _ = fmt.Fprintln(stderr, "rtunk: warning:", w)
	}
	return err
}

// resolvedVersionFor is the version `where`/`exec` (toolbox) resolve for category+id when the CLI
// arg wasn't pinned with @version -- mirrors fetchToolRef/fetchRuntimeRef's own resolution
// (pkg/cache/download.Download) so both commands predict the exact cache path Download would use,
// without invoking it. "actions" has no KnownGoodVersion of its own (only Actions.Enabled's own
// @version pin, if any); "lint"/"plugins" resolve to a linter/plugin, not a concrete tool or
// runtime build, so there is no path to predict -- reject rather than guess.
func resolvedVersionFor(cfg config.Config, category, id string) (string, error) {
	switch category {
	case "runtimes":
		return download.ResolveVersion(cfg.Runtimes.Enabled, id, cfg.Runtimes.Definitions[id].KnownGoodVersion), nil
	case "tools":
		return download.ResolveVersion(cfg.Lint.Enabled, id, cfg.Tools[id].KnownGoodVersion), nil
	case "actions":
		return download.ResolveVersion(cfg.Actions.Enabled, id, ""), nil
	default:
		return "", fmt.Errorf("rtunk: %s has no resolvable version; pin one with %s@<version>", category, id)
	}
}

// printValue marshals v as YAML (every definition's field tags, e.g. Linter/Tool/Runtime, are
// already YAML tags matching plugin.yaml's own vocabulary) and, for --output json, round-trips
// that through yaml.Unmarshal into a generic any -- yaml.v3 decodes mappings into
// map[string]interface{}, so the result re-marshals as JSON with the same field names, no
// separate set of json tags to keep in sync.
func printValue(w io.Writer, v any, format string) error {
	data, err := yaml.Marshal(v)
	if err != nil {
		return err
	}

	switch format {
	case "", "yaml":
		_, err := w.Write(data)
		return err
	case "json":
		var generic any
		if err := yaml.Unmarshal(data, &generic); err != nil {
			return err
		}
		js, err := json.MarshalIndent(generic, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(w, string(js))
		return err
	default:
		return fmt.Errorf("invalid --output %q: must be yaml or json", format)
	}
}

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
	files = dropSymlinks(files)
	if len(files) == 0 {
		return nil, errNoFiles
	}
	return files, nil
}

// dropSymlinks removes symlinks from files: linters like prettier refuse an explicit symlink
// target outright, and a tracked symlink's real content is already selected separately under its
// target path, so nothing is lost by skipping the link itself.
func dropSymlinks(files []string) []string {
	out := files[:0]
	for _, f := range files {
		if info, err := os.Lstat(f); err == nil && info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		out = append(out, f)
	}
	return out
}

// selectFiles is the no-path file selection: the diff from the diff base (from, else the branch's
// upstream, else HEAD) to the working tree, plus untracked non-ignored files. With an explicit
// base or an upstream, the diff base is merge-base(base, HEAD); with neither, it is HEAD directly,
// so the no-upstream case picks up every staged and unstaged change since the last commit, not
// staged changes only -- or, in a repo with no commits yet (no HEAD to diff against), everything.
// Outside git, errOutsideGitNoPaths. Deleted files are never selected. Paths are absolute.
func selectFiles(repoRoot, from string) ([]string, error) {
	if !git.IsRepo(repoRoot) {
		return nil, errOutsideGitNoPaths
	}
	changed, untracked, err := git.ChangedFiles(repoRoot, from)
	if err != nil {
		return nil, err
	}
	return append(changed, untracked...), nil
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
	if !git.IsRepo(repoRoot) {
		return abs, nil
	}
	listed, err := git.Files(repoRoot, abs...)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, f := range listed {
		if info, err := os.Stat(f); err == nil && !info.IsDir() { // -c lists index entries deleted on disk
			files = append(files, f)
		}
	}
	return files, nil
}

// partiallyStaged returns the absolute paths with both staged and unstaged changes: rewriting one
// in the working tree would leave the index out of sync with what the user staged.
func partiallyStaged(repoRoot string) map[string]bool {
	unstaged, _ := git.DiffNames(repoRoot, false)
	staged, _ := git.DiffNames(repoRoot, true)
	in := map[string]bool{}
	for _, f := range staged {
		in[f] = true
	}
	out := map[string]bool{}
	for _, f := range unstaged {
		if in[f] {
			out[f] = true
		}
	}
	return out
}

// filterLinters implements check/fmt's --filter and --exclude flags: a runtime-only restriction
// on which linters run this invocation, applied by trimming cfg.Lint.Definitions before it
// reaches engine.Run (engine.Run iterates exactly that map -- see pkg/run/engine/engine.go's
// own Run, which ranges over env.Cfg.Lint.Definitions -- so trimming it here needs no engine
// change). Nothing is written back to trunk.yaml; this only affects the one invocation.
//
// filter is an allow-list (bare ids only) or a deny-list (every id prefixed with "-"), matching
// real trunk's own --filter semantics ("comma separated list of linters... to include or
// exclude"). Mixing bare and "-"-prefixed ids in the same --filter value is a usage error --
// trunk's own docs describe --filter as one list or the other, not both at once. exclude is
// always a deny-list (real trunk's own "--exclude: shorthand for an inverse --filter"), so its
// entries are never "-"-prefixed by the caller.
//
// Passing both filter and exclude is a usage error: they are two spellings of the same mechanism
// (real trunk's own docs call --exclude sugar for one specific --filter shape), not independently
// composable filters. Passing neither returns cfg unchanged.
func filterLinters(cfg config.Config, filter, exclude string) (config.Config, error) {
	if filter == "" && exclude == "" {
		return cfg, nil
	}
	if filter != "" && exclude != "" {
		return config.Config{}, fmt.Errorf("rtunk: --filter and --exclude are mutually exclusive")
	}

	var keep map[string]bool // nil means "allow-list mode": keep only these ids
	var drop map[string]bool // deny-list mode: keep everything except these ids

	if filter != "" {
		ids := strings.Split(filter, ",")
		isDenyList := strings.HasPrefix(ids[0], "-")
		keep = map[string]bool{}
		drop = map[string]bool{}
		for _, raw := range ids {
			isDeny := strings.HasPrefix(raw, "-")
			if isDeny != isDenyList {
				return config.Config{}, fmt.Errorf("rtunk: --filter cannot mix included and excluded linters (%q)", filter)
			}
			id := strings.TrimPrefix(raw, "-")
			if _, ok := cfg.Lint.Definitions[id]; !ok {
				return config.Config{}, fmt.Errorf("rtunk: --filter: unknown linter %q", id)
			}
			if isDeny {
				drop[id] = true
			} else {
				keep[id] = true
			}
		}
		if isDenyList {
			keep = nil // switch to deny-list mode below
		} else {
			drop = nil
		}
	} else {
		drop = map[string]bool{}
		for id := range strings.SplitSeq(exclude, ",") {
			if _, ok := cfg.Lint.Definitions[id]; !ok {
				return config.Config{}, fmt.Errorf("rtunk: --exclude: unknown linter %q", id)
			}
			drop[id] = true
		}
	}

	trimmed := map[string]config.Linter{}
	for id, def := range cfg.Lint.Definitions {
		switch {
		case keep != nil:
			if keep[id] {
				trimmed[id] = def
			}
		case drop != nil:
			if !drop[id] {
				trimmed[id] = def
			}
		}
	}
	cfg.Lint.Definitions = trimmed
	return cfg, nil
}

// progressOpts is what the user asked for on the progress side of a run.
type progressOpts struct {
	NoProgress bool // --no-progress: no per-linter lines and no live view
	ASCII      bool // --ascii: ASCII glyphs in the live view
	LiveHeight int  // --live-height / RTUNK_LIVE_HEIGHT; 0 means half the terminal
}

// newRenderer builds the renderer for a --format value ("human", "json" or "sarif"), reading the
// environment once: stdout being a terminal and NO_COLOR decide color, stderr being a terminal
// and TERM decide the live view, the locale decides ASCII glyphs.
func newRenderer(format string, stdout, stderr io.Writer, kind render.Kind, p progressOpts) render.Renderer {
	utf8 := render.IsUTF8Locale(os.Getenv("LC_ALL"), os.Getenv("LC_CTYPE"), os.Getenv("LANG"))
	return buildRenderer(format, stdout, stderr, kind, p, isTerminal(stderr), os.Getenv("TERM"), utf8)
}

// buildRenderer is newRenderer with the environment decisions passed in, so they can be tested.
// With the live view active the inner renderer gets NoProgress (its plain lines would collide with
// the area) and is wrapped; otherwise the v0.9.1 plain progress lines are untouched.
func buildRenderer(format string, stdout, stderr io.Writer, kind render.Kind, p progressOpts, stderrIsTTY bool, term string, utf8 bool) render.Renderer {
	f := render.Human
	switch format {
	case "json":
		f = render.JSON
	case "sarif":
		f = render.SARIF
	}
	liveOn := render.LiveEnabled(stderrIsTTY, p.NoProgress, term)
	inner := render.New(stdout, stderr, render.Options{
		Format: f, Command: kind, NoProgress: p.NoProgress || liveOn,
		Color:   render.UseColor(isTerminal(stdout), os.Getenv("NO_COLOR")),
		Version: Version,
	})
	if !liveOn {
		return inner
	}
	size := func() (int, int) { return 80, 24 }
	if file, ok := stderr.(*os.File); ok {
		size = func() (int, int) { return render.TermSize(file) }
	}
	return render.NewLive(inner, render.LiveOptions{
		Out: stderr, Size: size, Height: p.LiveHeight, ASCII: p.ASCII || !utf8, Command: kind,
	})
}

// isTerminal reports whether w is a character device (a terminal). ModeCharDevice is also true
// for /dev/null: harmless for color, and replaced by golang.org/x/term with the live view.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// stdinIsTerminal is a variable so tests can pin it: `go test` may hand the test binary the
// caller's own terminal as stdin, which would silently turn the actions-run log off.
var stdinIsTerminal = actions.IsInteractive

// startLog opens the run log for one command invocation (returning nil, a valid no-op Writer, if
// the log cannot be opened -- Start prints the one warning to stderr). The caller ends it with
// log.End once it knows whether the run itself failed.
func startLog(cli *CLI, cmd, repoRoot, configPath string, argv Argv, concurrency int, dryRun bool, stderr io.Writer) *runlog.Writer {
	// run_start.repo_root must be usable from any cwd, so never record a relative path.
	if abs, err := filepath.Abs(repoRoot); err == nil {
		repoRoot = abs
	}
	return runlog.Start(runlog.StartOpts{
		CacheDir: cli.CacheDir, RepoRoot: repoRoot, Cmd: cmd, Version: Version,
		Argv: append([]string{"rtunk"}, argv...), Config: configPath,
		Concurrency: concurrency, DryRun: dryRun, Warn: stderr,
	})
}

// logsRepoRoot is the repository key the run-writing commands log under: the config file's
// grandparent directory (<repoRoot>/.trunk/trunk.yaml or <repoRoot>/.rtunk/rtunk.yaml), the same
// derivation check and fmt use for their repoRoot.
func logsRepoRoot(cli *CLI) (string, error) {
	configPath := cli.Config
	if configPath == "" {
		found, err := findConfig()
		if err != nil {
			return "", err
		}
		configPath = found
	}
	return filepath.Dir(filepath.Dir(configPath)), nil
}

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
	if git.IsRepo(repoRoot) {
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
