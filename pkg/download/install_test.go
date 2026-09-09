package download

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/config"
)

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestArchName(t *testing.T) {
	d := &config.Download{ArchMap: map[string]string{"amd64": "x64"}}
	got := ArchName(d)
	if runtime.GOARCH == "amd64" && got != "x64" {
		t.Errorf("expected amd64 to map to x64, got %q", got)
	}
	if runtime.GOARCH != "amd64" && got != runtime.GOARCH {
		t.Errorf("expected unmapped arch to pass through, got %q", got)
	}
}

func TestSubstituteDownload(t *testing.T) {
	d := &config.Download{Version: "8.30.1", ArchMap: map[string]string{"amd64": "x64"}}
	got := substituteDownload("gitleaks_${version}_${os}_${arch}.${ext}", d)
	want := "gitleaks_8.30.1_" + runtime.GOOS + "_" + ArchName(d) + "." + archiveExt()
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSubstituteDownload_ExtraArgs(t *testing.T) {
	d := &config.Download{Version: "0.10.0", ExtraArgs: map[string]string{"semver": "0.10.0"}}
	got := substituteDownload("https://github.com/tamasfe/taplo/releases/download/${semver}/taplo-darwin-aarch64.gz", d)
	want := "https://github.com/tamasfe/taplo/releases/download/0.10.0/taplo-darwin-aarch64.gz"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestAssetURL(t *testing.T) {
	d := &config.Download{GitHub: "gitleaks/gitleaks", Version: "8.30.1"}
	got := assetURL(d, "gitleaks_8.30.1_linux_x64.tar.gz")
	want := "https://github.com/gitleaks/gitleaks/releases/download/v8.30.1/gitleaks_8.30.1_linux_x64.tar.gz"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestVerifyChecksum(t *testing.T) {
	data := []byte("fake binary content")
	sums := "b0000000000000000000000000000000000000000000000000000000000000  other_file.tar.gz\n" +
		sha256Hex(data) + "  asset.tar.gz\n"

	if err := verifyChecksum(data, "asset.tar.gz", []byte(sums)); err != nil {
		t.Errorf("expected match, got %v", err)
	}
	if err := verifyChecksum([]byte("tampered"), "asset.tar.gz", []byte(sums)); err == nil {
		t.Error("expected checksum mismatch error")
	}
	if err := verifyChecksum(data, "missing.tar.gz", []byte(sums)); err == nil {
		t.Error("expected missing-entry error")
	}
}

func TestExtractTarGz_Flat(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	content := []byte("#!/bin/sh\necho hi\n")
	if err := tw.WriteHeader(&tar.Header{Name: "gitleaks", Mode: 0o755, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	tw.Write(content)
	tw.Close()
	gz.Close()

	dir := t.TempDir()
	if err := extractTarGz(buf.Bytes(), dir, 0); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "gitleaks"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Errorf("extracted content mismatch: %q", got)
	}
}

// TestExtractTarGz_StripComponents mimics a real runtime distribution's
// shape (node-v22.16.0-darwin-arm64/bin/node, .../lib/...): everything
// wrapped in one top-level directory, which strip_components: 1 should
// remove so bin/node lands directly at destDir/bin/node.
func TestExtractTarGz_StripComponents(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	writeTarEntry(t, tw, "node-v22.16.0-darwin-arm64/", nil, true)
	writeTarEntry(t, tw, "node-v22.16.0-darwin-arm64/bin/", nil, true)
	content := []byte("#!/bin/sh\necho node\n")
	writeTarEntry(t, tw, "node-v22.16.0-darwin-arm64/bin/node", content, false)
	writeTarEntry(t, tw, "node-v22.16.0-darwin-arm64/README.md", []byte("hi"), false)
	tw.Close()
	gz.Close()

	dir := t.TempDir()
	if err := extractTarGz(buf.Bytes(), dir, 1); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "bin", "node"))
	if err != nil {
		t.Fatalf("expected bin/node at destDir directly (wrapper dir stripped), got %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("content mismatch: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "node-v22.16.0-darwin-arm64")); err == nil {
		t.Error("expected the wrapper directory itself to NOT be recreated")
	}
}

// TestExtractTarGz_Symlink mirrors a real node distribution's own shape:
// bin/npm is a symlink into ../lib/node_modules/npm/bin/npm-cli.js, not a
// regular file — a real bug found via dogfooding: symlink entries were
// silently dropped, leaving npm/corepack entirely missing after
// extraction even though the archive itself carried them.
func TestExtractTarGz_Symlink(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	writeTarEntry(t, tw, "bin/", nil, true)
	writeTarEntry(t, tw, "lib/npm-cli.js", []byte("#!/usr/bin/env node\n"), false)
	if err := tw.WriteHeader(&tar.Header{
		Name:     "bin/npm",
		Typeflag: tar.TypeSymlink,
		Linkname: "../lib/npm-cli.js",
		Mode:     0o777,
	}); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()

	dir := t.TempDir()
	if err := extractTarGz(buf.Bytes(), dir, 0); err != nil {
		t.Fatal(err)
	}
	target, err := os.Readlink(filepath.Join(dir, "bin", "npm"))
	if err != nil {
		t.Fatalf("expected bin/npm extracted as a symlink, got %v", err)
	}
	if target != "../lib/npm-cli.js" {
		t.Errorf("expected symlink target ../lib/npm-cli.js, got %q", target)
	}
}

func writeTarEntry(t *testing.T, tw *tar.Writer, name string, content []byte, dir bool) {
	t.Helper()
	hdr := &tar.Header{Name: name, Mode: 0o755}
	if dir {
		hdr.Typeflag = tar.TypeDir
	} else {
		hdr.Typeflag = tar.TypeReg
		hdr.Size = int64(len(content))
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if !dir {
		if _, err := tw.Write(content); err != nil {
			t.Fatal(err)
		}
	}
}

func TestExtractZip_Flat(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	fw, err := zw.Create("gitleaks.exe")
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("MZ fake exe")
	fw.Write(content)
	zw.Close()

	dir := t.TempDir()
	if err := extractZip(buf.Bytes(), dir, 0); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "gitleaks.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Errorf("extracted content mismatch: %q", got)
	}
}

func TestExtractZip_StripComponents(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	fw, err := zw.Create("node-v22.16.0-win-x64/node.exe")
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("fake node.exe")
	fw.Write(content)
	zw.Close()

	dir := t.TempDir()
	if err := extractZip(buf.Bytes(), dir, 1); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "node.exe"))
	if err != nil {
		t.Fatalf("expected node.exe at destDir directly, got %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("content mismatch: %q", got)
	}
}

// TestExtractZip_Symlink mirrors the zip-format equivalent of
// TestExtractTarGz_Symlink: a symlink entry (Unix mode bits set in the
// zip's external attributes, per archive/zip's own os.ModeSymlink
// exposure) whose "content" is the link target text, not file bytes.
func TestExtractZip_Symlink(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	hdr := &zip.FileHeader{Name: "bin/npm", Method: zip.Store}
	hdr.SetMode(os.ModeSymlink | 0o777)
	fw, err := zw.CreateHeader(hdr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte("../lib/npm-cli.js")); err != nil {
		t.Fatal(err)
	}
	zw.Close()

	dir := t.TempDir()
	if err := extractZip(buf.Bytes(), dir, 0); err != nil {
		t.Fatal(err)
	}
	target, err := os.Readlink(filepath.Join(dir, "bin", "npm"))
	if err != nil {
		t.Fatalf("expected bin/npm extracted as a symlink, got %v", err)
	}
	if target != "../lib/npm-cli.js" {
		t.Errorf("expected symlink target ../lib/npm-cli.js, got %q", target)
	}
}

// TestExtractSingleFile_Gzip mirrors taplo's real download shape: a single
// gzip-compressed binary, not a tar/zip archive — extractSingleFile must
// detect it by magic bytes (asset is never even set for a full-URL download
// like taplo's) rather than trusting an extension.
func TestExtractSingleFile_Gzip(t *testing.T) {
	content := []byte("#!/bin/sh\necho fake taplo\n")
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	if err := extractSingleFile(buf.Bytes(), dir, "taplo"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "taplo"))
	if err != nil {
		t.Fatalf("expected taplo at destDir directly, got %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("content mismatch: %q", got)
	}
}

func TestExtractSingleFile_Zip(t *testing.T) {
	content := []byte("fake binary")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	fw, err := zw.Create("taplo.exe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	if err := extractSingleFile(buf.Bytes(), dir, "taplo"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "taplo"))
	if err != nil {
		t.Fatalf("expected taplo at destDir directly, got %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("content mismatch: %q", got)
	}
}

func TestExtractSingleFile_Raw(t *testing.T) {
	content := []byte("already a raw binary")
	dir := t.TempDir()
	if err := extractSingleFile(content, dir, "taplo"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "taplo"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Errorf("content mismatch: %q", got)
	}
}

func TestStripPathComponents(t *testing.T) {
	cases := []struct {
		name string
		n    int
		want string
	}{
		{"a/b/c", 0, "a/b/c"},
		{"a/b/c", 1, filepath.Join("b", "c")},
		{"a/b/c", 2, "c"},
		{"a/b/c", 3, ""},
		{"a", 1, ""},
	}
	for _, c := range cases {
		if got := stripPathComponents(c.name, c.n); got != c.want {
			t.Errorf("stripPathComponents(%q, %d) = %q, want %q", c.name, c.n, got, c.want)
		}
	}
}
