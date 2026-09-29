package renovate

import (
	"regexp"

	"github.com/xunleii/rtunk/pkg/trunk/config"
)

// Annotation is a resolved Renovate regex-manager target: the datasource and dependency name
// Renovate needs to look up and bump a version on its own.
type Annotation struct {
	Datasource string
	DepName    string
	// ExtractVersion, when set, is a Renovate extractVersion regex (with a named "version" group)
	// stripping what trunk.yaml's pin doesn't carry from the datasource's own versions.
	ExtractVersion string
}

// Comment renders a as the "# renovate: ..." line the regexManagers config in internal/cli
// matches.
func (a Annotation) Comment() string {
	c := "# renovate: datasource=" + a.Datasource + " depName=" + a.DepName
	if a.ExtractVersion != "" {
		c += " extractVersion=" + a.ExtractVersion
	}
	return c
}

// githubOwnerRepoRE extracts a GitHub (owner, repo) pair from a URL that either names a repo
// directly (a plugins.sources[] URI, e.g. "https://github.com/trunk-io/plugins") or points
// somewhere under it (a DownloadEntry URL, e.g.
// "https://github.com/rhysd/actionlint/releases/download/v${version}/...") -- the
// "(?:/.*)?$" tail makes both shapes match with the same pattern.
var githubOwnerRepoRE = regexp.MustCompile(`^https://github\.com/([^/]+)/([^/]+)(?:/.*)?$`)

func extractGitHubOwnerRepo(url string) (owner, repo string, ok bool) {
	m := githubOwnerRepoRE.FindStringSubmatch(url)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// resolveDownloadRecipe resolves cfg.Downloads[downloadName]'s own (owner, repo) pair -- ok only
// if every one of its DownloadEntry.URL values is a GitHub URL and they all agree on the same
// (owner, repo). A recipe with zero entries, an entry that doesn't match, or entries that
// disagree is not confidently GitHub: skip rather than guess which entry is authoritative.
func resolveDownloadRecipe(cfg config.Config, downloadName string) (owner, repo string, ok bool) {
	dl, exists := cfg.Downloads[downloadName]
	if !exists || len(dl.Downloads) == 0 {
		return "", "", false
	}
	for i, entry := range dl.Downloads {
		o, r, matched := extractGitHubOwnerRepo(entry.URL)
		if !matched {
			return "", "", false
		}
		if i == 0 {
			owner, repo = o, r
			continue
		}
		if o != owner || r != repo {
			return "", "", false
		}
	}
	return owner, repo, true
}

// runtimeDatasources is the Renovate datasource (+ extractVersion, when the datasource's versions
// carry something a trunk.yaml pin doesn't) for each runtime type this package can confidently
// annotate. A type absent here means Renovate can't track it (ruby: no datasource).
var runtimeDatasources = map[string]Annotation{
	"go": {
		Datasource: "go",
		// Go module versions are always "v"-prefixed, but a trunk.yaml pin isn't (rtunk's own
		// installer adds the "v" back right before `go install`; see pkg/cache/runtime/go.go).
		ExtractVersion: `^v(?<version>.+)$`,
	},
	"node":   {Datasource: "npm"},
	"python": {Datasource: "pypi"},
	"php":    {Datasource: "packagist"},
	"rust":   {Datasource: "crate"},
}

// resolveToolAnnotation is the Tool-source resolution shared by ForLint's bridged Tool. Tries a
// Download recipe first, then Runtime+Package; neither present (or neither resolvable) is
// ok=false.
func resolveToolAnnotation(cfg config.Config, tool config.Tool) (Annotation, string, bool) {
	if tool.Download != "" {
		owner, repo, ok := resolveDownloadRecipe(cfg, tool.Download)
		if !ok {
			return Annotation{}, "", false
		}
		return Annotation{Datasource: "github-releases", DepName: owner + "/" + repo}, tool.KnownGoodVersion, true
	}
	if tool.Runtime != "" && tool.Package != "" {
		ds, ok := runtimeDatasources[tool.Runtime]
		if !ok {
			return Annotation{}, "", false
		}
		return Annotation{Datasource: ds.Datasource, DepName: tool.ResolvedPackage(tool.KnownGoodVersion), ExtractVersion: ds.ExtractVersion}, tool.KnownGoodVersion, true
	}
	return Annotation{}, "", false
}

// ForLint resolves the Renovate annotation for a lint.enabled entry's bare id (no @version), by
// bridging through the Linter's own Tools[] to the single Tool it references. ok is false --
// annotation omitted, never guessed -- when: the linter id doesn't exist, the linter references
// zero or more than one tool (ambiguous: which tool's version would this even be pinning), or
// that tool's own source doesn't resolve via resolveToolAnnotation.
func ForLint(cfg config.Config, id string) (Annotation, string, bool) {
	linter, exists := cfg.Lint.Definitions[id]
	if !exists || len(linter.Tools) != 1 {
		return Annotation{}, "", false
	}
	tool, exists := cfg.Tools[linter.Tools[0]]
	if !exists {
		return Annotation{}, "", false
	}
	return resolveToolAnnotation(cfg, tool)
}

// ForRuntime is ForLint's runtimes.enabled equivalent. A Runtime carries its own Download recipe
// directly (no Tools[] bridge, and never Runtime+Package -- nothing installs a runtime via
// another runtime), so this only ever tries the Download-recipe path.
func ForRuntime(cfg config.Config, id string) (Annotation, string, bool) {
	rt, exists := cfg.Runtimes.Definitions[id]
	if !exists || rt.Download == "" {
		return Annotation{}, "", false
	}
	owner, repo, ok := resolveDownloadRecipe(cfg, rt.Download)
	if !ok {
		return Annotation{}, "", false
	}
	return Annotation{Datasource: "github-releases", DepName: owner + "/" + repo}, rt.KnownGoodVersion, true
}

// ForPluginSource resolves a plugins.sources[] entry's own git ref. Always datasource
// "github-tags" -- rtunk's plugin sources are always git repos. ok is false for a Local source
// (no upstream ref to track) or a non-GitHub URI (same "don't guess" rule as everywhere else in
// this package -- a GitLab/Bitbucket-hosted plugin source would need its own datasource name,
// not "github-tags").
func ForPluginSource(src config.PluginSource) (Annotation, bool) {
	if src.Local != "" {
		return Annotation{}, false
	}
	owner, repo, ok := extractGitHubOwnerRepo(src.URI)
	if !ok {
		return Annotation{}, false
	}
	return Annotation{Datasource: "github-tags", DepName: owner + "/" + repo}, true
}
