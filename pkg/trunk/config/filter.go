package config

import "strings"

// filterEnabled trims cfg's category definitions, tools, and downloads down to what trunk.yaml
// actually turned on plus whatever those enabled definitions need transitively — the effective
// configuration rtunk would act on — rather than every definition a plugin source happens to
// contribute (a source like github.com/trunk-io/plugins defines hundreds of linters no given repo
// uses). Runs on the fully merged cfg, after duplicate detection, so an unused definition can
// still be caught as a duplicate; it just won't survive into the returned Config.
//
// "Used by the enabled ones" follows the reference graph one hop at a time: an enabled linter's
// tools:/files:/commands[].parser.runtime, then a kept tool's runtime:/download:, an enabled
// action's runtime:, and a kept runtime's download:. A kept FileType's own inherit: chain is
// followed to a fixed point (unlike the rest of the graph, it can be more than one hop deep —
// e.g. bazel -> bazel-build -> ...). There's no cycle to worry about elsewhere (a Tool/Runtime
// never references a Linter/Action back), so a single pass is enough there.
func filterEnabled(cfg *Config) {
	keepLint := filterMap(cfg.Lint.Definitions, enabledIDs(cfg.Lint.Enabled))
	keepActions := filterMap(cfg.Actions.Definitions, enabledIDs(cfg.Actions.Enabled))

	toolIDs := map[string]struct{}{}
	fileIDs := map[string]struct{}{}
	for _, l := range keepLint {
		for _, t := range l.Tools {
			toolIDs[t] = struct{}{}
		}
		for _, f := range l.Files {
			fileIDs[f] = struct{}{}
		}
	}
	keepTools := filterMap(cfg.Tools, toolIDs)
	keepFiles := filterMapTransitive(cfg.Lint.Files, fileIDs, func(f FileType) []string { return f.Inherit })

	runtimeIDs := enabledIDs(cfg.Runtimes.Enabled)
	for _, l := range keepLint {
		for _, cmd := range l.Commands {
			if cmd.Parser != nil && cmd.Parser.Runtime != "" {
				runtimeIDs[cmd.Parser.Runtime] = struct{}{}
			}
		}
	}
	for _, t := range keepTools {
		if t.Runtime != "" {
			runtimeIDs[t.Runtime] = struct{}{}
		}
	}
	for _, a := range keepActions {
		if a.Runtime != "" {
			runtimeIDs[a.Runtime] = struct{}{}
		}
	}
	keepRuntimes := filterMap(cfg.Runtimes.Definitions, runtimeIDs)

	downloadIDs := map[string]struct{}{}
	for _, t := range keepTools {
		if t.Download != "" {
			downloadIDs[t.Download] = struct{}{}
		}
	}
	for _, r := range keepRuntimes {
		if r.Download != "" {
			downloadIDs[r.Download] = struct{}{}
		}
	}
	keepDownloads := filterMap(cfg.Downloads, downloadIDs)

	cfg.Lint.Definitions = keepLint
	cfg.Actions.Definitions = keepActions
	cfg.Tools = keepTools
	cfg.Lint.Files = keepFiles
	cfg.Runtimes.Definitions = keepRuntimes
	cfg.Downloads = keepDownloads
}

// filterMap returns the subset of m whose keys are in keep. A key in keep with no matching entry
// in m (a dangling reference) is simply absent from the result — Validate is what reports that,
// not this filter.
func filterMap[T any](m map[string]T, keep map[string]struct{}) map[string]T {
	out := make(map[string]T, len(keep))
	for k, v := range m {
		if _, ok := keep[k]; ok {
			out[k] = v
		}
	}
	return out
}

// filterMapTransitive is filterMap, but first grows keep to a fixed point by following refs(v)
// for every entry it pulls in — e.g. FileType.Inherit, which can chain more than one hop deep.
func filterMapTransitive[T any](m map[string]T, keep map[string]struct{}, refs func(T) []string) map[string]T {
	queue := make([]string, 0, len(keep))
	for k := range keep {
		queue = append(queue, k)
	}
	for len(queue) > 0 {
		k := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		v, ok := m[k]
		if !ok {
			continue // dangling reference; Validate reports it, not this filter
		}
		for _, ref := range refs(v) {
			if _, seen := keep[ref]; !seen {
				keep[ref] = struct{}{}
				queue = append(queue, ref)
			}
		}
	}
	return filterMap(m, keep)
}

// enabledIDs turns a trunk.yaml enabled: list (each entry optionally pinned as `id@version`) into
// a bare-id set, matching how Validate's checkEnabled reads the same lists.
func enabledIDs(enabled []string) map[string]struct{} {
	out := make(map[string]struct{}, len(enabled))
	for _, e := range enabled {
		id, _, _ := strings.Cut(e, "@")
		out[id] = struct{}{}
	}
	return out
}
