package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// findTrunkYAML walks up from the working directory looking for .rtunk/rtunk.yaml (preferred) or
// .trunk/trunk.yaml (fallback, for compat with a repo that hasn't run `rtunk init` yet) at each
// level, the same way git locates .git -- the nearest directory that has either file wins, and
// .rtunk/rtunk.yaml is preferred over .trunk/trunk.yaml when a single directory has both (the
// common case once `rtunk init` has run in an existing trunk repo). The walk is bounded by the git
// repository root (if any): a miss must not fall through to an unrelated config file sitting
// further up the filesystem, e.g. in a parent repo or the home dir. Outside a git repo, it falls
// back to walking to the filesystem root.
func findTrunkYAML() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	start := dir

	var gitRoot string
	if out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output(); err == nil {
		gitRoot = strings.TrimSpace(string(out))
	}

	for {
		if candidate := filepath.Join(dir, ".rtunk", "rtunk.yaml"); fileExists(candidate) {
			return candidate, nil
		}
		if candidate := filepath.Join(dir, ".trunk", "trunk.yaml"); fileExists(candidate) {
			return candidate, nil
		}
		if dir == gitRoot {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("no .rtunk/rtunk.yaml or .trunk/trunk.yaml found (searched from %s upward); use --config to specify one", start)
}

// fileExists is findTrunkYAML's own os.Stat-based existence check, factored out since it's now
// called twice per directory level instead of once.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
