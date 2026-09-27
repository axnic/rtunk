package download_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/trunk/config"
	"github.com/xunleii/rtunk/pkg/trunk/download"
)

func tarGzBytes(t *testing.T, topDir, name, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: topDir + "/" + name, Mode: 0o755, Size: int64(len(content))}))
	_, err := tw.Write([]byte(content))
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

func TestDownload_Runtime(t *testing.T) {
	archive := tarGzBytes(t, "tool-1.0.0", "shellcheck", "#!/bin/sh\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer srv.Close()

	cfg := config.Config{
		Downloads: map[string]config.Download{
			"shellcheck": {Downloads: []config.DownloadEntry{{
				OS:  config.OSSpec{"linux": "linux", "macos": "macos", "windows": "windows"},
				CPU: config.OSSpec{"x86_64": "x86_64", "arm_64": "arm_64"},
				URL: srv.URL + "/shellcheck.tar.gz", StripComponents: 1,
			}}},
		},
		Tools: map[string]config.Tool{},
		Runtimes: config.CategoryConfig[config.Runtime]{
			Definitions: map[string]config.Runtime{
				"shellcheck": {Type: "shellcheck", Download: "shellcheck", KnownGoodVersion: "1.0.0", Shims: []string{"shellcheck"}},
			},
		},
	}

	cacheDir := t.TempDir()
	events, err := download.Download(cfg, cacheDir, "/repo", download.Ref{Category: "runtimes", ID: "shellcheck"})
	require.NoError(t, err)

	var phases []download.Phase
	for ev := range events {
		require.NoError(t, ev.Err, "event: %+v", ev)
		phases = append(phases, ev.Phase)
	}
	assert.Contains(t, phases, download.Started)
	assert.Contains(t, phases, download.Done)

	root, err := download.Root(cacheDir)
	require.NoError(t, err)
	shimPath := download.ShimPath(root, "runtimes", "shellcheck", "1.0.0", "shellcheck")
	assert.FileExists(t, shimPath)
}

func TestDownload_RemovesBlobAfterInstall(t *testing.T) {
	archive := tarGzBytes(t, "tool-1.0.0", "shellcheck", "#!/bin/sh\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer srv.Close()

	cfg := config.Config{
		Downloads: map[string]config.Download{
			"shellcheck": {Downloads: []config.DownloadEntry{{
				OS:  config.OSSpec{"linux": "linux", "macos": "macos", "windows": "windows"},
				CPU: config.OSSpec{"x86_64": "x86_64", "arm_64": "arm_64"},
				URL: srv.URL + "/shellcheck.tar.gz", StripComponents: 1,
			}}},
		},
		Tools: map[string]config.Tool{},
		Runtimes: config.CategoryConfig[config.Runtime]{
			Definitions: map[string]config.Runtime{
				"shellcheck": {Type: "shellcheck", Download: "shellcheck", KnownGoodVersion: "0.9.0", Shims: []string{"shellcheck"}},
			},
		},
	}

	cacheDir := t.TempDir()
	events, err := download.Download(cfg, cacheDir, "/repo", download.Ref{Category: "runtimes", ID: "shellcheck"})
	require.NoError(t, err)

	for ev := range events {
		require.NotEqual(t, download.Failed, ev.Phase, "%+v", ev.Err)
	}

	root, err := download.Root(cacheDir)
	require.NoError(t, err)
	blobs, err := filepath.Glob(filepath.Join(root, "blobs", "sha256", "*"))
	require.NoError(t, err)
	assert.Empty(t, blobs, "no blob should remain once its install has finished")

	install := download.InstallDir(root, "runtimes", "shellcheck", "0.9.0")
	assert.DirExists(t, install)
}

func TestDownload_Runtime_AlreadyCached(t *testing.T) {
	cfg := config.Config{
		Downloads: map[string]config.Download{"shellcheck": {}},
		Runtimes: config.CategoryConfig[config.Runtime]{
			Definitions: map[string]config.Runtime{
				"shellcheck": {Type: "shellcheck", Download: "shellcheck", KnownGoodVersion: "1.0.0"},
			},
		},
	}
	cacheDir := t.TempDir()
	root, err := download.Root(cacheDir)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(download.InstallDir(root, "runtimes", "shellcheck", "1.0.0"), 0o755))

	events, err := download.Download(cfg, cacheDir, "/repo", download.Ref{Category: "runtimes", ID: "shellcheck"})
	require.NoError(t, err)
	var phases []download.Phase
	for ev := range events {
		require.NoError(t, ev.Err)
		phases = append(phases, ev.Phase)
	}
	assert.Equal(t, []download.Phase{download.Cached}, phases, "an already-installed version must not be re-fetched")
}

func TestDownload_Runtime_SystemVersion(t *testing.T) {
	cfg := config.Config{
		Runtimes: config.CategoryConfig[config.Runtime]{
			Definitions: map[string]config.Runtime{"php": {Type: "php", SystemVersion: ">=8.0.0"}},
		},
	}
	events, err := download.Download(cfg, t.TempDir(), "/repo", download.Ref{Category: "runtimes", ID: "php"})
	require.NoError(t, err)
	var phases []download.Phase
	for ev := range events {
		require.NoError(t, ev.Err)
		phases = append(phases, ev.Phase)
	}
	assert.Equal(t, []download.Phase{download.Cached}, phases, "a system_version runtime is never downloaded")
}

func TestDownload_UnknownRef(t *testing.T) {
	events, err := download.Download(config.Config{}, t.TempDir(), "/repo", download.Ref{Category: "runtimes", ID: "nope"})
	require.NoError(t, err)
	ev := <-events
	assert.Equal(t, download.Failed, ev.Phase)
	assert.Error(t, ev.Err)
}

func TestDownload_LintRef_ExpandsToTools(t *testing.T) {
	archive := tarGzBytes(t, "tool-1.0.0", "actionlint", "#!/bin/sh\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer srv.Close()

	cfg := config.Config{
		Downloads: map[string]config.Download{
			"actionlint": {Downloads: []config.DownloadEntry{{
				OS:  config.OSSpec{"linux": "linux", "macos": "macos", "windows": "windows"},
				CPU: config.OSSpec{"x86_64": "x86_64", "arm_64": "arm_64"},
				URL: srv.URL + "/actionlint.tar.gz", StripComponents: 1,
			}}},
		},
		Tools: map[string]config.Tool{
			"actionlint": {Name: "actionlint", Download: "actionlint", KnownGoodVersion: "1.0.0", Shims: []string{"actionlint"}},
		},
		Lint: config.LintConfig{
			CategoryConfig: config.CategoryConfig[config.Linter]{
				Definitions: map[string]config.Linter{"actionlint": {Name: "actionlint", Tools: []string{"actionlint"}}},
			},
		},
	}

	events, err := download.Download(cfg, t.TempDir(), "/repo", download.Ref{Category: "lint", ID: "actionlint"})
	require.NoError(t, err)
	var sawToolDone bool
	for ev := range events {
		require.NoError(t, ev.Err, "event: %+v", ev)
		if ev.Ref.Category == "tools" && ev.Ref.ID == "actionlint" && ev.Phase == download.Done {
			sawToolDone = true
		}
	}
	assert.True(t, sawToolDone, "a lint ref must expand into fetching its underlying tool(s)")
}

// buildFakePythonBinary compiles a tiny real executable that stands in for a python interpreter
// in TestDownload_ToolRuntimePackage_PythonPythonPath: it exits 0 and prints "ok" if PYTHONPATH
// contains a site-packages path, else exits 1 with a stderr message -- genuinely proving whether
// the env reached it, the way a real python failing an import would. It must be a real compiled
// binary, not a shell script (see that test's comment for why a script-as-interpreter doesn't
// work here).
func buildFakePythonBinary(t *testing.T) string {
	t.Helper()
	src := `package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	if strings.Contains(os.Getenv("PYTHONPATH"), "site-packages") {
		fmt.Println("ok")
		return
	}
	fmt.Fprintln(os.Stderr, "no site-packages on PYTHONPATH")
	os.Exit(1)
}
`
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "fakepython.go")
	require.NoError(t, os.WriteFile(srcPath, []byte(src), 0o644))
	binPath := filepath.Join(dir, "fakepython")
	out, err := exec.Command("go", "build", "-o", binPath, srcPath).CombinedOutput()
	require.NoError(t, err, "building fake python helper: %s", out)
	return binPath
}

// nodeToolConfig builds a runtime+package "tools" config: a "node" runtime fetched from srv (a
// tar.gz containing a stub bin/npm) and an "eslint" tool installed through it -- the shape
// TestDownload_ToolRuntimePackage_* tests exercise end to end.
func nodeToolConfig(nodeArchiveURL string) config.Config {
	return config.Config{
		Downloads: map[string]config.Download{
			"node": {Downloads: []config.DownloadEntry{{
				OS:  config.OSSpec{"linux": "linux", "macos": "macos", "windows": "windows"},
				CPU: config.OSSpec{"x86_64": "x86_64", "arm_64": "arm_64"},
				URL: nodeArchiveURL, StripComponents: 1,
			}}},
		},
		Tools: map[string]config.Tool{
			"eslint": {Name: "eslint", Runtime: "node", Package: "eslint", KnownGoodVersion: "8.10.0", Shims: []string{"eslint"}},
		},
		Runtimes: config.CategoryConfig[config.Runtime]{
			Definitions: map[string]config.Runtime{
				"node": {Type: "node", Download: "node", KnownGoodVersion: "18.0.0"},
			},
		},
	}
}

// TestDownload_ToolRuntimePackage_NodeModulesBin drives a runtime:+package: tool ("eslint" through
// a "node" runtime) through the real Download() end to end, with a stub npm that lays its
// installed binary out at node_modules/.bin/<name> the way real npm does. This is the layout
// installNodePackage's `npm install --prefix` actually produces -- shimSearchPaths must know to
// look there, or FindShimTarget always fails for every runtime+package tool (the bug this test
// pins down).
func TestDownload_ToolRuntimePackage_NodeModulesBin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("npm stub is a POSIX shell script")
	}

	npmScript := `#!/bin/sh
mkdir -p "$3/node_modules/.bin"
cat > "$3/node_modules/.bin/eslint" <<'EOS'
#!/bin/sh
echo ran-eslint
EOS
chmod +x "$3/node_modules/.bin/eslint"
`
	archive := tarGzBytes(t, "node-18.0.0", "bin/npm", npmScript)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer srv.Close()

	cfg := nodeToolConfig(srv.URL + "/node.tar.gz")
	cacheDir := t.TempDir()
	events, err := download.Download(cfg, cacheDir, "/repo", download.Ref{Category: "tools", ID: "eslint"})
	require.NoError(t, err)

	var phases []download.Phase
	for ev := range events {
		require.NoError(t, ev.Err, "event: %+v", ev)
		phases = append(phases, ev.Phase)
	}
	assert.Contains(t, phases, download.Done)

	root, err := download.Root(cacheDir)
	require.NoError(t, err)
	shimPath := download.ShimPath(root, "tools", "eslint", "8.10.0", "eslint")
	require.FileExists(t, shimPath)

	out, err := exec.Command(shimPath).CombinedOutput()
	require.NoError(t, err, "shim output: %s", out)
	assert.Equal(t, "ran-eslint\n", string(out))
}

// TestInstallPackagesFile_Node_NodeModulesBin drives InstallPackagesFile with a stub npm that
// lays its installed binary out at node_modules/.bin/<name> -- the same real layout
// installNodePackage's own test (TestDownload_ToolRuntimePackage_NodeModulesBin) already pins
// down for a named-package install; this is the packages_file (manifest) equivalent.
func TestInstallPackagesFile_Node_NodeModulesBin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("npm stub is a POSIX shell script")
	}

	runtimeDir := t.TempDir()
	npmScript := `#!/bin/sh
mkdir -p node_modules/.bin
cat > node_modules/.bin/commitlint <<'EOS'
#!/bin/sh
echo ran-commitlint
EOS
chmod +x node_modules/.bin/commitlint
`
	require.NoError(t, os.MkdirAll(filepath.Join(runtimeDir, "bin"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(runtimeDir, "bin", "npm"), []byte(npmScript), 0o755))

	packagesFile := filepath.Join(t.TempDir(), "package.json")
	require.NoError(t, os.WriteFile(packagesFile, []byte(`{"dependencies":{"@commitlint/cli":"^19.0"}}`), 0o644))

	pkgInstallDir := filepath.Join(t.TempDir(), "install")
	rt := config.Runtime{Type: "node"}
	err := download.InstallPackagesFile(rt, runtimeDir, pkgInstallDir, packagesFile)
	require.NoError(t, err)

	shimTarget := filepath.Join(pkgInstallDir, "node_modules", ".bin", "commitlint")
	require.FileExists(t, shimTarget)
	out, err := exec.Command(shimTarget).CombinedOutput()
	require.NoError(t, err, "output: %s", out)
	assert.Equal(t, "ran-commitlint\n", string(out))
}

// actionPackagesConfig builds a "node" runtime config (no tools) -- the shape
// TestDownload_ActionPackagesRef_InstallsAndDedupesByContentHash needs: an action-packages ref
// only needs a runtime to install its manifest through, not a named tool/package.
func actionPackagesConfig(nodeArchiveURL string) config.Config {
	return config.Config{
		Downloads: map[string]config.Download{
			"node": {Downloads: []config.DownloadEntry{{
				OS:  config.OSSpec{"linux": "linux", "macos": "macos", "windows": "windows"},
				CPU: config.OSSpec{"x86_64": "x86_64", "arm_64": "arm_64"},
				URL: nodeArchiveURL, StripComponents: 1,
			}}},
		},
		Runtimes: config.CategoryConfig[config.Runtime]{
			Definitions: map[string]config.Runtime{
				"node": {Type: "node", Download: "node", KnownGoodVersion: "18.0.0"},
			},
		},
	}
}

// TestDownload_ActionPackagesRef_InstallsAndDedupesByContentHash drives a Ref{Category:
// "action-packages"} through the real Download() end to end: installing an action's packages_file
// manifest through its runtime, exactly the same claimInstall-guarded, registry-visible path tools
// and runtimes already use (Task 2). A second action with a byte-identical manifest (different
// path, different action ID) must dedupe to the SAME on-disk install and report Cached, not
// re-run npm -- the guarantee resolvePackagesFileBinDir's own doc comment already promised before
// this task, which this task must not regress.
func TestDownload_ActionPackagesRef_InstallsAndDedupesByContentHash(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("npm stub is a POSIX shell script")
	}

	npmScript := `#!/bin/sh
mkdir -p node_modules/.bin
cat > node_modules/.bin/commitlint <<'EOS'
#!/bin/sh
echo ran-commitlint
EOS
chmod +x node_modules/.bin/commitlint
`
	archive := tarGzBytes(t, "node-18.0.0", "bin/npm", npmScript)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer srv.Close()

	manifestContent := []byte(`{"dependencies":{"@commitlint/cli":"^19.0"}}`)
	manifest1 := filepath.Join(t.TempDir(), "package.json")
	require.NoError(t, os.WriteFile(manifest1, manifestContent, 0o644))
	manifest2 := filepath.Join(t.TempDir(), "package.json") // different path, identical content
	require.NoError(t, os.WriteFile(manifest2, manifestContent, 0o644))

	cfg := actionPackagesConfig(srv.URL + "/node.tar.gz")
	cfg.Actions = config.CategoryConfig[config.Action]{
		Definitions: map[string]config.Action{
			"commitlint":   {ID: "commitlint", Runtime: "node", PackagesFile: manifest1},
			"other-action": {ID: "other-action", Runtime: "node", PackagesFile: manifest2},
		},
	}

	cacheDir := t.TempDir()
	events, err := download.Download(cfg, cacheDir, "/repo", download.Ref{Category: "action-packages", ID: "commitlint"})
	require.NoError(t, err)
	var phases []download.Phase
	for ev := range events {
		require.NoError(t, ev.Err, "event: %+v", ev)
		phases = append(phases, ev.Phase)
	}
	assert.Contains(t, phases, download.Done)

	events2, err := download.Download(cfg, cacheDir, "/repo", download.Ref{Category: "action-packages", ID: "other-action"})
	require.NoError(t, err)
	var phases2 []download.Phase
	for ev := range events2 {
		require.NoError(t, ev.Err, "event: %+v", ev)
		phases2 = append(phases2, ev.Phase)
	}
	assert.Equal(t, []download.Phase{download.Cached}, phases2,
		"a byte-identical manifest from a different action must dedupe, not re-run npm")

	root, err := download.Root(cacheDir)
	require.NoError(t, err)
	sum := sha256.Sum256(manifestContent)
	installDir := download.InstallDir(root, "action-packages", hex.EncodeToString(sum[:]), "manifest")
	shimTarget := filepath.Join(installDir, "node_modules", ".bin", "commitlint")
	require.FileExists(t, shimTarget)
}

func TestInstallPackagesFile_UnsupportedRuntime(t *testing.T) {
	err := download.InstallPackagesFile(config.Runtime{Type: "python"}, t.TempDir(), t.TempDir(), "package.json")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `packages_file install not yet supported for runtime "python"`)
}

// pythonToolConfig builds a runtime+package "tools" config: a "python" runtime fetched from srv
// (a tar.gz containing a stub bin/pip) and a "black" tool installed through it -- the shape
// TestDownload_ToolRuntimePackage_PythonPythonPath exercises end to end.
func pythonToolConfig(pythonArchiveURL string) config.Config {
	return config.Config{
		Downloads: map[string]config.Download{
			"python": {Downloads: []config.DownloadEntry{{
				OS:  config.OSSpec{"linux": "linux", "macos": "macos", "windows": "windows"},
				CPU: config.OSSpec{"x86_64": "x86_64", "arm_64": "arm_64"},
				URL: pythonArchiveURL, StripComponents: 1,
			}}},
		},
		Tools: map[string]config.Tool{
			"black": {Name: "black", Runtime: "python", Package: "black", KnownGoodVersion: "24.0.0", Shims: []string{"black"}},
		},
		Runtimes: config.CategoryConfig[config.Runtime]{
			Definitions: map[string]config.Runtime{
				"python": {Type: "python", Download: "python", KnownGoodVersion: "3.11.0"},
			},
		},
	}
}

// TestDownload_ToolRuntimePackage_PythonPythonPath pins down Fix 1: pip install --prefix writes a
// console-script whose shebang points at the runtime's own python -- but that python has no
// venv/pyvenv.cfg to find the --prefix'd site-packages at run time. The fake pip below genuinely
// reproduces this: it writes a fake python whose shebang the console-script points to, and that
// fake python only succeeds if PYTHONPATH already contains a site-packages dir when it runs --
// exactly what a real ModuleNotFoundError failure mode looks like. Driving this through the real
// Download() and then actually exec'ing the resulting shim (not just checking it exists) is the
// only way to catch this class of bug, per the final review.
func TestDownload_ToolRuntimePackage_PythonPythonPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pip stub is a POSIX shell script")
	}

	// The fake python is a small compiled binary (built below), not a written "#!/bin/sh" script:
	// a script-interpreter shebanged from another script hits ENOEXEC on both Linux and Darwin
	// (the kernel refuses to chain two levels of "#!"), and a POSIX shell's own silent
	// fallback-to-/bin/sh-on-ENOEXEC behavior would then make the exec "succeed" vacuously without
	// ever running the fake python at all -- masking exactly the bug this test exists to catch. A
	// real compiled binary keeps the shebang a single, kernel-supported hop to a real executable,
	// exactly like a real pip console-script's "#!/path/to/python".
	fakePythonBin := buildFakePythonBinary(t)

	pipScript := `#!/bin/sh
prefix=$3
rtdir=$(dirname "$(dirname "$0")")
mkdir -p "$prefix/bin" "$prefix/lib/python3.99/site-packages"
cp ` + fakePythonBin + ` "$rtdir/bin/python"
chmod +x "$rtdir/bin/python"
cat > "$prefix/bin/black" <<EOS
#!$rtdir/bin/python
EOS
chmod +x "$prefix/bin/black"
`
	archive := tarGzBytes(t, "python-3.11.0", "bin/pip", pipScript)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer srv.Close()

	cfg := pythonToolConfig(srv.URL + "/python.tar.gz")
	// A short, explicit /tmp-rooted cacheDir, not t.TempDir(): the fake console-script's shebang
	// line below must hold the runtime's full absolute python path, and t.TempDir()'s
	// $TMPDIR/TestName/NNN nesting (very long under macOS's default $TMPDIR) pushes that past the
	// kernel's shebang-line length limit, causing the exec to silently no-op instead of running --
	// not a bug in the code under test, just an artifact of test-harness path length.
	cacheDir, err := os.MkdirTemp("/tmp", "rtunkpy-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(cacheDir) })

	events, err := download.Download(cfg, cacheDir, "/repo", download.Ref{Category: "tools", ID: "black"})
	require.NoError(t, err)

	var phases []download.Phase
	for ev := range events {
		require.NoError(t, ev.Err, "event: %+v", ev)
		phases = append(phases, ev.Phase)
	}
	assert.Contains(t, phases, download.Done)

	root, err := download.Root(cacheDir)
	require.NoError(t, err)
	shimPath := download.ShimPath(root, "tools", "black", "24.0.0", "black")
	require.FileExists(t, shimPath)

	out, err := exec.Command(shimPath).CombinedOutput()
	require.NoError(t, err, "shim output: %s", out)
	assert.Equal(t, "ok\n", string(out), "the installed tool's shim must genuinely run, proving PYTHONPATH reached the shebang's python")
}

// TestDownload_ToolRuntimePackage_RuntimeFetchFailure pins down that a runtime+package tool's
// Failed event carries the runtime's own real failure (a bad download), not the confusing
// downstream symptom of proceeding to InstallPackage anyway with no runtime on disk ("npm not
// found").
func TestDownload_ToolRuntimePackage_RuntimeFetchFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := nodeToolConfig(srv.URL + "/node.tar.gz")
	events, err := download.Download(cfg, t.TempDir(), "/repo", download.Ref{Category: "tools", ID: "eslint"})
	require.NoError(t, err)

	var toolFailed *download.Event
	for ev := range events {
		if ev.Ref.Category == "tools" && ev.Ref.ID == "eslint" && ev.Phase == download.Failed {
			e := ev
			toolFailed = &e
		}
	}
	require.NotNil(t, toolFailed, "the tool ref itself must report Failed when its runtime fails to fetch")
	require.Error(t, toolFailed.Err)
	assert.NotContains(t, toolFailed.Err.Error(), "npm not found",
		"must report the runtime's real failure, not the downstream symptom of proceeding anyway")
	assert.ErrorContains(t, toolFailed.Err, "unexpected status", "must surface the runtime download's actual HTTP failure")
}

// TestDownload_Runtime_FailedInstallNotPoisoned pins down Fix 3: a failed/interrupted install
// must not leave installDir behind looking "done" -- dirNonEmpty(installDir) is the only signal
// fetchRuntimeRef has for "already cached", and the pre-fix InstallDownload created that directory
// as its very first action, before ever touching the blob. So a first attempt that fails partway
// (here: a corrupt archive) must not make a second attempt for the same ref report Cached; it must
// retry for real.
func TestDownload_Runtime_FailedInstallNotPoisoned(t *testing.T) {
	good := tarGzBytes(t, "tool-1.0.0", "shellcheck", "#!/bin/sh\n")
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			_, _ = w.Write([]byte("not a valid gzip stream"))
			return
		}
		_, _ = w.Write(good)
	}))
	defer srv.Close()

	cfg := config.Config{
		Downloads: map[string]config.Download{
			"shellcheck": {Downloads: []config.DownloadEntry{{
				OS:  config.OSSpec{"linux": "linux", "macos": "macos", "windows": "windows"},
				CPU: config.OSSpec{"x86_64": "x86_64", "arm_64": "arm_64"},
				URL: srv.URL + "/shellcheck.tar.gz", StripComponents: 1,
			}}},
		},
		Runtimes: config.CategoryConfig[config.Runtime]{
			Definitions: map[string]config.Runtime{
				"shellcheck": {Type: "shellcheck", Download: "shellcheck", KnownGoodVersion: "1.0.0", Shims: []string{"shellcheck"}},
			},
		},
	}
	cacheDir := t.TempDir()

	events, err := download.Download(cfg, cacheDir, "/repo", download.Ref{Category: "runtimes", ID: "shellcheck"})
	require.NoError(t, err)
	var sawFailed bool
	for ev := range events {
		if ev.Phase == download.Failed {
			sawFailed = true
		}
	}
	require.True(t, sawFailed, "the corrupt archive must fail the first attempt")

	events, err = download.Download(cfg, cacheDir, "/repo", download.Ref{Category: "runtimes", ID: "shellcheck"})
	require.NoError(t, err)
	var phases []download.Phase
	for ev := range events {
		require.NotEqual(t, download.Failed, ev.Phase, "event: %+v", ev)
		phases = append(phases, ev.Phase)
	}
	assert.NotContains(t, phases, download.Cached, "a failed install must not poison the cache as Cached")
	assert.Contains(t, phases, download.Done, "the retry must actually (re)install")
}

// TestDownload_Runtime_WithArgsDerivedSemver pins down a real production bug: real taplo's own
// download recipe references ${semver} in its URL, a template var derived from ${version} via the
// download's own args: regex (real GitHub release tags like "release-cli-0.10.0" carry a prefix
// its release ASSETS don't) -- before ResolveArgs existed, ${semver} was never substituted at all,
// so the literal string "${semver}" ended up in the request URL and every fetch 404'd.
func TestDownload_Runtime_WithArgsDerivedSemver(t *testing.T) {
	archive := tarGzBytes(t, "tool-1.0.0", "taplo", "#!/bin/sh\n")
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write(archive)
	}))
	defer srv.Close()

	cfg := config.Config{
		Downloads: map[string]config.Download{
			"taplo": {
				Args: map[string]string{
					"semver": "${version}=>(?:release-cli-|release-taplo-cli-)?(?P<semver>.*)",
				},
				Downloads: []config.DownloadEntry{{
					OS:  config.OSSpec{"linux": "linux", "macos": "macos", "windows": "windows"},
					CPU: config.OSSpec{"x86_64": "x86_64", "arm_64": "arm_64"},
					URL: srv.URL + "/releases/download/${semver}/taplo.tar.gz", StripComponents: 1,
				}},
			},
		},
		Runtimes: config.CategoryConfig[config.Runtime]{
			Definitions: map[string]config.Runtime{
				"taplo": {Type: "taplo", Download: "taplo", KnownGoodVersion: "release-cli-1.0.0", Shims: []string{"taplo"}},
			},
		},
	}

	cacheDir := t.TempDir()
	events, err := download.Download(cfg, cacheDir, "/repo", download.Ref{Category: "runtimes", ID: "taplo"})
	require.NoError(t, err)

	var phases []download.Phase
	for ev := range events {
		require.NoError(t, ev.Err, "event: %+v", ev)
		phases = append(phases, ev.Phase)
	}
	assert.Contains(t, phases, download.Done)
	assert.Equal(t, "/releases/download/1.0.0/taplo.tar.gz", gotPath,
		"${semver} must resolve to the bare version (release-tag prefix stripped), not the literal string \"${semver}\" or the raw prefixed version")
}

func TestDownload_PluginsRef_AlwaysCached(t *testing.T) {
	cfg := config.Config{Plugins: struct {
		Sources map[string]config.PluginSource
	}{Sources: map[string]config.PluginSource{"trunk": {ID: "trunk"}}}}

	events, err := download.Download(cfg, t.TempDir(), "/repo", download.Ref{Category: "plugins", ID: "trunk"})
	require.NoError(t, err)
	ev := <-events
	assert.Equal(t, download.Cached, ev.Phase, "resolving cfg already fetched every plugin source it references")
}

func TestDownload_PluginsRef_UnknownSource(t *testing.T) {
	events, err := download.Download(config.Config{}, t.TempDir(), "/repo", download.Ref{Category: "plugins", ID: "nope"})
	require.NoError(t, err)
	ev := <-events
	assert.Equal(t, download.Failed, ev.Phase)
	assert.ErrorContains(t, ev.Err, `unknown plugin source "nope"`)
}

func TestDownload_LintRef_UnknownDefinition(t *testing.T) {
	events, err := download.Download(config.Config{}, t.TempDir(), "/repo", download.Ref{Category: "lint", ID: "nope"})
	require.NoError(t, err)
	ev := <-events
	assert.Equal(t, download.Failed, ev.Phase)
	assert.ErrorContains(t, ev.Err, `unknown lint definition "nope"`)
}

func TestDownload_ActionRef_UnknownAction(t *testing.T) {
	events, err := download.Download(config.Config{}, t.TempDir(), "/repo", download.Ref{Category: "actions", ID: "nope"})
	require.NoError(t, err)
	ev := <-events
	assert.Equal(t, download.Failed, ev.Phase)
	assert.ErrorContains(t, ev.Err, `unknown action "nope"`)
}

// TestDownload_ActionRef_NoRuntime_Cached covers an action with no runtime: field at all -- real
// catalog data has actions like go-mod-tidy that shell out directly, needing nothing fetched.
func TestDownload_ActionRef_NoRuntime_Cached(t *testing.T) {
	cfg := config.Config{
		Actions: config.CategoryConfig[config.Action]{
			Definitions: map[string]config.Action{"go-mod-tidy": {ID: "go-mod-tidy"}},
		},
	}
	events, err := download.Download(cfg, t.TempDir(), "/repo", download.Ref{Category: "actions", ID: "go-mod-tidy"})
	require.NoError(t, err)
	ev := <-events
	assert.Equal(t, download.Cached, ev.Phase)
}

// TestDownload_ActionRef_ExpandsToRuntime covers an action that DOES name a runtime -- expanded
// into a "runtimes" fetch (a system_version runtime, so this needs no network to prove the
// expansion happens).
func TestDownload_ActionRef_ExpandsToRuntime(t *testing.T) {
	cfg := config.Config{
		Actions: config.CategoryConfig[config.Action]{
			Definitions: map[string]config.Action{"commitlint": {ID: "commitlint", Runtime: "node"}},
		},
		Runtimes: config.CategoryConfig[config.Runtime]{
			Definitions: map[string]config.Runtime{"node": {Type: "node", SystemVersion: ">=18.0.0"}},
		},
	}
	events, err := download.Download(cfg, t.TempDir(), "/repo", download.Ref{Category: "actions", ID: "commitlint"})
	require.NoError(t, err)
	ev := <-events
	assert.Equal(t, download.Cached, ev.Phase, "expanded into the runtime's own fetch, which is Cached for system_version")
}

func TestDownload_ToolRef_UnknownDownloadRecipe(t *testing.T) {
	cfg := config.Config{
		Tools: map[string]config.Tool{"foo": {Name: "foo", Download: "missing-recipe", KnownGoodVersion: "1.0.0"}},
	}
	events, err := download.Download(cfg, t.TempDir(), "/repo", download.Ref{Category: "tools", ID: "foo"})
	require.NoError(t, err)
	ev := <-events
	assert.Equal(t, download.Failed, ev.Phase)
	assert.ErrorContains(t, ev.Err, `no download recipe "missing-recipe"`)
}

func TestDownload_ToolRef_RuntimePackage_UnknownRuntime(t *testing.T) {
	cfg := config.Config{
		Tools: map[string]config.Tool{"black": {Name: "black", Runtime: "missing-runtime", Package: "black", KnownGoodVersion: "1.0.0"}},
	}
	events, err := download.Download(cfg, t.TempDir(), "/repo", download.Ref{Category: "tools", ID: "black"})
	require.NoError(t, err)
	ev := <-events
	assert.Equal(t, download.Failed, ev.Phase)
	assert.ErrorContains(t, ev.Err, `no runtime "missing-runtime"`)
}

// TestDownload_AllRefs_DefaultsToEveryToolAndRuntime covers Download()'s empty-refs default path
// (bare `rtunk download`): every tool and runtime in cfg, pre-cached here so this needs no network.
func TestDownload_AllRefs_DefaultsToEveryToolAndRuntime(t *testing.T) {
	cfg := config.Config{
		Tools: map[string]config.Tool{
			"actionlint": {Name: "actionlint", KnownGoodVersion: "1.0.0"},
		},
		Runtimes: config.CategoryConfig[config.Runtime]{
			Definitions: map[string]config.Runtime{
				"python": {Type: "python", SystemVersion: ">=3.11.0"},
			},
		},
	}
	cacheDir := t.TempDir()
	root, err := download.Root(cacheDir)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(download.InstallDir(root, "tools", "actionlint", "1.0.0"), 0o755))

	events, err := download.Download(cfg, cacheDir, "/repo")
	require.NoError(t, err)

	seen := map[download.Ref]download.Phase{}
	for ev := range events {
		require.NoError(t, ev.Err, "event: %+v", ev)
		seen[ev.Ref] = ev.Phase
	}
	assert.Equal(t, download.Cached, seen[download.Ref{Category: "tools", ID: "actionlint"}],
		"the pre-cached tool must be fetched by default, not just an explicitly-named ref")
	assert.Equal(t, download.Cached, seen[download.Ref{Category: "runtimes", ID: "python"}],
		"the system_version runtime must be fetched by default too")
}

func TestPending_DedupesRuntimeAndSkipsInstalled(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{
		Tools: map[string]config.Tool{
			"a": {Runtime: "node", Package: "a", KnownGoodVersion: "1"},
			"b": {Runtime: "node", Package: "b", KnownGoodVersion: "1"},
			"c": {Download: "c", KnownGoodVersion: "1"},
		},
		Runtimes: config.CategoryConfig[config.Runtime]{Definitions: map[string]config.Runtime{"node": {KnownGoodVersion: "20"}}},
	}
	refs := []download.Ref{{Category: "tools", ID: "a"}, {Category: "tools", ID: "b"}, {Category: "tools", ID: "c"}}

	got := download.Pending(cfg, root, refs...)
	assert.Len(t, got, 4, "node once, plus a, b and c")

	require.NoError(t, os.MkdirAll(download.InstallDir(root, "tools", "c", "1"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(download.InstallDir(root, "tools", "c", "1"), "f"), nil, 0o644))
	assert.Len(t, download.Pending(cfg, root, refs...), 3, "an installed tool is not pending")
}
