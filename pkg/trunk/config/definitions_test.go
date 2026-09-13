package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/xunleii/rtunk/pkg/trunk/config"
)

// TestAction_Interactive checks the interactive field's two documented literal forms (bare
// `true`, or the string "optional") both decode without error.
func TestAction_Interactive(t *testing.T) {
	for _, snippet := range []string{"interactive: true", "interactive: optional"} {
		var a config.Action
		require.NoError(t, yaml.Unmarshal([]byte(snippet), &a))
		assert.NotEmpty(t, a.Interactive)
	}
}

// TestOSSpec_UnmarshalYAML checks both documented forms (ARCHITECTURE.md): a bare name decoding
// to a single key mapping to itself, and a map from trunk's vocabulary to upstream's own naming —
// modeled on the real shellcheck/plugin.yaml download entries, which use one of each.
func TestOSSpec_UnmarshalYAML(t *testing.T) {
	var d config.Download
	require.NoError(t, yaml.Unmarshal([]byte(`
name: shellcheck
downloads:
  - os: { linux: linux }
    cpu: { arm_64: aarch64, x86_64: x86_64 }
  - os: macos
    cpu: x86_64
`), &d))

	assert.Equal(t, config.OSSpec{"linux": "linux"}, d.Downloads[0].OS)
	assert.Equal(t, config.OSSpec{"arm_64": "aarch64", "x86_64": "x86_64"}, d.Downloads[0].CPU)
	assert.Equal(t, config.OSSpec{"macos": "macos"}, d.Downloads[1].OS)
	assert.Equal(t, config.OSSpec{"x86_64": "x86_64"}, d.Downloads[1].CPU)
}

// TestCommand_Enabled checks that an absent yaml "enabled:" key decodes to nil (a command is on
// by default), while an explicit "enabled: false" decodes to a non-nil false -- real catalog data
// (ruff's own "format" command) relies on distinguishing "not specified" from "explicitly off".
func TestCommand_Enabled(t *testing.T) {
	var withDefault config.Command
	require.NoError(t, yaml.Unmarshal([]byte(`
name: format
run: echo hi
`), &withDefault))
	assert.Nil(t, withDefault.Enabled, "no enabled: key at all must default to on (nil), not false")

	var explicitOff config.Command
	require.NoError(t, yaml.Unmarshal([]byte(`
name: format
run: echo hi
enabled: false
`), &explicitOff))
	require.NotNil(t, explicitOff.Enabled)
	assert.False(t, *explicitOff.Enabled)
}

func TestAction_ParsesEnvironmentAndNotifyOnError(t *testing.T) {
	var a config.Action
	err := yaml.Unmarshal([]byte(`
id: git-lfs
run: git lfs "${hook}" ${@}
environment:
  - name: SSH_AUTH_SOCK
    value: ${env.SSH_AUTH_SOCK}
    optional: true
notify_on_error: false
`), &a)
	require.NoError(t, err)
	require.Len(t, a.Environment, 1)
	assert.Equal(t, "SSH_AUTH_SOCK", a.Environment[0].Name)
	assert.Equal(t, "${env.SSH_AUTH_SOCK}", a.Environment[0].Value)
	assert.True(t, a.Environment[0].Optional)
	require.NotNil(t, a.NotifyOnError)
	assert.False(t, *a.NotifyOnError)
}

func TestAction_NotifyOnError_UnsetIsNil(t *testing.T) {
	var a config.Action
	err := yaml.Unmarshal([]byte(`id: git-blame-ignore-revs
run: bash ${cwd}/update_config.sh
`), &a)
	require.NoError(t, err)
	assert.Nil(t, a.NotifyOnError, "an omitted notify_on_error must decode as nil, not false, so callers can tell 'unset' from 'explicitly false'")
}
