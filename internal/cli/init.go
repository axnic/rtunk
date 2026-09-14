package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/xunleii/rtunk/pkg/trunk/githooks"
)

// initScaffold is the exact content `rtunk init` writes to a fresh .rtunk/rtunk.yaml -- v1.11.0 is
// a real, currently-working trunk-io/plugins tag, the same one this repo's own real
// .trunk/trunk.yaml pins (not an arbitrary placeholder). Enabled lists are intentionally omitted
// rather than written empty (`enabled: []`) -- trunkFile's own struct fields already default to
// nil when absent, so there is no functional difference, and a shorter scaffold reads better as a
// starting point built on via the already-existing `rtunk check enable`/`rtunk actions enable`/
// `rtunk git-hooks install`.
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

func (c *initCmd) Run(stdout io.Writer) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	repoRoot, err := gitRepoRoot(cwd)
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

	if err := os.MkdirAll(rtunkDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(configPath, []byte(initScaffold), 0o644); err != nil {
		return err
	}

	fmt.Fprintf(stdout, "initialized rtunk at %s\n", configPath)
	fmt.Fprintln(stdout, "next: rtunk check enable <linter>, rtunk actions enable <action>, rtunk git-hooks install")
	return nil
}

// deinitCmd is `rtunk deinit`: ROADMAP.md v0.7, reversing `rtunk init` -- removes .rtunk/ and any
// git hooks `rtunk git-hooks install` (a separate, already-shipped command any real init'd repo
// would have run) could have added, since "reversing init" means undoing everything rtunk itself
// could have set up, not just the config file alone.
type deinitCmd struct{}

func (c *deinitCmd) Run(stdout io.Writer) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	repoRoot, err := gitRepoRoot(cwd)
	if err != nil {
		return err
	}

	rtunkDir := filepath.Join(repoRoot, ".rtunk")
	if _, statErr := os.Stat(rtunkDir); os.IsNotExist(statErr) {
		fmt.Fprintln(stdout, "nothing to deinit")
		return nil
	}

	if err := os.RemoveAll(rtunkDir); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "removed %s\n", rtunkDir)

	removed, err := githooks.Uninstall(repoRoot)
	if err != nil {
		return err
	}
	for _, name := range removed {
		fmt.Fprintf(stdout, "removed hook: %s\n", name)
	}
	return nil
}
