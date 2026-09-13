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
