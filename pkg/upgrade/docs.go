// Package upgrade checks rtunk's own GitHub Releases for a newer version and, if the user asks,
// downloads and installs it over the currently-running binary. Never touches linters/tools/
// runtimes/plugins -- those are reproducibly pinned via trunk.yaml's own enabled: lists (AGENTS.md
// "Reproducibility"), not something an "upgrade" command silently bumps.
package upgrade
