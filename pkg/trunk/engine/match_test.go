package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/trunk/config"
)

// runGit runs a git subcommand in dir, failing the test on error -- used to build a real git
// repository fixture so filterGitignored's tests exercise the real `git check-ignore` binary
// rather than a hand-rolled stand-in for git's own ignore semantics.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func TestFiles_Extension(t *testing.T) {
	dir := t.TempDir()
	goFile := filepath.Join(dir, "main.go")
	mustWrite(t, goFile, "package main\n")
	mustWrite(t, filepath.Join(dir, "notes.txt"), "hello\n")

	cfg := config.Config{Lint: config.LintConfig{Files: map[string]config.FileType{
		"go": {Name: "go", Extensions: []string{"go"}},
	}}}
	got, err := Files(cfg, config.Linter{Files: []string{"go"}}, dir, []string{dir})
	require.NoError(t, err)
	require.Equal(t, []string{goFile}, got)
}

func TestFiles_Filename(t *testing.T) {
	dir := t.TempDir()
	dockerfile := filepath.Join(dir, "Dockerfile")
	mustWrite(t, dockerfile, "FROM scratch\n")
	mustWrite(t, filepath.Join(dir, "Dockerfile.notes"), "n/a\n")

	cfg := config.Config{Lint: config.LintConfig{Files: map[string]config.FileType{
		"docker": {Name: "docker", Filenames: []string{"Dockerfile"}},
	}}}
	got, err := Files(cfg, config.Linter{Files: []string{"docker"}}, dir, []string{dir})
	require.NoError(t, err)
	require.Equal(t, []string{dockerfile}, got)
}

func TestFiles_Regex(t *testing.T) {
	dir := t.TempDir()
	match := filepath.Join(dir, "config.prod.yaml")
	mustWrite(t, match, "a: 1\n")
	mustWrite(t, filepath.Join(dir, "config.yaml"), "a: 1\n")

	cfg := config.Config{Lint: config.LintConfig{Files: map[string]config.FileType{
		"prod-config": {Name: "prod-config", Regexes: []string{`config\.prod\.yaml$`}},
	}}}
	got, err := Files(cfg, config.Linter{Files: []string{"prod-config"}}, dir, []string{dir})
	require.NoError(t, err)
	require.Equal(t, []string{match}, got)
}

func TestFiles_ShebangOnlyForExtensionlessFiles(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "run")
	mustWrite(t, script, "#!/usr/bin/env bash\necho hi\n")
	mustWrite(t, filepath.Join(dir, "run.txt"), "#!/usr/bin/env bash\necho hi\n")

	cfg := config.Config{Lint: config.LintConfig{Files: map[string]config.FileType{
		"shell": {Name: "shell", Shebangs: []string{"bash", "sh"}},
	}}}
	got, err := Files(cfg, config.Linter{Files: []string{"shell"}}, dir, []string{dir})
	require.NoError(t, err)
	require.Equal(t, []string{script}, got)
}

func TestFiles_Inherit(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "template.yaml")
	mustWrite(t, f, "AWSTemplateFormatVersion: '2010-09-09'\n")

	cfg := config.Config{Lint: config.LintConfig{Files: map[string]config.FileType{
		"yaml":           {Name: "yaml", Extensions: []string{"yaml", "yml"}},
		"cloudformation": {Name: "cloudformation", Inherit: []string{"yaml"}},
	}}}
	got, err := Files(cfg, config.Linter{Files: []string{"cloudformation"}}, dir, []string{dir})
	require.NoError(t, err)
	require.Equal(t, []string{f}, got)
}

func TestFiles_RequiredYAMLKeysNarrows(t *testing.T) {
	dir := t.TempDir()
	cfnFile := filepath.Join(dir, "stack.yaml")
	mustWrite(t, cfnFile, "AWSTemplateFormatVersion: '2010-09-09'\nResources: {}\n")
	mustWrite(t, filepath.Join(dir, "plain.yaml"), "a: 1\n")

	cfg := config.Config{Lint: config.LintConfig{Files: map[string]config.FileType{
		"cloudformation": {
			Name:             "cloudformation",
			Extensions:       []string{"yaml", "yml"},
			RequiredYAMLKeys: []string{"AWSTemplateFormatVersion"},
		},
	}}}
	got, err := Files(cfg, config.Linter{Files: []string{"cloudformation"}}, dir, []string{dir})
	require.NoError(t, err)
	require.Equal(t, []string{cfnFile}, got, "plain.yaml lacks the required key and must not match")
}

func TestFiles_InheritCycleDoesNotHang(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "main.go"), "package main\n")

	cfg := config.Config{Lint: config.LintConfig{Files: map[string]config.FileType{
		"a": {Name: "a", Inherit: []string{"b"}},
		"b": {Name: "b", Inherit: []string{"a"}},
	}}}
	got, err := Files(cfg, config.Linter{Files: []string{"a"}}, dir, []string{dir})
	require.NoError(t, err)
	require.Empty(t, got, "a's own Inherit cycle has no structural criteria of its own to match")
}

func TestFiles_All(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "anything.xyz")
	mustWrite(t, f, "content\n")

	got, err := Files(config.Config{}, config.Linter{Files: []string{"ALL"}}, dir, []string{dir})
	require.NoError(t, err)
	require.Equal(t, []string{f}, got)
}

func TestFiles_SkipsDotGit(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o755))
	mustWrite(t, filepath.Join(dir, ".git", "config.go"), "not real source\n")
	real := filepath.Join(dir, "main.go")
	mustWrite(t, real, "package main\n")

	cfg := config.Config{Lint: config.LintConfig{Files: map[string]config.FileType{
		"go": {Name: "go", Extensions: []string{"go"}},
	}}}
	got, err := Files(cfg, config.Linter{Files: []string{"go"}}, dir, []string{dir})
	require.NoError(t, err)
	require.Equal(t, []string{real}, got)
}

// TestFiles_RespectsGitignore covers files ignored by a real .gitignore: git-ignored files must
// never reach a linter, matching trunk's own behavior of only checking tracked-or-trackable
// source. "generated.go" (gitignored) must not appear alongside "main.go" (not ignored).
func TestFiles_RespectsGitignore(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")

	mustWrite(t, filepath.Join(dir, ".gitignore"), "generated.go\n")
	real := filepath.Join(dir, "main.go")
	mustWrite(t, real, "package main\n")
	mustWrite(t, filepath.Join(dir, "generated.go"), "package main\n")

	cfg := config.Config{Lint: config.LintConfig{Files: map[string]config.FileType{
		"go": {Name: "go", Extensions: []string{"go"}},
	}}}
	got, err := Files(cfg, config.Linter{Files: []string{"go"}}, dir, []string{dir})
	require.NoError(t, err)
	require.Equal(t, []string{real}, got, "generated.go is gitignored and must be excluded")
}

// TestFiles_GitignoreNoOpOutsideGitRepo covers a directory with no .git anywhere above it (e.g.
// checking a plain, non-version-controlled tree): `git check-ignore` errors "not a git
// repository", and Files must still return every structurally matched file rather than treating
// that error as "everything is ignored" or failing the whole call.
func TestFiles_GitignoreNoOpOutsideGitRepo(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "main.go")
	mustWrite(t, real, "package main\n")

	cfg := config.Config{Lint: config.LintConfig{Files: map[string]config.FileType{
		"go": {Name: "go", Extensions: []string{"go"}},
	}}}
	got, err := Files(cfg, config.Linter{Files: []string{"go"}}, dir, []string{dir})
	require.NoError(t, err)
	require.Equal(t, []string{real}, got)
}

// TestFiles_RejectsPathOutsideRepoRoot covers the path-traversal boundary check: a path in paths
// that resolves outside repoRoot must be rejected outright, not silently matched and only failing
// later, deep inside sandbox staging.
func TestFiles_RejectsPathOutsideRepoRoot(t *testing.T) {
	repoRoot := t.TempDir()
	outside := t.TempDir() // a sibling temp dir, guaranteed not under repoRoot
	mustWrite(t, filepath.Join(outside, "file.go"), "package main\n")

	cfg := config.Config{Lint: config.LintConfig{Files: map[string]config.FileType{
		"go": {Name: "go", Extensions: []string{"go"}},
	}}}
	_, err := Files(cfg, config.Linter{Files: []string{"go"}}, repoRoot, []string{outside})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "outside repository root")
}
