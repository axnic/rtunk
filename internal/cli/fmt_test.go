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
// both realistic InPlace formatter candidates over the same .md files. Two formatters writing the
// SAME content to a shared file would only produce one ChangedFiles entry naturally (the second
// rewrite is a no-op by the hashing definition) -- writing DIFFERENT content is what makes both
// linters genuinely report the file changed, so dedup has a real duplicate to collapse.
func TestFmtCmd_DedupesFilesChangedByMultipleLinters(t *testing.T) {
	cfgPath, repoRoot := writeLinterFixture(t, []string{"fmtA", "fmtB"}, `    - name: fmtA
      description: A fake formatter writing content A
      files: [ALL]
      commands:
        - name: format
          run: printf 'AAA\n' > ${target}
          output: rewrite
          success_codes: [0]
          in_place: true
          formatter: true
    - name: fmtB
      description: A fake formatter writing content B
      files: [ALL]
      commands:
        - name: format
          run: printf 'BBB\n' > ${target}
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
