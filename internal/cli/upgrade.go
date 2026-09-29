package cli

import (
	"fmt"
	"io"
	"os"
	"runtime"

	"github.com/xunleii/rtunk/pkg/upgrade"
)

// githubAPIBase overrides upgrade.LatestRelease's apiBase -- "" means the real
// https://api.github.com. Test-only seam (package cli's own white-box test convention, same as
// check_enable_test.go's writeScratchTrunkYAML): never set outside a test.
var githubAPIBase = ""

// selfExecutablePath resolves the path upgradeCmd replaces -- os.Executable() in real use,
// overridable in tests so they never actually replace the real `go test` binary mid-run.
var selfExecutablePath = os.Executable

// upgradeCmd is `rtunk upgrade`: ROADMAP.md v0.6, checking rtunk's own GitHub Releases (never
// linters/tools/runtimes/plugins -- those stay reproducibly pinned via trunk.yaml).
type upgradeCmd struct {
	Check bool `aliases:"dry-run" help:"Report whether a newer release is available, without installing it. (alias: --dry-run)"`
}

func (c *upgradeCmd) Run(cli *CLI, stdout io.Writer) error {
	if Version == "dev" {
		_, _ = fmt.Fprintln(stdout, "rtunk: cannot determine current version, skipping")
		return nil
	}

	rel, err := upgrade.LatestRelease(githubAPIBase, "xunleii", "rtunk")
	if err != nil {
		return err
	}

	newVersion, available := upgrade.Available(rel, Version)
	if !available {
		_, _ = fmt.Fprintf(stdout, "rtunk is up to date (%s)\n", Version)
		return nil
	}

	if c.Check {
		_, _ = fmt.Fprintf(stdout, "a newer release is available: %s -> %s\n", Version, newVersion)
		return fmt.Errorf("rtunk: upgrade available (%s -> %s)", Version, newVersion)
	}

	assetName := upgrade.AssetName(runtime.GOOS, runtime.GOARCH)
	var assetURL string
	for _, a := range rel.Assets {
		if a.Name == assetName {
			assetURL = a.BrowserDownloadURL
			break
		}
	}
	if assetURL == "" {
		return fmt.Errorf("rtunk: release %s has no asset named %q for this platform", newVersion, assetName)
	}

	targetPath, err := selfExecutablePath()
	if err != nil {
		return err
	}

	if err := upgrade.Apply(cli.CacheDir, assetURL, targetPath); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "upgraded %s -> %s\n", Version, newVersion)
	return nil
}
