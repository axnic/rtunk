package download

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/config"
)

// Install downloads, verifies, and extracts d into destDir (creating it if
// needed). The caller decides what "already installed" means for its own
// purposes (a specific binary present, a directory non-empty...) and only
// calls this on a cache miss.
func Install(d *config.Download, destDir string) error {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	asset := substituteDownload(d.Asset, d)
	data, err := fetch(assetURL(d, asset))
	if err != nil {
		return fmt.Errorf("download %s: %w", asset, err)
	}
	if d.Checksums != "" {
		checksumsAsset := substituteDownload(d.Checksums, d)
		sums, err := fetch(assetURL(d, checksumsAsset))
		if err != nil {
			return fmt.Errorf("download checksums %s: %w", checksumsAsset, err)
		}
		if err := verifyChecksum(data, asset, sums); err != nil {
			return err
		}
	}
	if d.RenameSingleFile {
		if err := extractSingleFile(data, destDir, d.Bin); err != nil {
			return fmt.Errorf("extract %s: %w", asset, err)
		}
		return nil
	}
	if err := extractArchive(data, asset, destDir, d.StripComponents); err != nil {
		return fmt.Errorf("extract %s: %w", asset, err)
	}
	return nil
}

// ArchName is d's own arch substitution (${arch}/${cpu}) — exported so
// callers that lay out their own cache directory alongside Install (e.g.
// pkg/shim's osArch-keyed tool dirs) can replicate the same key.
func ArchName(d *config.Download) string {
	if m, ok := d.ArchMap[runtime.GOARCH]; ok {
		return m
	}
	return runtime.GOARCH
}

func osName(d *config.Download) string {
	if m, ok := d.OSMap[runtime.GOOS]; ok {
		return m
	}
	return runtime.GOOS
}

func archiveExt() string {
	if runtime.GOOS == "windows" {
		return "zip"
	}
	return "tar.gz"
}

func substituteDownload(tpl string, d *config.Download) string {
	arch := ArchName(d)
	pairs := []string{
		"${version}", d.Version,
		"${os}", osName(d),
		"${arch}", arch,
		"${cpu}", arch, // some real trunk definitions (trufflehog) name the placeholder ${cpu}
		"${ext}", archiveExt(),
	}
	for name, value := range d.ExtraArgs {
		pairs = append(pairs, "${"+name+"}", value)
	}
	return strings.NewReplacer(pairs...).Replace(tpl)
}

// assetURL builds the download URL for asset: d.URL (a full template) when
// set, else the GitHub-releases convention (github/<GitHub>/releases/...).
func assetURL(d *config.Download, asset string) string {
	if d.URL != "" {
		return substituteDownload(d.URL, d)
	}
	return fmt.Sprintf("https://github.com/%s/releases/download/v%s/%s", d.GitHub, d.Version, asset)
}

func fetch(url string) ([]byte, error) {
	if !strings.HasPrefix(url, "https://") {
		return nil, fmt.Errorf("refusing non-HTTPS download URL %q", url)
	}
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %s for %s", resp.Status, url)
	}
	return io.ReadAll(resp.Body)
}

func verifyChecksum(data []byte, assetName string, checksumsFile []byte) error {
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	for _, line := range strings.Split(string(checksumsFile), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == assetName {
			if fields[0] != got {
				return fmt.Errorf("checksum mismatch for %s: expected %s, got %s", assetName, fields[0], got)
			}
			return nil
		}
	}
	return fmt.Errorf("no checksum entry for %s in checksums file", assetName)
}

// extractSingleFile decompresses data straight to destDir/bin — real
// rename_single_file downloads are all gzip (detected by magic bytes rather
// than trusting an asset extension, since a full-URL download like taplo's
// never sets Asset at all); a zip is also accepted (its lone entry, in case
// a platform's release ever uses that instead), and anything else is
// assumed already-raw and written as-is.
func extractSingleFile(data []byte, destDir, bin string) error {
	dest := filepath.Join(destDir, bin)
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return err
		}
		defer gz.Close()
		return writeExtracted(dest, gz)
	}
	if zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data))); err == nil {
		for _, f := range zr.File {
			if f.FileInfo().IsDir() {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return err
			}
			err = writeExtracted(dest, rc)
			rc.Close()
			return err
		}
		return fmt.Errorf("zip archive has no files")
	}
	return writeExtracted(dest, bytes.NewReader(data))
}

func extractArchive(data []byte, assetName, destDir string, stripComponents int) error {
	if strings.HasSuffix(assetName, ".zip") {
		return extractZip(data, destDir, stripComponents)
	}
	return extractTarGz(data, destDir, stripComponents)
}

// extractTarGz extracts data into destDir, stripping the first
// stripComponents path segments from every entry's name first (tar's own
// --strip-components) — 0 (the default) is right for a flat single-binary
// archive (most linter tools); a real runtime distribution (e.g. node's
// node-v22.16.0-darwin-arm64/bin/node) wraps everything in one top-level
// directory and needs 1 to land its own bin/lib/... at destDir directly.
// An entry with nothing left after stripping (the wrapper directory entry
// itself) is skipped; directory entries are otherwise recreated implicitly
// by MkdirAll-ing each file's own parent.
func extractTarGz(data []byte, destDir string, stripComponents int) error {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeSymlink {
			continue
		}
		rel := stripPathComponents(hdr.Name, stripComponents)
		if rel == "" {
			continue
		}
		dest := filepath.Join(destDir, rel)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		// A real distribution's own bin/ commonly symlinks into a sibling
		// lib/ dir (node's bin/npm -> ../lib/node_modules/npm/bin/npm-cli.js)
		// rather than shipping a regular file — dropping these silently
		// left such shims entirely missing after extraction.
		if hdr.Typeflag == tar.TypeSymlink {
			os.Remove(dest) // idempotent re-extraction: os.Symlink errors if dest exists
			if err := os.Symlink(hdr.Linkname, dest); err != nil {
				return err
			}
			continue
		}
		if err := writeExtracted(dest, tr); err != nil {
			return err
		}
	}
}

func extractZip(data []byte, destDir string, stripComponents int) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rel := stripPathComponents(f.Name, stripComponents)
		if rel == "" {
			continue
		}
		dest := filepath.Join(destDir, rel)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		if f.Mode()&os.ModeSymlink != 0 {
			target, rerr := io.ReadAll(rc)
			rc.Close()
			if rerr != nil {
				return rerr
			}
			os.Remove(dest) // idempotent re-extraction: os.Symlink errors if dest exists
			if err := os.Symlink(string(target), dest); err != nil {
				return err
			}
			continue
		}
		err = writeExtracted(dest, rc)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// stripPathComponents removes the first n slash-separated segments from
// name, returning "" if nothing is left (the wrapper directory entry
// itself, or n exceeding name's own depth).
func stripPathComponents(name string, n int) string {
	parts := strings.Split(filepath.ToSlash(name), "/")
	if len(parts) <= n {
		return ""
	}
	return filepath.Join(parts[n:]...)
}

func writeExtracted(path string, r io.Reader) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, r)
	return err
}
