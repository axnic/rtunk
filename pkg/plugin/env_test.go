package plugin

import (
	"os"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
)

func TestExpandTemplate_EnvWithDefault(t *testing.T) {
	os.Unsetenv("RTUNK_TEST_ENV_VAR")
	if got := expandTemplate("${env.RTUNK_TEST_ENV_VAR:-fallback}", nil); got != "fallback" {
		t.Errorf("expected the default, got %q", got)
	}
	t.Setenv("RTUNK_TEST_ENV_VAR", "set-value")
	if got := expandTemplate("${env.RTUNK_TEST_ENV_VAR:-fallback}", nil); got != "set-value" {
		t.Errorf("expected the env value to win over the default, got %q", got)
	}
	if got := expandTemplate("${env.RTUNK_TEST_ENV_VAR}", nil); got != "set-value" {
		t.Errorf("expected the env value with no default form, got %q", got)
	}
}

func TestExpandTemplate_PathVars(t *testing.T) {
	vars := map[string]string{"runtime": "/cache/runtime/node/22.16.0"}
	got := expandTemplate("${runtime}/bin", vars)
	if got != "/cache/runtime/node/22.16.0/bin" {
		t.Errorf("got %q", got)
	}
	if got := expandTemplate("${unknown}", vars); got != "" {
		t.Errorf("expected an unresolvable var to expand to empty, got %q", got)
	}
}

func TestResolveEnv_OptionalOmittedWhenEmpty(t *testing.T) {
	os.Unsetenv("RTUNK_TEST_OPTIONAL_VAR")
	defs := []envVarDef{
		{Name: "NODE_OPTIONS", Value: "${env.RTUNK_TEST_OPTIONAL_VAR}", Optional: true},
	}
	if got := resolveEnv(defs, nil); got != nil {
		t.Errorf("expected the optional unset var to be omitted entirely, got %+v", got)
	}
}

func TestResolveEnv_ListJoinsWithPathSeparator(t *testing.T) {
	t.Setenv("PATH", "/usr/bin")
	defs := []envVarDef{
		{Name: "PATH", List: []string{"${runtime}/bin", "${runtime}", "${env.PATH}"}},
	}
	vars := map[string]string{"runtime": "/cache/runtime/node/22.16.0"}
	got := resolveEnv(defs, vars)
	want := "PATH=/cache/runtime/node/22.16.0/bin" + string(os.PathListSeparator) +
		"/cache/runtime/node/22.16.0" + string(os.PathListSeparator) + "/usr/bin"
	if len(got) != 1 || got[0] != want {
		t.Errorf("got %+v, want [%q]", got, want)
	}
}

// TestResolveEnv_RealNodeRuntimeEnvironment decodes the actual
// runtime_environment block from github.com/trunk-io/plugins'
// runtimes/node/plugin.yaml (v1.10.2) and checks it resolves sanely: PATH
// gets the runtime's bin dirs prepended, and unset optional proxy vars are
// dropped rather than exported empty.
func TestResolveEnv_RealNodeRuntimeEnvironment(t *testing.T) {
	const raw = `
- name: HOME
  value: ${env.HOME:-}
- name: PATH
  list:
    - "${runtime}/bin"
    - "${runtime}"
    - "${env.PATH}"
- name: http_proxy
  value: ${env.http_proxy}
  optional: true
- name: NODE_OPTIONS
  value: ${env.NODE_OPTIONS}
  optional: true
`
	var defs []envVarDef
	if err := yaml.Unmarshal([]byte(raw), &defs); err != nil {
		t.Fatal(err)
	}

	os.Unsetenv("http_proxy")
	os.Unsetenv("NODE_OPTIONS")
	t.Setenv("PATH", "/usr/bin")
	t.Setenv("HOME", "/home/me")

	vars := map[string]string{"runtime": "/cache/runtime/node/22.16.0"}
	got := resolveEnv(defs, vars)

	names := map[string]string{}
	for _, kv := range got {
		k, v, _ := strings.Cut(kv, "=")
		names[k] = v
	}
	if len(got) != 2 {
		t.Fatalf("expected only HOME and PATH (the two optional proxy/NODE_OPTIONS vars unset), got %+v", got)
	}
	if names["HOME"] != "/home/me" {
		t.Errorf("HOME: got %q", names["HOME"])
	}
	wantPath := "/cache/runtime/node/22.16.0/bin" + string(os.PathListSeparator) +
		"/cache/runtime/node/22.16.0" + string(os.PathListSeparator) + "/usr/bin"
	if names["PATH"] != wantPath {
		t.Errorf("PATH: got %q, want %q", names["PATH"], wantPath)
	}
}
