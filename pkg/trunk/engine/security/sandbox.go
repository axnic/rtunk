package security

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/xunleii/rtunk/pkg/trunk/output"
)

// StageSandbox copies files from dir into a fresh temporary directory, mirroring dir's own
// relative layout, so a Finding's path can be mapped back through dir to repoRoot afterward (see
// RemapFindings). sandboxType selects which files: "copy_targets" copies exactly targets (each
// already relative to dir); "expanded" copies every regular file directly inside dir
// (non-recursive), which naturally includes targets since they live in dir -- an inferred
// best-effort interpretation, see the design spec: real trunk-io usage (gokart, tflint) needs
// sibling-file context a single isolated file wouldn't provide.
//
// cleanup is always non-nil once sandboxDir was created, even when err != nil (e.g. a copy
// failed partway) -- callers must always defer cleanup() immediately after a non-nil sandboxDir
// is returned, to avoid leaking the temp directory.
//
// tmpBase controls where the sandbox's own temp directory is created: "" uses the OS default
// (os.MkdirTemp's own existing behavior, unchanged for check-time sandboxing), a non-empty path
// creates it as a subdirectory of that path instead. A dry-run InPlace sandbox (see engine.go's
// own runBatch) passes repoRoot here: staging inside the real repo tree means a genuine ancestor
// directory walk from within the sandbox (e.g. real prettier's own config-resolution algorithm)
// still passes through repoRoot itself and finds any real config file living there, rather than
// hitting an isolated /tmp directory with no path back to the repo at all.
func StageSandbox(sandboxType, dir string, targets []string, tmpBase string) (sandboxDir string, cleanup func(), err error) {
	sandboxDir, err = os.MkdirTemp(tmpBase, "rtunk-check-sandbox-*")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { os.RemoveAll(sandboxDir) }

	var relFiles []string
	switch sandboxType {
	case "expanded":
		entries, err := os.ReadDir(dir)
		if err != nil {
			return sandboxDir, cleanup, err
		}
		seen := map[string]bool{}
		for _, e := range entries {
			if e.Type().IsRegular() {
				relFiles = append(relFiles, e.Name())
				seen[e.Name()] = true
			}
		}
		// dir's own top-level entries don't cover a target that lives in a nested directory
		// relative to dir (e.g. a RunFrom of "${parent}"/"${root_or_parent_with*}" resolving to
		// an ancestor of the actual file) -- without this, "expanded" would silently stage
		// everything except the one file the invocation is actually meant to check.
		for _, t := range targets {
			if !seen[t] {
				relFiles = append(relFiles, t)
			}
		}
	case "copy_targets":
		relFiles = targets
	}

	for _, rel := range relFiles {
		if err := copySandboxFile(sandboxDir, filepath.Join(dir, rel), filepath.Join(sandboxDir, rel)); err != nil {
			return sandboxDir, cleanup, err
		}
	}
	return sandboxDir, cleanup, nil
}

// copySandboxFile copies src to dst, creating dst's parent directories as needed, preserving
// src's file mode. Refuses to write outside sandboxDir even if dst was computed from a rel that
// somehow escaped it (see StageSandbox's callers -- Files() is expected to reject an out-of-repo
// path before it ever reaches here, but this function does not trust that upstream check alone: a
// path-traversal write here would be a real security bug, not a cosmetic one, so it is guarded
// independently).
func copySandboxFile(sandboxDir, src, dst string) error {
	relCheck, err := filepath.Rel(sandboxDir, dst)
	if err != nil || relCheck == ".." || strings.HasPrefix(relCheck, ".."+string(filepath.Separator)) || filepath.IsAbs(relCheck) {
		return fmt.Errorf("security: refusing to stage %q outside sandbox %q", dst, sandboxDir)
	}

	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

// RemapFindings rewrites every finding's File from a path relative to base (the resolved RunFrom
// directory a command actually ran against -- real or, when sandboxed, mirrored) back to a path
// relative to repoRoot, the report's established contract. A no-op whenever base == repoRoot
// (every command that doesn't use RunFrom/SandboxType today), so it is safe to call
// unconditionally for every job.
func RemapFindings(findings []output.Finding, base, repoRoot string) {
	if base == repoRoot {
		return
	}
	for i, f := range findings {
		abs := f.File
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(base, f.File)
		}
		if rel, err := filepath.Rel(repoRoot, abs); err == nil {
			findings[i].File = rel
		}
	}
}
