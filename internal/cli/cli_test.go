package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// trunkYAML is the same fixture pkg/trunk/config's own tests resolve against: one local plugin
// source (testdata/pluginrepo) defining eslint/prettier/actionlint/shellcheck/node/commitlint,
// with lint [actionlint, prettier], runtimes [node], and actions [commitlint] enabled.
const trunkYAML = "../../pkg/trunk/config/testdata/trunk-with-plugins.yaml"

func run2(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	err = Run(args, &out, &errOut)
	return out.String(), errOut.String(), err
}

func TestConfigPrint(t *testing.T) {
	stdout, stderr, err := run2(t, "--config", trunkYAML, "config", "print")
	require.NoError(t, err, "stderr: %s", stderr)
	assert.Contains(t, stdout, "actionlint")
	assert.Contains(t, stdout, "node")
	assert.Contains(t, stdout, "commitlint")
	assert.NotContains(t, stdout, "eslint", "eslint is an unreferenced tool the default enabled+used print must drop")
}

// TestConfigPrint_All: --all switches to config.ResolveAll, surfacing eslint/shellcheck -- tools
// the fixture plugin repo defines but nothing in trunk-with-plugins.yaml enables or references
// (see TestConfigPrint above, and pkg/trunk/config's TestResolveAll_WithPluginRepo).
func TestConfigPrint_All(t *testing.T) {
	stdout, stderr, err := run2(t, "--config", trunkYAML, "config", "print", "--all")
	require.NoError(t, err, "stderr: %s", stderr)
	assert.Contains(t, stdout, "eslint")
	assert.Contains(t, stdout, "shellcheck")
}

func TestFindTrunkYAML(t *testing.T) {
	// EvalSymlinks: on macOS, t.TempDir() lives under /var, a symlink to /private/var, and
	// os.Getwd() (which findTrunkYAML calls) returns the resolved physical path -- normalize here
	// so the two sides of the comparison below agree.
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".trunk"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".trunk", "trunk.yaml"), []byte("version: \"0.1\"\n"), 0o644))

	sub := filepath.Join(root, "a", "b")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	cwd, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.Chdir(cwd)) })
	require.NoError(t, os.Chdir(sub))

	found, err := findTrunkYAML()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, ".trunk", "trunk.yaml"), found)
}

func TestFindTrunkYAML_BoundedByGitRoot(t *testing.T) {
	// trunk.yaml sits outside the git repo; the walk must stop at the repo root and not fall
	// through to it, or a subdirectory of some unrelated repo could pick up a stranger's config.
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".trunk"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".trunk", "trunk.yaml"), []byte("version: \"0.1\"\n"), 0o644))

	repo := filepath.Join(root, "repo")
	sub := filepath.Join(repo, "a", "b")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	require.NoError(t, exec.Command("git", "-C", repo, "init").Run())

	cwd, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.Chdir(cwd)) })
	require.NoError(t, os.Chdir(sub))

	_, err = findTrunkYAML()
	assert.Error(t, err)
}

func TestFindTrunkYAML_NotFound(t *testing.T) {
	cwd, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.Chdir(cwd)) })
	require.NoError(t, os.Chdir(t.TempDir()))

	_, err = findTrunkYAML()
	assert.Error(t, err)
}

func TestFindTrunkYAML_PrefersRtunkYAMLOverTrunkYAML(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".rtunk"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".trunk"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".rtunk", "rtunk.yaml"), []byte("version: \"0.1\"\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".trunk", "trunk.yaml"), []byte("version: \"0.1\"\n"), 0o644))

	cwd, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.Chdir(cwd)) })
	require.NoError(t, os.Chdir(root))

	found, err := findTrunkYAML()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, ".rtunk", "rtunk.yaml"), found)
}

func TestFindTrunkYAML_FindsRtunkYAMLAlone(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".rtunk"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".rtunk", "rtunk.yaml"), []byte("version: \"0.1\"\n"), 0o644))

	cwd, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.Chdir(cwd)) })
	require.NoError(t, os.Chdir(root))

	found, err := findTrunkYAML()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, ".rtunk", "rtunk.yaml"), found)
}

func TestFindTrunkYAML_FallsBackToTrunkYAMLWhenNoRtunkYAML(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".trunk"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".trunk", "trunk.yaml"), []byte("version: \"0.1\"\n"), 0o644))

	cwd, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.Chdir(cwd)) })
	require.NoError(t, os.Chdir(root))

	found, err := findTrunkYAML()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, ".trunk", "trunk.yaml"), found)
}

func TestUnknownCommand(t *testing.T) {
	_, _, err := run2(t, "bogus")
	assert.Error(t, err)
}

func TestVersionFlag_PrintsCliVersion(t *testing.T) {
	old := Version
	Version = "v9.9.9"
	t.Cleanup(func() { Version = old })

	stdout, _, err := run2(t, "--version")
	require.NoError(t, err)
	assert.Contains(t, stdout, "v9.9.9")
}
