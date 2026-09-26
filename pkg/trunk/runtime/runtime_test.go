package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestLookup pins every runtime type's Renovate data and supported operations, so moving a
// runtime around (or dropping a field) can't silently drop an annotation or an install path.
func TestLookup(t *testing.T) {
	cases := []struct {
		typ, datasource, extractVersion string
		installFile, shimEnv            bool
	}{
		{"go", "go", `^v(?<version>.+)$`, false, false},
		{"node", "npm", "", true, false},
		{"python", "pypi", "", false, true},
		{"php", "packagist", "", false, false},
		{"rust", "crate", "", false, false},
		{"ruby", "", "", false, false},
	}
	for _, c := range cases {
		t.Run(c.typ, func(t *testing.T) {
			rt, ok := Lookup(c.typ)
			assert.True(t, ok)
			assert.NotNil(t, rt.Install)
			assert.Equal(t, c.datasource, rt.Datasource)
			assert.Equal(t, c.extractVersion, rt.ExtractVersion)
			assert.Equal(t, c.installFile, rt.InstallFile != nil)
			assert.Equal(t, c.shimEnv, rt.ShimEnv != nil)
		})
	}
	_, ok := Lookup("java")
	assert.False(t, ok)
}
