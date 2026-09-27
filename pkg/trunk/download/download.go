package download

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
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
func Download(cfg config.Config, cacheDir, repoRoot string, refs ...Ref) (<-chan Event, error) {
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
				fetchOne(cfg, root, repoRoot, ref, events)
			}(ref)
		}
		wg.Wait()
	}()
	return events, nil
}

// runtimeLocks serializes concurrent installs of one runtime directory (see fetchRuntimeRef).
var runtimeLocks sync.Map // installDir -> *sync.Mutex

// Pending returns the refs a Download of refs would really fetch: those not installed yet (with
// their version resolved) plus the not-installed runtimes their packages need, deduplicated. The
// engine uses it to announce the number of installs before starting them.
func Pending(cfg config.Config, root string, refs ...Ref) []Ref {
	var out []Ref
	seen := map[Ref]bool{}
	add := func(category, id, version, dir string) {
		r := Ref{Category: category, ID: id, Version: version}
		if !seen[r] && !dirNonEmpty(dir) {
			seen[r] = true
			out = append(out, r)
		}
	}
	for _, ref := range refs {
		switch ref.Category {
		case "tools":
			tool, ok := cfg.Tools[ref.ID]
			if !ok {
				continue
			}
			version := cmp.Or(ref.Version, ResolveVersion(cfg.Lint.Enabled, ref.ID, tool.KnownGoodVersion))
			if dirNonEmpty(InstallDir(root, "tools", ref.ID, version)) {
				continue
			}
			if rt, ok := cfg.Runtimes.Definitions[tool.Runtime]; ok && tool.Download == "" && rt.SystemVersion == "" {
				rv := ResolveVersion(cfg.Runtimes.Enabled, tool.Runtime, rt.KnownGoodVersion)
				add("runtimes", tool.Runtime, rv, InstallDir(root, "runtimes", tool.Runtime, rv))
			}
			add("tools", ref.ID, version, InstallDir(root, "tools", ref.ID, version))
		case "runtimes":
			rt, ok := cfg.Runtimes.Definitions[ref.ID]
			if !ok || rt.SystemVersion != "" {
				continue
			}
			version := cmp.Or(ref.Version, ResolveVersion(cfg.Runtimes.Enabled, ref.ID, rt.KnownGoodVersion))
			add("runtimes", ref.ID, version, InstallDir(root, "runtimes", ref.ID, version))
		}
	}
	return out
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

func fetchOne(cfg config.Config, root, repoRoot string, ref Ref, events chan<- Event) {
	switch ref.Category {
	case "runtimes":
		_ = fetchRuntimeRef(cfg, root, repoRoot, ref, events) // failure already emitted as a Failed event
	case "tools":
		fetchToolRef(cfg, root, repoRoot, ref, events)
	case "lint":
		fetchLintRef(cfg, root, repoRoot, ref, events)
	case "actions":
		fetchActionRef(cfg, root, repoRoot, ref, events)
	case "action-packages":
		fetchActionPackagesRef(cfg, root, repoRoot, ref, events)
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
func fetchRuntimeRef(cfg config.Config, root, repoRoot string, ref Ref, events chan<- Event) error {
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
	// Several tools fetched in parallel can need the same runtime: the first installs it, the
	// others wait here and then find it Cached.
	mu, _ := runtimeLocks.LoadOrStore(installDir, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()
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
	release, err := claimInstall(ref, installDir, repoRoot, events)
	if release == nil {
		return err
	}
	defer release()
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
func fetchToolRef(cfg config.Config, root, repoRoot string, ref Ref, events chan<- Event) {
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
		release, _ := claimInstall(ref, installDir, repoRoot, events)
		if release == nil {
			return
		}
		defer release()
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
		if err := fetchRuntimeRef(cfg, root, repoRoot, Ref{Category: "runtimes", ID: tool.Runtime, Version: runtimeVersion}, events); err != nil {
			events <- Event{Ref: ref, Phase: Failed, Err: fmt.Errorf("download: tool %q: runtime %q: %w", ref.ID, tool.Runtime, err)}
			return
		}
	}

	release, _ := claimInstall(ref, installDir, repoRoot, events)
	if release == nil {
		return
	}
	defer release()
	events <- Event{Ref: ref, Phase: Started}
	if err := InstallPackage(rt, runtimeInstallDir, installDir, tool.Package, version); err != nil {
		events <- Event{Ref: ref, Phase: Failed, Err: err}
		return
	}
	// ${home} (the go runtime's HOME) is a scratch home inside rtunk's own cache: left unresolved it
	// ended up as the literal relative path "${home}", and every go tool then wrote its caches and
	// telemetry to ./${home}/Library/... in whatever repo it ran from.
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o750); err != nil {
		events <- Event{Ref: ref, Phase: Failed, Err: err}
		return
	}
	env := BuildEnv(append(append([]config.EnvironmentEntry{}, rt.LinterEnvironment...), rt.RuntimeEnvironment...),
		map[string]string{"runtime": runtimeInstallDir, "linter": installDir, "home": home})
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
	// The blob's only reader was InstallDownload, just above; keeping it around after a
	// successful install only grows the cache forever for content nothing reads again. Best-
	// effort: a failure to remove is not worth failing the install over. A concurrent installer of
	// this same ref racing this unlink is exactly what claimInstall's per-install-item lock (below)
	// serializes against -- it is not safe on its own.
	_ = os.Remove(blobPath)
	return nil
}

// claimInstall takes installDir's cross-process install lock (see acquireInstallLock) for ref. A
// nil release means ref already got its terminal event and the caller must return err as is:
// Failed (err set) when another process holds the lock, or Cached (err nil) when another process
// finished installing it between the caller's own dirNonEmpty check and this claim. Otherwise the
// caller installs, then calls release.
func claimInstall(ref Ref, installDir, repoRoot string, events chan<- Event) (release func(), err error) {
	release, err = acquireInstallLock(installDir, repoRoot)
	if err != nil {
		events <- Event{Ref: ref, Phase: Failed, Err: err}
		return nil, err
	}
	if dirNonEmpty(installDir) {
		release()
		events <- Event{Ref: ref, Phase: Cached}
		return nil, nil
	}
	return release, nil
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
func fetchLintRef(cfg config.Config, root, repoRoot string, ref Ref, events chan<- Event) {
	l, ok := cfg.Lint.Definitions[ref.ID]
	if !ok {
		events <- Event{Ref: ref, Phase: Failed, Err: fmt.Errorf("download: unknown lint definition %q", ref.ID)}
		return
	}
	for _, toolID := range l.Tools {
		fetchToolRef(cfg, root, repoRoot, Ref{Category: "tools", ID: toolID}, events)
	}
}

// fetchActionRef expands an "actions" ref into its runtime, if it names one (some actions, like
// go-mod-tidy, shell out directly with no runtime: field -- ARCHITECTURE.md "actions:").
func fetchActionRef(cfg config.Config, root, repoRoot string, ref Ref, events chan<- Event) {
	a, ok := cfg.Actions.Definitions[ref.ID]
	if !ok {
		events <- Event{Ref: ref, Phase: Failed, Err: fmt.Errorf("download: unknown action %q", ref.ID)}
		return
	}
	if a.Runtime == "" {
		events <- Event{Ref: ref, Phase: Cached}
		return
	}
	_ = fetchRuntimeRef(cfg, root, repoRoot, Ref{Category: "runtimes", ID: a.Runtime}, events) // failure already emitted as a Failed event
}

// actionPackagesFilePath resolves action.PackagesFile against its SourceRoot/SourceDir -- the same
// join pkg/trunk/actions.Run's own cwd resolution already does, duplicated here (not imported: this
// package cannot depend on pkg/trunk/actions) so fetchActionPackagesRef and resolvedRefs read the
// exact same manifest path.
func actionPackagesFilePath(action config.Action) string {
	if action.SourceRoot == "" {
		return action.PackagesFile
	}
	return filepath.Join(action.SourceRoot, action.SourceDir, action.PackagesFile)
}

// actionPackagesHash reads action's packages_file manifest and returns its content's sha256 hex --
// the identity fetchActionPackagesRef installs by (InstallDir(root, "action-packages", hash,
// "manifest")). resolvedRefs/Prune must key their keep-set on this same hash, not on the action's
// own ID: two actions sharing one byte-identical manifest share one on-disk install, so the action
// ID alone would either double-count or (worse) never match what's actually on disk.
func actionPackagesHash(action config.Action) (string, error) {
	data, err := os.ReadFile(actionPackagesFilePath(action))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// ActionPackagesBinDir returns actionID's packages_file install's node_modules/.bin dir, for a
// caller (pkg/trunk/actions.Run) that has just confirmed via Download that the install exists.
// Recomputes the same content-hash path fetchActionPackagesRef used rather than threading it back
// out of the Event stream, matching how every other resolve*Dir function in this codebase
// recomputes its own path rather than parsing it out of an Event.
func ActionPackagesBinDir(cfg config.Config, root, actionID string) (string, error) {
	action, ok := cfg.Actions.Definitions[actionID]
	if !ok {
		return "", fmt.Errorf("download: unknown action %q", actionID)
	}
	hash, err := actionPackagesHash(action)
	if err != nil {
		return "", err
	}
	installDir := InstallDir(root, "action-packages", hash, "manifest")
	return filepath.Join(installDir, "node_modules", ".bin"), nil
}

// fetchActionPackagesRef installs ref.ID's (an action id) packages_file manifest, deduped by the
// manifest's own content hash so two actions sharing one manifest install once -- mirrors
// fetchToolRef's runtime+package branch, just keyed by content hash instead of a declared version.
func fetchActionPackagesRef(cfg config.Config, root, repoRoot string, ref Ref, events chan<- Event) {
	action, ok := cfg.Actions.Definitions[ref.ID]
	if !ok || action.PackagesFile == "" {
		events <- Event{Ref: ref, Phase: Failed, Err: fmt.Errorf("download: action %q has no packages_file", ref.ID)}
		return
	}
	rt, ok := cfg.Runtimes.Definitions[action.Runtime]
	if !ok {
		events <- Event{Ref: ref, Phase: Failed, Err: fmt.Errorf("download: action %q: no runtime %q", ref.ID, action.Runtime)}
		return
	}
	runtimeVersion := ResolveVersion(cfg.Runtimes.Enabled, action.Runtime, rt.KnownGoodVersion)
	runtimeInstallDir := InstallDir(root, "runtimes", action.Runtime, runtimeVersion)
	if !dirNonEmpty(runtimeInstallDir) {
		if err := fetchRuntimeRef(cfg, root, repoRoot, Ref{Category: "runtimes", ID: action.Runtime, Version: runtimeVersion}, events); err != nil {
			events <- Event{Ref: ref, Phase: Failed, Err: fmt.Errorf("download: action %q: runtime %q: %w", ref.ID, action.Runtime, err)}
			return
		}
	}

	hash, err := actionPackagesHash(action)
	if err != nil {
		events <- Event{Ref: ref, Phase: Failed, Err: err}
		return
	}
	installDir := InstallDir(root, "action-packages", hash, "manifest")
	if dirNonEmpty(installDir) {
		events <- Event{Ref: ref, Phase: Cached}
		return
	}

	release, _ := claimInstall(ref, installDir, repoRoot, events)
	if release == nil {
		return
	}
	defer release()
	events <- Event{Ref: ref, Phase: Started}
	if err := InstallPackagesFile(rt, runtimeInstallDir, installDir, actionPackagesFilePath(action)); err != nil {
		events <- Event{Ref: ref, Phase: Failed, Err: err}
		return
	}
	events <- Event{Ref: ref, Phase: Done}
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
