package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolveVersion_LdflagsValueWins(t *testing.T) {
	old := version
	version = "v1.2.3"
	t.Cleanup(func() { version = old })
	assert.Equal(t, "v1.2.3", resolveVersion())
}

func TestResolveVersion_FallsBackToDevWithoutLdflagsOrModuleVersion(t *testing.T) {
	// go test itself builds this binary without -ldflags and (for an in-repo `go test` run, not
	// a `go install`) debug.ReadBuildInfo().Main.Version is "(devel)" -- both fallback branches
	// correctly land on "dev".
	old := version
	version = "dev"
	t.Cleanup(func() { version = old })
	assert.Equal(t, "dev", resolveVersion())
}

// debug.ReadBuildInfo() can't be faked in-process (no seam), so the pseudo-version fallback path
// that resolveVersion() takes under a plain `go build` (as opposed to `go test`'s "(devel)") is
// covered directly against the isUnresolvedVersion helper instead, with the exact pseudo-version
// string reproduced live from `go build ./cmd/rtunk` in this module.
func TestIsUnresolvedVersion(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"devel literal", "(devel)", true},
		{"go build pseudo-version", "v0.0.0-20260914192836-83c4ff4b160c", true},
		{"empty", "", true},
		{"real semver tag", "v1.2.3", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isUnresolvedVersion(tt.in))
		})
	}
}
