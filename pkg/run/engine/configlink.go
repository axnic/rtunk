package engine

import (
	"os"
	"path/filepath"
	"sync"
)

// configLinks refcounts the symlinks linkDirectConfigs drops into a directory, so concurrent
// jobs sharing one directory (typically repoRoot) neither race to create the same link nor
// remove it under each other.
var configLinks = struct {
	sync.Mutex
	refs map[string]int
}{refs: map[string]int{}}

// linkDirectConfigs makes the linter's config files kept in <repoRoot>/.trunk/configs visible
// to a tool running in dir, by symlinking each of names there -- what trunk does, and what
// tools like yamllint and markdownlint need since they only look in their cwd and its parents.
// A file of the same name already present in dir (the project's own config) always wins and is
// left alone. The returned function removes the links it created once no job uses them anymore.
func linkDirectConfigs(repoRoot, dir string, names []string) (cleanup func()) {
	var linked []string
	configLinks.Lock()
	defer configLinks.Unlock()
	for _, name := range names {
		src := filepath.Join(repoRoot, ".trunk", "configs", name)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		dst := filepath.Join(dir, name)
		if configLinks.refs[dst] == 0 {
			if _, err := os.Lstat(dst); err == nil {
				continue
			}
			if err := os.Symlink(src, dst); err != nil {
				continue
			}
		}
		configLinks.refs[dst]++
		linked = append(linked, dst)
	}
	return func() {
		configLinks.Lock()
		defer configLinks.Unlock()
		for _, dst := range linked {
			if configLinks.refs[dst]--; configLinks.refs[dst] == 0 {
				delete(configLinks.refs, dst)
				_ = os.Remove(dst)
			}
		}
	}
}
