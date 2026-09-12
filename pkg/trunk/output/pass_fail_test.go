package output

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParsePassFail(t *testing.T) {
	got := ParsePassFail("check-added-large-files", []string{"a.bin", "b.bin"})
	want := []Finding{
		{Linter: "check-added-large-files", File: "a.bin", Severity: "error", Message: "file did not pass"},
		{Linter: "check-added-large-files", File: "b.bin", Severity: "error", Message: "file did not pass"},
	}
	assert.Equal(t, want, got)
}
