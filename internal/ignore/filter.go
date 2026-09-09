package ignore

import (
	"os"
	"path/filepath"
	"strings"

	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/internal/diagnostic"
)

// docExt are extensions treated as prose, not source code: rtunk-ignore is a
// source-comment convention (SPECS.md §9.1), and a doc page that *documents*
// or *shows an example of* the syntax (like SPECS.md's own §9.1) would
// otherwise be misread as containing real directives. Diagnostics in these
// files (e.g. a gitleaks secret finding in a README) still pass through —
// only directive scanning is skipped.
var docExt = map[string]bool{".md": true, ".markdown": true, ".txt": true, ".rst": true}

type block struct {
	start, end int
	targets    []target
	startDir   *Directive
}

// Apply filters diags (all belonging to the same file) against directives
// parsed from that file's content, returning the surviving diagnostics plus
// one `rtunk/ignore-does-nothing` note per directive that never suppressed
// anything (SPECS.md §9.1) — unless that directive's own target list
// includes "rtunk", which silences the check for it explicitly.
func Apply(path string, diags []diagnostic.Diagnostic, directives []*Directive) []diagnostic.Diagnostic {
	blocks := resolveBlocks(directives)

	var kept []diagnostic.Diagnostic
	for _, d := range diags {
		if suppressed(d, directives, blocks) {
			continue
		}
		kept = append(kept, d)
	}

	for _, dir := range directives {
		if dir.Kind == KindBlockEnd || dir.Used || hasSilencer(dir) {
			continue
		}
		kept = append(kept, diagnostic.Diagnostic{
			Path:        path,
			Line:        dir.Line,
			Severity:    diagnostic.Note,
			Code:        "rtunk/ignore-does-nothing",
			Message:     "this ignore directive never suppressed anything",
			LinterName:  "rtunk",
			CommandName: "ignore",
		})
	}
	return kept
}

// FilterAll applies Apply across every target file (not just the ones that
// produced a diagnostic, so an unused directive in an otherwise-clean file
// is still reported).
//
// ponytail: a file excluded from a given linter by lint.ignore (config.go)
// never runs that linter this pass, so any rtunk-ignore(that-linter/...)
// comment in it will read as "unused" even though the real reason is the
// config-level skip, not a clean file — acceptable for now, revisit if it
// causes confusing false positives in practice.
func FilterAll(root string, targets []string, diags []diagnostic.Diagnostic) ([]diagnostic.Diagnostic, error) {
	byPath := map[string][]diagnostic.Diagnostic{}
	for _, d := range diags {
		byPath[d.Path] = append(byPath[d.Path], d)
	}

	var out []diagnostic.Diagnostic
	for _, path := range targets {
		if docExt[strings.ToLower(filepath.Ext(path))] {
			out = append(out, byPath[path]...)
			continue
		}
		content, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			continue // deleted/renamed since target resolution: nothing to check
		}
		directives := ParseDirectives(content)
		if len(directives) == 0 && len(byPath[path]) == 0 {
			continue
		}
		out = append(out, Apply(path, byPath[path], directives)...)
	}
	return out, nil
}

func suppressed(d diagnostic.Diagnostic, directives []*Directive, blocks []block) bool {
	for _, dir := range directives {
		var onLine bool
		switch dir.Kind {
		case KindLine:
			onLine = d.Line == dir.Line
		case KindAll:
			onLine = true
		default:
			continue // KindBlockStart/KindBlockEnd handled via blocks below
		}
		if onLine && matchesTargets(d, dir.Targets) {
			dir.Used = true
			return true
		}
	}
	for i := range blocks {
		b := &blocks[i]
		if d.Line >= b.start && d.Line <= b.end && matchesTargets(d, b.targets) {
			b.startDir.Used = true
			return true
		}
	}
	return false
}

func matchesTargets(d diagnostic.Diagnostic, targets []target) bool {
	if len(targets) == 0 {
		return true
	}
	for _, t := range targets {
		if t.Linter != "" && t.Linter != d.LinterName {
			continue
		}
		if t.Code != "" && t.Code != d.Code {
			continue
		}
		return true
	}
	return false
}

func hasSilencer(dir *Directive) bool {
	for _, t := range dir.Targets {
		if t.Linter == "rtunk" {
			return true
		}
	}
	return false
}

func resolveBlocks(directives []*Directive) []block {
	var blocks []block
	var open *Directive
	for _, dir := range directives {
		switch dir.Kind {
		case KindBlockStart:
			open = dir
		case KindBlockEnd:
			if open != nil {
				blocks = append(blocks, block{start: open.Line, end: dir.Line, targets: open.Targets, startDir: open})
				open = nil
			}
		}
	}
	return blocks
}
