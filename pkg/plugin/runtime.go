package plugin

import (
	"fmt"

	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/config"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/download"
)

// Runtime is a managed language runtime (node/python/go/ruby/php/java/rust)
// resolved from a Plugin's own catalog. Installing it (pkg/runtime.Ensure)
// is this package's caller's job, not this one's; Runtime only carries
// what's needed to do that (Download) and to build its own environment
// (Env/LinterEnv).
type Runtime struct {
	Name     string
	Version  string
	Download *config.Download
	def      trunkRuntime
}

// RuntimeFor resolves the Runtime named name from p (matching
// trunkRuntime.Type), pinned at version (p's own KnownGoodVersion if
// version is empty).
func (p *Plugin) RuntimeFor(name, version string) (*Runtime, error) {
	def := findRuntime(p.Runtimes, name)
	if def == nil {
		return nil, fmt.Errorf("runtime %q not found", name)
	}
	v := def.KnownGoodVersion
	if version != "" {
		v = version
	}
	group := findDownloadGroup(p.Downloads, def.Download)
	if group == nil {
		return nil, fmt.Errorf("runtime %q: download %q not found in this catalog's downloads", name, def.Download)
	}
	// bin ("") is irrelevant here: a whole runtime distribution has no
	// single Download.Bin the way a Tool does — pkg/runtime checks for one
	// of its own Shims instead.
	dl, reason := download.Resolve(*group, v, "")
	if dl == nil {
		return nil, fmt.Errorf("runtime %q: %s", name, reason)
	}
	return &Runtime{Name: name, Version: v, Download: dl, def: *def}, nil
}

func findRuntime(runtimes []trunkRuntime, name string) *trunkRuntime {
	for i := range runtimes {
		if runtimes[i].Type == name {
			return &runtimes[i]
		}
	}
	return nil
}

// Shims are this runtime's own exposed binary names (real example: node's
// [node, npm, npx, corepack]).
func (r *Runtime) Shims() []string {
	return shimNames(r.def.Shims)
}

// Env resolves this runtime's own RuntimeEnvironment — the environment to
// run its binaries (node/npm/...) standalone — substituting ${runtime}
// with dir (pkg/runtime.Ensure's own result).
func (r *Runtime) Env(dir string) []string {
	return resolveEnv(r.def.RuntimeEnvironment, map[string]string{"runtime": dir})
}

// LinterEnv resolves the (different) environment a Linter using this
// runtime needs at run time, substituting ${linter} with linterDir (that
// Linter's own installed directory) — real example: node's
// linter_environment points NODE_PATH/PATH at the linter's own
// node_modules, not the runtime's own bin/.
func (r *Runtime) LinterEnv(linterDir string) []string {
	return resolveEnv(r.def.LinterEnvironment, map[string]string{"linter": linterDir})
}
