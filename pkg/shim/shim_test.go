package shim

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestMergeEnv_ExtraWinsOnCollision(t *testing.T) {
	rt := Shim{Env: map[string]string{"HOME": "/rt-home", "FOO": "bar"}}
	got := mergeEnv(rt, map[string]string{"HOME": "/tool-home"})
	want := map[string]string{"HOME": "/tool-home", "FOO": "bar"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestPrependPath_OwnDirsBeforeRuntime(t *testing.T) {
	rt := Shim{Path: []string{"/runtime/bin"}}
	got := prependPath(rt, "/tool/bin")
	want := []string{"/tool/bin", "/runtime/bin"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestVersionSpecs(t *testing.T) {
	cases := []struct {
		fn           func(string, string) string
		pkg, version string
		want         string
	}{
		{goSpec, "cuelang.org/go/cmd/cue", "0.5.0", "cuelang.org/go/cmd/cue@v0.5.0"},
		{goSpec, "example.com/tool", "v1.2.3", "example.com/tool@v1.2.3"}, // already has "v", not doubled
		{goSpec, "example.com/tool", "", "example.com/tool"},
		{nodeSpec, "eslint", "8.10.0", "eslint@8.10.0"},
		{nodeSpec, "eslint", "", "eslint"},
		{pythonSpec, "bandit", "1.8.6", "bandit==1.8.6"},
		{pythonSpec, "bandit", "", "bandit"},
		{rubySpec, "brakeman", "5.4.0", "brakeman:5.4.0"},
		{rubySpec, "brakeman", "", "brakeman"},
		{phpSpec, "friendsofphp/php-cs-fixer", "3.54.0", "friendsofphp/php-cs-fixer:3.54.0"},
		{phpSpec, "friendsofphp/php-cs-fixer", "", "friendsofphp/php-cs-fixer"},
	}
	for _, c := range cases {
		if got := c.fn(c.pkg, c.version); got != c.want {
			t.Errorf("%q@%q = %q, want %q", c.pkg, c.version, got, c.want)
		}
	}
}

func TestAllPresent(t *testing.T) {
	dir := t.TempDir()
	pkgs := []Package{{Name: "eslint", Shims: []string{"eslint"}}}
	if allPresent(dir, pkgs) {
		t.Error("expected false before the shim exists")
	}
	if err := os.WriteFile(filepath.Join(dir, "eslint"), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if !allPresent(dir, pkgs) {
		t.Error("expected true once the shim exists")
	}
}

func TestBuildShims_ErrorsOnMissingShim(t *testing.T) {
	dir := t.TempDir()
	pkgs := []Package{{Name: "eslint", Shims: []string{"eslint"}}}
	if _, err := buildShims(dir, pkgs, dir, nil, nil); err == nil {
		t.Error("expected an error naming the missing shim")
	}
	if err := os.WriteFile(filepath.Join(dir, "eslint"), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	shims, err := buildShims(dir, pkgs, "/install/dir", []string{"/pathdir"}, map[string]string{"X": "1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(shims) != 1 || shims[0].Name != "eslint" || shims[0].Dir != "/install/dir" {
		t.Errorf("got %+v", shims)
	}
}

func TestFor_KnownAndUnknownRuntimes(t *testing.T) {
	for _, name := range []string{"go", "node", "python", "ruby", "php"} {
		if _, ok := For(name); !ok {
			t.Errorf("expected an installer registered for %q", name)
		}
	}
	if _, ok := For("java"); ok {
		t.Error("expected no installer for java — it has no package-manager-installed tools in the catalog")
	}
}
