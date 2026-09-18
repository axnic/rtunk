package download

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// shimSearchPaths are the conventional locations a named executable ends up at inside an
// extracted install dir -- trunk itself doesn't symlink individual shims, it prepends
// ${runtime}/bin and ${runtime} to PATH (ARCHITECTURE.md runtime_environment); rtunk resolves one
// concrete path instead, since `rtunk where`/`rtunk exec` need that, not a PATH entry.
func shimSearchPaths(installDir, name string) []string {
	return []string{
		filepath.Join(installDir, name),
		filepath.Join(installDir, "bin", name),
		// npm install --prefix <installDir> (installNodePackage) lays its bins out here, not at
		// <installDir>/bin -- without this a runtime+package tool's FindShimTarget always fails.
		filepath.Join(installDir, "node_modules", ".bin", name),
		// composer require --working-dir <installDir> (installPhpPackage) lays its bins out
		// here (composer's own default bin-dir), not at <installDir>/bin.
		filepath.Join(installDir, "vendor", "bin", name),
	}
}

// FindShimTarget locates name inside installDir, checking the conventional locations an
// extracted archive lays binaries out at. Returns an error if none exist -- a real, debuggable
// failure (the archive didn't contain what the plugin's shims: list promised) rather than a
// silently wrong shim.
func FindShimTarget(installDir, name string) (string, error) {
	for _, p := range shimSearchPaths(installDir, name) {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("download: %s: no executable found under %s", name, installDir)
}

// WriteShim writes a POSIX shell wrapper at path that execs target with whatever arguments it's
// called with, for a download-recipe tool/runtime with no environment to thread through.
func WriteShim(path, target string) error {
	return WriteEnvShim(path, target, nil)
}

// WriteEnvShim is WriteShim plus exported environment variables (NAME=value pairs, as built by
// BuildEnv) set before exec -- needed for a runtime-based tool's shim, which must put its
// runtime's bin dir and its own node_modules/.bin on PATH before the real target (an
// npm-installed script with a `#!/usr/bin/env node` shebang) can even resolve node.
func WriteEnvShim(path, target string, env []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	script := "#!/bin/sh\n"
	for _, kv := range env {
		script += fmt.Sprintf("export %s\n", shellQuoteAssignment(kv))
	}
	script += fmt.Sprintf("exec %s \"$@\"\n", shellQuote(target))
	//nolint:gosec // a shim is a /bin/sh script the user is meant to exec; it must be executable
	return os.WriteFile(path, []byte(script), 0o755)
}

// shellQuote wraps s in single quotes for safe use as one /bin/sh word, escaping any single quote
// s itself contains.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shellQuoteAssignment quotes only the value half of a NAME=value pair, leaving NAME= unquoted
// (a shell identifier never needs it) so the result is valid `export NAME='value'` syntax.
func shellQuoteAssignment(kv string) string {
	if i := strings.Index(kv, "="); i >= 0 {
		return kv[:i+1] + shellQuote(kv[i+1:])
	}
	return shellQuote(kv)
}
