package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFmtCmd_ReportsOnlyChangedFiles drives `rtunk fmt` end to end against a real fake formatter
// command (a plain shell one-liner, no compiled binary needed): one file already holds the
// formatter's target content (untouched), the other doesn't (genuinely rewritten) -- the report
// must name only the second.
func TestFmtCmd_ReportsOnlyChangedFiles(t *testing.T) {
	cfgPath, repoRoot := writeLinterFixture(t, []string{"fakefmt"}, `    - name: fakefmt
      description: A fake in-place formatter
      files: [ALL]
      commands:
        - name: format
          run: printf 'formatted\n' > ${target}
          output: rewrite
          success_codes: [0]
          in_place: true
          formatter: true
`)
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "work"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "work", "already.txt"), []byte("formatted\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "work", "messy.txt"), []byte("messy\n"), 0o644))

	cacheDir := t.TempDir()
	stdout, stderr, err := run2(t, "--config", cfgPath, "--cache-dir", cacheDir, "fmt", filepath.Join(repoRoot, "work"))
	require.NoError(t, err, "stderr: %s", stderr)

	want := "work/messy.txt\n\n1 file(s) reformatted\n"
	assert.Equal(t, want, stdout)
}

// TestFmtCmd_DedupesFilesChangedByMultipleLinters proves the same file reported changed by two
// DIFFERENT linters is listed once, not twice, in the final report and its count -- real and
// reachable: this repo's own actual .trunk/trunk.yaml enables both prettier and markdownlint,
// both realistic InPlace formatter candidates over the same .md files. Both formatters here
// unconditionally overwriting the file (as this fixture did before runStableFormat existed) would
// now be a genuine, permanent conflict -- indistinguishable from
// TestFmtCmd_UnstableReportsConflictingLinters' own fixture -- so each instead appends its own
// marker only once (idempotent, and commutative with the other regardless of run order), giving
// runStableFormat's own dry-run check nothing left to do after round 1 while still making both
// linters genuinely report the file changed on their first pass, so dedup has a real duplicate to
// collapse.
func TestFmtCmd_DedupesFilesChangedByMultipleLinters(t *testing.T) {
	cfgPath, repoRoot := writeLinterFixture(t, []string{"fmtA", "fmtB"}, `    - name: fmtA
      description: Appends AAA only if the file doesn't already contain it
      files: [ALL]
      commands:
        - name: format
          run: grep -qF AAA ${target} || printf 'AAA\n' >> ${target}
          output: rewrite
          success_codes: [0]
          in_place: true
          formatter: true
    - name: fmtB
      description: Appends BBB only if the file doesn't already contain it
      files: [ALL]
      commands:
        - name: format
          run: grep -qF BBB ${target} || printf 'BBB\n' >> ${target}
          output: rewrite
          success_codes: [0]
          in_place: true
          formatter: true
`)
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "work"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "work", "shared.txt"), []byte("original\n"), 0o644))

	cacheDir := t.TempDir()
	stdout, stderr, err := run2(t, "--config", cfgPath, "--cache-dir", cacheDir, "fmt", filepath.Join(repoRoot, "work"))
	require.NoError(t, err, "stderr: %s", stderr)

	want := "work/shared.txt\n\n1 file(s) reformatted\n"
	assert.Equal(t, want, stdout, "the same file changed by two different linters must be listed once, not twice")
}

func TestFmtCmd_NoFixAlias_MatchesCheckFlag(t *testing.T) {
	cfgPath, _ := writeLinterFixture(t, nil, "")
	longOut, longStderr, longErr := run2(t, "--config", cfgPath, "fmt", "--check", filepath.Dir(filepath.Dir(cfgPath)))
	aliasOut, aliasStderr, aliasErr := run2(t, "--config", cfgPath, "fmt", "--no-fix", filepath.Dir(filepath.Dir(cfgPath)))
	shortOut, shortStderr, shortErr := run2(t, "--config", cfgPath, "fmt", "-n", filepath.Dir(filepath.Dir(cfgPath)))
	assert.Equal(t, longErr, aliasErr)
	assert.Equal(t, longOut, aliasOut)
	assert.Equal(t, longStderr, aliasStderr)
	assert.Equal(t, longErr, shortErr)
	assert.Equal(t, longOut, shortOut)
	assert.Equal(t, longStderr, shortStderr)
}

// TestFmtCmd_PrintFailures_Accepted: fmt already always prints Failed events unconditionally --
// this is a documented no-op.
func TestFmtCmd_PrintFailures_Accepted(t *testing.T) {
	cfgPath, _ := writeLinterFixture(t, nil, "")
	_, _, err := run2(t, "--config", cfgPath, "fmt", "--print-failures", filepath.Dir(filepath.Dir(cfgPath)))
	require.NoError(t, err)
}

func TestFmtCmd_Filter_OnlyRunsAllowedFormatter(t *testing.T) {
	cfgPath, _ := writeLinterFixture(t, []string{"keep-me", "drop-me"}, `    - name: keep-me
      description: Should run
      files: [ALL]
      commands:
        - name: fmt
          run: echo unused
          output: rewrite
          formatter: true
          in_place: true
    - name: drop-me
      description: Should not run
      files: [ALL]
      commands:
        - name: fmt
          run: echo unused
          output: rewrite
          formatter: true
          in_place: true
`)
	_, stderr, _ := run2(t, "--config", cfgPath, "fmt", "--filter", "keep-me", filepath.Dir(filepath.Dir(cfgPath)))
	assert.Contains(t, stderr, "keep-me")
	assert.NotContains(t, stderr, "drop-me")
}

func TestFmtCmd_Exclude_SkipsExcludedFormatter(t *testing.T) {
	cfgPath, _ := writeLinterFixture(t, []string{"keep-me", "drop-me"}, `    - name: keep-me
      description: Should run
      files: [ALL]
      commands:
        - name: fmt
          run: echo unused
          output: rewrite
          formatter: true
          in_place: true
    - name: drop-me
      description: Should not run
      files: [ALL]
      commands:
        - name: fmt
          run: echo unused
          output: rewrite
          formatter: true
          in_place: true
`)
	_, stderr, _ := run2(t, "--config", cfgPath, "fmt", "--exclude", "drop-me", filepath.Dir(filepath.Dir(cfgPath)))
	assert.Contains(t, stderr, "keep-me")
	assert.NotContains(t, stderr, "drop-me")
}
