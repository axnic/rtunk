package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func run(t *testing.T, dir string, args ...string) {
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
	run(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "a.txt", "a")
	write(t, dir, "b.txt", "b")
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func TestIsRepo(t *testing.T) {
	assert.True(t, IsRepo(newRepo(t)))
	assert.False(t, IsRepo(t.TempDir()))
}

func TestRepoRoot(t *testing.T) {
	dir := newRepo(t)
	require.NoError(t, os.Mkdir(filepath.Join(dir, "sub"), 0o755))

	root, err := RepoRoot(filepath.Join(dir, "sub"))
	require.NoError(t, err)
	// macOS temp dirs resolve through a symlink (/tmp -> /private/tmp); compare real paths.
	wantReal, _ := filepath.EvalSymlinks(dir)
	gotReal, _ := filepath.EvalSymlinks(root)
	assert.Equal(t, wantReal, gotReal)

	_, err = RepoRoot(t.TempDir())
	assert.Error(t, err)
}

// TestRepoRoot_OutsideGitRepo_SurfacesGitError proves RepoRoot surfaces git's own real stderr
// message (e.g. "fatal: not a git repository...") instead of a generic "exit status 128", and
// doesn't prefix its own "rtunk:" on top of the one cmd/rtunk/main.go already adds.
func TestRepoRoot_OutsideGitRepo_SurfacesGitError(t *testing.T) {
	_, err := RepoRoot(t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a git repository")
	assert.NotContains(t, err.Error(), "rtunk: rtunk:")
}

func TestFiles_RespectsGitignore(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "new.txt", "n")
	write(t, dir, "ignored.log", "i")
	write(t, dir, ".gitignore", "*.log\n")

	got, err := Files(dir)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{
		filepath.Join(dir, ".gitignore"), filepath.Join(dir, "a.txt"), filepath.Join(dir, "b.txt"), filepath.Join(dir, "new.txt"),
	}, got)
}

func TestChangedFiles_NoUpstream_DiffFromHEADAndUntracked(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "a.txt", "a2")
	write(t, dir, "new.txt", "n")

	changed, untracked, err := ChangedFiles(dir, "")
	require.NoError(t, err)
	assert.Equal(t, []string{filepath.Join(dir, "a.txt")}, changed)
	assert.Equal(t, []string{filepath.Join(dir, "new.txt")}, untracked)
}

func TestChangedFiles_NoCommitsYet(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "a.txt", "a")

	changed, untracked, err := ChangedFiles(dir, "")
	require.NoError(t, err)
	assert.Empty(t, changed)
	assert.Equal(t, []string{filepath.Join(dir, "a.txt")}, untracked)
}

func TestChangedFiles_From(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "a.txt", "a2")
	run(t, dir, "commit", "-qam", "change a")

	changed, _, err := ChangedFiles(dir, "HEAD~1")
	require.NoError(t, err)
	assert.Equal(t, []string{filepath.Join(dir, "a.txt")}, changed)

	_, _, err = ChangedFiles(dir, "nope")
	assert.Error(t, err)
}

func TestDiffNames(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "a.txt", "a2")
	run(t, dir, "add", "a.txt")
	write(t, dir, "b.txt", "b2")

	staged, err := DiffNames(dir, true)
	require.NoError(t, err)
	assert.Equal(t, []string{filepath.Join(dir, "a.txt")}, staged)

	unstaged, err := DiffNames(dir, false)
	require.NoError(t, err)
	assert.Equal(t, []string{filepath.Join(dir, "b.txt")}, unstaged)
}
