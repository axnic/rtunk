package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// renovateCmd is `rtunk renovate`: ROADMAP.md's v1.1 addition, generating Renovate
// annotations for trunk.yaml's version pins (see
// docs/superpowers/specs/2026-09-17-renovate-annotations-design.md). enable/disable turn the
// annotations on or off and warn when the Renovate regexManager is missing.
type renovateCmd struct {
	Enable  renovateAnnotateCmd `cmd:"" help:"Annotate trunk.yaml's version pins for Renovate."`
	Disable renovateDisableCmd  `cmd:"" help:"Remove the Renovate annotations from trunk.yaml."`
	Config  renovateConfigCmd   `cmd:"" help:"Print the Renovate regexManagers config to add."`
}

// rewriteYAML re-encodes doc over configPath at the indent width data already used, so comments
// and layout everywhere else survive the round trip.
func rewriteYAML(configPath string, data []byte, doc *yaml.Node) error {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(detectIndentWidth(data))
	if err := enc.Encode(doc); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	//nolint:gosec // trunk.yaml is a repo-tracked config file, readable like every other tracked file
	return os.WriteFile(configPath, buf.Bytes(), 0o644)
}

// renovateConfigFiles are the places Renovate reads its own configuration from.
var renovateConfigFiles = []string{
	"renovate.json", "renovate.json5", ".renovaterc", ".renovaterc.json",
	".github/renovate.json", ".github/renovate.json5", ".gitlab/renovate.json", ".gitlab/renovate.json5",
}

// warnIfNoRegexManager warns on stderr when none of the Renovate config files at the repo root
// carries the regexManager `renovate config` prints (matched on its "renovate: datasource"
// pattern) -- without it the annotations are inert.
func warnIfNoRegexManager(stderr io.Writer, configPath string) {
	repoRoot := filepath.Dir(filepath.Dir(configPath))
	for _, f := range renovateConfigFiles {
		if b, err := os.ReadFile(filepath.Join(repoRoot, f)); err == nil && bytes.Contains(b, []byte("renovate: datasource")) {
			return
		}
	}
	_, _ = fmt.Fprintln(stderr, "warning: no Renovate regexManager found for the annotations; add the output of `rtunk renovate config` to your Renovate config")
}
