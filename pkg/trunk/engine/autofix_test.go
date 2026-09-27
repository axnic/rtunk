package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/trunk/output"
)

func TestApplyInlineFixes_SingleFix(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	require.NoError(t, os.WriteFile(path, []byte("let x = 1"), 0o644))

	changed, err := ApplyInlineFixes([]output.Finding{
		{File: path, Fix: &output.InlineFix{Range: [2]int{0, 3}, Text: "const"}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{path}, changed)
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "const x = 1", string(got))
}

func TestApplyInlineFixes_NonOverlapping_BothApplied(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	require.NoError(t, os.WriteFile(path, []byte("aaaa bbbb"), 0o644))

	changed, err := ApplyInlineFixes([]output.Finding{
		{File: path, Fix: &output.InlineFix{Range: [2]int{0, 4}, Text: "AAAA"}},
		{File: path, Fix: &output.InlineFix{Range: [2]int{5, 9}, Text: "BBBB"}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{path}, changed)
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "AAAA BBBB", string(got))
}

func TestApplyInlineFixes_Overlapping_OnlyOneApplied(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	require.NoError(t, os.WriteFile(path, []byte("0123456789"), 0o644))

	changed, err := ApplyInlineFixes([]output.Finding{
		{File: path, Fix: &output.InlineFix{Range: [2]int{2, 8}, Text: "X"}}, // lower start, applied second
		{File: path, Fix: &output.InlineFix{Range: [2]int{5, 9}, Text: "Y"}}, // higher start -- applied first
	})
	require.NoError(t, err)
	assert.Equal(t, []string{path}, changed)
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "01234Y9", string(got), "only the [5,9) fix landed; the overlapping [2,8) one was skipped")
}

func TestApplyInlineFixes_StaleRange_Skipped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	require.NoError(t, os.WriteFile(path, []byte("short"), 0o644))

	changed, err := ApplyInlineFixes([]output.Finding{
		{File: path, Fix: &output.InlineFix{Range: [2]int{10, 20}, Text: "boom"}},
	})
	require.NoError(t, err)
	assert.Empty(t, changed)
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "short", string(got), "an out-of-bounds range must never be applied")
}

func TestApplyInlineFixes_NoFixes_NoOp(t *testing.T) {
	changed, err := ApplyInlineFixes([]output.Finding{{File: "/nonexistent"}})
	require.NoError(t, err)
	assert.Empty(t, changed)
}

// TestApplyInlineFixes_UTF16Range_NonASCIIPrefix: ESLint's own fix.range indexes the JS source
// string, which is UTF-16 (ECMA-262) -- "é" is one UTF-16 unit but two UTF-8 bytes, so a
// byte-offset read of the same range would misplace the fix by one byte for every such character
// preceding it.
func TestApplyInlineFixes_UTF16Range_NonASCIIPrefix(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.js")
	require.NoError(t, os.WriteFile(path, []byte("// é\nlet x = 1"), 0o644))

	changed, err := ApplyInlineFixes([]output.Finding{
		{File: path, Fix: &output.InlineFix{Range: [2]int{5, 8}, Text: "const"}}, // UTF-16 units 5-8 = "let"
	})
	require.NoError(t, err)
	assert.Equal(t, []string{path}, changed)
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "// é\nconst x = 1", string(got))
}

func TestApplyInlineFixes_MissingFile_SkippedNotAborted(t *testing.T) {
	dir := t.TempDir()
	present := filepath.Join(dir, "present.txt")
	require.NoError(t, os.WriteFile(present, []byte("aaaa"), 0o644))
	missing := filepath.Join(dir, "gone.txt")

	changed, err := ApplyInlineFixes([]output.Finding{
		{File: missing, Fix: &output.InlineFix{Range: [2]int{0, 1}, Text: "x"}},
		{File: present, Fix: &output.InlineFix{Range: [2]int{0, 4}, Text: "bbbb"}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{present}, changed, "a missing file must not abort fixes to other files")
}
