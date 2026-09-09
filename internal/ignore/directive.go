package ignore

import (
	"regexp"
	"strings"
)

type Kind int

const (
	KindLine Kind = iota
	KindAll
	KindBlockStart
	KindBlockEnd
)

// target is one "linter" or "linter/code" entry inside a directive's parens.
type target struct {
	Linter string // "" means every linter (bare rtunk-ignore, no parens)
	Code   string // "" means every code of Linter
}

// Directive is one parsed rtunk-ignore(...)/trunk-ignore(...) comment
// (SPECS.md §9.1 — trunk-ignore is accepted as a strict alias, indefinitely,
// so a repo migrating from trunk doesn't need to rewrite existing comments).
type Directive struct {
	Kind    Kind
	Targets []target // empty = suppress everything in scope
	Line    int      // the line this directive applies to: for KindLine, same
	// line as the comment if code precedes it, else the next
	// line (a standalone comment reads as "ignore below")
	Used bool // set by Apply once it actually suppresses a diagnostic
}

var directiveRe = regexp.MustCompile(`(?:rtunk|trunk)-ignore(-all|-begin|-end)?(?:\(([^)]*)\))?`)

var commentMarkers = []string{"//", "#", "--", ";;", "/*", "\"\"\"", "'''"}

// ParseDirectives scans file content for rtunk-ignore/trunk-ignore comments,
// one directive per matching line (the first match per line; a line with
// unrelated code preceding a second occurrence is not a supported case).
func ParseDirectives(content []byte) []*Directive {
	var directives []*Directive
	for i, line := range strings.Split(string(content), "\n") {
		lineNo := i + 1
		loc := directiveRe.FindStringSubmatchIndex(line)
		if loc == nil {
			continue
		}
		variant, args := submatch(line, loc, 1), submatch(line, loc, 2)

		d := &Directive{Line: lineNo, Targets: parseTargets(args)}
		switch variant {
		case "-all":
			d.Kind = KindAll
		case "-begin":
			d.Kind = KindBlockStart
		case "-end":
			d.Kind = KindBlockEnd
		default:
			d.Kind = KindLine
			if isStandaloneComment(line[:loc[0]]) {
				d.Line = lineNo + 1
			}
		}
		directives = append(directives, d)
	}
	return directives
}

func submatch(s string, loc []int, group int) string {
	start, end := loc[2*group], loc[2*group+1]
	if start < 0 {
		return ""
	}
	return s[start:end]
}

// isStandaloneComment reports whether everything before the directive on
// this line is just a comment marker (possibly indented) rather than real
// code — i.e. whether the directive is a comment-only line rather than a
// trailing comment on a code line.
//
// ponytail: a fixed marker list, not real per-language comment parsing —
// covers //, #, --, /* style line-starts; block comments spanning several
// lines before the marker aren't detected as such.
func isStandaloneComment(before string) bool {
	t := strings.TrimSpace(before)
	if t == "" {
		return true
	}
	for _, m := range commentMarkers {
		if strings.HasPrefix(t, m) {
			return true
		}
	}
	return false
}

func parseTargets(args string) []target {
	args = strings.TrimSpace(args)
	if args == "" {
		return nil
	}
	var targets []target
	for _, part := range strings.Split(args, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if idx := strings.IndexByte(part, '/'); idx >= 0 {
			targets = append(targets, target{Linter: part[:idx], Code: part[idx+1:]})
		} else {
			targets = append(targets, target{Linter: part})
		}
	}
	return targets
}
