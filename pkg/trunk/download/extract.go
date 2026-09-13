package download

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ulikunitz/xz"

	"github.com/xunleii/rtunk/pkg/trunk/config"
)

// InstallDownload materializes one fetched blob into destDir: a straight copy (chmod +x) for a
// bare-binary download (entry.Executable), or an extracted archive otherwise, format inferred
// from url's own extension since blobPath's name is a content hash, not a filename.
//
// singleFileName names the result for a bare .gz URL (not .tar.gz/.tgz, which are ordinary tar
// archives): unlike a tar/zip archive, gzip alone compresses exactly one anonymous byte stream
// with no filename of its own, so there is nothing else to name it after. Real catalog examples
// (taplo, tree-sitter) always pass their own Download recipe's own Name here -- the recipe's own
// canonical name is what the tool's shims: entry (and FindShimTarget's own installDir/name
// lookup) expects to find on disk, regardless of the release asset's own filename (e.g.
// "taplo-darwin-aarch64.gz" must still become an installed file literally named "taplo"). Unused
// (pass "") for every other archive format, which name themselves from their own internal
// structure.
//
// All work happens inside a scratch temp directory (a sibling of destDir, so the final
// os.Rename below stays on one filesystem), published into destDir only via finalizeInstall once
// everything has succeeded (see Fix 3). Without this, destDir existed for the entire
// download/extract window -- and permanently after any failure -- because it used to be created
// as this function's very first action; dirNonEmpty(destDir) (fetchRuntimeRef/fetchToolRef's own
// "already cached" check) would then wrongly report a failed or interrupted install as Cached
// forever, with no error and no way to detect it short of `rtunk cache clean`.
func InstallDownload(blobPath, url, destDir string, entry config.DownloadEntry, singleFileName string) error {
	if err := os.MkdirAll(filepath.Dir(destDir), 0o755); err != nil {
		return err
	}
	tmpDir, err := os.MkdirTemp(filepath.Dir(destDir), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir) // no-op once finalizeInstall renames it into destDir

	if entry.Executable {
		name := filepath.Base(strings.SplitN(url, "?", 2)[0])
		if err := copyFile(blobPath, filepath.Join(tmpDir, name), 0o755); err != nil {
			return err
		}
		return finalizeInstall(tmpDir, destDir)
	}

	f, err := os.Open(blobPath)
	if err != nil {
		return err
	}
	defer f.Close()

	switch {
	case strings.HasSuffix(url, ".tar.gz") || strings.HasSuffix(url, ".tgz"):
		gz, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer gz.Close()
		if err := extractTar(gz, tmpDir, entry.StripComponents); err != nil {
			return err
		}
	case strings.HasSuffix(url, ".tar.xz"):
		xr, err := xz.NewReader(f)
		if err != nil {
			return err
		}
		if err := extractTar(xr, tmpDir, entry.StripComponents); err != nil {
			return err
		}
	case strings.HasSuffix(url, ".zip"):
		info, err := f.Stat()
		if err != nil {
			return err
		}
		zr, err := zip.NewReader(f, info.Size())
		if err != nil {
			return err
		}
		if err := extractZip(zr, tmpDir, entry.StripComponents); err != nil {
			return err
		}
	case strings.HasSuffix(url, ".gz"):
		// A bare .gz (not .tar.gz/.tgz, already matched above) is gzip alone: exactly one
		// anonymous compressed byte stream, no filename or directory structure of its own --
		// real catalog examples (taplo, tree-sitter) both ship their tool binary this way.
		gz, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer gz.Close()
		out, err := os.OpenFile(filepath.Join(tmpDir, singleFileName), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, gz); err != nil {
			out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
	default:
		return fmt.Errorf("download: %s: unrecognized archive format", url)
	}
	return finalizeInstall(tmpDir, destDir)
}

// finalizeInstall atomically publishes a completed install: tmpDir (scratch work done in a
// sibling directory of destDir, so this stays on one filesystem -- os.Rename requires that) is
// renamed into destDir only once every step has already succeeded. If destDir already exists, a
// concurrent or earlier caller won the race and finished first -- that's success, not a conflict:
// this caller's tmpDir is discarded and the winner's result is used as-is.
func finalizeInstall(tmpDir, destDir string) error {
	if err := os.Rename(tmpDir, destDir); err != nil {
		if dirNonEmpty(destDir) {
			_ = os.RemoveAll(tmpDir)
			return nil
		}
		return err
	}
	return nil
}

// extractTar walks a tar stream, stripping the first strip path components off every entry name
// (tar-style, matching ARCHITECTURE.md "downloads[].strip_components") and writing files under
// destDir, preserving each entry's mode (in particular the executable bit). Symlink entries are
// preserved too (not just regular files): node.org's real darwin/linux tarballs place bin/npm,
// bin/npx, and bin/corepack as symlinks into lib/node_modules/*/bin/*-cli.js, so skipping them (as
// v0.2 originally did, believing regular files were enough) silently produced a node runtime with
// no working npm.
func extractTar(r io.Reader, destDir string, strip int) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name, ok := stripPath(hdr.Name, strip)
		if !ok {
			continue // stripped away entirely (e.g. the top-level dir entry itself)
		}
		switch hdr.Typeflag {
		case tar.TypeReg:
			dst := filepath.Join(destDir, name)
			if err := verifyWithinDest(destDir, dst, hdr.Name); err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			dst := filepath.Join(destDir, name)
			if err := verifyWithinDest(destDir, dst, hdr.Name); err != nil {
				return err
			}
			if err := verifySymlinkWithinDest(destDir, dst, hdr.Linkname, hdr.Name); err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(hdr.Linkname, dst); err != nil {
				return err
			}
		default:
			continue // dirs and other special entry types -- v0.2 only needs regular files/symlinks
		}
	}
}

// extractZip is extractTar's archive/zip equivalent.
func extractZip(zr *zip.Reader, destDir string, strip int) error {
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name, ok := stripPath(f.Name, strip)
		if !ok {
			continue
		}
		dst := filepath.Join(destDir, name)
		if err := verifyWithinDest(destDir, dst, f.Name); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode())
		if err != nil {
			rc.Close()
			return err
		}
		if _, err := io.Copy(out, rc); err != nil {
			out.Close()
			rc.Close()
			return err
		}
		out.Close()
		rc.Close()
	}
	return nil
}

// verifyWithinDest rejects zip-slip (CWE-22): an archive entry's raw name (e.g. containing "..")
// can otherwise survive stripPath and make filepath.Join(destDir, name) resolve outside destDir,
// letting a malicious/corrupt archive write anywhere the process has permission to. name is the
// entry's original (pre-strip) path, kept only for a debuggable error message.
func verifyWithinDest(destDir, dst, name string) error {
	rel, err := filepath.Rel(destDir, dst)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("extract: entry %q escapes destination directory", name)
	}
	return nil
}

// verifySymlinkWithinDest rejects a symlink entry whose target escapes destDir: an absolute
// target, or a relative one that resolves outside destDir once joined to the symlink's own
// directory. Without this, a malicious archive's symlink could point anywhere the process can
// reach (CWE-59) -- unlike verifyWithinDest, which only guards the symlink's own path, this guards
// where it points.
func verifySymlinkWithinDest(destDir, dst, linkname, name string) error {
	if filepath.IsAbs(linkname) {
		return fmt.Errorf("extract: symlink entry %q has an absolute target %q", name, linkname)
	}
	resolved := filepath.Join(filepath.Dir(dst), linkname)
	rel, err := filepath.Rel(destDir, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("extract: symlink entry %q target %q escapes destination directory", name, linkname)
	}
	return nil
}

// stripPath drops the first n path components of name (tar-style strip_components). ok is false
// if name has n or fewer components -- the entry is entirely consumed by stripping (e.g. the
// top-level directory entry itself) and should be skipped.
func stripPath(name string, n int) (string, bool) {
	parts := strings.Split(filepath.ToSlash(name), "/")
	if len(parts) <= n {
		return "", false
	}
	return filepath.Join(parts[n:]...), true
}

// copyFile copies src to dst with the given mode, used for entry.Executable == true downloads
// that are a bare binary, not an archive.
func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
