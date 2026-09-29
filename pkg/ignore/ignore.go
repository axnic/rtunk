package ignore

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/xunleii/rtunk/pkg/trunk/output"
)

// directiveRE matches an (rtunk|trunk)-ignore directive anywhere on a line: the bare form
// (same-line or next-line), -all (whole file), or -begin/-end (block range). Group 1 is the
// variant suffix ("", "-all", "-begin", "-end"); group 2 is the comma-separated linter[/rule]
// list.
//
// ponytail: matches anywhere on the line, not just inside a real comment -- a string literal
// that happens to spell out this exact text is a false positive (over-suppression, not
// under-suppression). Add a comment-delimiter check against cfg.Lint.CommentFormats if that ever
// bites in practice; resolving it per-FileType instead would be wrong (config.filterEnabled trims
// cfg.Lint.Files down to what enabled linters reference, so a broad linter like cspell running on
// a file whose own language linter isn't enabled would find no FileType and silently skip every
// directive in it).
var directiveRE = regexp.MustCompile(`(?:rtunk|trunk)-ignore(-all|-begin|-end)?\(([^)]*)\)`)

// isCommentLeaderOnly reports whether s (the text on a line before a directive match) is nothing
// but a comment opener and/or whitespace -- no letter or digit -- so the directive reads as
// standalone (applies to the NEXT line) rather than trailing real code (applies to its own line).
// Delimiter-agnostic on purpose: rtunk-ignore's payload never names which comment style is in
// use, and this avoids re-deriving that per file type (see directiveRE's own ponytail note).
func isCommentLeaderOnly(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// target is one linter[/rule] a directive names; rule == "" means every rule of linter.
type target struct{ linter, rule string }

// parseTargets splits a directive's comma-separated payload into targets. Each entry splits on
// its FIRST "/" only -- a rule ID can itself contain "/" (e.g. @typescript-eslint/no-unused-vars,
// markdownlint's MD013/line-length). A bare entry (no "/") is another rule of the PRECEDING
// entry's linter when that entry itself had a "/" (real usage: "eslint/no-console,no-unused-vars"
// is both rules of eslint) -- otherwise it's its own whole-linter target (real usage:
// "eslint,prettier" is two separate linters, entirely suppressed, neither naming a rule).
func parseTargets(payload string) []target {
	var out []target
	prevLinter := ""
	prevHadSlash := false
	for _, raw := range strings.Split(payload, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if linter, rule, ok := strings.Cut(raw, "/"); ok {
			linter, rule = strings.TrimSpace(linter), strings.TrimSpace(rule)
			out = append(out, target{linter: linter, rule: rule})
			prevLinter, prevHadSlash = linter, true
			continue
		}
		if prevHadSlash {
			out = append(out, target{linter: prevLinter, rule: raw})
			continue
		}
		out = append(out, target{linter: raw})
		prevLinter, prevHadSlash = raw, false
	}
	return out
}

// ruleSet is the rules of one linter a directive suppresses; any true means every rule.
type ruleSet struct {
	any   bool
	rules map[string]bool
}

func (rs *ruleSet) add(rule string) {
	if rule == "" {
		rs.any = true
		return
	}
	if rs.rules == nil {
		rs.rules = map[string]bool{}
	}
	rs.rules[rule] = true
}

func (rs *ruleSet) matches(rule string) bool {
	return rs != nil && (rs.any || rs.rules[rule])
}

// blockRange is one closed rtunk-ignore-begin/-end range for one linter.
type blockRange struct {
	linter     string
	rules      *ruleSet
	start, end int // inclusive, 1-indexed
}

// fileIndex is one file's resolved suppressions.
type fileIndex struct {
	all    map[string]*ruleSet         // linter -> ruleSet, from -all (whole file)
	lines  map[int]map[string]*ruleSet // line -> linter -> ruleSet, from the bare directive
	ranges []blockRange                // from matched -begin/-end pairs
}

// suppresses reports whether f is covered by idx. Line-0 findings (pass_fail, and any parser that
// doesn't report a line) can only be suppressed by -all -- there is no line to match a same-line,
// next-line, or block directive against.
func (idx *fileIndex) suppresses(f output.Finding) bool {
	if idx.all[f.Linter].matches(f.RuleID) {
		return true
	}
	if f.Line <= 0 {
		return false
	}
	if idx.lines[f.Line][f.Linter].matches(f.RuleID) {
		return true
	}
	for _, r := range idx.ranges {
		if r.linter == f.Linter && f.Line >= r.start && f.Line <= r.end && r.rules.matches(f.RuleID) {
			return true
		}
	}
	return false
}

// buildIndex scans content's lines for directives and resolves them into a fileIndex. An
// unmatched -begin (no -end before EOF) suppresses nothing: the findings it would have hidden
// stay visible, which fails loud rather than silently hiding everything after a typo'd or
// forgotten -end. A stray -end with no open -begin is likewise ignored. Ranges are tracked per
// (linter, rule) with a stack, so nested or overlapping blocks for different linters/rules don't
// interfere with each other.
func buildIndex(content []byte) *fileIndex {
	idx := &fileIndex{all: map[string]*ruleSet{}, lines: map[int]map[string]*ruleSet{}}
	open := map[target][]int{}

	for i, raw := range strings.Split(string(content), "\n") {
		lineNo := i + 1
		m := directiveRE.FindStringSubmatchIndex(raw)
		if m == nil {
			continue
		}
		variant := "" // group 1 (the variant suffix) didn't participate: bare directive
		if m[2] >= 0 {
			variant = raw[m[2]:m[3]]
		}
		payload := raw[m[4]:m[5]]
		targets := parseTargets(payload)

		switch variant {
		case "-all":
			for _, t := range targets {
				rs := idx.all[t.linter]
				if rs == nil {
					rs = &ruleSet{}
					idx.all[t.linter] = rs
				}
				rs.add(t.rule)
			}
		case "-begin":
			for _, t := range targets {
				open[t] = append(open[t], lineNo)
			}
		case "-end":
			for _, t := range targets {
				stack := open[t]
				if len(stack) == 0 {
					continue
				}
				start := stack[len(stack)-1]
				open[t] = stack[:len(stack)-1]
				rs := &ruleSet{}
				rs.add(t.rule)
				idx.ranges = append(idx.ranges, blockRange{linter: t.linter, rules: rs, start: start, end: lineNo})
			}
		default: // same-line or next-line
			targetLine := lineNo
			if isCommentLeaderOnly(raw[:m[0]]) {
				targetLine = lineNo + 1
			}
			byLinter := idx.lines[targetLine]
			if byLinter == nil {
				byLinter = map[string]*ruleSet{}
				idx.lines[targetLine] = byLinter
			}
			for _, t := range targets {
				rs := byLinter[t.linter]
				if rs == nil {
					rs = &ruleSet{}
					byLinter[t.linter] = rs
				}
				rs.add(t.rule)
			}
		}
	}
	return idx
}

// Filter drops findings an rtunk-ignore/trunk-ignore directive in their own file suppresses.
// suppressed is the number dropped. A finding whose File is empty, or that can't be read back
// under repoRoot, passes through unfiltered -- there is no content to scan.
func Filter(repoRoot string, findings []output.Finding) (kept []output.Finding, suppressed int) {
	kept = make([]output.Finding, 0, len(findings))
	indexes := map[string]*fileIndex{}
	for _, f := range findings {
		if f.File == "" {
			kept = append(kept, f)
			continue
		}
		idx, cached := indexes[f.File]
		if !cached {
			idx = loadIndex(repoRoot, f.File)
			indexes[f.File] = idx
		}
		if idx != nil && idx.suppresses(f) {
			suppressed++
			continue
		}
		kept = append(kept, f)
	}
	return kept, suppressed
}

func loadIndex(repoRoot, file string) *fileIndex {
	content, err := os.ReadFile(filepath.Join(repoRoot, file))
	if err != nil {
		return nil
	}
	return buildIndex(content)
}
