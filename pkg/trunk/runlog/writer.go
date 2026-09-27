package runlog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xunleii/rtunk/pkg/trunk/download"
)

const (
	// maxOutput caps each output event's data, and each Tee'd stream cumulatively -- a chatty
	// linter must not balloon one log file.
	maxOutput = 1 << 20
	// keepRuns is how many runs per repository survive; Start prunes the older ones.
	keepRuns = 50
	// stampLayout names a run file; fixed width, so lexical order is chronological order.
	stampLayout = "20060102T150405.000000000Z"
)

// Writer is one open run log. A nil *Writer is valid and every method on it is a no-op (Tee
// returns its destination untouched), so call sites never need an `if log != nil`.
type Writer struct {
	mu     sync.Mutex
	f      *os.File
	enc    *json.Encoder
	warn   io.Writer
	dead   bool // set on the first write error, or by End
	nextID atomic.Int64
	start  time.Time
	name   string // file name without ".jsonl"
}

// StartOpts is everything Start records in the run_start event and needs to place the file.
type StartOpts struct {
	CacheDir    string
	RepoRoot    string
	Cmd         string // "check", "fmt" or "actions-run": part of the file name
	Version     string
	Argv        []string // rtunk's own command line, program name included
	Config      string   // path of the trunk.yaml/rtunk.yaml in use
	Concurrency int
	DryRun      bool
	Warn        io.Writer // receives the single warning if logging has to be given up
}

// Start opens a new run file under <cache>/logs/<sha256(repoRoot)>/, prunes that repository to its
// newest keepRuns runs, and writes the run_start event. It never fails the run: on any error it
// prints one warning to o.Warn and returns nil, which is a valid no-op Writer.
func Start(o StartOpts) *Writer {
	if o.Warn == nil {
		o.Warn = io.Discard
	}
	w, err := open(o)
	if err != nil {
		_, _ = fmt.Fprintf(o.Warn, "rtunk: run log disabled: %v\n", err)
		return nil
	}
	cwd, _ := os.Getwd()
	w.Emit(Event{
		T: KindRunStart, Rtunk: o.Version, Argv: o.Argv, Cwd: cwd, RepoRoot: o.RepoRoot,
		Config: o.Config, Concurrency: o.Concurrency, DryRun: o.DryRun,
	})
	return w
}

func open(o StartOpts) (*Writer, error) {
	root, err := logsRoot(o.CacheDir)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(root, repoKey(o.RepoRoot))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	name := time.Now().UTC().Format(stampLayout) + "-" + o.Cmd + ".jsonl"
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	prune(dir)
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false) // keep "<redacted>" and shell redirections readable
	return &Writer{f: f, enc: enc, warn: o.Warn, start: time.Now(), name: strings.TrimSuffix(name, ".jsonl")}, nil
}

// logsRoot is <cache>/logs, a sibling of download.Root's <cache>/downloads: `rtunk cache clean`
// removes the whole shared cache root (this included), and logs have their own `rtunk logs clean`.
func logsRoot(cacheDir string) (string, error) {
	root, err := download.Root(cacheDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(root), "logs"), nil
}

// repoKey is the per-repository directory name: the SHA-256 of the absolute repo root, the same
// keying actions history uses, with symlinks resolved.
func repoKey(repoRoot string) string {
	if abs, err := filepath.Abs(repoRoot); err == nil {
		repoRoot = abs
		// Physical path, so /tmp and /private/tmp (macOS) key the same repo; keep abs if it cannot resolve.
		if real, err := filepath.EvalSymlinks(abs); err == nil {
			repoRoot = real
		}
	}
	sum := sha256.Sum256([]byte(repoRoot))
	return hex.EncodeToString(sum[:])
}

// runNames lists dir's run files, oldest first (the fixed-width stamp makes name order
// chronological). A missing dir is an empty list.
func runNames(dir string) []string {
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
			names = append(names, e.Name())
		}
	}
	return names
}

func prune(dir string) {
	names := runNames(dir)
	for _, n := range names[:max(0, len(names)-keepRuns)] {
		_ = os.Remove(filepath.Join(dir, n))
	}
}

// Name is the run's uid: the file name without ".jsonl", what `rtunk logs list` prints and
// `rtunk logs show` accepts. A nil Writer (logging disabled) returns "".
func (w *Writer) Name() string {
	if w == nil {
		return ""
	}
	return w.name
}

// NextID allocates the id that ties one invocation's events together, unique across the whole run
// (a run can call the engine several times, e.g. `check --fix`). 0 on a nil Writer.
func (w *Writer) NextID() int {
	if w == nil {
		return 0
	}
	return int(w.nextID.Add(1))
}

// Emit stamps ev with the current time and appends it as one JSON line. Safe for concurrent use.
// The first write error prints one warning and silently disables the writer for the rest of the
// run: logging never fails a run.
func (w *Writer) Emit(ev Event) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.dead {
		return
	}
	ev.TS = time.Now().UTC().Format(time.RFC3339Nano) // under the lock: line order is timestamp order
	if err := w.enc.Encode(ev); err != nil {
		w.dead = true
		_, _ = fmt.Fprintf(w.warn, "rtunk: run log disabled: %v\n", err)
	}
}

// Cap truncates s to the per-event output limit, reporting whether it cut anything.
func Cap(s string) (string, bool) {
	if len(s) <= maxOutput {
		return s, false
	}
	return s[:maxOutput], true
}

// Output logs one captured stream of invocation id; empty data logs nothing.
func (w *Writer) Output(id int, stream, data string) {
	if w == nil || data == "" {
		return
	}
	d, truncated := Cap(data)
	w.Emit(Event{T: KindOutput, ID: id, Stream: stream, Data: d, Truncated: truncated})
}

// Tee returns a writer that forwards to dst and logs what it forwarded as output events, for a
// process whose output is streamed live rather than captured. Each returned writer must be used
// by one goroutine (exec.Cmd does this per stream). Cut off cumulatively at maxOutput.
func (w *Writer) Tee(id int, stream string, dst io.Writer) io.Writer {
	if w == nil {
		return dst
	}
	return &teeWriter{w: w, dst: dst, id: id, stream: stream, budget: maxOutput}
}

type teeWriter struct {
	w      *Writer
	dst    io.Writer
	id     int
	stream string
	budget int
	cut    bool
}

func (t *teeWriter) Write(p []byte) (int, error) {
	n, err := t.dst.Write(p)
	if n == 0 || t.cut {
		return n, err
	}
	chunk := p[:n]
	truncated := len(chunk) > t.budget
	if truncated {
		chunk = chunk[:t.budget]
		t.cut = true
	}
	t.budget -= len(chunk)
	t.w.Emit(Event{T: KindOutput, ID: t.id, Stream: t.stream, Data: string(chunk), Truncated: truncated})
	return n, err
}

// End writes run_end and closes the file. failed is the caller's verdict on the run itself (a
// linter that could not run, an action that could not launch), not on what it found.
func (w *Writer) End(failed bool) {
	if w == nil {
		return
	}
	status := "ok"
	if failed {
		status = "failed"
	}
	w.Emit(Event{T: KindRunEnd, Ms: time.Since(w.start).Milliseconds(), Status: status})
	w.mu.Lock()
	defer w.mu.Unlock()
	w.dead = true
	_ = w.f.Close()
}

// Clean deletes repoRoot's logs, or every repository's when repoRoot is "".
func Clean(cacheDir, repoRoot string) error {
	root, err := logsRoot(cacheDir)
	if err != nil {
		return err
	}
	if repoRoot == "" {
		return os.RemoveAll(root)
	}
	return os.RemoveAll(filepath.Join(root, repoKey(repoRoot)))
}
