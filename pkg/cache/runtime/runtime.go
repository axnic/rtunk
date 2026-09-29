package runtime

// Runtime is everything rtunk knows about one runtime type. A nil func / empty string means "not
// supported".
type Runtime struct {
	// Install installs pkg@version, plus every one of extra, through the runtime's package
	// manager into pkgDir, as one atomic unit: either all of them land together or none do (see
	// install.Finalize -- a second call against an already-finalized pkgDir would otherwise
	// silently no-op). extra is the RAW, unparsed catalog string per entry (see
	// config.Tool.ExtraPackages) -- each runtime's own installer parses it in its own ecosystem's
	// native syntax, since rtunk itself doesn't invent one.
	Install func(runtimeDir, pkgDir, pkg, version string, extra []string) error
	// InstallFile installs every dependency named in a manifest (node's package.json only).
	InstallFile func(runtimeDir, pkgDir, file string) error
	// ShimEnv returns environment an installed package tool's shim needs beyond what the plugin's
	// own runtime_environment/linter_environment provides -- a value only known once the package
	// is on disk, which those templates can't express (python's PYTHONPATH only).
	ShimEnv func(installDir string) ([]string, error)
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
