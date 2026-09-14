package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/xunleii/rtunk/pkg/trunk/download"
)

// recentFmtRun is a plain fmt/check --fix run's own outcome, persisted so the next invocation
// (within recentRunWindow) can warn if it's re-touching files the previous run also touched -- a
// cheap, single-record heuristic for the same "is this actually converging" question
// --verify-stable answers rigorously within one invocation, without doubling every plain run's
// cost. Only one record is kept per repo (not a log): only the most recent run is ever relevant to
// this comparison.
type recentFmtRun struct {
	Timestamp time.Time
	Changed   map[string][]string
}

// recentFmtRunPath is <cacheDir>/fmt-last-run/<sha256(repoRoot)>.json -- keyed by repo root the
// same way pkg/trunk/actions' history log is, since this is likewise per-repo state with no home
// in .rtunk/ yet (see that package's own history.go doc comment for why).
func recentFmtRunPath(cacheDir, repoRoot string) (string, error) {
	root, err := download.Root(cacheDir)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(repoRoot))
	return filepath.Join(root, "fmt-last-run", hex.EncodeToString(sum[:])+".json"), nil
}

// loadRecentFmtRun reads repoRoot's last recorded run. ok is false (with a nil error) when none
// has ever been recorded yet.
func loadRecentFmtRun(cacheDir, repoRoot string) (r recentFmtRun, ok bool, err error) {
	path, err := recentFmtRunPath(cacheDir, repoRoot)
	if err != nil {
		return recentFmtRun{}, false, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return recentFmtRun{}, false, nil
	}
	if err != nil {
		return recentFmtRun{}, false, err
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return recentFmtRun{}, false, err
	}
	return r, true, nil
}

// saveRecentFmtRun overwrites repoRoot's recorded run, atomically (temp file + rename, same
// pattern as pkg/trunk/actions/history.go's writeHistory) so a crash mid-write never leaves a
// half-written file behind to be mistaken for a valid record.
func saveRecentFmtRun(cacheDir, repoRoot string, r recentFmtRun) error {
	path, err := recentFmtRunPath(cacheDir, repoRoot)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	data, err := json.Marshal(r)
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op once renamed below
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
