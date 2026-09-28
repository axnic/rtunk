package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/trunk/config"
)

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
		git(t, repoRoot, "init", "-q", "-b", "main")
		require.NoError(t, os.WriteFile(filepath.Join(repoRoot, ".gitignore"), []byte("pluginrepo/\n"), 0o644))
		git(t, repoRoot, "add", ".")
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
