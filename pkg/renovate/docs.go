// Package renovate resolves, for a trunk.yaml/rtunk.yaml version-pinned entry, whether rtunk can
// confidently name the Renovate regex-manager datasource/depName that lets Renovate track and
// bump it on its own -- rtunk never checks an upstream itself (see this package's own design
// spec, docs/superpowers/specs/2026-09-17-renovate-annotations-design.md, "Why not build this
// ourselves"). Every function here either resolves confidently or reports ok=false; none of them
// guess.
package renovate
