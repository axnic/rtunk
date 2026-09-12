package check

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/trunk/config"
)

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
	got, err := Files(cfg, config.Linter{Files: []string{"go"}}, []string{dir})
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
	got, err := Files(cfg, config.Linter{Files: []string{"docker"}}, []string{dir})
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
	got, err := Files(cfg, config.Linter{Files: []string{"prod-config"}}, []string{dir})
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
	got, err := Files(cfg, config.Linter{Files: []string{"shell"}}, []string{dir})
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
	got, err := Files(cfg, config.Linter{Files: []string{"cloudformation"}}, []string{dir})
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
	got, err := Files(cfg, config.Linter{Files: []string{"cloudformation"}}, []string{dir})
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
	got, err := Files(cfg, config.Linter{Files: []string{"a"}}, []string{dir})
	require.NoError(t, err)
	require.Empty(t, got, "a's own Inherit cycle has no structural criteria of its own to match")
}

func TestFiles_All(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "anything.xyz")
	mustWrite(t, f, "content\n")

	got, err := Files(config.Config{}, config.Linter{Files: []string{"ALL"}}, []string{dir})
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
	got, err := Files(cfg, config.Linter{Files: []string{"go"}}, []string{dir})
	require.NoError(t, err)
	require.Equal(t, []string{real}, got)
}
