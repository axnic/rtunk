package actions

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/xunleii/rtunk/pkg/cache/download"
)

// maxHistoryEntries bounds the on-disk log -- an unbounded audit log for a local dev tool is the
// wrong default; 200 covers weeks of normal use and keeps the file trivially small.
const maxHistoryEntries = 200

// historyPath is <cacheDir>/actions-history/<sha256(repoRoot)>.jsonl -- keyed by repo root, not
// global, since history is inherently per-repo; .rtunk/ (the natural repo-local home) doesn't
// exist until v0.7's `rtunk init`, so the shared cache dir is the only persistent-state location
// that exists today.
func historyPath(cacheDir, repoRoot string) (string, error) {
	root, err := download.Root(cacheDir)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(repoRoot))
	return filepath.Join(root, "actions-history", hex.EncodeToString(sum[:])+".jsonl"), nil
}

// AppendHistory records r, truncating to the newest maxHistoryEntries.
func AppendHistory(cacheDir, repoRoot string, r Result) error {
	path, err := historyPath(cacheDir, repoRoot)
	if err != nil {
		return err
	}
	entries, err := readHistory(path)
	if err != nil {
		return err
	}
	entries = append(entries, r)
	if len(entries) > maxHistoryEntries {
		entries = entries[len(entries)-maxHistoryEntries:]
	}
	return writeHistory(path, entries)
}

// History returns repoRoot's recorded runs, most recent first, optionally filtered to actionID
// ("" for all) and capped at limit (0 for no cap beyond the log's own 200-entry bound).
func History(cacheDir, repoRoot, actionID string, limit int) ([]Result, error) {
	path, err := historyPath(cacheDir, repoRoot)
	if err != nil {
		return nil, err
	}
	entries, err := readHistory(path)
	if err != nil {
		return nil, err
	}
	var out []Result
	for _, entrie := range slices.Backward(entries) {
		if actionID != "" && entrie.ActionID != actionID {
			continue
		}
		out = append(out, entrie)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func readHistory(path string) ([]Result, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Result
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var r Result
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// writeHistory rewrites path atomically (temp file + rename, same pattern as
// pkg/trunk/config/cache.go's saveSourceCache) -- simplest correct approach at this file size.
func writeHistory(path string, entries []Result) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	var b strings.Builder
	for _, e := range entries {
		data, err := json.Marshal(e)
		if err != nil {
			return err
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.WriteString(b.String()); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
