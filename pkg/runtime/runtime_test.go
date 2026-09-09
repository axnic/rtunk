package runtime

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSplitEnv_SeparatesPathFromOtherVars(t *testing.T) {
	lines := []string{
		"PATH=" + filepath.Join("a", "bin") + string(os.PathListSeparator) + filepath.Join("b", "bin"),
		"HOME=/home/x",
		"GO111MODULE=on",
	}
	path, env := splitEnv(lines)
	wantPath := []string{filepath.Join("a", "bin"), filepath.Join("b", "bin")}
	if !reflect.DeepEqual(path, wantPath) {
		t.Errorf("path = %+v, want %+v", path, wantPath)
	}
	wantEnv := map[string]string{"HOME": "/home/x", "GO111MODULE": "on"}
	if !reflect.DeepEqual(env, wantEnv) {
		t.Errorf("env = %+v, want %+v", env, wantEnv)
	}
}

func TestSplitEnv_NoPath(t *testing.T) {
	path, env := splitEnv([]string{"HOME=/home/x"})
	if path != nil {
		t.Errorf("expected no PATH entries, got %+v", path)
	}
	if env["HOME"] != "/home/x" {
		t.Errorf("got %+v", env)
	}
}

func TestInstalled_ChecksBinThenFlatLayout(t *testing.T) {
	dir := t.TempDir()
	if installed(dir, []string{"node"}) {
		t.Error("expected not installed before anything is written")
	}
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin", "node"), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if !installed(dir, []string{"node"}) {
		t.Error("expected installed once bin/node exists")
	}

	flat := t.TempDir()
	if err := os.WriteFile(filepath.Join(flat, "go"), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if !installed(flat, []string{"go"}) {
		t.Error("expected installed via the flat (non-bin/) layout too")
	}
}

func TestInstalled_NoShimsFallsBackToDirExistence(t *testing.T) {
	if installed(filepath.Join(t.TempDir(), "missing"), nil) {
		t.Error("expected not installed for a nonexistent dir with no shims")
	}
	if !installed(t.TempDir(), nil) {
		t.Error("expected installed for an existing dir with no shims to check")
	}
}
