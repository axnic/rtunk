package download

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
)

// ProgressFunc reports bytes received so far out of total (total is -1 if the server didn't send
// Content-Length). Called from FetchBlob's own goroutine; callers that forward it onto a channel
// (see Event) are responsible for their own synchronization.
type ProgressFunc func(bytes, total int64)

// requireSecureScheme rejects any blobURL that isn't https:// or loopback http:// (the checksum
// model is TOFU -- no lockfile, no --secure mode -- so the URL itself is the only integrity
// anchor; a plain HTTP fetch to a real host lets a network attacker choose the bytes). Loopback
// stays allowed because the test suite's httptest.Server only ever serves plain HTTP there.
func requireSecureScheme(blobURL string) error {
	u, err := url.Parse(blobURL)
	if err != nil {
		return fmt.Errorf("download: fetch %s: %w", blobURL, err)
	}
	if u.Scheme == "https" {
		return nil
	}
	host := u.Hostname()
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("download: fetch %s: refusing non-https URL to non-loopback host %q (TOFU checksum model has no other integrity anchor)", blobURL, host)
}

// FetchBlob downloads blobURL over HTTPS (or HTTP, for loopback -- see requireSecureScheme) into
// root's content-addressed blob store: streamed through a SHA256 hasher into a temp file, renamed
// to BlobPath(root, sum) only once the hash is known, so a path is never read unless its name
// matches its own content (see the spec's "Checksum model"). progress may be nil.
func FetchBlob(root, blobURL string, progress ProgressFunc) (string, error) {
	if err := requireSecureScheme(blobURL); err != nil {
		return "", err
	}
	// blobURL is a catalog-supplied release URL, already forced to https (or loopback) by
	// requireSecureScheme above.
	resp, err := http.Get(blobURL) //nolint:gosec,noctx // v0.2 has no per-fetch context/cancellation yet
	if err != nil {
		return "", fmt.Errorf("download: fetch %s: %w", blobURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download: fetch %s: unexpected status %s", blobURL, resp.Status)
	}

	blobsDir := filepath.Join(root, "blobs", "sha256")
	if err := os.MkdirAll(blobsDir, 0o750); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(blobsDir, ".tmp-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // no-op once renamed below

	hasher := sha256.New()
	var written int64
	buf := make([]byte, 32*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := tmp.Write(buf[:n]); err != nil {
				_ = tmp.Close()
				return "", err
			}
			hasher.Write(buf[:n])
			written += int64(n)
			if progress != nil {
				progress(written, resp.ContentLength)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			_ = tmp.Close()
			return "", fmt.Errorf("download: fetch %s: %w", blobURL, readErr)
		}
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}

	sum := hex.EncodeToString(hasher.Sum(nil))
	dst := BlobPath(root, sum)
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return "", err
	}
	return dst, nil
}
