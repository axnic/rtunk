package runtime

import (
	"os"
	"path/filepath"
	stdruntime "runtime"

	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/config"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/download"
)

// uvVersion is the pinned astral-sh/uv release bootstrapped into every
// python runtime — no in-distribution equivalent to node's bundled
// corepack exists for python, so this is a plain GitHub-release download
// like any other "binary" tool (pkg/shim's own binary.go), just triggered
// once at runtime-install time instead of per-tool.
//
// ponytail: asset URL/naming below follows uv's documented release
// convention but isn't empirically verified against a real download in
// this sandboxed environment (no network access) — confirm asset names at
// https://github.com/astral-sh/uv/releases before relying on this in
// production, the same way this codebase's other download templates were
// hand-verified.
const uvVersion = "0.9.7"

func bootstrapPython(dir string, _ []string) error {
	binDir := filepath.Join(dir, "bin")
	if _, err := os.Stat(filepath.Join(binDir, "uv")); err == nil {
		return nil // already bootstrapped
	}
	d := &config.Download{
		Version:         uvVersion,
		URL:             "https://github.com/astral-sh/uv/releases/download/${version}/uv-${arch}-${os}.tar.gz",
		Bin:             "uv",
		StripComponents: 1,
		ArchMap:         map[string]string{"amd64": "x86_64", "arm64": "aarch64"},
		OSMap:           map[string]string{"darwin": "apple-darwin", "linux": "unknown-linux-gnu"},
	}
	if stdruntime.GOOS == "windows" {
		d.URL = "https://github.com/astral-sh/uv/releases/download/${version}/uv-${arch}-pc-windows-msvc.zip"
		d.Bin = "uv.exe"
	}
	return download.Install(d, binDir)
}
