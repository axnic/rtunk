// Package shim installs CLI tools/runtimes coming from a language's own
// package manager (or a plain GitHub-release binary download) and produces
// a Shim per exposed binary: enough to actually exec it — PATH entries and
// extra environment, no on-disk wrapper script (rtunk resolves this once,
// synchronously, right before running a command, unlike trunk's own lazy
// marker-file-checked wrapper scripts — see binary.go's doc comment).
package shim

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
)

// Shim is what's needed to exec one binary a Tool/Runtime exposes.
type Shim struct {
	// Name is the exposed shim name (a linter's `tools:` reference, a
	// runtime's own shims[] entry...) — can differ from Exec (real
	// example: golangci-lint2 exposes shim "golangci-lint2" but execs the
	// actual binary "golangci-lint").
	Name string
	Exec string
	// Dir is this shim's own install directory — its cache-key identity
	// (<name>/<version>/... under whichever Cache subdirectory it lives
	// in).
	Dir string
	// Path are PATH entries to prepend, in order (this shim's own bin dir
	// first, then anything it depends on — e.g. a language runtime's own
	// bin dir).
	Path []string
	// Env is extra environment this shim needs beyond PATH (GOPATH/GOROOT,
	// VIRTUAL_ENV, NODE_PATH...).
	Env map[string]string
}

// Package is one language-package-manager install request: a name+version
// pin plus the shim names it's expected to expose once installed.
type Package struct {
	Name, Version string
	Shims         []string
}

// Installer installs pkgs (batched — one underlying package-manager
// invocation for all of them, not one per package) into dest, using rt
// (the language runtime they run on top of — already Ensure'd by the
// caller) for PATH/env, and returns one Shim per shim name declared across
// pkgs.
type Installer func(rt Shim, dest string, pkgs []Package) ([]Shim, error)

// installers is the registry of language installers — keyed by the same
// runtime type strings trunk's own plugin.yaml catalog uses
// (trunkTool.Runtime/trunkRuntime.Type). Only languages with a real
// package-manager-installable tool in the catalog are implemented; java
// and rust tools observed there are plain binary downloads (see binary.go)
// running on top of a runtime's own shim, not package-manager installs —
// no installer needed for them.
var installers = map[string]Installer{
	"go":     InstallGo,
	"node":   InstallNode,
	"python": InstallPython,
	"ruby":   InstallRuby,
	"php":    InstallPHP,
}

// For returns the Installer registered for runtimeType, if any.
func For(runtimeType string) (Installer, bool) {
	i, ok := installers[runtimeType]
	return i, ok
}

// mergeEnv combines rt's own env with extra, extra winning on key
// collision — every language installer builds its own Shim's Env this
// way so a tool inherits whatever its runtime itself needs (e.g. an
// HOME/proxy passthrough) on top of its own package-manager-specific
// variables.
func mergeEnv(rt Shim, extra map[string]string) map[string]string {
	env := make(map[string]string, len(rt.Env)+len(extra))
	maps.Copy(env, rt.Env)
	maps.Copy(env, extra)
	return env
}

// prependPath returns dirs followed by rt's own Path entries — the
// standard "this shim's own bin dir(s), then its runtime's" ordering
// every language installer uses.
func prependPath(rt Shim, dirs ...string) []string {
	return append(append([]string{}, dirs...), rt.Path...)
}

// pathEnv formats path as a PATH= assignment for exec.Cmd.Env.
func pathEnv(path []string) string {
	return "PATH=" + strings.Join(path, string(os.PathListSeparator))
}

// allPresent reports whether every shim every package in pkgs declares
// already exists under binDir — each language installer checks this
// before running its package manager, so a repeat call across an
// unchanged batch is a stat sweep, not a re-install.
func allPresent(binDir string, pkgs []Package) bool {
	for _, p := range pkgs {
		for _, name := range p.Shims {
			if _, err := os.Stat(filepath.Join(binDir, name)); err != nil {
				return false
			}
		}
	}
	return true
}

// buildShims stats every shim every package in pkgs declares under
// binDir, erroring (naming the owning package) at the first missing one —
// called once install (or allPresent's skip) is expected to have made
// them all available.
func buildShims(binDir string, pkgs []Package, dir string, path []string, env map[string]string) ([]Shim, error) {
	var shims []Shim
	for _, p := range pkgs {
		for _, name := range p.Shims {
			if _, err := os.Stat(filepath.Join(binDir, name)); err != nil {
				return nil, fmt.Errorf("%s: %s not found in %s after install", p.Name, name, binDir)
			}
			shims = append(shims, Shim{Name: name, Exec: name, Dir: dir, Path: path, Env: env})
		}
	}
	return shims, nil
}
