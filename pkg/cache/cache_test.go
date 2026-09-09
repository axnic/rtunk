package cache

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNew_CreatesRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nested", "cache")
	c, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if c.Root() != root {
		t.Errorf("expected Root() = %q, got %q", root, c.Root())
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		t.Errorf("expected %s to be created, got %v", root, err)
	}
}

func TestSubdirs_CreateAndDiffer(t *testing.T) {
	c, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dirs := map[string]func() (string, error){
		"runtimes": c.Runtimes,
		"tools":    c.Tools,
		"linters":  c.Linters,
		"plugins":  c.Plugins,
	}
	seen := map[string]string{}
	for name, fn := range dirs {
		dir, err := fn()
		if err != nil {
			t.Fatal(err)
		}
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Errorf("%s: expected %s to be created, got %v", name, dir, err)
		}
		for other, otherDir := range seen {
			if dir == otherDir {
				t.Errorf("%s and %s share the same directory %s", name, other, dir)
			}
		}
		seen[name] = dir
	}
}

func TestRepoKey_StableAndDistinguishesInputs(t *testing.T) {
	k1 := RepoKey(Repo{URL: "https://example.com/org/repo.git", Lock: "abc"})
	k2 := RepoKey(Repo{URL: "https://example.com/org/repo.git", Root: "/tmp/clone-b", Lock: "abc"})
	if k1 != k2 {
		t.Errorf("same remote+lock at different local paths should share a cache key: %q != %q", k1, k2)
	}
	if len(k1) != 32 {
		t.Errorf("expected a 32-char key, got %d: %q", len(k1), k1)
	}

	k3 := RepoKey(Repo{Root: "/tmp/clone-a", Lock: "abc"})
	if k3 == k1 {
		t.Error("no-remote fallback should not collide with the remote-based key")
	}

	k4 := RepoKey(Repo{URL: "https://example.com/org/repo.git", Lock: "def"})
	if k4 == k1 {
		t.Error("a different config lock should change the key even for the same remote")
	}
}

func TestWorkspace_CreatesPerRepoDir(t *testing.T) {
	c, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := Repo{URL: "https://example.com/org/repo.git", Lock: "abc"}
	dir, err := c.Workspace(repo)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(c.Root(), "repos", RepoKey(repo))
	if dir != want {
		t.Errorf("expected %q, got %q", want, dir)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Errorf("expected %s to be created, got %v", dir, err)
	}
}
