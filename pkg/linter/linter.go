// Package linter (this file) runs a resolved config.LinterDefinition's
// commands: variable substitution, target grouping, exit-code check,
// output parsing. It uses pkg/tool to resolve (installing if needed) the
// definition's own binary before running it.
//
// ponytail: only two target modes (${file}, ${parent}) and three output
// types (regex, sarif, gitleaks_json) are implemented — extend once a
// bundled linter needs batching, ${parent_with()}, or lsp_json/arcanist.
package linter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	oexec "os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"

	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/internal/diagnostic"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/internal/output"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/internal/progress"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/config"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/plugin"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/shim"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/tool"
)

// Linter wraps a resolved LinterDefinition: it knows how to run its
// Commands (installing its own binary via pkg/tool first, if needed).
type Linter struct {
	Def config.LinterDefinition
	// LintersDir is pkg/cache's Cache.Linters() result: this linter's own
	// binary lives under LintersDir/<name>/...
	LintersDir string
	// Runtimes are every already-Ensure'd runtime (pkg/runtime.Ensure's
	// own result) a package-manager-installed linter binary might need.
	Runtimes map[string]shim.Shim
	// ProjectCacheDir, if set, is symlinked into (pkg/cache.Cache.Workspace
	// for the current repo) via pkg/tool.EnsureOneProject instead of a
	// plain EnsureOne — "" skips that (no project symlink, e.g.
	// rtunk-explorer's one-shot use).
	ProjectCacheDir string
	Sem             *semaphore.Weighted // shared global bound on concurrent subprocess invocations (--jobs); nil means unbounded
	Progress        *progress.Tracker   // optional: reports each job's start/end for the live progress bar; nil disables reporting
}

func NewLinter(def config.LinterDefinition, lintersDir string, runtimes map[string]shim.Shim, sem *semaphore.Weighted, prog *progress.Tracker) *Linter {
	return &Linter{Def: def, LintersDir: lintersDir, Runtimes: runtimes, Sem: sem, Progress: prog}
}

func (l *Linter) reportStart(name string) {
	if l.Progress != nil {
		l.Progress.Start(name)
	}
}

func (l *Linter) reportDone(name string) {
	if l.Progress != nil {
		l.Progress.Done(name)
	}
}

// acquire blocks until both the optional per-command local cap (Command.MaxConcurrency)
// and the shared global pool admit one more concurrent subprocess.
func (l *Linter) acquire(ctx context.Context, local *semaphore.Weighted) error {
	if local != nil {
		if err := local.Acquire(ctx, 1); err != nil {
			return err
		}
	}
	if l.Sem != nil {
		if err := l.Sem.Acquire(ctx, 1); err != nil {
			if local != nil {
				local.Release(1)
			}
			return err
		}
	}
	return nil
}

func (l *Linter) release(local *semaphore.Weighted) {
	if l.Sem != nil {
		l.Sem.Release(1)
	}
	if local != nil {
		local.Release(1)
	}
}

type group struct {
	files    []string // files belonging to this group, for in_place hashing
	resolved string   // ${target} substitution value
}

// Run executes c against targets already scoped to Def.Files: it ensures the
// binary is installed (pkg/tool.EnsureOneProject), runs it (per Command.Target
// grouping), and parses its output into diagnostics (lint commands) or
// reports rewritten files (in_place commands).
func (l *Linter) Run(root string, c config.Command, targets []string) ([]diagnostic.Diagnostic, []string, []diagnostic.Fix, error) {
	return l.run(root, c, targets)
}

// CountJobs reports how many subprocess invocations c would launch against
// targets (matching + target-grouping only, no execution) — used for a
// cheap prepass that sizes the progress bar's total before the real run starts.
func (l *Linter) CountJobs(c config.Command, targets []string) int {
	matched := filterMatches(l.Def, targets)
	if len(matched) == 0 {
		return 0
	}
	groups, err := groupTargets(c.Target, matched)
	if err != nil {
		return 0
	}
	return len(groups)
}

func (l *Linter) run(root string, c config.Command, targets []string) ([]diagnostic.Diagnostic, []string, []diagnostic.Fix, error) {
	matched := filterMatches(l.Def, targets)
	if len(matched) == 0 {
		return nil, nil, nil, nil
	}
	s, err := tool.EnsureOneProject(l.LintersDir, plugin.ToolFor(l.Def), l.Runtimes, l.ProjectCacheDir)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("%s: %w", l.Def.Name, err)
	}
	groups, err := groupTargets(c.Target, matched)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("%s/%s: %w", l.Def.Name, c.Name, err)
	}
	configArgs := resolveConfigArgs(root, l.Def)

	// max_concurrency further restricts this specific command's own groups,
	// on top of (never beyond) the shared global pool.
	var localSem *semaphore.Weighted
	if c.MaxConcurrency > 0 {
		localSem = semaphore.NewWeighted(int64(c.MaxConcurrency))
	}

	if c.InPlace {
		var mu sync.Mutex
		var changed []string
		eg, ctx := errgroup.WithContext(context.Background())
		for _, g := range groups {
			g := g
			eg.Go(func() error {
				if err := l.acquire(ctx, localSem); err != nil {
					return err
				}
				defer l.release(localSem)
				name := l.Def.Name + "/" + c.Name + ": " + g.resolved
				l.reportStart(name)
				defer l.reportDone(name)
				ch, err := runInPlace(root, s, configArgs, c, g)
				if err != nil {
					return err
				}
				mu.Lock()
				changed = append(changed, ch...)
				mu.Unlock()
				return nil
			})
		}
		if err := eg.Wait(); err != nil {
			return nil, nil, nil, fmt.Errorf("%s/%s: %w", l.Def.Name, c.Name, err)
		}
		return nil, changed, nil, nil
	}

	// output: rewrite (not in_place): the tool prints the whole reformatted
	// file to stdout instead of writing it — rtunk proposes a Fix (diff)
	// rather than applying it blindly, so the caller can preview/prompt.
	if c.Output == "rewrite" {
		var mu sync.Mutex
		var fixes []diagnostic.Fix
		eg, ctx := errgroup.WithContext(context.Background())
		for _, g := range groups {
			g := g
			eg.Go(func() error {
				if err := l.acquire(ctx, localSem); err != nil {
					return err
				}
				defer l.release(localSem)
				name := l.Def.Name + "/" + c.Name + ": " + g.resolved
				l.reportStart(name)
				defer l.reportDone(name)
				fix, err := runRewrite(root, s, configArgs, c, g, l.Def.Name)
				if err != nil {
					return err
				}
				if fix == nil {
					return nil
				}
				mu.Lock()
				fixes = append(fixes, *fix)
				mu.Unlock()
				return nil
			})
		}
		if err := eg.Wait(); err != nil {
			return nil, nil, nil, fmt.Errorf("%s/%s: %w", l.Def.Name, c.Name, err)
		}
		return nil, nil, fixes, nil
	}

	severity, err := severityOf(c.Severity)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("%s/%s: %w", l.Def.Name, c.Name, err)
	}
	successCodes := c.SuccessCodes
	if len(successCodes) == 0 {
		successCodes = []int{0}
	}

	var mu sync.Mutex
	var diags []diagnostic.Diagnostic
	eg, ctx := errgroup.WithContext(context.Background())
	for _, g := range groups {
		g := g
		eg.Go(func() error {
			if err := l.acquire(ctx, localSem); err != nil {
				return err
			}
			defer l.release(localSem)
			name := l.Def.Name + "/" + c.Name + ": " + g.resolved
			l.reportStart(name)
			defer l.reportDone(name)
			out, code, err := runCommand(root, s, configArgs, c, g.resolved)
			if err != nil {
				return err
			}
			if !containsInt(successCodes, code) {
				return fmt.Errorf("exit code %d not in success_codes %v", code, successCodes)
			}
			if c.Parser != nil {
				out, err = runParser(root, l.Def.PluginDir, *c.Parser, out)
				if err != nil {
					return err
				}
			}
			d, err := parseOutput(root, c, out, l.Def.Name)
			if err != nil {
				return err
			}
			// sarif carries its own per-result severity; every other output
			// type is uniform per-command, from Command.Severity.
			if c.Output != "sarif" {
				for i := range d {
					d[i].Severity = severity
				}
			}
			mu.Lock()
			diags = append(diags, d...)
			mu.Unlock()
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, nil, nil, fmt.Errorf("%s/%s: %w", l.Def.Name, c.Name, err)
	}
	return diags, nil, nil, nil
}

func parseOutput(root string, c config.Command, raw, linterName string) ([]diagnostic.Diagnostic, error) {
	var d []diagnostic.Diagnostic
	var err error
	switch c.Output {
	case "regex":
		re, rerr := regexp.Compile(string(c.ParseRegex))
		if rerr != nil {
			return nil, fmt.Errorf("invalid parse_regex: %w", rerr)
		}
		d = output.ParseRegex(re, raw, linterName, c.Name)
	case "gitleaks_json":
		d, err = output.ParseGitleaksJSON(raw, linterName, c.Name)
	// sarif_uri is checkov's name for the same thing: its own plugin.yaml
	// writes real SARIF straight to the tmp_file path (confirmed empirically —
	// no separate "uri" indirection to resolve), so it needs no distinct parser.
	case "sarif", "sarif_uri":
		d, err = output.ParseSarif(raw, linterName, c.Name)
	case "markdownlint":
		d, err = output.ParseMarkdownlint(raw, linterName, c.Name)
	case "taplo":
		d, err = output.ParseTaplo(raw, linterName, c.Name)
	default:
		return nil, fmt.Errorf("unsupported output type %q", c.Output)
	}
	if err != nil {
		return nil, err
	}
	// Most tools print paths relative to the invocation's cwd (root),
	// sometimes with a leading "./" depending on the ${target} grouping used
	// ("." vs "./subdir") — normalize so Path always matches the bare
	// relative form used everywhere else (targets, internal/ignore). taplo
	// specifically prints an absolute path; re-relativize it to root.
	for i := range d {
		if filepath.IsAbs(d[i].Path) {
			if rel, err := filepath.Rel(root, d[i].Path); err == nil {
				d[i].Path = rel
				continue
			}
		}
		d[i].Path = filepath.Clean(d[i].Path)
	}
	return d, nil
}

func filterMatches(def config.LinterDefinition, targets []string) []string {
	var out []string
	for _, t := range targets {
		if def.Matches(t) {
			out = append(out, t)
		}
	}
	return out
}

func groupTargets(targetTpl string, files []string) ([]group, error) {
	switch targetTpl {
	case "${file}":
		groups := make([]group, 0, len(files))
		for _, f := range files {
			groups = append(groups, group{files: []string{f}, resolved: f})
		}
		return groups, nil
	case "${parent}":
		seen := map[string]bool{}
		var groups []group
		for _, f := range files {
			dir := filepath.Dir(f)
			resolved := "./" + dir
			if dir == "." {
				resolved = "."
			}
			if seen[resolved] {
				continue
			}
			seen[resolved] = true
			groups = append(groups, group{resolved: resolved})
		}
		return groups, nil
	default:
		return nil, fmt.Errorf("unsupported target %q", targetTpl)
	}
}

// resolveConfigArgs returns the extra CLI args (e.g. ["-c", ".yamllint.yaml"])
// that point a linter at its own project-level config file, if it declares
// a ConfigFlag and one of its DirectConfigs exists — checked at the repo
// root first, then under .trunk/configs/ (where trunk itself stores its
// managed copy, e.g. this repo's own .trunk/configs/.yamllint.yaml — a repo
// migrating from trunk keeps that layout without needing to move anything).
func resolveConfigArgs(root string, def config.LinterDefinition) []string {
	if def.ConfigFlag == "" {
		return nil
	}
	for _, name := range def.DirectConfigs {
		if _, err := os.Stat(filepath.Join(root, name)); err == nil {
			return []string{def.ConfigFlag, name}
		}
		trunkPath := filepath.Join(".trunk", "configs", name)
		if _, err := os.Stat(filepath.Join(root, trunkPath)); err == nil {
			return []string{def.ConfigFlag, trunkPath}
		}
	}
	return nil
}

func runInPlace(root string, s shim.Shim, configArgs []string, c config.Command, g group) ([]string, error) {
	before := make([]string, len(g.files))
	for i, f := range g.files {
		h, err := hashFile(filepath.Join(root, f))
		if err != nil {
			return nil, err
		}
		before[i] = h
	}
	successCodes := c.SuccessCodes
	if len(successCodes) == 0 {
		successCodes = []int{0}
	}
	_, code, err := runCommand(root, s, configArgs, c, g.resolved)
	if err != nil {
		return nil, err
	}
	if !containsInt(successCodes, code) {
		return nil, fmt.Errorf("exit code %d not in success_codes %v", code, successCodes)
	}
	var changed []string
	for i, f := range g.files {
		h, err := hashFile(filepath.Join(root, f))
		if err != nil {
			return nil, err
		}
		if h != before[i] {
			changed = append(changed, f)
		}
	}
	return changed, nil
}

// runRewrite runs c (whose target must be ${file}: g.resolved is the single
// file it applies to) and returns a Fix proposing its stdout as the new
// content, or nil if the file is already correctly formatted.
func runRewrite(root string, s shim.Shim, configArgs []string, c config.Command, g group, linterName string) (*diagnostic.Fix, error) {
	before, err := os.ReadFile(filepath.Join(root, g.resolved))
	if err != nil {
		return nil, err
	}
	successCodes := c.SuccessCodes
	if len(successCodes) == 0 {
		successCodes = []int{0}
	}
	out, code, err := runCommand(root, s, configArgs, c, g.resolved)
	if err != nil {
		return nil, err
	}
	if !containsInt(successCodes, code) {
		return nil, fmt.Errorf("exit code %d not in success_codes %v", code, successCodes)
	}
	after := []byte(out)
	if bytes.Equal(before, after) {
		return nil, nil
	}
	return &diagnostic.Fix{Path: g.resolved, LinterName: linterName, CommandName: c.Name, Before: before, After: after}, nil
}

// runParser feeds raw (the main command's own output) to parser.Run via
// stdin and returns the parser's stdout — what Output actually parses: a
// bundled script transforming a tool's native format into one rtunk
// understands (e.g. trufflehog's own JSON into SARIF via trunk's own
// trufflehog_to_sarif.py). A non-zero exit is always an error — unlike the
// main command, there's no per-parser success_codes.
func runParser(root, pluginDir string, p config.Parser, raw string) (string, error) {
	args := strings.Fields(strings.ReplaceAll(p.Run, "${plugin}", pluginDir))
	if len(args) == 0 {
		return "", fmt.Errorf("parser.run is empty")
	}
	cmd := oexec.Command(args[0], args[1:]...)
	cmd.Dir = root
	cmd.Stdin = strings.NewReader(raw)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("parser %q: %w: %s", p.Run, err, stderr.String())
	}
	return stdout.String(), nil
}

// runCommand substitutes ${target} (and ${tmpfile} when read_output_from is
// tmp_file) and runs c.Run, with s.Path (if any) prepended to PATH so a
// downloaded binary is found without needing an explicit ${linter}/ prefix
// in the definition's `run` template.
func runCommand(root string, s shim.Shim, configArgs []string, c config.Command, target string) (string, int, error) {
	if c.ReadOutputFrom == "tmp_file" {
		tmp, err := os.CreateTemp("", "rtunk-*")
		if err != nil {
			return "", 0, err
		}
		tmp.Close()
		defer os.Remove(tmp.Name())
		args := append(strings.Fields(substitute(c.Run, target, tmp.Name())), configArgs...)
		if _, _, code, err := run(root, s, args); err != nil {
			return "", 0, err
		} else if data, rerr := os.ReadFile(tmp.Name()); rerr != nil {
			return "", 0, rerr
		} else {
			return string(data), code, nil
		}
	}
	args := append(strings.Fields(substitute(c.Run, target, "")), configArgs...)
	stdout, stderr, code, err := run(root, s, args)
	if err != nil {
		return "", 0, err
	}
	switch c.ReadOutputFrom {
	case "stdout":
		return stdout, code, nil
	case "stderr":
		return stderr, code, nil
	default:
		return stdout + stderr, code, nil
	}
}

func substitute(tpl, target, tmpfile string) string {
	r := strings.NewReplacer("${target}", target, "${tmpfile}", tmpfile)
	return r.Replace(tpl)
}

// run executes args, keeping stdout and stderr separate so runCommand can
// honor read_output_from: some tools — e.g. trufflehog, whose own JSON
// findings go to stdout while its own log lines go to stderr — break if the
// two streams are blindly merged before a Parser or Output parses them.
func run(root string, s shim.Shim, args []string) (stdout, stderr string, exitCode int, err error) {
	name := args[0]
	if len(s.Path) > 0 {
		// exec.Command resolves a bare name via the *current* process's PATH
		// (os/exec.LookPath), ignoring cmd.Env — so a downloaded binary must
		// be addressed by its full path, not just have PATH set for the child.
		name = filepath.Join(s.Path[0], name)
	}
	cmd := oexec.Command(name, args[1:]...)
	cmd.Dir = root
	if len(s.Path) > 0 {
		cmd.Env = append(os.Environ(), "PATH="+strings.Join(s.Path, string(os.PathListSeparator))+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	if len(s.Env) > 0 {
		if cmd.Env == nil {
			cmd.Env = os.Environ()
		}
		for k, v := range s.Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	runErr := cmd.Run()
	if runErr == nil {
		return outBuf.String(), errBuf.String(), 0, nil
	}
	var exitErr *oexec.ExitError
	if errors.As(runErr, &exitErr) {
		return outBuf.String(), errBuf.String(), exitErr.ExitCode(), nil
	}
	return "", "", 0, runErr
}

func severityOf(s string) (diagnostic.Severity, error) {
	if s == "" {
		return diagnostic.Warning, nil
	}
	return diagnostic.ParseSeverity(s)
}

func containsInt(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func hashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
