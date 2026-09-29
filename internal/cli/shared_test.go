package cli

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/trunk/config"
)

// --- filterLinters ---

func threeLinterConfig() config.Config {
	return config.Config{
		Lint: config.LintConfig{
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{
					"actionlint": {Description: "lints workflows"},
					"prettier":   {Description: "formats"},
					"shellcheck": {Description: "lints shell"},
				},
			},
		},
	}
}

func TestFilterLinters_NeitherGiven_ReturnsUnchanged(t *testing.T) {
	cfg := threeLinterConfig()
	got, err := filterLinters(cfg, "", "")
	require.NoError(t, err)
	assert.Equal(t, cfg, got)
}

func TestFilterLinters_FilterAllowList(t *testing.T) {
	got, err := filterLinters(threeLinterConfig(), "actionlint,prettier", "")
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"actionlint": true, "prettier": true}, keysOf(got.Lint.Definitions))
}

func TestFilterLinters_FilterDenyList(t *testing.T) {
	got, err := filterLinters(threeLinterConfig(), "-prettier", "")
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"actionlint": true, "shellcheck": true}, keysOf(got.Lint.Definitions))
}

func TestFilterLinters_FilterMixedAllowAndDeny_IsUsageError(t *testing.T) {
	_, err := filterLinters(threeLinterConfig(), "actionlint,-prettier", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mix")
}

func TestFilterLinters_UnknownIDInFilter_IsUsageError(t *testing.T) {
	_, err := filterLinters(threeLinterConfig(), "does-not-exist", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"does-not-exist"`)
}

func TestFilterLinters_UnknownIDInExclude_IsUsageError(t *testing.T) {
	_, err := filterLinters(threeLinterConfig(), "", "does-not-exist")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"does-not-exist"`)
}

func TestFilterLinters_Exclude(t *testing.T) {
	got, err := filterLinters(threeLinterConfig(), "", "shellcheck")
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"actionlint": true, "prettier": true}, keysOf(got.Lint.Definitions))
}

func TestFilterLinters_ExcludeMultiple(t *testing.T) {
	got, err := filterLinters(threeLinterConfig(), "", "shellcheck,prettier")
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"actionlint": true}, keysOf(got.Lint.Definitions))
}

func TestFilterLinters_BothFilterAndExclude_IsUsageError(t *testing.T) {
	_, err := filterLinters(threeLinterConfig(), "actionlint", "shellcheck")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--filter and --exclude")
}

func keysOf(m map[string]config.Linter) map[string]bool {
	out := map[string]bool{}
	for k := range m {
		out[k] = true
	}
	return out
}

// --- renderer / format ---

// twoLinterFixture: alpha reports one finding on work/file.txt, beta fails to run.
func twoLinterFixture(t *testing.T) (cfgPath, work string) {
	t.Helper()
	cfgPath, repoRoot := writeLinterFixture(t, []string{"alpha", "beta"}, `    - name: alpha
      files: [ALL]
      commands:
        - name: check
          run: "false"
          output: pass_fail
    - name: beta
      files: [ALL]
      commands:
        - name: check
          run: "false"
          output: pass_fail
          error_codes: [1]
`)
	work = filepath.Join(repoRoot, "work")
	require.NoError(t, os.MkdirAll(work, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(work, "file.txt"), []byte("hi\n"), 0o644))
	return cfgPath, work
}

func TestFormatJSON_OneDocumentOnStdoutAndProgressOnStderr(t *testing.T) {
	cfgPath, work := twoLinterFixture(t)
	cache := t.TempDir()

	stdout, stderr, err := run2(t, "--config", cfgPath, "--cache-dir", cache, "check", "--format", "json", work)
	require.Error(t, err, "findings and a failed linter still exit non-zero")
	dec := json.NewDecoder(strings.NewReader(stdout))
	var doc struct {
		Command  string `json:"command"`
		Issues   []struct{ File, Linter string }
		Failures []struct{ Linter string }
	}
	require.NoError(t, dec.Decode(&doc))
	assert.ErrorIs(t, dec.Decode(new(any)), io.EOF, "exactly one JSON document")
	assert.Equal(t, "check", doc.Command)
	require.Len(t, doc.Issues, 1)
	assert.Equal(t, "work/file.txt", doc.Issues[0].File)
	assert.Equal(t, "alpha", doc.Issues[0].Linter)
	require.Len(t, doc.Failures, 1)
	assert.Equal(t, "beta", doc.Failures[0].Linter)
	assert.Contains(t, stderr, "alpha", "progress stays on stderr")

	_, quiet, _ := run2(t, "--config", cfgPath, "--cache-dir", cache, "check", "--format", "json", "--no-progress", work)
	assert.Empty(t, quiet)
}

func TestFormatSARIF_CheckEmitsOneDocument(t *testing.T) {
	cfgPath, work := twoLinterFixture(t)
	stdout, _, err := run2(t, "--config", cfgPath, "--cache-dir", t.TempDir(), "check", "--format", "sarif", work)
	require.Error(t, err)
	var doc struct {
		Version string
		Runs    []struct{ Results []any }
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &doc))
	assert.Equal(t, "2.1.0", doc.Version)
	require.Len(t, doc.Runs, 1)
	assert.Len(t, doc.Runs[0].Results, 1)
}

func TestFormat_UnknownIsAUsageError(t *testing.T) {
	cfgPath, work := twoLinterFixture(t)
	_, _, err := run2(t, "--config", cfgPath, "check", "--format", "xml", work)
	assert.Error(t, err)
	_, _, err = run2(t, "--config", cfgPath, "fmt", "--format", "xml", work)
	assert.Error(t, err)
}

func TestFormat_HumanOutsideATerminalHasNoEscapes(t *testing.T) {
	cfgPath, work := twoLinterFixture(t)
	stdout, _, _ := run2(t, "--config", cfgPath, "--cache-dir", t.TempDir(), "check", work)
	assert.NotContains(t, stdout, "\x1b")
	assert.Contains(t, stdout, "  (1)\n")
}

func TestIsTerminal(t *testing.T) {
	assert.False(t, isTerminal(io.Discard))
	assert.False(t, isTerminal(&strings.Builder{}))
	f, err := os.CreateTemp(t.TempDir(), "x")
	require.NoError(t, err)
	assert.False(t, isTerminal(f), "a regular file is not a terminal")
}

func TestFmtSARIFIsRefusedBeforeAnythingRuns(t *testing.T) {
	cfgPath, repoRoot := writeLinterFixture(t, []string{"fx"}, fmtFixture)
	target := filepath.Join(repoRoot, "a.txt")
	require.NoError(t, os.WriteFile(target, []byte("messy\n"), 0o644))
	cache := t.TempDir()

	stdout, stderr, err := run2(t, "--config", cfgPath, "--cache-dir", cache, "fmt", "--format", "sarif", target)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--format sarif is only supported by check")
	assert.Empty(t, stdout)
	assert.Empty(t, stderr, "no linter ran, so no progress")
	got, _ := os.ReadFile(target)
	assert.Equal(t, "messy\n", string(got), "nothing was written")
	entries, _ := os.ReadDir(filepath.Join(cache, "logs"))
	assert.Empty(t, entries, "no run log was started")
}

func TestFmtJSON_ListsChangedFiles(t *testing.T) {
	cfgPath, repoRoot := writeLinterFixture(t, []string{"fx"}, fmtFixture)
	target := filepath.Join(repoRoot, "a.txt")
	require.NoError(t, os.WriteFile(target, []byte("messy\n"), 0o644))

	stdout, _, err := run2(t, "--config", cfgPath, "--cache-dir", t.TempDir(), "fmt", "--format", "json", target)
	require.NoError(t, err)
	var doc struct {
		Command string
		Changed []string
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &doc))
	assert.Equal(t, "fmt", doc.Command)
	assert.Equal(t, []string{"a.txt"}, doc.Changed)

	require.NoError(t, os.WriteFile(target, []byte("messy\n"), 0o644))
	stdout, _, err = run2(t, "--config", cfgPath, "--cache-dir", t.TempDir(), "fmt", "--check", "--format", "json", target)
	require.Error(t, err, "--check with files to reformat still exits non-zero")
	require.NoError(t, json.Unmarshal([]byte(stdout), &doc))
	assert.Equal(t, []string{"a.txt"}, doc.Changed)
}

func TestCheckFix_MachineFormatsEmitOneDocument(t *testing.T) {
	cfgPath, repoRoot := writeLinterFixture(t, []string{"fakefix"}, `    - name: fakefix
      files: [ALL]
      commands:
        - name: lint
          run: grep -qxF formatted ${target}
          output: pass_fail
        - name: format
          run: printf 'formatted\n' > ${target}
          output: rewrite
          success_codes: [0]
          in_place: true
          formatter: true
`)
	target := filepath.Join(repoRoot, "f.txt")
	require.NoError(t, os.WriteFile(target, []byte("messy\n"), 0o644))

	stdout, _, err := run2(t, "--config", cfgPath, "--cache-dir", t.TempDir(), "check", "--format-before-check", "--format", "json", target)
	require.NoError(t, err)
	dec := json.NewDecoder(strings.NewReader(stdout))
	var doc struct {
		Issues  []any
		Changed []string
	}
	require.NoError(t, dec.Decode(&doc))
	assert.ErrorIs(t, dec.Decode(new(any)), io.EOF, "one document, not one per pass")
	assert.Empty(t, doc.Issues, "the formatter pass fixed the file before the check pass")
	assert.Equal(t, []string{"f.txt"}, doc.Changed)

	require.NoError(t, os.WriteFile(target, []byte("messy\n"), 0o644))
	stdout, _, err = run2(t, "--config", cfgPath, "--cache-dir", t.TempDir(), "check", "--format-before-check", "--format", "sarif", target)
	require.NoError(t, err)
	var sarif map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &sarif))
	assert.Equal(t, "2.1.0", sarif["version"])
}

func TestMachineFormats_NoFilesWritesNoDocument(t *testing.T) {
	cfgPath, work := twoLinterFixture(t)
	repoRoot := filepath.Dir(work)
	gitCmd(t, repoRoot, "init", "-q", "-b", "main")
	gitCmd(t, repoRoot, "add", ".")
	gitCmd(t, repoRoot, "commit", "-q", "-m", "init") // clean tree, no upstream: no path means legitimately nothing to do
	for _, format := range []string{"json", "sarif"} {
		stdout, _, err := run2(t, "--config", cfgPath, "--cache-dir", t.TempDir(), "check", "--format", format)
		require.NoError(t, err)
		assert.Empty(t, stdout)
	}
}

func TestCheckFix_FormatterFailureIsInTheMachineDocument(t *testing.T) {
	cfgPath, repoRoot := writeLinterFixture(t, []string{"brokenfmt"}, `    - name: brokenfmt
      files: [ALL]
      commands:
        - name: lint
          run: "true"
          output: pass_fail
        - name: format
          run: "false"
          output: rewrite
          success_codes: [0]
          in_place: true
          formatter: true
`)
	target := filepath.Join(repoRoot, "f.txt")
	require.NoError(t, os.WriteFile(target, []byte("x\n"), 0o644))

	stdout, _, err := run2(t, "--config", cfgPath, "--cache-dir", t.TempDir(), "check", "--format-before-check", "--format", "json", target)
	require.Error(t, err, "the formatter failure still exits non-zero")
	var doc struct {
		Failures []struct{ Linter, Error string }
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &doc))
	require.Len(t, doc.Failures, 1, "the document must explain the exit code")
	assert.Equal(t, "brokenfmt", doc.Failures[0].Linter)

	stdout, _, err = run2(t, "--config", cfgPath, "--cache-dir", t.TempDir(), "check", "--format-before-check", "--format", "sarif", target)
	require.Error(t, err)
	assert.Contains(t, stdout, `"executionSuccessful": false`)
}

// --- linters list / actions list ---

// listFixture: a git repo with a.go, b.go and README.md, and five linters over go/markdown/yaml
// file types: gofmt (enabled, pinned), mdlint (enabled), vet (available: matches go), gitleaks
// (available: ALL), yamllint (matches nothing here).
func listFixture(t *testing.T, inGit bool) (cfgPath string) {
	t.Helper()
	cfgPath, repoRoot := writeLinterFixture(t, []string{"gofmt@1.2.3", "mdlint"}, `    - name: gofmt
      description: Format go
      files: [go]
    - name: mdlint
      description: Lint markdown
      files: [markdown]
    - name: vet
      description: Vet go
      files: [go]
    - name: gitleaks
      description: Find secrets
      files: [ALL]
    - name: yamllint
      description: Lint yaml
      files: [yaml]
`)
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "pluginrepo", "linters", "plugin.yaml"), []byte(`version: "0.1"
lint:
  files:
    - name: go
      extensions: [go]
    - name: markdown
      extensions: [md]
    - name: yaml
      extensions: [ymlx]
`), 0o644))
	for name, body := range map[string]string{"a.go": "package a\n", "b.go": "package b\n", "README.md": "# hi\n"} {
		require.NoError(t, os.WriteFile(filepath.Join(repoRoot, name), []byte(body), 0o644))
	}
	if inGit {
		gitCmd(t, repoRoot, "init", "-q", "-b", "main")
		require.NoError(t, os.WriteFile(filepath.Join(repoRoot, ".gitignore"), []byte("pluginrepo/\n"), 0o644))
		gitCmd(t, repoRoot, "add", ".")
	}
	return cfgPath
}

func TestLintersList_GroupsAndCounts(t *testing.T) {
	cfgPath := listFixture(t, true)
	stdout, stderr, err := run2(t, "--config", cfgPath, "linters", "list")
	require.NoError(t, err, "stderr: %s", stderr)

	assert.Equal(t, ""+
		"Enabled\n"+
		"  ✔ gofmt@1.2.3  2 go files\n"+
		"  ✔ mdlint       1 markdown file\n"+
		"Available for this repo (not enabled)\n"+
		"  ◯ gitleaks     5 files\n"+
		"  ◯ vet          2 go files\n"+
		"(1 other linter doesn't match any file here — rtunk linters list --all)\n"+
		"\n"+
		"Enable one with: rtunk linters enable <id>\n", stdout)
}

func TestLintersList_AllAddsTheRest(t *testing.T) {
	cfgPath := listFixture(t, true)
	stdout, _, err := run2(t, "--config", cfgPath, "linters", "list", "--all")
	require.NoError(t, err)
	assert.Contains(t, stdout, "Other (no matching file)\n  ◯ yamllint     0 yaml files\n")
	assert.NotContains(t, stdout, "rtunk linters list --all")
}

func TestLintersList_JSON(t *testing.T) {
	cfgPath := listFixture(t, true)
	stdout, _, err := run2(t, "--config", cfgPath, "linters", "list", "--format", "json")
	require.NoError(t, err)
	var doc struct {
		Enabled []struct {
			ID      string `json:"id"`
			Version string `json:"version"`
			Files   int    `json:"files"`
		} `json:"enabled"`
		Available []struct {
			ID    string `json:"id"`
			Files int    `json:"files"`
		} `json:"available"`
		Other []any `json:"other"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &doc))
	require.Len(t, doc.Enabled, 2)
	assert.Equal(t, "gofmt", doc.Enabled[0].ID)
	assert.Equal(t, "1.2.3", doc.Enabled[0].Version)
	assert.Equal(t, 2, doc.Enabled[0].Files)
	require.Len(t, doc.Available, 2)
	assert.Equal(t, "gitleaks", doc.Available[0].ID)
	assert.NotNil(t, doc.Other, "[] not null")
	assert.Empty(t, doc.Other, "other is only filled with --all")

	stdout, _, err = run2(t, "--config", cfgPath, "linters", "list", "--all", "--format", "json")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal([]byte(stdout), &doc))
	assert.Len(t, doc.Other, 1)
}

func TestLintersList_OutsideGitStillCounts(t *testing.T) {
	cfgPath := listFixture(t, false)
	stdout, _, err := run2(t, "--config", cfgPath, "linters", "list")
	require.NoError(t, err)
	assert.Contains(t, stdout, "gofmt@1.2.3")
	assert.Contains(t, stdout, "2 go files")
}

// suggestIfFixture builds a minimal config.Config with a single "go" file type (extension .go)
// plus whichever linters the caller adds to Definitions -- buildLintersList never touches disk,
// so files/repoRoot need not point at anything real.
func suggestIfFixture() config.Config {
	var cfg config.Config
	cfg.Lint.Definitions = map[string]config.Linter{}
	cfg.Lint.Files = map[string]config.FileType{
		"go":   {Name: "go", Extensions: []string{"go"}},
		"yaml": {Name: "yaml", Extensions: []string{"ymlx"}}, // deliberately matches nothing below
	}
	return cfg
}

func TestBuildLintersList_SuggestIfNever_NeverSuggestedEvenWithMatchingFiles(t *testing.T) {
	cfg := suggestIfFixture()
	cfg.Lint.Definitions["nope"] = config.Linter{Name: "nope", Files: []string{"go"}, SuggestIf: "never"}

	l := buildLintersList(cfg, "/repo", []string{"/repo/a.go"})

	assert.NotContains(t, ids(l.Available), "nope")
	assert.Contains(t, ids(l.Other), "nope")
}

func TestBuildLintersList_SuggestIfConfigPresent_SuggestedByConfigFileAlone(t *testing.T) {
	cfg := suggestIfFixture()
	cfg.Lint.Definitions["cfglint"] = config.Linter{
		Name:          "cfglint",
		Files:         []string{"yaml"}, // matches 0 files below
		DirectConfigs: []string{".foolintrc"},
		SuggestIf:     "config_present",
	}

	l := buildLintersList(cfg, "/repo", []string{"/repo/.foolintrc"})

	assert.Contains(t, ids(l.Available), "cfglint")
	assert.NotContains(t, ids(l.Other), "cfglint")
}

func TestBuildLintersList_SuggestIfUnset_UnchangedFromToday(t *testing.T) {
	cfg := suggestIfFixture()
	cfg.Lint.Definitions["plainlint"] = config.Linter{Name: "plainlint", Files: []string{"go"}}

	l := buildLintersList(cfg, "/repo", []string{"/repo/a.go"})

	assert.Contains(t, ids(l.Available), "plainlint")
	assert.NotContains(t, ids(l.Other), "plainlint")
}

func ids(items []listItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.ID
	}
	return out
}

func TestActionsList_TwoGroups(t *testing.T) {
	stdout, stderr, err := run2(t, "--config", trunkYAML, "actions", "list")
	require.NoError(t, err, "stderr: %s", stderr)
	assert.Contains(t, stdout, "Enabled\n  ✔ commitlint")
	assert.Contains(t, stdout, "Enable one with: rtunk actions enable <id>\n")
	assert.NotContains(t, stdout, "Other (")

	js, _, err := run2(t, "--config", trunkYAML, "actions", "list", "--format", "json")
	require.NoError(t, err)
	var doc struct {
		Enabled []struct {
			ID string `json:"id"`
		} `json:"enabled"`
		Available []any `json:"available"`
	}
	require.NoError(t, json.Unmarshal([]byte(js), &doc))
	require.Len(t, doc.Enabled, 1)
	assert.Equal(t, "commitlint", doc.Enabled[0].ID)
	assert.NotNil(t, doc.Available)
}

// --- git-backed file selection ---

func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t", "-c", "commit.gpgsign=false"}, args...)...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
}

// newRepo returns a git repo with a.txt and b.txt committed on main.
func newRepo(t *testing.T) string {
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "a.txt", "a")
	write(t, dir, "b.txt", "b")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func TestSelectFiles_NotInGit(t *testing.T) {
	_, err := selectFiles(t.TempDir(), "")
	require.ErrorIs(t, err, errOutsideGitNoPaths)
}

// TestErrOutsideGitNoPaths_NoOwnPrefix: cmd/rtunk/main.go prints every returned error as
// "rtunk: "+err.Error() -- a sentinel that embeds its own "rtunk: " prefix doubles it in the
// user-facing output ("rtunk: rtunk: ..."). No other error in this package carries the prefix.
func TestErrOutsideGitNoPaths_NoOwnPrefix(t *testing.T) {
	assert.False(t, strings.HasPrefix(errOutsideGitNoPaths.Error(), "rtunk:"),
		"main.go already adds the \"rtunk:\" prefix to every returned error")
}

func TestSelectFiles_NoUpstream_DiffFromHEADAndUntracked(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "c.txt", "c")
	gitCmd(t, dir, "add", "c.txt")
	gitCmd(t, dir, "commit", "-qam", "add c")

	write(t, dir, "a.txt", "a2")
	gitCmd(t, dir, "add", "a.txt")                             // staged
	write(t, dir, "b.txt", "b2")                               // unstaged
	write(t, dir, "new.txt", "n")                              // untracked
	require.NoError(t, os.Remove(filepath.Join(dir, "c.txt"))) // deleted: not selected

	files, err := selectFiles(dir, "")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{
		filepath.Join(dir, "a.txt"), filepath.Join(dir, "b.txt"), filepath.Join(dir, "new.txt"),
	}, files)
}

// TestSelectFiles_NoUpstream_NoCommitsYet: a fresh `git init` with no commits has no HEAD to diff
// against -- "everything since the last commit" should reasonably mean everything, not a raw git
// error surfaced from a `diff ... HEAD` that can't resolve.
func TestSelectFiles_NoUpstream_NoCommitsYet(t *testing.T) {
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "a.txt", "a")
	gitCmd(t, dir, "add", "a.txt") // staged
	write(t, dir, "new.txt", "n")  // untracked

	files, err := selectFiles(dir, "")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{filepath.Join(dir, "a.txt"), filepath.Join(dir, "new.txt")}, files)
}

func TestSelectFiles_Upstream_DiffAndUntracked(t *testing.T) {
	dir := newRepo(t)
	gitCmd(t, dir, "branch", "base")
	gitCmd(t, dir, "checkout", "-q", "-b", "feat")
	gitCmd(t, dir, "branch", "--set-upstream-to=base")
	write(t, dir, "a.txt", "a2")
	gitCmd(t, dir, "commit", "-qam", "change a")
	write(t, dir, "b.txt", "b2") // unstaged
	write(t, dir, "new.txt", "n")
	require.NoError(t, os.Remove(filepath.Join(dir, "a.txt"))) // deleted: not selected

	files, err := selectFiles(dir, "")
	require.NoError(t, err)
	assert.Equal(t, []string{filepath.Join(dir, "b.txt"), filepath.Join(dir, "new.txt")}, files)
}

func TestSelectFiles_From(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "a.txt", "a2")
	gitCmd(t, dir, "commit", "-qam", "change a")

	files, err := selectFiles(dir, "HEAD~1")
	require.NoError(t, err)
	assert.Equal(t, []string{filepath.Join(dir, "a.txt")}, files)

	_, err = selectFiles(dir, "nope")
	assert.Error(t, err)
}

// TestResolvePaths_DropsSymlinks: an untracked symlink (e.g. a root-level `.markdownlint.yaml` ->
// `.trunk/configs/.markdownlint.yaml` convention) must not reach formatters -- prettier refuses an
// explicit symlink target outright, and the link's real content is already selected separately
// under its target path.
func TestResolvePaths_DropsSymlinks(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "new.txt", "n") // untracked, alongside the symlink
	require.NoError(t, os.Symlink(filepath.Join(dir, "a.txt"), filepath.Join(dir, "link.txt")))

	files, err := resolvePaths(dir, nil, "")
	require.NoError(t, err)
	assert.NotContains(t, files, filepath.Join(dir, "link.txt"))
	assert.Contains(t, files, filepath.Join(dir, "new.txt"))
}

func TestExpandPaths(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "new.txt", "n")
	write(t, dir, "ignored.log", "i")
	write(t, dir, ".gitignore", "*.log\n")
	require.NoError(t, os.Remove(filepath.Join(dir, "b.txt")))

	got, err := expandPaths(dir, []string{dir})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{
		filepath.Join(dir, ".gitignore"), filepath.Join(dir, "a.txt"), filepath.Join(dir, "new.txt"),
	}, got)

	_, err = expandPaths(dir, []string{filepath.Join(dir, "missing")})
	assert.Error(t, err)

	// outside git: passthrough
	plain := t.TempDir()
	got, err = expandPaths(plain, []string{plain})
	require.NoError(t, err)
	assert.Equal(t, []string{plain}, got)
}

func TestPartiallyStaged(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "a.txt", "a2")
	gitCmd(t, dir, "add", "a.txt")
	write(t, dir, "a.txt", "a3") // staged + unstaged
	write(t, dir, "b.txt", "b2")
	gitCmd(t, dir, "add", "b.txt") // fully staged

	got := partiallyStaged(dir)
	assert.Equal(t, map[string]bool{filepath.Join(dir, "a.txt"): true}, got)
}

const fmtFixture = `    - name: fx
      files: [ALL]
      commands:
        - name: format
          run: printf 'formatted\n' > ${target}
          output: rewrite
          success_codes: [0]
          in_place: true
          formatter: true
`

func TestCheckFmt_NoPathsOutsideGit_HardError(t *testing.T) {
	cfgPath, _ := writeLinterFixture(t, []string{"fx"}, fmtFixture)
	for _, cmd := range []string{"check", "fmt"} {
		_, stderr, err := run2(t, "--config", cfgPath, cmd)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "explicit paths")
		_ = stderr
	}
}

func TestFmt_DefaultSelectionStagedOnly_SkipsPartiallyStaged(t *testing.T) {
	cfgPath, repoRoot := writeLinterFixture(t, []string{"fx"}, fmtFixture)
	gitCmd(t, repoRoot, "init", "-q", "-b", "main")
	write(t, repoRoot, ".gitignore", "pluginrepo/\n")
	for _, f := range []string{"staged.txt", "partial.txt", "untouched.txt"} {
		write(t, repoRoot, f, "orig\n")
	}
	gitCmd(t, repoRoot, "add", ".")
	gitCmd(t, repoRoot, "commit", "-q", "-m", "init")
	write(t, repoRoot, "staged.txt", "s\n")
	write(t, repoRoot, "partial.txt", "p1\n")
	gitCmd(t, repoRoot, "add", "staged.txt", "partial.txt")
	write(t, repoRoot, "partial.txt", "p2\n")
	cache := t.TempDir()

	_, stderr, err := run2(t, "--config", cfgPath, "--cache-dir", cache, "fmt")
	require.NoError(t, err, stderr)
	assert.Contains(t, stderr, "skipping partially staged file")
	read := func(f string) string { b, _ := os.ReadFile(filepath.Join(repoRoot, f)); return string(b) }
	assert.Equal(t, "formatted\n", read("staged.txt"))
	assert.Equal(t, "p2\n", read("partial.txt"))
	assert.Equal(t, "orig\n", read("untouched.txt"))

	_, stderr, err = run2(t, "--config", cfgPath, "--cache-dir", cache, "fmt", "--force")
	require.NoError(t, err, stderr)
	assert.Equal(t, "formatted\n", read("partial.txt"))
}
