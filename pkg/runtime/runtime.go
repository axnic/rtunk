// Package runtime installs managed language runtimes (node/python/go/
// ruby/php/java/rust) resolved from a plugin catalog (pkg/plugin.Plugins)
// and caches them under pkg/cache's Cache.Runtimes(), producing a
// pkg/shim.Shim per runtime — its own exposed binaries (node/npm/...,
// python/pip/..., go/gofmt/...) with PATH/env set to run standalone.
package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/cache"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/config"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/download"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/plugin"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/shim"
)

// bootstrap provisions extra binaries a freshly-extracted runtime needs
// beyond what its own download ships (pnpm for node, uv for python) —
// registered per runtime name, a no-op for anything not listed (go/ruby/
// php/java/rust ship everything pkg/shim needs already).
var bootstrap = map[string]func(dir string, path []string) error{
	"node":   bootstrapNode,
	"python": bootstrapPython,
}

// Ensure installs every ref (name[@version]) not yet cached under
// c.Runtimes(), resolved from ps, returning one Shim per runtime name. A
// ref that fails to resolve or install is a warning, not a hard error —
// matches pkg/plugin.DiscoverTools's own policy of never letting one bad
// entry stop the rest.
func Ensure(c *cache.Cache, ps plugin.Plugins, refs []config.PackageVersion) (map[string]shim.Shim, []string, error) {
	base, err := c.Runtimes()
	if err != nil {
		return nil, nil, err
	}
	result := map[string]shim.Shim{}
	var warnings []string
	for _, ref := range refs {
		name, version := ref.Name(), ref.Version()
		rt, err := ps.Runtime(name, version)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("runtimes.enabled %s: %v", name, err))
			continue
		}
		s, err := ensureOne(base, name, rt)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("runtimes.enabled %s: %v", name, err))
			continue
		}
		result[name] = s
	}
	return result, warnings, nil
}

// ensureOne installs rt under base/<name>/<version>/ if it isn't already
// there (idempotent — a second call is a stat + return), runs name's own
// bootstrap step if it has one, and returns the resulting Shim.
func ensureOne(base, name string, rt *plugin.Runtime) (shim.Shim, error) {
	dir := filepath.Join(base, name, rt.Version)
	shims := rt.Shims()
	if !installed(dir, shims) {
		if err := download.Install(rt.Download, dir); err != nil {
			return shim.Shim{}, err
		}
	}
	path, env := splitEnv(rt.Env(dir))
	if fn, ok := bootstrap[name]; ok {
		if err := fn(dir, path); err != nil {
			return shim.Shim{}, err
		}
	}
	execName := name
	if len(shims) > 0 {
		execName = shims[0]
	}
	return shim.Shim{Name: name, Exec: execName, Dir: dir, Path: path, Env: env}, nil
}

// installed reports whether dir already has this runtime's own binaries —
// checked via its first shim rather than a single well-known file (a
// runtime distribution is a whole tree, not one binary), in either of the
// two locations real runtime_environment PATH entries reference:
// <dir>/bin/<shim> or <dir>/<shim> (Windows places binaries there instead
// of bin/).
func installed(dir string, shims []string) bool {
	if len(shims) == 0 {
		_, err := os.Stat(dir)
		return err == nil
	}
	if _, err := os.Stat(filepath.Join(dir, "bin", shims[0])); err == nil {
		return true
	}
	_, err := os.Stat(filepath.Join(dir, shims[0]))
	return err == nil
}

// splitEnv separates a resolved "NAME=value" list (pkg/plugin.Runtime.Env's
// own shape) into PATH's own entries (Shim.Path — os.PathListSeparator-
// joined by pkg/plugin's resolveEnv, split back apart here) and everything
// else (Shim.Env).
func splitEnv(lines []string) (path []string, env map[string]string) {
	env = map[string]string{}
	for _, l := range lines {
		name, value, _ := strings.Cut(l, "=")
		if name == "PATH" {
			if value != "" {
				path = strings.Split(value, string(os.PathListSeparator))
			}
			continue
		}
		env[name] = value
	}
	return path, env
}
