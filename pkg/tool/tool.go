// Package tool installs CLI tools (standalone config.Tools entries or a
// linter's own binary) resolved from a plugin catalog (pkg/plugin.Plugins)
// and caches them under pkg/cache's Cache.Tools() (or Cache.Linters(),
// whichever base directory the caller passes — the two are laid out
// identically), producing a pkg/shim.Shim per tool.
package tool

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sort"

	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/config"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/plugin"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/shim"
)

// Ensure installs every ref (name[@version]) not yet cached under base,
// resolved from ps, batching package+runtime installs by runtime type
// (one underlying npm/pip/... invocation per runtime, not per tool) —
// runtimes supplies each runtime's own already-Ensure'd Shim
// (pkg/runtime.Ensure's own result); a tool needing one that's missing
// there is a warning, not a hard error, same as every other resolution
// failure here (matches pkg/plugin.DiscoverTools's own policy).
func Ensure(base string, ps plugin.Plugins, runtimes map[string]shim.Shim, refs []config.PackageVersion) (map[string]shim.Shim, []string) {
	result := map[string]shim.Shim{}
	var warnings []string
	byRuntime := map[string][]namedPackage{}

	for _, ref := range refs {
		name, version := ref.Name(), ref.Version()
		t, err := ps.Tool(name, version)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("tools %s: %v", name, err))
			continue
		}
		switch {
		case t.Download != nil:
			s, err := shim.InstallBinary(name, t.Download, base, nil)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("tools %s: %v", name, err))
				continue
			}
			result[name] = s
		case t.PackageInstall != nil:
			pi := t.PackageInstall
			byRuntime[pi.Runtime] = append(byRuntime[pi.Runtime], namedPackage{
				name: name,
				pkg:  shim.Package{Name: pi.Package, Version: pi.Version, Shims: pi.Shims},
			})
		default:
			warnings = append(warnings, fmt.Sprintf("tools %s: no install mechanism resolved", name))
		}
	}

	for runtimeType, pkgs := range byRuntime {
		installed, w := installBatch(base, runtimeType, runtimes, pkgs)
		warnings = append(warnings, w...)
		maps.Copy(result, installed)
	}
	return result, warnings
}

type namedPackage struct {
	name string
	pkg  shim.Package
}

// installBatch runs one pkg/shim.Installer invocation for every package in
// pkgs sharing runtimeType, then maps each resulting Shim back to its
// owning tool name (a package's own first declared shim names the tool it
// came from).
func installBatch(base, runtimeType string, runtimes map[string]shim.Shim, pkgs []namedPackage) (map[string]shim.Shim, []string) {
	var warnings []string
	rt, ok := runtimes[runtimeType]
	if !ok {
		for _, p := range pkgs {
			warnings = append(warnings, fmt.Sprintf("tools %s: runtime %q not installed", p.name, runtimeType))
		}
		return nil, warnings
	}
	installer, ok := shim.For(runtimeType)
	if !ok {
		for _, p := range pkgs {
			warnings = append(warnings, fmt.Sprintf("tools %s: no installer for runtime %q", p.name, runtimeType))
		}
		return nil, warnings
	}

	specs := make([]shim.Package, len(pkgs))
	for i, p := range pkgs {
		specs[i] = p.pkg
	}
	dest := filepath.Join(base, runtimeType, batchKey(specs))
	shims, err := installer(rt, dest, specs)
	if err != nil {
		for _, p := range pkgs {
			warnings = append(warnings, fmt.Sprintf("tools %s: %v", p.name, err))
		}
		return nil, warnings
	}

	byShimName := make(map[string]shim.Shim, len(shims))
	for _, s := range shims {
		byShimName[s.Name] = s
	}
	result := map[string]shim.Shim{}
	for _, p := range pkgs {
		if len(p.pkg.Shims) == 0 {
			continue
		}
		if s, ok := byShimName[p.pkg.Shims[0]]; ok {
			result[p.name] = s
		}
	}
	return result, warnings
}

// batchKey names the shared install dir for a batch of packages installed
// together in one package-manager invocation — deterministic for the same
// set regardless of order, so repeated Ensure calls across an unchanged
// set of tools stay a cache hit (pkg/shim's own allPresent check), and the
// dir changes (a fresh install) whenever the set itself changes.
func batchKey(pkgs []shim.Package) string {
	specs := make([]string, len(pkgs))
	for i, p := range pkgs {
		specs[i] = p.Name + "@" + p.Version
	}
	sort.Strings(specs)
	sum := sha256.Sum256(fmt.Appendf(nil, "%v", specs))
	return hex.EncodeToString(sum[:])[:16]
}

// EnsureProject calls Ensure, then symlinks each resulting Shim's own Dir
// into projectCacheDir's own tools/<name> — a stable path for the current
// repo to reference (pkg/cache.Cache.Workspace's own result) even as a
// tool's pinned version changes across config edits. A no-op beyond
// Ensure if projectCacheDir is "".
func EnsureProject(base string, ps plugin.Plugins, runtimes map[string]shim.Shim, refs []config.PackageVersion, projectCacheDir string) (map[string]shim.Shim, []string) {
	result, warnings := Ensure(base, ps, runtimes, refs)
	if projectCacheDir == "" {
		return result, warnings
	}
	for name, s := range result {
		if err := symlinkInto(projectCacheDir, "tools", name, s.Dir); err != nil {
			warnings = append(warnings, fmt.Sprintf("tools %s: %v", name, err))
		}
	}
	return result, warnings
}

// EnsureOne installs t (already resolved independently of any catalog —
// e.g. pkg/plugin.ToolFor's own result, a linter's own embedded
// Download/PackageInstall) under base if it isn't already cached, using
// runtimes (pkg/runtime.Ensure's own result) for a PackageInstall tool's
// own runtime. Unlike Ensure, there's no batching across multiple tools:
// this is a single, standalone install — a Linter's own lazy, run-time
// install of just its own binary, not a bulk resolve from a catalog.
func EnsureOne(base string, t plugin.Tool, runtimes map[string]shim.Shim) (shim.Shim, error) {
	switch {
	case t.Download != nil:
		return shim.InstallBinary(t.Name, t.Download, base, nil)
	case t.PackageInstall != nil:
		pi := t.PackageInstall
		pkgs := []namedPackage{{name: t.Name, pkg: shim.Package{Name: pi.Package, Version: pi.Version, Shims: pi.Shims}}}
		result, warnings := installBatch(base, pi.Runtime, runtimes, pkgs)
		if s, ok := result[t.Name]; ok {
			return s, nil
		}
		if len(warnings) > 0 {
			return shim.Shim{}, fmt.Errorf("%s", warnings[0])
		}
		return shim.Shim{}, fmt.Errorf("%s: install produced no shim", t.Name)
	default:
		return shim.Shim{}, fmt.Errorf("%s: no install mechanism resolved", t.Name)
	}
}

// EnsureOneProject calls EnsureOne, then symlinks the result's own Dir
// into projectCacheDir's own tools/<name> — see EnsureProject.
func EnsureOneProject(base string, t plugin.Tool, runtimes map[string]shim.Shim, projectCacheDir string) (shim.Shim, error) {
	s, err := EnsureOne(base, t, runtimes)
	if err != nil || projectCacheDir == "" {
		return s, err
	}
	if err := symlinkInto(projectCacheDir, "tools", t.Name, s.Dir); err != nil {
		return shim.Shim{}, err
	}
	return s, nil
}

// symlinkInto symlinks target at <projectCacheDir>/<subdir>/<name>,
// creating parent directories as needed. Idempotent: a stale symlink
// (pointing elsewhere, e.g. an older cached version) is replaced;
// os.Symlink itself errors if the link path already exists, so a plain
// existence check isn't enough.
func symlinkInto(projectCacheDir, subdir, name, target string) error {
	link := filepath.Join(projectCacheDir, subdir, name)
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return err
	}
	if existing, err := os.Readlink(link); err == nil && existing == target {
		return nil
	}
	os.Remove(link)
	return os.Symlink(target, link)
}
