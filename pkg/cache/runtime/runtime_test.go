package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestLookup pins every runtime type's supported operations, so moving a runtime around (or
// dropping a field) can't silently drop an install path. Renovate datasource data is pinned by
// pkg/renovate's own tests, not here -- this package no longer knows about Renovate.
func TestLookup(t *testing.T) {
	cases := []struct {
		typ                  string
		installFile, shimEnv bool
	}{
		{"go", false, false},
		{"node", true, false},
		{"python", false, true},
		{"php", false, false},
		{"rust", false, false},
		{"ruby", false, false},
	}
	for _, c := range cases {
		t.Run(c.typ, func(t *testing.T) {
			rt, ok := Lookup(c.typ)
			assert.True(t, ok)
			assert.NotNil(t, rt.Install)
			assert.Equal(t, c.installFile, rt.InstallFile != nil)
			assert.Equal(t, c.shimEnv, rt.ShimEnv != nil)
		})
	}
	_, ok := Lookup("java")
	assert.False(t, ok)
}
