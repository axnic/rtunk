package cli

import (
	"io"

	"github.com/xunleii/rtunk/pkg/cache/download"
)

type cachePruneCmd struct{}

func (c *cachePruneCmd) Run(cli *CLI, _ io.Writer) error {
	return download.Prune(cli.CacheDir)
}
