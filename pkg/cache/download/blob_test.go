package download_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xunleii/rtunk/pkg/cache/download"
)

func TestFetchBlob(t *testing.T) {
	const body = "hello world"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "11")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	root := t.TempDir()
	var gotBytes, gotTotal int64
	path, err := download.FetchBlob(root, srv.URL, func(n, total int64) {
		gotBytes, gotTotal = n, total
	})
	require.NoError(t, err)

	// sha256("hello world")
	want := filepath.Join(root, "blobs", "sha256", "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9")
	assert.Equal(t, want, path)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, body, string(data))
	assert.Equal(t, int64(11), gotBytes)
	assert.Equal(t, int64(11), gotTotal)
}

// TestFetchBlob_RejectsNonHTTPS pins down Fix 5: with no lockfile/--secure mode, a plain HTTP
// fetch to a non-loopback host lets a network attacker choose the bytes with no other integrity
// anchor (the TOFU checksum model's only anchor is the URL itself). This must be rejected before
// any network call -- if it made one, this test would hang or dial a real host.
func TestFetchBlob_RejectsNonHTTPS(t *testing.T) {
	_, err := download.FetchBlob(t.TempDir(), "http://example.com/whatever", nil)
	assert.ErrorContains(t, err, "https")
}

func TestFetchBlob_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := download.FetchBlob(t.TempDir(), srv.URL, nil)
	assert.Error(t, err)
}
