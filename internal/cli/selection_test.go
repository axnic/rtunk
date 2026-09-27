package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t", "-c", "commit.gpgsign=false"}, args...)...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
}

// newRepo returns a git repo with a.txt and b.txt committed on main.
func newRepo(t *testing.T) string {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "a.txt", "a")
	write(t, dir, "b.txt", "b")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func TestSelectFiles_NotInGit(t *testing.T) {
	_, err := selectFiles(t.TempDir(), "")
	require.ErrorIs(t, err, errOutsideGitNoPaths)
}

func TestSelectFiles_NoUpstream_DiffFromHEADAndUntracked(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "c.txt", "c")
	git(t, dir, "add", "c.txt")
	git(t, dir, "commit", "-qam", "add c")

	write(t, dir, "a.txt", "a2")
	git(t, dir, "add", "a.txt")                                // staged
	write(t, dir, "b.txt", "b2")                                // unstaged
	write(t, dir, "new.txt", "n")                               // untracked
	require.NoError(t, os.Remove(filepath.Join(dir, "c.txt"))) // deleted: not selected

	files, err := selectFiles(dir, "")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{
		filepath.Join(dir, "a.txt"), filepath.Join(dir, "b.txt"), filepath.Join(dir, "new.txt"),
	}, files)
}

func TestSelectFiles_Upstream_DiffAndUntracked(t *testing.T) {
	dir := newRepo(t)
	git(t, dir, "branch", "base")
	git(t, dir, "checkout", "-q", "-b", "feat")
	git(t, dir, "branch", "--set-upstream-to=base")
	write(t, dir, "a.txt", "a2")
	git(t, dir, "commit", "-qam", "change a")
	write(t, dir, "b.txt", "b2") // unstaged
	write(t, dir, "new.txt", "n")
	require.NoError(t, os.Remove(filepath.Join(dir, "a.txt"))) // deleted: not selected

	files, err := selectFiles(dir, "")
	require.NoError(t, err)
	assert.Equal(t, []string{filepath.Join(dir, "b.txt"), filepath.Join(dir, "new.txt")}, files)
}

func TestSelectFiles_From(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "a.txt", "a2")
	git(t, dir, "commit", "-qam", "change a")

	files, err := selectFiles(dir, "HEAD~1")
	require.NoError(t, err)
	assert.Equal(t, []string{filepath.Join(dir, "a.txt")}, files)

	_, err = selectFiles(dir, "nope")
	assert.Error(t, err)
}

func TestExpandPaths(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "new.txt", "n")
	write(t, dir, "ignored.log", "i")
	write(t, dir, ".gitignore", "*.log\n")
	require.NoError(t, os.Remove(filepath.Join(dir, "b.txt")))

	got, err := expandPaths(dir, []string{dir})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{
		filepath.Join(dir, ".gitignore"), filepath.Join(dir, "a.txt"), filepath.Join(dir, "new.txt"),
	}, got)

	_, err = expandPaths(dir, []string{filepath.Join(dir, "missing")})
	assert.Error(t, err)

	// outside git: passthrough
	plain := t.TempDir()
	got, err = expandPaths(plain, []string{plain})
	require.NoError(t, err)
	assert.Equal(t, []string{plain}, got)
}

func TestPartiallyStaged(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "a.txt", "a2")
	git(t, dir, "add", "a.txt")
	write(t, dir, "a.txt", "a3") // staged + unstaged
	write(t, dir, "b.txt", "b2")
	git(t, dir, "add", "b.txt") // fully staged

	got := partiallyStaged(dir)
	assert.Equal(t, map[string]bool{filepath.Join(dir, "a.txt"): true}, got)
}

const fmtFixture = `    - name: fx
      files: [ALL]
      commands:
        - name: format
          run: printf 'formatted\n' > ${target}
          output: rewrite
          success_codes: [0]
          in_place: true
          formatter: true
`

func TestCheckFmt_NoPathsOutsideGit_HardError(t *testing.T) {
	cfgPath, _ := writeLinterFixture(t, []string{"fx"}, fmtFixture)
	for _, cmd := range []string{"check", "fmt"} {
		_, stderr, err := run2(t, "--config", cfgPath, cmd)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "explicit paths")
		_ = stderr
	}
}

func TestFmt_DefaultSelectionStagedOnly_SkipsPartiallyStaged(t *testing.T) {
	cfgPath, repoRoot := writeLinterFixture(t, []string{"fx"}, fmtFixture)
	git(t, repoRoot, "init", "-q", "-b", "main")
	write(t, repoRoot, ".gitignore", "pluginrepo/\n")
	for _, f := range []string{"staged.txt", "partial.txt", "untouched.txt"} {
		write(t, repoRoot, f, "orig\n")
	}
	git(t, repoRoot, "add", ".")
	git(t, repoRoot, "commit", "-q", "-m", "init")
	write(t, repoRoot, "staged.txt", "s\n")
	write(t, repoRoot, "partial.txt", "p1\n")
	git(t, repoRoot, "add", "staged.txt", "partial.txt")
	write(t, repoRoot, "partial.txt", "p2\n")
	cache := t.TempDir()

	_, stderr, err := run2(t, "--config", cfgPath, "--cache-dir", cache, "fmt")
	require.NoError(t, err, stderr)
	assert.Contains(t, stderr, "skipping partially staged file")
	read := func(f string) string { b, _ := os.ReadFile(filepath.Join(repoRoot, f)); return string(b) }
	assert.Equal(t, "formatted\n", read("staged.txt"))
	assert.Equal(t, "p2\n", read("partial.txt"))
	assert.Equal(t, "orig\n", read("untouched.txt"))

	_, stderr, err = run2(t, "--config", cfgPath, "--cache-dir", cache, "fmt", "--force")
	require.NoError(t, err, stderr)
	assert.Equal(t, "formatted\n", read("partial.txt"))
}
