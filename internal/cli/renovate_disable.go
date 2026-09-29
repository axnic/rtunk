package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type renovateDisableCmd struct{}

func (c *renovateDisableCmd) Run(cli *CLI, stdout io.Writer, stderr Stderr) error {
	configPath := cli.Config
	if configPath == "" {
		found, err := findConfig()
		if err != nil {
			return err
		}
		configPath = found
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return err
	}
	removed := stripRenovateComments(&doc)
	if removed > 0 {
		if err := rewriteYAML(configPath, data, &doc); err != nil {
			return err
		}
	}
	_, _ = fmt.Fprintf(stdout, "%d annotation(s) removed\n", removed)
	warnIfNoRegexManager(stderr, configPath)
	return nil
}

// stripRenovateComments clears every "# renovate:" head comment under n -- the exact shape
// annotateDoc writes, whole-comment, on entry nodes and on ref key nodes -- and returns how many.
func stripRenovateComments(n *yaml.Node) int {
	count := 0
	if strings.HasPrefix(strings.TrimSpace(n.HeadComment), "# renovate:") {
		n.HeadComment = ""
		count++
	}
	for _, c := range n.Content {
		count += stripRenovateComments(c)
	}
	return count
}
