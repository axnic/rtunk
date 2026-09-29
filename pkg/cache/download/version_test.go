package download_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/xunleii/rtunk/pkg/cache/download"
)

func TestResolveVersion_Pinned(t *testing.T) {
	got := download.ResolveVersion([]string{"node@22.16.0", "python@3.14.4"}, "node", "22.18.0")
	assert.Equal(t, "22.16.0", got)
}

func TestResolveVersion_FallsBackToKnownGoodVersion(t *testing.T) {
	got := download.ResolveVersion([]string{"node@22.16.0"}, "python", "3.14.4")
	assert.Equal(t, "3.14.4", got, "python isn't in the enabled list, so KnownGoodVersion wins")
}

func TestResolveVersion_EnabledWithoutPin(t *testing.T) {
	got := download.ResolveVersion([]string{"git-diff-check"}, "git-diff-check", "1.0.0")
	assert.Equal(t, "1.0.0", got, "enabled but with no @version falls back the same as not-enabled")
}
