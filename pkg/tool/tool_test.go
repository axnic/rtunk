package tool

import (
	"os"
	"path/filepath"
	"testing"

	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/shim"
)

func TestBatchKey_OrderIndependentButSetSensitive(t *testing.T) {
	a := []shim.Package{{Name: "eslint", Version: "8.0.0"}, {Name: "prettier", Version: "3.0.0"}}
	b := []shim.Package{{Name: "prettier", Version: "3.0.0"}, {Name: "eslint", Version: "8.0.0"}}
	if batchKey(a) != batchKey(b) {
		t.Error("expected the same key regardless of input order")
	}
	c := []shim.Package{{Name: "eslint", Version: "8.5.0"}, {Name: "prettier", Version: "3.0.0"}}
	if batchKey(a) == batchKey(c) {
		t.Error("expected a different key once a version in the set changes")
	}
}

func TestInstallBatch_MissingRuntimeWarns(t *testing.T) {
	pkgs := []namedPackage{{name: "eslint", pkg: shim.Package{Name: "eslint", Shims: []string{"eslint"}}}}
	result, warnings := installBatch(t.TempDir(), "node", map[string]shim.Shim{}, pkgs)
	if len(result) != 0 {
		t.Errorf("expected no results, got %+v", result)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected one warning, got %+v", warnings)
	}
}

func TestInstallBatch_UnknownRuntimeTypeWarns(t *testing.T) {
	pkgs := []namedPackage{{name: "x", pkg: shim.Package{Name: "x", Shims: []string{"x"}}}}
	runtimes := map[string]shim.Shim{"cobol": {Name: "cobol"}}
	result, warnings := installBatch(t.TempDir(), "cobol", runtimes, pkgs)
	if len(result) != 0 {
		t.Errorf("expected no results, got %+v", result)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected one warning for the unregistered installer, got %+v", warnings)
	}
}

func TestSymlinkInto_CreatesAndReplacesStaleLink(t *testing.T) {
	projectCacheDir := t.TempDir()
	targetA := filepath.Join(t.TempDir(), "a")
	targetB := filepath.Join(t.TempDir(), "b")

	if err := symlinkInto(projectCacheDir, "tools", "eslint", targetA); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(projectCacheDir, "tools", "eslint")
	got, err := os.Readlink(link)
	if err != nil || got != targetA {
		t.Fatalf("expected symlink to %s, got %s (err %v)", targetA, got, err)
	}

	// A second call with a different target (e.g. a version bump) must
	// replace the stale link, not error on "already exists".
	if err := symlinkInto(projectCacheDir, "tools", "eslint", targetB); err != nil {
		t.Fatal(err)
	}
	got, err = os.Readlink(link)
	if err != nil || got != targetB {
		t.Fatalf("expected symlink updated to %s, got %s (err %v)", targetB, got, err)
	}

	// A third call with the same target is a no-op, not an error.
	if err := symlinkInto(projectCacheDir, "tools", "eslint", targetB); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureProject_EmptyProjectCacheDirIsNoop(t *testing.T) {
	// Ensure itself needs a real plugin.Plugins to resolve anything, so
	// this only exercises the "" short-circuit with no refs — a real
	// end-to-end EnsureProject exercise belongs to pkg/workspace's own
	// tests, where a Plugins fixture naturally exists.
	result, warnings := EnsureProject(t.TempDir(), nil, nil, nil, "")
	if len(result) != 0 || len(warnings) != 0 {
		t.Errorf("expected no results/warnings for an empty ref list, got %+v %+v", result, warnings)
	}
}
