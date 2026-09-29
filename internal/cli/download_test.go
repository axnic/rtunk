package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// nodeTarGz builds a minimal tar.gz archive laid out the way a real runtime download's
// strip_components: 1 recipe expects: one top-level dir wrapping the shimmed executable, mirroring
// pkg/cache/download/download_test.go's own tarGzBytes helper (unexported there, in a different
// package, so duplicated here rather than exported cross-package for one test).
func nodeTarGz(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "node-1.0.0/node", Mode: 0o755, Size: int64(len("#!/bin/sh\n"))}))
	_, err := tw.Write([]byte("#!/bin/sh\n"))
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

// writeNodeFixture builds a throwaway trunk.yaml + local plugin repo whose "node" runtime's
// download: recipe URL points at srv, instead of the shared trunkYAML fixture's real (network)
// nodejs.org URL -- see TestDownloadCmd_Targeted.
func writeNodeFixture(t *testing.T, srv *httptest.Server) (trunkYAMLPath string) {
	t.Helper()
	root := t.TempDir()
	pluginDir := filepath.Join(root, "pluginrepo")
	require.NoError(t, os.MkdirAll(filepath.Join(pluginDir, "runtimes", "node"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pluginDir, "plugin.yaml"), []byte("version: \"0.1\"\n"), 0o644))

	nodePlugin := `downloads:
  - name: node
    version: 1.0.0
    downloads:
      - os: { linux: linux, macos: macos, windows: windows }
        cpu: { x86_64: x86_64, arm_64: arm_64 }
        url: ` + srv.URL + `/node.tar.gz
        strip_components: 1
runtimes:
  definitions:
    - type: node
      download: node
      known_good_version: 1.0.0
      shims: [node]
`
	require.NoError(t, os.WriteFile(filepath.Join(pluginDir, "runtimes", "node", "plugin.yaml"), []byte(nodePlugin), 0o644))

	trunk := `version: "0.1"
plugins:
  sources:
    - id: trunk
      local: ./pluginrepo
runtimes:
  enabled:
    - node@1.0.0
`
	path := filepath.Join(root, "trunk.yaml")
	require.NoError(t, os.WriteFile(path, []byte(trunk), 0o644))
	return path
}

// TestDownloadCmd_Targeted fetches one item (runtimes/node) end to end through the real CLI/Kong
// wiring and pkg/cache/download.Download, against an httptest.Server standing in for the download
// recipe's URL -- not the shared trunkYAML fixture's real nodejs.org URL, which would make this
// test network-dependent and unreliable in a sandboxed/offline run.
func TestDownloadCmd_Targeted(t *testing.T) {
	archive := nodeTarGz(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer srv.Close()

	configPath := writeNodeFixture(t, srv)
	stdout, stderr, err := run2(t, "--config", configPath, "--cache-dir", t.TempDir(), "toolbox", "download", "runtime", "node")
	require.NoError(t, err, "stderr: %s", stderr)
	assert.Contains(t, stdout, "runtimes node: done")
}

func TestDownloadCmd_UnknownCategory(t *testing.T) {
	_, _, err := run2(t, "--config", trunkYAML, "toolbox", "download", "bogus", "whatever")
	assert.Error(t, err)
}
