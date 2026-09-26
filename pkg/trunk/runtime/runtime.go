// Package runtime holds everything rtunk knows about one runtime type (go, node, python, ...):
// how a package is installed through that runtime's own package manager, what extra environment
// its shims need, and which Renovate datasource tracks its packages. pkg/trunk/download and
// pkg/trunk/renovate consult it through Lookup and know no runtime by name.
package runtime

// Runtime is everything rtunk knows about one runtime type. A nil func / empty string means "not
// supported".
type Runtime struct {
	// Install installs pkg@version through the runtime's package manager into pkgDir.
	Install func(runtimeDir, pkgDir, pkg, version string) error
	// InstallFile installs every dependency named in a manifest (node's package.json only).
	InstallFile func(runtimeDir, pkgDir, file string) error
	// ShimEnv returns environment an installed package tool's shim needs beyond what the plugin's
	// own runtime_environment/linter_environment provides -- a value only known once the package
	// is on disk, which those templates can't express (python's PYTHONPATH only).
	ShimEnv func(installDir string) ([]string, error)
	// Datasource is the Renovate datasource tracking this runtime's packages; "" means none.
	Datasource string
	// ExtractVersion is the Renovate extractVersion regex, when the datasource's versions carry
	// something a trunk.yaml pin doesn't (go's "v" prefix only).
	ExtractVersion string
}

// registry is keyed by config.Runtime.Type. Types absent here (java, ...) are unsupported.
var registry = map[string]Runtime{
	"go":     goRuntime,
	"node":   nodeRuntime,
	"python": pythonRuntime,
	"php":    phpRuntime,
	"ruby":   rubyRuntime,
	"rust":   rustRuntime,
}

// Lookup returns the Runtime for a runtime type; ok is false for an unknown type.
func Lookup(typ string) (Runtime, bool) {
	rt, ok := registry[typ]
	return rt, ok
}
