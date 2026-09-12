package check

import (
	"io"
	"os"
	"path/filepath"
)

// stageSandbox copies files from dir into a fresh temporary directory, mirroring dir's own
// relative layout, so a Finding's path can be mapped back through dir to repoRoot afterward (see
// remapFindings). sandboxType selects which files: "copy_targets" copies exactly targets (each
// already relative to dir); "expanded" copies every regular file directly inside dir
// (non-recursive), which naturally includes targets since they live in dir -- an inferred
// best-effort interpretation, see the design spec: real trunk-io usage (gokart, tflint) needs
// sibling-file context a single isolated file wouldn't provide.
//
// cleanup is always non-nil once sandboxDir was created, even when err != nil (e.g. a copy
// failed partway) -- callers must always defer cleanup() immediately after a non-nil sandboxDir
// is returned, to avoid leaking the temp directory.
func stageSandbox(sandboxType, dir string, targets []string) (sandboxDir string, cleanup func(), err error) {
	sandboxDir, err = os.MkdirTemp("", "rtunk-check-sandbox-*")
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
		for _, e := range entries {
			if e.Type().IsRegular() {
				relFiles = append(relFiles, e.Name())
			}
		}
	case "copy_targets":
		relFiles = targets
	}

	for _, rel := range relFiles {
		if err := copySandboxFile(filepath.Join(dir, rel), filepath.Join(sandboxDir, rel)); err != nil {
			return sandboxDir, cleanup, err
		}
	}
	return sandboxDir, cleanup, nil
}

// copySandboxFile copies src to dst, creating dst's parent directories as needed, preserving
// src's file mode.
func copySandboxFile(src, dst string) error {
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

// remapFindings rewrites every finding's File from a path relative to base (the resolved RunFrom
// directory a command actually ran against -- real or, when sandboxed, mirrored) back to a path
// relative to repoRoot, the report's established contract. A no-op whenever base == repoRoot
// (every command that doesn't use RunFrom/SandboxType today), so it is safe to call
// unconditionally for every job.
func remapFindings(findings []Finding, base, repoRoot string) {
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
