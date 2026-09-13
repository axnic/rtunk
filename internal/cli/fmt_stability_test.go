package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFmtCmd_Check_NeverWritesAndReportsWouldChange proves `rtunk fmt --check` never writes to
// disk while still correctly reporting a file that would be reformatted, and exits non-zero.
func TestFmtCmd_Check_NeverWritesAndReportsWouldChange(t *testing.T) {
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
	target := filepath.Join(repoRoot, "work", "messy.txt")
	require.NoError(t, os.WriteFile(target, []byte("messy\n"), 0o644))

	cacheDir := t.TempDir()
	stdout, stderr, err := run2(t, "--config", cfgPath, "--cache-dir", cacheDir, "fmt", "--check", filepath.Join(repoRoot, "work"))
	require.Error(t, err, "stderr: %s", stderr)

	want := "work/messy.txt\n\n1 file(s) would be reformatted\n"
	assert.Equal(t, want, stdout)

	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "messy\n", string(data), "--check must never write to the real file")
}

// TestFmtCmd_StableAfterOneRealRound proves plain `rtunk fmt` (no --check) reports success with
// no dry-run-check overhead visible in the outcome when a single idempotent formatter converges
// immediately -- the ordinary case, unaffected by the new stability machinery.
func TestFmtCmd_StableAfterOneRealRound(t *testing.T) {
	cfgPath, repoRoot := writeLinterFixture(t, []string{"fakefmt"}, `    - name: fakefmt
      description: A fake idempotent in-place formatter
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
	target := filepath.Join(repoRoot, "work", "messy.txt")
	require.NoError(t, os.WriteFile(target, []byte("messy\n"), 0o644))

	cacheDir := t.TempDir()
	stdout, stderr, err := run2(t, "--config", cfgPath, "--cache-dir", cacheDir, "fmt", filepath.Join(repoRoot, "work"))
	require.NoError(t, err, "stderr: %s", stderr)

	want := "work/messy.txt\n\n1 file(s) reformatted\n"
	assert.Equal(t, want, stdout)

	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "formatted\n", string(data))
}

// TestFmtCmd_StableAfterSecondRound covers the middle case: round 1's dry-run check finds a
// residual diff (a second formatter's own first invocation still had something to do), but round
// 2's dry-run check finds nothing left -- stable after 2 rounds, success, no conflict error. Two
// formatters both write DIFFERENT content on their first pass (fmtA then fmtB, each genuinely
// changing the file once), but fmtB's own SECOND invocation (round 2) is a no-op on top of what it
// already wrote -- so after round 2, nothing would change anymore.
func TestFmtCmd_StableAfterSecondRound(t *testing.T) {
	cfgPath, repoRoot := writeLinterFixture(t, []string{"fmtA", "fmtB"}, `    - name: fmtA
      description: Writes AAA only if the file doesn't already start with AAA
      files: [ALL]
      commands:
        - name: format
          run: grep -qxF AAA ${target} || printf 'AAA\n' > ${target}
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
	target := filepath.Join(repoRoot, "work", "shared.txt")
	require.NoError(t, os.WriteFile(target, []byte("original\n"), 0o644))

	cacheDir := t.TempDir()
	// -j 1 forces deterministic linter ordering (fmtA before fmtB, matching buildJobs' own
	// name-sorted queue order under a single worker) -- with the default concurrency (NumCPU), two
	// different linters' InPlace jobs can be picked up by workers in either order (they're still
	// serialized against each other by the shared inPlaceMu, but WHICH one goes first is not
	// guaranteed), which would make this fixture's outcome non-deterministic.
	stdout, stderr, err := run2(t, "--config", cfgPath, "--cache-dir", cacheDir, "fmt", "-j", "1", filepath.Join(repoRoot, "work"))
	require.NoError(t, err, "stderr: %s", stderr)
	assert.Contains(t, stdout, "work/shared.txt")
	assert.Contains(t, stdout, "1 file(s) reformatted")

	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "AAA\nBBB\n", string(data))
}

// TestFmtCmd_UnstableReportsConflictingLinters is the core conflict-detection test: two formatters
// that keep rewriting the same file to different content forever, never converging. Must report
// the exact "did not converge" error naming both linters as suspects, and the command must exit
// non-zero.
func TestFmtCmd_UnstableReportsConflictingLinters(t *testing.T) {
	cfgPath, repoRoot := writeLinterFixture(t, []string{"fmtA", "fmtB"}, `    - name: fmtA
      description: Always rewrites to AAA, undoing fmtB's own change
      files: [ALL]
      commands:
        - name: format
          run: printf 'AAA\n' > ${target}
          output: rewrite
          success_codes: [0]
          in_place: true
          formatter: true
    - name: fmtB
      description: Always rewrites to BBB, undoing fmtA's own change
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
	target := filepath.Join(repoRoot, "work", "oscillating.txt")
	require.NoError(t, os.WriteFile(target, []byte("original\n"), 0o644))

	cacheDir := t.TempDir()
	// -j 1 for the same determinism reason as TestFmtCmd_StableAfterSecondRound -- both linters
	// unconditionally overwrite here, so the outcome (never converges) doesn't actually depend on
	// which runs first, but forcing single-worker scheduling keeps this test's timing fully
	// reproducible rather than relying on that being true.
	stdout, stderr, err := run2(t, "--config", cfgPath, "--cache-dir", cacheDir, "fmt", "-j", "1", filepath.Join(repoRoot, "work"))
	require.Error(t, err)
	assert.Contains(t, stdout, "work/oscillating.txt")

	assert.Contains(t, err.Error(), "fmt did not converge after 2 attempts")
	assert.Contains(t, err.Error(), "work/oscillating.txt (conflicting: fmtA, fmtB)")
	_ = stderr
}
