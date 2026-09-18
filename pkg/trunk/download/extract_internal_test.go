package download

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCopyLimited exercises the decompression-bomb guard directly (white-box, package download):
// the exported extraction paths all bake in maxExtractedEntrySize (4 GiB), too large to generate
// as test fixture data, so this calls copyLimited with a small limit instead.
func TestCopyLimited(t *testing.T) {
	t.Run("within limit", func(t *testing.T) {
		var buf bytes.Buffer
		err := copyLimited(&buf, strings.NewReader("hello"), 5)
		require.NoError(t, err)
		require.Equal(t, "hello", buf.String())
	})

	t.Run("exceeds limit", func(t *testing.T) {
		var buf bytes.Buffer
		err := copyLimited(&buf, strings.NewReader("hello world"), 5)
		require.Error(t, err)
	})
}
