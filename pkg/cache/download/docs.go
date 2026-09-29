// Package download implements ROADMAP.md's v0.2 milestone: fetching the tool/runtime binaries a
// resolved pkg/trunk/config.Config references, hermetically and reproducibly, into a
// content-addressed local cache. See
// docs/superpowers/specs/2026-09-10-v0.2-download-design.md for the full design.
//
// lock.go implements ROADMAP.md v0.11's "concurrent installs fail fast, by name": a
// per-install-item file lock so a second rtunk process racing to install the same item never
// waits -- it either proceeds immediately (the recorded holder has crashed) or fails immediately,
// naming who currently holds it.
//
// registry.go implements ROADMAP.md v0.11's usage-tracking cache prune: one file per repository,
// overwritten wholesale on every RecordUsage call, recording exactly what that repository's
// just-resolved config.Config currently needs. prune.go's Prune, added alongside it in the same
// milestone, reads every such file back to decide what's still in use: it drops the entries whose
// repository no longer exists, then removes every install, shim, and plugin-source cache entry no
// still-existing repository's current entry references.
//
// runtime_shim.go implements ROADMAP.md v0.11's "one shared implementation for variable
// substitution and runtime resolution": the runtime-shim-directory resolution logic pkg/run/engine
// and pkg/run/actions each used to duplicate near-verbatim.
package download
