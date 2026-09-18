package download

import (
	"fmt"
	"os"
	"runtime"
	"sync"

	"github.com/xunleii/rtunk/pkg/trunk/config"
)

// Ref identifies one item to fetch. Version "" defers to whatever the enabled list (or the
// definition's KnownGoodVersion) pins -- see ResolveVersion.
type Ref struct {
	Category string // "tools" | "runtimes" | "lint" | "actions" | "plugins"
	ID       string
	Version  string
}

// Phase is one Event's point in an item's fetch lifecycle.
type Phase int

// The phases a Ref moves through, in the order a successful fetch reports them.
const (
	Started  Phase = iota // fetch beginning for this ref
	Progress              // bytes received so far (HTTP fetches only; npm installs skip straight to Done)
	Cached                // already present locally (or a system_version runtime); nothing fetched
	Done                  // fetch + extract/install + shim complete
	Failed                // Err is set
)

// Event reports one Ref's fetch progress, streamed on the channel Download returns.
type Event struct {
	Ref          Ref
	Phase        Phase
	Bytes, Total int64 // Progress only; Total -1 if the server didn't send Content-Length
	Err          error // Failed only
}

// maxParallel bounds concurrent fetches, so a large `rtunk download` doesn't hammer upstream
// registries with one goroutine per item.
//
// ponytail: fixed constant for now; add a --parallel flag if someone actually needs to tune it.
const maxParallel = 4

// Download fetches refs into cacheDir. An empty refs list means every tool and runtime currently
// in cfg (cfg is expected to already be enabled+used-trimmed, i.e. the result of
// pkg/trunk/config.Resolve) -- what bare `rtunk download` runs. It returns immediately with a
// channel of lifecycle events, closed once every ref reaches Cached/Done/Failed; a non-nil error
// return is a setup failure only (e.g. an unwritable cacheDir), never a per-item failure -- those
// are Failed events on the channel.
func Download(cfg config.Config, cacheDir string, refs ...Ref) (<-chan Event, error) {
	root, err := Root(cacheDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, err
	}

	if len(refs) == 0 {
		refs = allRefs(cfg)
	}

	events := make(chan Event)
	go func() {
		defer close(events)
		sem := make(chan struct{}, maxParallel)
		var wg sync.WaitGroup
		for _, ref := range refs {
			wg.Add(1)
			sem <- struct{}{}
			go func(ref Ref) {
				defer wg.Done()
				defer func() { <-sem }()
				fetchOne(cfg, root, ref, events)
			}(ref)
		}
		wg.Wait()
	}()
	return events, nil
}

// allRefs is every enabled+used tool and runtime in cfg -- what a bare `rtunk download` fetches.
// lint/actions/plugins refs (Task 10) are for targeted `rtunk download <category> <id>` use only:
// their underlying tools/runtimes are already covered here.
func allRefs(cfg config.Config) []Ref {
	refs := make([]Ref, 0, len(cfg.Tools)+len(cfg.Runtimes.Definitions))
	for id := range cfg.Tools {
		refs = append(refs, Ref{Category: "tools", ID: id})
	}
	for id := range cfg.Runtimes.Definitions {
		refs = append(refs, Ref{Category: "runtimes", ID: id})
	}
	return refs
}

func fetchOne(cfg config.Config, root string, ref Ref, events chan<- Event) {
	switch ref.Category {
	case "runtimes":
		_ = fetchRuntimeRef(cfg, root, ref, events) // failure already emitted as a Failed event
	case "tools":
		fetchToolRef(cfg, root, ref, events)
	case "lint":
		fetchLintRef(cfg, root, ref, events)
	case "actions":
		fetchActionRef(cfg, root, ref, events)
	case "plugins":
		fetchPluginRef(cfg, ref, events)
	default:
		events <- Event{Ref: ref, Phase: Failed, Err: fmt.Errorf("download: unknown category %q", ref.Category)}
	}
}

// fetchRuntimeRef fetches a "runtimes" ref: a system_version runtime is always Cached (never
// downloaded, per the spec's "Fetch mechanisms"); otherwise its download: recipe is fetched and
// extracted like any other download, and its declared Shims are written. It returns a non-nil
// error whenever it emitted a Failed event, so fetchToolRef's runtime+package branch can bail out
// on a real runtime failure instead of proceeding to InstallPackage with no runtime on disk (see
// Fix 4: that used to surface a confusing "npm not found" instead of the real cause).
func fetchRuntimeRef(cfg config.Config, root string, ref Ref, events chan<- Event) error {
	rt, ok := cfg.Runtimes.Definitions[ref.ID]
	if !ok {
		err := fmt.Errorf("download: unknown runtime %q", ref.ID)
		events <- Event{Ref: ref, Phase: Failed, Err: err}
		return err
	}
	if rt.SystemVersion != "" {
		events <- Event{Ref: ref, Phase: Cached}
		return nil
	}

	version := ref.Version
	if version == "" {
		version = ResolveVersion(cfg.Runtimes.Enabled, ref.ID, rt.KnownGoodVersion)
	}

	installDir := InstallDir(root, "runtimes", ref.ID, version)
	if dirNonEmpty(installDir) {
		events <- Event{Ref: ref, Phase: Cached}
		return nil
	}

	dl, ok := cfg.Downloads[rt.Download]
	if !ok {
		err := fmt.Errorf("download: runtime %q: no download recipe %q", ref.ID, rt.Download)
		events <- Event{Ref: ref, Phase: Failed, Err: err}
		return err
	}
	if err := fetchDownload(root, ref, dl, version, installDir, events); err != nil {
		return err // fetchDownload already emitted the Failed event
	}
	for _, name := range rt.Shims {
		target, err := FindShimTarget(installDir, name)
		if err != nil {
			events <- Event{Ref: ref, Phase: Failed, Err: err}
			return err
		}
		if err := WriteShim(ShimPath(root, "runtimes", ref.ID, version, name), target); err != nil {
			events <- Event{Ref: ref, Phase: Failed, Err: err}
			return err
		}
	}
	events <- Event{Ref: ref, Phase: Done}
	return nil
}

// fetchToolRef fetches a "tools" ref: a download-recipe tool follows the same path as a runtime;
// a runtime+package tool ensures its runtime is fetched first, then installs the package through
// it (Task 8), writing an environment-injecting shim (Task 5/6) instead of a plain one.
func fetchToolRef(cfg config.Config, root string, ref Ref, events chan<- Event) {
	tool, ok := cfg.Tools[ref.ID]
	if !ok {
		events <- Event{Ref: ref, Phase: Failed, Err: fmt.Errorf("download: unknown tool %q", ref.ID)}
		return
	}

	version := ref.Version
	if version == "" {
		version = ResolveVersion(cfg.Lint.Enabled, ref.ID, tool.KnownGoodVersion)
	}
	installDir := InstallDir(root, "tools", ref.ID, version)
	if dirNonEmpty(installDir) {
		events <- Event{Ref: ref, Phase: Cached}
		return
	}

	if tool.Download != "" {
		dl, ok := cfg.Downloads[tool.Download]
		if !ok {
			events <- Event{Ref: ref, Phase: Failed, Err: fmt.Errorf("download: tool %q: no download recipe %q", ref.ID, tool.Download)}
			return
		}
		if err := fetchDownload(root, ref, dl, version, installDir, events); err != nil {
			return
		}
		for _, name := range tool.Shims {
			target, err := FindShimTarget(installDir, name)
			if err != nil {
				events <- Event{Ref: ref, Phase: Failed, Err: err}
				return
			}
			if err := WriteShim(ShimPath(root, "tools", ref.ID, version, name), target); err != nil {
				events <- Event{Ref: ref, Phase: Failed, Err: err}
				return
			}
		}
		events <- Event{Ref: ref, Phase: Done}
		return
	}

	// runtime + package
	rt, ok := cfg.Runtimes.Definitions[tool.Runtime]
	if !ok {
		events <- Event{Ref: ref, Phase: Failed, Err: fmt.Errorf("download: tool %q: no runtime %q", ref.ID, tool.Runtime)}
		return
	}
	runtimeVersion := ResolveVersion(cfg.Runtimes.Enabled, tool.Runtime, rt.KnownGoodVersion)
	runtimeInstallDir := InstallDir(root, "runtimes", tool.Runtime, runtimeVersion)
	if !dirNonEmpty(runtimeInstallDir) {
		if err := fetchRuntimeRef(cfg, root, Ref{Category: "runtimes", ID: tool.Runtime, Version: runtimeVersion}, events); err != nil {
			events <- Event{Ref: ref, Phase: Failed, Err: fmt.Errorf("download: tool %q: runtime %q: %w", ref.ID, tool.Runtime, err)}
			return
		}
	}

	events <- Event{Ref: ref, Phase: Started}
	if err := InstallPackage(rt, runtimeInstallDir, installDir, tool.Package, version); err != nil {
		events <- Event{Ref: ref, Phase: Failed, Err: err}
		return
	}
	env := BuildEnv(append(append([]config.EnvironmentEntry{}, rt.LinterEnvironment...), rt.RuntimeEnvironment...),
		map[string]string{"runtime": runtimeInstallDir, "linter": installDir})
	extraEnv, err := ExtraToolEnv(rt, installDir)
	if err != nil {
		events <- Event{Ref: ref, Phase: Failed, Err: err}
		return
	}
	env = append(env, extraEnv...)
	for _, name := range tool.Shims {
		target, err := FindShimTarget(installDir, name)
		if err != nil {
			events <- Event{Ref: ref, Phase: Failed, Err: err}
			return
		}
		if err := WriteEnvShim(ShimPath(root, "tools", ref.ID, version, name), target, env); err != nil {
			events <- Event{Ref: ref, Phase: Failed, Err: err}
			return
		}
	}
	events <- Event{Ref: ref, Phase: Done}
}

// fetchDownload is fetchRuntimeRef/fetchToolRef's shared "download: recipe" path: match the
// current platform's entry, fetch its blob, extract it into installDir. Emits Started/Progress
// itself; the caller emits Done (after writing shims) or Failed on a returned error.
func fetchDownload(root string, ref Ref, dl config.Download, version, installDir string, events chan<- Event) error {
	events <- Event{Ref: ref, Phase: Started}
	entry, osVal, cpuVal, ok := MatchEntry(dl.Downloads, runtime.GOOS, runtime.GOARCH, version)
	if !ok {
		err := fmt.Errorf("download: %s %q: no download entry for %s/%s", ref.Category, ref.ID, runtime.GOOS, runtime.GOARCH)
		events <- Event{Ref: ref, Phase: Failed, Err: err}
		return err
	}
	extraArgs, err := ResolveArgs(dl.Args, version, osVal, cpuVal)
	if err != nil {
		events <- Event{Ref: ref, Phase: Failed, Err: err}
		return err
	}
	url := TemplateURL(entry.URL, version, osVal, cpuVal, extraArgs)
	blobPath, err := FetchBlob(root, url, func(n, total int64) {
		events <- Event{Ref: ref, Phase: Progress, Bytes: n, Total: total}
	})
	if err != nil {
		events <- Event{Ref: ref, Phase: Failed, Err: err}
		return err
	}
	if err := InstallDownload(blobPath, url, installDir, entry, dl.Name); err != nil {
		events <- Event{Ref: ref, Phase: Failed, Err: err}
		return err
	}
	return nil
}

// dirNonEmpty reports whether path already exists as a directory -- how fetchOne skips straight
// to Cached instead of re-fetching an already-installed version. Existence, not contents, is the
// signal: InstallDir's own directory is only ever created by a completed InstallDownload/
// InstallPackage call (or, in tests, to simulate one), never left dangling empty.
func dirNonEmpty(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// fetchLintRef expands a "lint" ref into its underlying tool(s) (Linter.Tools) -- a linter has no
// binary of its own to fetch. Events land against Ref{Category: "tools", ...} for each, not this
// wrapper ref.
func fetchLintRef(cfg config.Config, root string, ref Ref, events chan<- Event) {
	l, ok := cfg.Lint.Definitions[ref.ID]
	if !ok {
		events <- Event{Ref: ref, Phase: Failed, Err: fmt.Errorf("download: unknown lint definition %q", ref.ID)}
		return
	}
	for _, toolID := range l.Tools {
		fetchToolRef(cfg, root, Ref{Category: "tools", ID: toolID}, events)
	}
}

// fetchActionRef expands an "actions" ref into its runtime, if it names one (some actions, like
// go-mod-tidy, shell out directly with no runtime: field -- ARCHITECTURE.md "actions:").
func fetchActionRef(cfg config.Config, root string, ref Ref, events chan<- Event) {
	a, ok := cfg.Actions.Definitions[ref.ID]
	if !ok {
		events <- Event{Ref: ref, Phase: Failed, Err: fmt.Errorf("download: unknown action %q", ref.ID)}
		return
	}
	if a.Runtime == "" {
		events <- Event{Ref: ref, Phase: Cached}
		return
	}
	_ = fetchRuntimeRef(cfg, root, Ref{Category: "runtimes", ID: a.Runtime}, events) // failure already emitted as a Failed event
}

// fetchPluginRef reports a "plugins" ref as always Cached: by the time cfg exists, resolving it
// (pkg/trunk/config.Resolve) already fetched every plugin source it references (git.go's own
// cache) -- there is no separate fetch step left for Download to do.
func fetchPluginRef(cfg config.Config, ref Ref, events chan<- Event) {
	if _, ok := cfg.Plugins.Sources[ref.ID]; !ok {
		events <- Event{Ref: ref, Phase: Failed, Err: fmt.Errorf("download: unknown plugin source %q", ref.ID)}
		return
	}
	events <- Event{Ref: ref, Phase: Cached}
}
