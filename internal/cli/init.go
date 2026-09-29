package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/xunleii/rtunk/pkg/git"
)

// initScaffold is the exact content `rtunk init` writes to a fresh .rtunk/rtunk.yaml -- v1.11.0 is
// a real, currently-working trunk-io/plugins tag, the same one this repo's own real
// .trunk/trunk.yaml pins (not an arbitrary placeholder). Enabled lists are intentionally omitted
// rather than written empty (`enabled: []`) -- trunkFile's own struct fields already default to
// nil when absent, so there is no functional difference, and a shorter scaffold reads better as a
// starting point built on via the already-existing `rtunk linters enable`/`rtunk actions enable`/
// `rtunk git-hooks sync`.
const initScaffold = `version: "0.1"
plugins:
  sources:
    - id: trunk
      uri: https://github.com/trunk-io/plugins
      ref: v1.11.0
`

// initCmd is `rtunk init`: ROADMAP.md v0.7, scaffolding .rtunk/rtunk.yaml.
type initCmd struct {
	Force bool `help:"Overwrite an existing .rtunk/rtunk.yaml."`
}

func (c *initCmd) Run(stdout io.Writer, stderr Stderr) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	repoRoot, err := git.RepoRoot(cwd)
	if err != nil {
		return err
	}

	rtunkDir := filepath.Join(repoRoot, ".rtunk")
	configPath := filepath.Join(rtunkDir, "rtunk.yaml")

	if !c.Force {
		if _, statErr := os.Stat(configPath); statErr == nil {
			return fmt.Errorf("rtunk: %s already exists; use --force to overwrite", configPath)
		}
	}

	// Task 1 made findConfig prefer .rtunk/rtunk.yaml over .trunk/trunk.yaml -- so writing the
	// scaffold here would silently shadow a real, already-in-use .trunk/trunk.yaml for every other
	// command from this point on. Warn (not fail: init still succeeds) so that isn't silent.
	trunkYAMLPath := filepath.Join(repoRoot, ".trunk", "trunk.yaml")
	if _, statErr := os.Stat(trunkYAMLPath); statErr == nil {
		_, _ = fmt.Fprintf(stderr, "warning: %s already exists -- %s now takes precedence for this repo\n", trunkYAMLPath, configPath)
	}

	//nolint:gosec // .rtunk/ and its rtunk.yaml are repo-tracked, readable like every other tracked path
	if err := os.MkdirAll(rtunkDir, 0o755); err != nil {
		return err
	}
	//nolint:gosec // see above
	if err := os.WriteFile(configPath, []byte(initScaffold), 0o644); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(stdout, "initialized rtunk at %s\n", configPath)
	_, _ = fmt.Fprintln(stdout, "next: rtunk linters enable <linter>, rtunk actions enable <action>, rtunk git-hooks sync")
	return nil
}
