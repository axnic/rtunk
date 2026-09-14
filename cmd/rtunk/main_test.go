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
