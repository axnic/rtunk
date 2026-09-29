// Package actions runs trunk-style Actions (ARCHITECTURE.md `actions:`): single Run invocations
// with git-hook/manual-CLI context, not file-matched batch jobs -- deliberately a separate package
// from pkg/run/engine (the lint/fmt job-queue engine), since the two execution models share
// little beyond "resolve a runtime shim, exec a shell command".
package actions
