package cli

import (
	"fmt"
	"os"
	"testing"
)

// TestMain points every test that does not pass --cache-dir at a throwaway directory: check and
// fmt now always write a run log, which would otherwise land in the developer's real OS cache dir.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "rtunk-cli-test-cache-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_ = os.Setenv("RTUNK_CACHE_DIR", dir)
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
