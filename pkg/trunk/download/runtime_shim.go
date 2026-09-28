// Package download's runtime_shim.go implements ROADMAP.md v0.11's "one shared implementation for
// variable substitution and runtime resolution": the runtime-shim-directory resolution logic
// pkg/trunk/engine and pkg/trunk/actions each used to duplicate near-verbatim.
package download

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/xunleii/rtunk/pkg/trunk/config"
)

// ResolveRuntimeShimDir resolves runtimeID's shim directory, fetching it first via Download if not
// already cached (a shim already on disk is treated as fully installed, matching every other
// cache-hit check in this package). onEvent, when non-nil, is called once per event a triggered
// fetch emits -- callers that report progress (pkg/trunk/engine) translate through their own
// mechanism; callers that don't (pkg/trunk/actions) pass nil. Touch only runs once every event has
// been consumed with none of them Failed, so a failed fetch never bumps the mtime a future `cache
// prune` reads.
func ResolveRuntimeShimDir(cfg config.Config, root, cacheDir, repoRoot, runtimeID string, onEvent func(Event)) (string, error) {
	rt, ok := cfg.Runtimes.Definitions[runtimeID]
	if !ok {
		return "", fmt.Errorf("download: runtime %q referenced but not found in resolved config", runtimeID)
	}
	if len(rt.Shims) == 0 {
		return "", fmt.Errorf("download: runtime %q has no shims declared", runtimeID)
	}
	version := ResolveVersion(cfg.Runtimes.Enabled, runtimeID, rt.KnownGoodVersion)
	shimPath := ShimPath(root, "runtimes", runtimeID, version, rt.Shims[0])
	if _, statErr := os.Stat(shimPath); statErr != nil {
		evs, err := Download(cfg, cacheDir, repoRoot, Ref{Category: "runtimes", ID: runtimeID, Version: version})
		if err != nil {
			return "", err
		}
		for ev := range evs {
			if onEvent != nil {
				onEvent(ev)
			}
			if ev.Phase == Failed {
				return "", ev.Err
			}
		}
	}
	Touch(root, "runtimes", runtimeID, version)
	return filepath.Dir(shimPath), nil
}
