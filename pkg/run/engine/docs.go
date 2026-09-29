// Package engine implements the check/fmt-shared job-queue execution engine: matching files
// against a linter's Files criteria, resolving RunFrom/SandboxType (see engine/security), and
// running commands against the result. See docs/superpowers/specs/
// 2026-09-12-check-engine-refactor-design.md for the full design.
package engine
