package plugin

import (
	"fmt"

	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/config"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/download"
)

// Tool is a single CLI binary a Linter needs, resolved from a Plugin's own
// catalog: either a GitHub-release Download or a language-runtime
// PackageInstall (npm/pip/go/gem/composer). Installing it (pkg/tool.Ensure)
// is this package's caller's job, not this one's.
type Tool struct {
	Name           string
	Download       *config.Download
	PackageInstall *config.PackageInstall
}

// ToolFor returns the Tool def's own binary needs.
//
// ponytail: config.LinterDefinition still carries Download/PackageInstall
// directly (rather than every linter referencing a Tool by name the way
// trunk's own dialect does) — this is the seam where that gets adapted
// into the canonical Tool shape; fold LinterDefinition itself into
// referencing Tool by name once bundled/custom definitions are unified
// with catalog-sourced ones.
func ToolFor(def config.LinterDefinition) Tool {
	return Tool{Name: def.Name, Download: def.Download, PackageInstall: def.PackageInstall}
}

// resolveTool resolves tt (a linter's own `tools:` reference, or a
// tools.enabled entry matched against a Plugin's own Tools) into a Tool
// named name, pinned at pinnedVersion (tt's own KnownGoodVersion if
// empty) — shared by translateLinter and Plugin.DiscoverTools so there's
// one place that knows how to turn a trunkTool into an installable Tool.
func resolveTool(tt trunkTool, downloads []download.Group, name, pinnedVersion string) (Tool, string) {
	version := tt.KnownGoodVersion
	if pinnedVersion != "" {
		version = pinnedVersion
	}
	shims := shimNames(tt.Shims)
	if len(shims) == 0 {
		shims = []string{name}
	}
	result := Tool{Name: name}
	switch {
	case tt.Download != "":
		group := findDownloadGroup(downloads, tt.Download)
		if group == nil {
			return result, fmt.Sprintf("%s: download %q not found in this plugin.yaml's top-level downloads", name, tt.Download)
		}
		dl, reason := download.Resolve(*group, version, shims[0])
		if dl == nil {
			return result, fmt.Sprintf("%s: %s — commands will fail unless %q is already on PATH", name, reason, name)
		}
		result.Download = dl
		return result, ""
	case tt.Package == "":
		// neither package nor download: nothing this build knows how to
		// install — warn instead of silently leaving it uninstallable.
		return result, fmt.Sprintf("%s: no supported install mechanism declared — commands will fail unless %q is already on PATH", name, name)
	case tt.Runtime == "node" || tt.Runtime == "python" || tt.Runtime == "go" || tt.Runtime == "ruby" || tt.Runtime == "php":
		result.PackageInstall = &config.PackageInstall{
			Runtime: tt.Runtime,
			Package: tt.Package,
			Version: version,
			Shims:   shims,
		}
		return result, ""
	default:
		return result, fmt.Sprintf(
			"%s: install via %s package %q isn't supported yet — commands will fail unless %q is already on PATH",
			name, tt.Runtime, tt.Package, name)
	}
}
