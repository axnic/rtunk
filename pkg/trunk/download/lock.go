// Package download's lock.go implements ROADMAP.md v0.11's "concurrent installs fail fast, by
// name": a per-install-item file lock so a second rtunk process racing to install the same item
// never waits -- it either proceeds immediately (the recorded holder has crashed) or fails
// immediately, naming who currently holds it.
package download

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

// lockClaim is what a lock file at <installDir>.lock holds while a repository is fetching and
// installing that item.
type lockClaim struct {
	PID  int
	Repo string
}

// heldLocks records which lock paths this process currently holds, guarded by heldMu. A lock file
// carrying our own PID is otherwise ambiguous: signal 0 says "alive" whether we really hold it or
// it's a leftover from an earlier, killed rtunk that happened to get our PID (always the case for
// PID 1 in a container, and Ctrl-C skips the deferred release). Only heldLocks tells them apart.
var (
	heldMu    sync.Mutex
	heldLocks = map[string]bool{}
)

// acquireInstallLock claims installDir's lock, or reports who already holds it. The caller must
// call release exactly once it is done installing (success or failure) if err is nil.
//
// The claim is written to a temp file first and hard-linked into place: os.Link fails with EEXIST
// if the lock already exists, so the lock file appears atomically with its full contents -- a
// racing reader never sees a half-written (empty) claim and mistakes a live lock for a stale one.
func acquireInstallLock(installDir, repoRoot string) (release func(), err error) {
	lockPath := installDir + ".lock"
	data, err := json.Marshal(lockClaim{PID: os.Getpid(), Repo: repoRoot})
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(lockPath)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(dir, ".lock-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	_, werr := tmp.Write(data)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return nil, werr
	}

	// Held across the whole claim loop (never across an install): a claim-then-record, or a
	// self-PID stale check, must not interleave with another goroutine's release or claim.
	heldMu.Lock()
	defer heldMu.Unlock()
	for {
		linkErr := os.Link(tmp.Name(), lockPath)
		if linkErr == nil {
			heldLocks[lockPath] = true
			return func() {
				heldMu.Lock()
				defer heldMu.Unlock()
				delete(heldLocks, lockPath)
				_ = os.Remove(lockPath)
			}, nil
		}
		if !os.IsExist(linkErr) {
			return nil, linkErr
		}

		existing, readErr := os.ReadFile(lockPath)
		if os.IsNotExist(readErr) {
			continue // the holder released it between our Link and this ReadFile; retry the claim
		}
		if readErr != nil {
			return nil, readErr
		}
		var held lockClaim
		if json.Unmarshal(existing, &held) == nil && holderAlive(held.PID, lockPath) {
			return nil, fmt.Errorf("download: repository %q is already installing %s (pid %d); retry later",
				held.Repo, installDir, held.PID)
		}
		// Stale: crashed holder, or an unparseable leftover.
		// ponytail: two processes reclaiming the same stale lock at the same instant can both
		// win (one removes the other's fresh claim); only reachable right after a crash, and the
		// outcome is today's pre-lock behaviour. Switch to flock(2) if that ever matters.
		if rmErr := os.Remove(lockPath); rmErr != nil && !os.IsNotExist(rmErr) {
			return nil, rmErr
		}
	}
}

// holderAlive reports whether a claim by pid on lockPath is still live. For our own PID that is
// exactly "this process holds it right now" (heldMu must be held); any other PID is probed.
func holderAlive(pid int, lockPath string) bool {
	if pid == os.Getpid() {
		return heldLocks[lockPath]
	}
	return processAlive(pid)
}

// processAlive reports whether pid names a still-running process, using signal 0 (the standard
// POSIX liveness probe: it checks existence/permission without delivering anything real). EPERM
// means the process exists but belongs to another user -- still alive. No Windows branch --
// rtunk has no Windows build (AGENTS.md).
func processAlive(pid int) bool {
	if pid <= 0 {
		return false // 0/-1 would address a process group, not a holder
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}
