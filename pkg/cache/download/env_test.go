package download_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/xunleii/rtunk/pkg/cache/download"
	"github.com/xunleii/rtunk/pkg/trunk/config"
)

func TestBuildEnv(t *testing.T) {
	t.Setenv("PATH", "/usr/bin")
	t.Setenv("NODE_OPTIONS", "")

	entries := []config.EnvironmentEntry{
		{Name: "PATH", List: []string{"${runtime}/bin", "${runtime}", "${env.PATH}"}},
		{Name: "NODE_OPTIONS", Value: "${env.NODE_OPTIONS}", Optional: true},
		{Name: "NODE_PATH", Value: "${linter}/node_modules"},
	}
	subst := map[string]string{"runtime": "/cache/runtimes/node/22.18.0", "linter": "/cache/tools/eslint/8.10.0"}

	got := download.BuildEnv(entries, subst)
	assert.Contains(t, got, "PATH=/cache/runtimes/node/22.18.0/bin:/cache/runtimes/node/22.18.0:/usr/bin")
	assert.Contains(t, got, "NODE_PATH=/cache/tools/eslint/8.10.0/node_modules")
	assert.NotContains(t, got, "NODE_OPTIONS=", "an Optional entry that resolves empty must be omitted entirely")
}

func TestBuildEnv_RequiredEmptyIsKept(t *testing.T) {
	got := download.BuildEnv([]config.EnvironmentEntry{{Name: "FOO", Value: ""}}, nil)
	assert.Contains(t, got, "FOO=", "a non-Optional entry is kept even if its resolved value is empty")
}
