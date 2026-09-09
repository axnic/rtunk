package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/goccy/go-yaml"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"

	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/internal/autofix"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/internal/diagnostic"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/internal/gitutil"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/internal/ignore"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/internal/progress"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/internal/report"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/cache"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/config"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/linter"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/plugin"
	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/pkg/workspace"
)

// linterWork pairs a resolved linter definition with the targets it should
// actually run against (already filtered by lint.ignore) — computed once,
// reused both for the progress bar's job-count prepass and the real run.
type linterWork struct {
	def     config.LinterDefinition
	targets []string
}

func resolveWork(defs []config.LinterDefinition, targets []string, ignoreRules []config.LintIgnore) ([]linterWork, error) {
	items := make([]linterWork, 0, len(defs))
	for _, def := range defs {
		linterTargets, err := ignore.FilterPaths(targets, def.Name, ignoreRules)
		if err != nil {
			return nil, err
		}
		items = append(items, linterWork{def: def, targets: linterTargets})
	}
	return items, nil
}

// countJobs sums CountJobs across items' commands matching formatter (true
// for `fmt`'s commands, false for `check`'s) — sizes the progress bar's total.
func countJobs(items []linterWork, ws *workspace.Workspace, formatter bool) int {
	total := 0
	for _, it := range items {
		p := linter.NewLinter(it.def, ws.LintersDir, ws.Runtimes, nil, nil)
		for _, c := range it.def.Commands {
			if c.Formatter != formatter {
				continue
			}
			total += p.CountJobs(c, it.targets)
		}
	}
	return total
}

// newLinter builds a Linter for it, bound to ws's ProjectCacheDir so its
// tools install into this repo's own cache area (symlinked from the shared,
// version-pinned global install) rather than only the global one.
func newLinter(it linterWork, ws *workspace.Workspace, sem *semaphore.Weighted, prog *progress.Tracker) *linter.Linter {
	l := linter.NewLinter(it.def, ws.LintersDir, ws.Runtimes, sem, prog)
	l.ProjectCacheDir = ws.ProjectCacheDir
	return l
}

func main() {
	var cacheDirFlag string
	var jobs int
	var noProgress bool
	root := &cobra.Command{Use: "rtunk"}
	root.PersistentFlags().StringVar(&cacheDirFlag, "cache-dir", "", "override the cache directory (SPECS.md §2.2)")
	root.PersistentFlags().IntVarP(&jobs, "jobs", "j", runtime.NumCPU(), "number of concurrent linter processes (SPECS.md §7.4)")
	root.PersistentFlags().BoolVar(&noProgress, "no-progress", false, "don't show the live progress bar")
	root.AddCommand(checkCmd(&cacheDirFlag, &jobs, &noProgress), fmtCmd(&cacheDirFlag, &jobs, &noProgress), cacheCmd(&cacheDirFlag), configCmd())
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func checkCmd(cacheDirFlag *string, jobs *int, noProgress *bool) *cobra.Command {
	var failOn string
	var all bool
	cmd := &cobra.Command{
		Use:   "check [path]",
		Short: "Run linters on changed files (or the given path)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, cfg, targets, err := setup(args, all)
			if err != nil {
				return err
			}
			c, err := cfg.ResolveCache(*cacheDirFlag)
			if err != nil {
				return err
			}
			defs, ws, err := loadDefinitions(cmd, cfg, c, root)
			if err != nil {
				return err
			}
			severity, err := diagnostic.ParseSeverity(failOn)
			if err != nil {
				return err
			}
			items, err := resolveWork(defs, targets, cfg.Lint.Ignore)
			if err != nil {
				return err
			}

			total := 0
			if !*noProgress {
				total = countJobs(items, ws, false)
			}
			prog := progress.New(cmd.OutOrStdout(), "Checking", total)

			sem := semaphore.NewWeighted(int64(*jobs))
			var mu sync.Mutex
			var diags []diagnostic.Diagnostic
			eg := new(errgroup.Group)
			for _, it := range items {
				it := it
				eg.Go(func() error {
					p := newLinter(it, ws, sem, prog)
					for _, c := range it.def.Commands {
						if c.Formatter {
							continue
						}
						d, _, _, err := p.Run(root, c, it.targets)
						if err != nil {
							mu.Lock()
							fmt.Fprintf(cmd.ErrOrStderr(), "warning: %v\n", err)
							mu.Unlock()
							continue
						}
						mu.Lock()
						diags = append(diags, d...)
						mu.Unlock()
					}
					return nil
				})
			}
			egErr := eg.Wait()
			prog.Finish()
			if egErr != nil {
				return egErr
			}
			diags, err = ignore.FilterAll(root, targets, diags)
			if err != nil {
				return err
			}

			exit := report.Print(cmd.OutOrStdout(), diags, severity)
			if exit != 0 {
				os.Exit(exit)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&failOn, "fail-on", "warning", "minimum severity that fails the command (note|warning|error)")
	cmd.Flags().BoolVar(&all, "all", false, "scan the whole repo instead of git-diff-aware targeting")
	return cmd
}

func fmtCmd(cacheDirFlag *string, jobs *int, noProgress *bool) *cobra.Command {
	var all, autoYes, autoNo bool
	cmd := &cobra.Command{
		Use:   "fmt [path]",
		Short: "Run formatters on changed files (or the given path)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, cfg, targets, err := setup(args, all)
			if err != nil {
				return err
			}
			c, err := cfg.ResolveCache(*cacheDirFlag)
			if err != nil {
				return err
			}
			defs, ws, err := loadDefinitions(cmd, cfg, c, root)
			if err != nil {
				return err
			}
			items, err := resolveWork(defs, targets, cfg.Lint.Ignore)
			if err != nil {
				return err
			}

			total := 0
			if !*noProgress {
				total = countJobs(items, ws, true)
			}
			prog := progress.New(cmd.OutOrStdout(), "Formatting", total)

			sem := semaphore.NewWeighted(int64(*jobs))
			var mu sync.Mutex
			var changed []string
			var fixes []diagnostic.Fix
			eg := new(errgroup.Group)
			for _, it := range items {
				it := it
				eg.Go(func() error {
					p := newLinter(it, ws, sem, prog)
					for _, c := range it.def.Commands {
						if !c.Formatter {
							continue
						}
						_, ch, fx, err := p.Run(root, c, it.targets)
						if err != nil {
							mu.Lock()
							fmt.Fprintf(cmd.ErrOrStderr(), "warning: %v\n", err)
							mu.Unlock()
							continue
						}
						mu.Lock()
						changed = append(changed, ch...)
						fixes = append(fixes, fx...)
						mu.Unlock()
					}
					return nil
				})
			}
			egErr := eg.Wait()
			prog.Finish()
			if egErr != nil {
				return egErr
			}

			// Fixes (output: rewrite) are previewed and applied one at a
			// time, in the trunk AUTOFIXES style (diff + Y/n/all/none) —
			// unlike in_place commands (already applied above by the tool
			// itself), rtunk decides here whether to write them.
			applied, err := applyFixes(cmd, root, fixes, autoYes, autoNo)
			if err != nil {
				return err
			}
			changed = append(changed, applied...)

			for _, f := range changed {
				fmt.Fprintf(cmd.OutOrStdout(), "reformatted %s\n", f)
			}
			if len(changed) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "nothing to format")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "scan the whole repo instead of git-diff-aware targeting")
	cmd.Flags().BoolVarP(&autoYes, "fix", "y", false, "apply all proposed fixes without prompting")
	cmd.Flags().BoolVarP(&autoNo, "no-fix", "n", false, "show proposed fixes without applying any of them")
	return cmd
}

// applyFixes shows each fix's diff and, unless autoYes/autoNo short-circuit
// the decision, prompts Y/n/all/none (SPECS.md-style AUTOFIXES flow) before
// writing it. Returns the paths actually written.
func applyFixes(cmd *cobra.Command, root string, fixes []diagnostic.Fix, autoYes, autoNo bool) ([]string, error) {
	if len(fixes) == 0 {
		return nil, nil
	}
	applyAll, skipAll := autoYes, autoNo
	reader := bufio.NewReader(cmd.InOrStdin())
	var applied []string
	for _, fix := range fixes {
		diffText, err := autofix.Render(fix)
		if err != nil {
			return applied, err
		}
		fmt.Fprint(cmd.OutOrStdout(), diffText)

		apply := applyAll
		if !applyAll && !skipAll {
			choice, err := autofix.Prompt(reader, cmd.OutOrStdout(), fmt.Sprintf("Apply formatting to %s", fix.Path))
			if err != nil {
				return applied, err
			}
			switch choice {
			case autofix.Yes:
				apply = true
			case autofix.All:
				apply, applyAll = true, true
			case autofix.None:
				apply, skipAll = false, true
			case autofix.No:
				apply = false
			}
		}
		if apply {
			if err := os.WriteFile(filepath.Join(root, fix.Path), fix.After, 0o644); err != nil {
				return applied, err
			}
			applied = append(applied, fix.Path)
		}
	}
	return applied, nil
}

func cacheCmd(cacheDirFlag *string) *cobra.Command {
	cmd := &cobra.Command{Use: "cache", Short: "Inspect rtunk's cache (SPECS.md §2.2)"}
	cmd.AddCommand(&cobra.Command{
		Use:   "path",
		Short: "Print the active cache directory",
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			cfg := config.Default()
			if root, err := gitutil.Root(cwd); err == nil {
				if loaded, err := config.Load(root); err == nil {
					cfg = loaded
				}
			}
			c, err := cfg.ResolveCache(*cacheDirFlag)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), c.Root())
			return nil
		},
	})
	return cmd
}

func configCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Inspect rtunk's configuration (SPECS.md §6)"}
	cmd.AddCommand(&cobra.Command{
		Use:   "print",
		Short: "Print the effective configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			root, err := gitutil.Root(cwd)
			if err != nil {
				return fmt.Errorf("not a git repo: %w", err)
			}
			cfg, err := config.Load(root)
			if err != nil {
				return err
			}
			data, err := yaml.Marshal(cfg)
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(data)
			return err
		},
	})
	return cmd
}

// loadDefinitions resolves every cfg.Plugins.Sources entry (git clone at
// the pinned ref, translated from trunk's real plugin.yaml dialect — see
// pkg/plugin's package doc for the full pipeline) into a plugin.Plugins —
// searched together, not merged upfront (a name conflicting across two
// sources is caught exactly like one within a single source, the first
// time something looks it up). A source that fails to resolve is reported
// as a warning and dropped rather than failing the whole command; a
// command that doesn't translate (a hardcoded custom output type, an
// unsupported install mechanism) is likewise a warning, not a hard
// failure.
//
// pkg/plugin.Resolve never caches its own result (that's the whole point
// of its API — see its doc comment), so rtunk does the caching itself
// here, per source: a cache hit (plugin.Open at plugin.CachePath) skips
// cloning and re-parsing every plugin.yaml entirely; a miss resolves fresh
// and stores the result (best-effort — a cache-write failure must never
// fail the actual load) for next time.
//
// workspace.Resolve builds a Workspace from ps: its own Linters map is
// already the full, final set — cfg.Lint's custom/plugin-sourced
// definitions, and cfg.Lint.Enabled/Disabled filtering, all in one place —
// alongside Tools/Runtimes/ProjectCacheDir for newLinter to bind each
// Linter to.
func loadDefinitions(cmd *cobra.Command, cfg *config.Config, c *cache.Cache, root string) ([]config.LinterDefinition, *workspace.Workspace, error) {
	pluginsDir, err := c.Plugins()
	if err != nil {
		return nil, nil, err
	}
	var ps plugin.Plugins
	for _, source := range cfg.Plugins.Sources {
		p, err := plugin.Open(plugin.CachePath(pluginsDir, source))
		if err != nil {
			p, err = plugin.Resolve(source)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: plugins.sources %s: %v\n", source.ID, err)
				continue
			}
			_ = plugin.Store(pluginsDir, p)
		}
		ps = append(ps, p)
	}

	ws, err := workspace.Resolve(cfg, c, ps, root)
	if err != nil {
		return nil, nil, fmt.Errorf("plugins.sources: %w", err)
	}
	for _, w := range ws.Warnings {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: plugins.sources: %s\n", w)
	}

	defs := make([]config.LinterDefinition, 0, len(ws.Linters))
	for _, d := range ws.Linters {
		defs = append(defs, d)
	}
	return defs, ws, nil
}

// setup resolves the repo root, loads config, and resolves target files:
// the explicit path argument if given, else --all (whole repo) or files
// changed vs trunk_branch (§7.3).
func setup(args []string, all bool) (root string, cfg *config.Config, targets []string, err error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", nil, nil, err
	}
	root, err = gitutil.Root(cwd)
	if err != nil {
		return "", nil, nil, fmt.Errorf("not a git repo: %w", err)
	}
	cfg, err = config.Load(root)
	if err != nil {
		return "", nil, nil, err
	}
	if len(args) == 1 {
		targets, err = resolvePath(root, args[0])
		return root, cfg, targets, err
	}
	if all {
		targets, err = resolvePath(root, ".")
		return root, cfg, targets, err
	}
	targets, err = gitutil.ChangedFiles(root, cfg.Repo.TrunkBranch)
	return root, cfg, targets, err
}

// resolvePath resolves arg (a file or directory, "." for the whole repo)
// to a list of repo-relative files, respecting .gitignore (SPECS.md §9.2)
// via `git ls-files` rather than a raw directory walk.
func resolvePath(root, arg string) ([]string, error) {
	abs := arg
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, arg)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		rel, err := filepath.Rel(root, abs)
		return []string{rel}, err
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return nil, err
	}
	return gitutil.ListFiles(root, rel)
}
