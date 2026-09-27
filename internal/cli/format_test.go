package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	git(t, repoRoot, "init", "-q", "-b", "main")
	git(t, repoRoot, "add", ".")
	git(t, repoRoot, "commit", "-q", "-m", "init") // clean tree, no upstream: no path means legitimately nothing to do
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
