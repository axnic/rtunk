package runlog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Summary is one logged run as `rtunk logs list` shows it.
type Summary struct {
	Name   string // file name without ".jsonl"; what `rtunk logs show` accepts (any unique prefix)
	Path   string
	Cmd    string
	Start  time.Time
	Status string // "ok", "failed", or "interrupted" (no run_end: crashed or killed)
	Ms     int64
}

// List returns repoRoot's logged runs, newest first. A repository with no logs is an empty list,
// not an error.
func List(cacheDir, repoRoot string) ([]Summary, error) {
	root, err := logsRoot(cacheDir)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(root, repoKey(repoRoot))
	names := runNames(dir)
	slices.Reverse(names)

	out := make([]Summary, 0, len(names))
	for _, n := range names {
		stem := strings.TrimSuffix(n, ".jsonl")
		stamp, cmd, ok := strings.Cut(stem, "Z-")
		if !ok {
			continue
		}
		start, err := time.Parse(stampLayout, stamp+"Z")
		if err != nil {
			continue
		}
		s := Summary{Name: stem, Path: filepath.Join(dir, n), Cmd: cmd, Start: start, Status: "interrupted"}
		if last, ok := lastEvent(s.Path); ok && last.T == KindRunEnd {
			s.Status, s.Ms = last.Status, last.Ms
		}
		out = append(out, s)
	}
	return out, nil
}

// lastEvent decodes a file's final line, reading only its last 4 KiB: a run_end is tiny, while an
// interrupted file may end mid-line (which simply fails to decode).
func lastEvent(path string) (Event, bool) {
	f, err := os.Open(path)
	if err != nil {
		return Event{}, false
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return Event{}, false
	}
	off := max(0, st.Size()-4096)
	buf := make([]byte, st.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && !errors.Is(err, io.EOF) {
		return Event{}, false
	}
	lines := bytes.Split(bytes.TrimRight(buf, "\n"), []byte("\n"))
	var ev Event
	if err := json.Unmarshal(lines[len(lines)-1], &ev); err != nil {
		return Event{}, false
	}
	return ev, true
}

// Find resolves ref to one of repoRoot's runs: "" or "latest" is the newest, anything else must be
// a prefix of exactly one run's Name.
func Find(cacheDir, repoRoot, ref string) (Summary, error) {
	runs, err := List(cacheDir, repoRoot)
	if err != nil {
		return Summary{}, err
	}
	if len(runs) == 0 {
		return Summary{}, errors.New("runlog: no runs logged for this repository")
	}
	if ref == "" || ref == "latest" {
		return runs[0], nil
	}
	var matches []Summary
	for _, r := range runs {
		if strings.HasPrefix(r.Name, ref) {
			matches = append(matches, r)
		}
	}
	switch len(matches) {
	case 0:
		return Summary{}, fmt.Errorf("runlog: no run matches %q", ref)
	case 1:
		return matches[0], nil
	}
	return Summary{}, fmt.Errorf("runlog: %q matches %d runs, be more specific", ref, len(matches))
}

// Load decodes every event of the run file at path. A truncated final line (an interrupted run)
// ends the list without an error, so what was written is still readable.
func Load(path string) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var events []Event
	dec := json.NewDecoder(f)
	for dec.More() {
		var ev Event
		if err := dec.Decode(&ev); err != nil {
			if errors.Is(err, io.ErrUnexpectedEOF) {
				break
			}
			return events, err
		}
		events = append(events, ev)
	}
	return events, nil
}
