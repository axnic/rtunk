package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/xunleii/rtunk/pkg/git"
	"github.com/xunleii/rtunk/pkg/run/githooks"
)

// deinitCmd is `rtunk deinit`: ROADMAP.md v0.7, reversing `rtunk init` -- removes .rtunk/ and any
// git hooks `rtunk git-hooks install` (a separate, already-shipped command any real init'd repo
// would have run) could have added, since "reversing init" means undoing everything rtunk itself
// could have set up, not just the config file alone. Yes is accepted for trunk compatibility and
// has no effect: rtunk's deinit never prompts (an established non-goal since v0.7's own design),
// so -y/--yes asks for behavior deinit already has.
type deinitCmd struct {
	Yes bool `short:"y" help:"Accepted for trunk compatibility; deinit never prompts, this has no effect."`
}

func (c *deinitCmd) Run(stdout io.Writer) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	repoRoot, err := git.RepoRoot(cwd)
	if err != nil {
		return err
	}

	// githooks.Uninstall always runs, regardless of whether .rtunk/ exists -- a repo that only
	// ever used .trunk/trunk.yaml (rtunk's own primary drop-in-to-an-existing-trunk-repo use case)
	// can still have an installed hook, and it must not be left behind. It also runs BEFORE
	// removing .rtunk/: if Uninstall fails partway, the user keeps .rtunk/ rather than being left
	// with neither the config nor a working hook (every subsequent `git commit` would then fail to
	// find any config at all).
	removed, err := githooks.Uninstall(repoRoot)
	if err != nil {
		return err
	}

	rtunkDir := filepath.Join(repoRoot, ".rtunk")
	dirExisted := false
	if _, statErr := os.Stat(rtunkDir); statErr == nil {
		dirExisted = true
		if err := os.RemoveAll(rtunkDir); err != nil {
			return err
		}
	}

	if !dirExisted && len(removed) == 0 {
		_, _ = fmt.Fprintln(stdout, "nothing to deinit")
		return nil
	}
	for _, name := range removed {
		_, _ = fmt.Fprintf(stdout, "removed hook: %s\n", name)
	}
	if dirExisted {
		_, _ = fmt.Fprintf(stdout, "removed %s\n", rtunkDir)
	}
	return nil
}
