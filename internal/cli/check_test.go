package cli

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/xunleii/rtunk/pkg/trunk/config"
)

// TestCommand_DisableUpstream_RealCatalogShapeDecodes guards against the exact bug found live:
// the real cached trunk-io/plugins catalog (12 confirmed occurrences: clippy, detekt,
// golangci-lint x2, iwyu, oxipng, pinact x2, trufflehog, trunk-toolbox x3) declares
// disable_upstream as a bare bool, never the list-of-superseded-linter-ids this field used to
// model -- config.Command.DisableUpstream []string made this exact snippet (clippy's own
// plugin.yaml) fail to unmarshal ("cannot unmarshal !!bool into []string"), breaking the entire
// plugin source load.
func TestCommand_DisableUpstream_RealCatalogShapeDecodes(t *testing.T) {
	const snippet = `
name: lint
run: cargo clippy --message-format json --locked -- --cap-lints=warn --no-deps
success_codes: [0, 101, 383]
run_from: ${target_directory}
disable_upstream: true
`
	var cmd config.Command
	require.NoError(t, yaml.Unmarshal([]byte(snippet), &cmd))
	require.True(t, cmd.DisableUpstream)
}
