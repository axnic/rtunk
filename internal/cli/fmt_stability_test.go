package cli

import (
	"os"
	"path/filepath"
	"strings"
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
	assert.Equal(t, "rtunk: fmt --check found 1 file(s) needing reformatting", err.Error())

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
// residual diff, but round 2's dry-run check finds nothing left -- stable after 2 rounds, success,
// no conflict error. fmtA appends AAA only if missing; fmtB truncates the whole file to just BBB
// only if BBB is missing (destroying whatever fmtA already wrote). Under -j 1 (queue order
// fmtA-then-fmtB, held constant across every one of the 4 passes this loop makes), round 1 ends
// "BBB\n" (fmtA's own AAA gets destroyed by fmtB running second); the dry-run check then finds
// fmtA would append AAA again (since "BBB\n" has no AAA line) -- a residual diff, forcing round 2.
// Round 2 (same order) ends "BBB\nAAA\n": fmtA appends AAA ("BBB\n" has no AAA), fmtB finds BBB
// already present and does nothing. The second dry-run check finds both lines already present --
// stable. -j 1 is load-bearing here, not just cosmetic: without it, whichever linter's job a
// worker picks up first can make this fixture stabilize after round 1 instead of round 2 (verified
// by hand-tracing the reverse fmtB-then-fmtA order, which reaches the same final "BBB\nAAA\n"
// content but via only 1 real round) -- so -j 1 pins WHICH round this test actually exercises, not
// merely whether it's flaky. The stderr assertion below counts fmtA's own "done" lines to prove
// this empirically (2 real rounds), rather than trusting the hand-trace alone.
func TestFmtCmd_StableAfterSecondRound(t *testing.T) {
	cfgPath, repoRoot := writeLinterFixture(t, []string{"fmtA", "fmtB"}, `    - name: fmtA
      description: Appends AAA only if the file doesn't already contain it
      files: [ALL]
      commands:
        - name: format
          run: grep -qxF AAA ${target} || printf 'AAA\n' >> ${target}
          output: rewrite
          success_codes: [0]
          in_place: true
          formatter: true
    - name: fmtB
      description: Truncates the file to just BBB only if BBB isn't already present
      files: [ALL]
      commands:
        - name: format
          run: grep -qxF BBB ${target} || printf 'BBB\n' > ${target}
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
	assert.Equal(t, "work/shared.txt\n\n1 file(s) reformatted\n", stdout)
	assert.Equal(t, 2, strings.Count(stderr, "done fmtA: 1 file(s) changed"), "must take exactly 2 real rounds to stabilize; stderr: %s", stderr)

	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "BBB\nAAA\n", string(data))
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
	// -j 1 kept for consistency with the other multi-linter tests in this file, though this
	// fixture's outcome (never converges) doesn't actually depend on ordering: both linters
	// unconditionally overwrite regardless of which runs first.
	stdout, stderr, err := run2(t, "--config", cfgPath, "--cache-dir", cacheDir, "fmt", "-j", "1", filepath.Join(repoRoot, "work"))
	require.Error(t, err, "stderr: %s", stderr)
	assert.Equal(t, "work/oscillating.txt\n\n1 file(s) reformatted\n", stdout)
	assert.Equal(t, "fmt did not converge after 2 attempts. Still unstable:\n  work/oscillating.txt (conflicting: fmtA, fmtB)", err.Error())
}

// TestFmtCmd_ReportsSkippedLinterEvenWhenNothingChanged proves the skipped-linters report
// survives runStableFormat's "nothing written, nothing to verify" early return -- collectChangedByLinter
// must track Skipped events, not just Done ones, or this diagnostic silently vanishes in the most
// common real case (an already-formatted tree with one unsupported/disabled formatter present).
func TestFmtCmd_ReportsSkippedLinterEvenWhenNothingChanged(t *testing.T) {
	cfgPath, repoRoot := writeLinterFixture(t, []string{"badfmt"}, `    - name: badfmt
      description: A formatter with no in_place effect -- always Skipped
      files: [ALL]
      commands:
        - name: format
          run: echo unused
          output: rewrite
          formatter: true
`)
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "work"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "work", "a.txt"), []byte("x\n"), 0o644))

	cacheDir := t.TempDir()
	stdout, stderr, err := run2(t, "--config", cfgPath, "--cache-dir", cacheDir, "fmt", filepath.Join(repoRoot, "work"))
	require.NoError(t, err, "stderr: %s", stderr)
	assert.Contains(t, stdout, "linter(s) skipped: badfmt")
}

// TestFmtCmd_UnstableSingleSuspectWording proves the "only one linter's own real rounds ever
// reported this file changed" case gets an honest message, not a bare "(conflicting: )" or a
// misleading single-name "conflicting" label -- reachable via the known copy_targets sandbox
// limitation (Task 1): fmtMarker writes GOOD when it sees allow.marker (present in its real cwd,
// repoRoot, since RunFrom defaults to repoRoot) and BAD when it doesn't (as in the dry-run
// sandbox, which -- per copy_targets -- stages only the batch's own target files, never
// allow.marker). Round 1 (real): marker present, writes GOOD (a genuine change from "orig\n" --
// recorded, fmtMarker becomes the round-1 suspect). Round 1's dry-run check: sandboxed copy of the
// now-GOOD file, marker absent, writes BAD -- a residual diff, forcing round 2. Round 2 (real):
// marker present again, writes GOOD again -- but the file is already GOOD, so this is a no-op
// (nothing recorded in round 2's map). Round 2's dry-run check: same as round 1's, marker still
// absent, writes BAD again -- still unstable. unstableError's suspects come only from the two REAL
// rounds' own maps, so this file has exactly one (fmtMarker, from round 1 alone) even though the
// disagreement is entirely the dry-run sandbox's blind spot, not a second conflicting linter.
func TestFmtCmd_UnstableSingleSuspectWording(t *testing.T) {
	cfgPath, repoRoot := writeLinterFixture(t, []string{"fmtMarker"}, `    - name: fmtMarker
      description: Writes GOOD when a real ancestor marker is visible, BAD when it's not
      files: [ALL]
      commands:
        - name: format
          run: test -f allow.marker && printf 'GOOD\n' > ${target} || printf 'BAD\n' > ${target}
          output: rewrite
          success_codes: [0]
          in_place: true
          formatter: true
`)
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "allow.marker"), []byte(""), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "work"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "work", "a.txt"), []byte("orig\n"), 0o644))

	cacheDir := t.TempDir()
	_, stderr, err := run2(t, "--config", cfgPath, "--cache-dir", cacheDir, "fmt", filepath.Join(repoRoot, "work"))
	require.Error(t, err, "stderr: %s", stderr)
	assert.Equal(t, "fmt did not converge after 2 attempts. Still unstable:\n  work/a.txt (only fmtMarker reported changing this file -- likely a dry-run/real mismatch, e.g. it reads config the dry-run sandbox couldn't see)", err.Error())
}
