package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

func realNodeRuntimePlugin(t *testing.T, dir string) *Plugin {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "linters"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "runtimes/node/plugin.yaml"), `
downloads:
  - name: node
    downloads:
      - os: macos
        url: https://nodejs.org/dist/v${version}/node-v${version}-darwin-x64.tar.gz
        version: <16.0.0
        strip_components: 1
      - os:
          linux: linux
          macos: darwin
        cpu:
          x86_64: x64
          arm_64: arm64
        url: https://nodejs.org/dist/v${version}/node-v${version}-${os}-${cpu}.tar.gz
        strip_components: 1
      - os: windows
        cpu: x86_64
        url: https://nodejs.org/dist/v${version}/node-v${version}-win-x64.zip
        strip_components: 1

runtimes:
  definitions:
    - type: node
      download: node
      runtime_environment:
        - name: PATH
          list: ["${runtime}/bin", "${runtime}", "${env.PATH}"]
      linter_environment:
        - name: NODE_PATH
          value: ${linter}/node_modules
      known_good_version: 22.16.0
      shims: [node, npm]
`)
	p, warnings, err := buildPlugin(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %+v", warnings)
	}
	return p
}

func TestRuntimeFor_ResolvesRealNodeDefinition(t *testing.T) {
	p := realNodeRuntimePlugin(t, t.TempDir())
	rt, err := p.RuntimeFor("node", "")
	if err != nil {
		t.Fatal(err)
	}
	if rt.Version != "22.16.0" {
		t.Errorf("expected the known_good_version default, got %q", rt.Version)
	}
	if rt.Download == nil {
		t.Fatal("expected a resolved download")
	}
}

func TestRuntimeFor_UnknownNameErrors(t *testing.T) {
	p := realNodeRuntimePlugin(t, t.TempDir())
	if _, err := p.RuntimeFor("python", ""); err == nil {
		t.Error("expected an error for a runtime not in this catalog")
	}
}

func TestRuntimeEnv_ResolvesRuntimeAndLinterEnvironment(t *testing.T) {
	p := realNodeRuntimePlugin(t, t.TempDir())
	rt, err := p.RuntimeFor("node", "22.16.0")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "/usr/bin")

	env := rt.Env("/cache/runtime/node/22.16.0")
	if len(env) != 1 || env[0] != "PATH=/cache/runtime/node/22.16.0/bin"+string(os.PathListSeparator)+"/cache/runtime/node/22.16.0"+string(os.PathListSeparator)+"/usr/bin" {
		t.Errorf("got %+v", env)
	}

	linterEnv := rt.LinterEnv("/cache/tools/eslint/1.0.0")
	if len(linterEnv) != 1 || linterEnv[0] != "NODE_PATH=/cache/tools/eslint/1.0.0/node_modules" {
		t.Errorf("got %+v", linterEnv)
	}
}

func TestRuntimeFor_Shims(t *testing.T) {
	p := realNodeRuntimePlugin(t, t.TempDir())
	rt, err := p.RuntimeFor("node", "")
	if err != nil {
		t.Fatal(err)
	}
	got := rt.Shims()
	if len(got) != 2 || got[0] != "node" || got[1] != "npm" {
		t.Errorf("expected [node npm], got %+v", got)
	}
}
