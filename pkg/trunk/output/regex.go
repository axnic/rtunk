package output

import (
	"regexp"
	"strconv"
)

// ParseFromRegex parses data using pattern's named capture groups, straight from the real
// trunk-io catalog's own Command.ParseRegex field -- the same mechanism trunk's own closed-source
// engine uses for every "regex"-output linter (see docs/superpowers/specs/
// 2026-09-12-check-engine-refactor-design.md). A fixed name->Finding-field table does the mapping
// -- nothing else, no linter-specific logic anywhere in this function. A name the table has no
// entry for is simply never looked up: it isn't mapped to anything, on principle, not detected and
// aliased to its closest match (trunk's own catalog has at least one real inconsistency of this
// exact shape -- see the design spec). A table entry whose group is absent from a given pattern
// (e.g. no severity, no col) leaves that Finding field zero-valued, the same convention every
// other parser in this package already uses. Real trunk docs mark "path" as the only required
// group; a match missing it produces a Finding with an empty File rather than erroring, since a
// missing group is a config-authoring mistake in the catalog, not a signal to fail the whole run.
func ParseFromRegex(pattern string, data []byte, linter string) ([]Finding, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	names := re.SubexpNames()

	var findings []Finding
	for _, m := range re.FindAllStringSubmatch(string(data), -1) {
		f := Finding{Linter: linter}
		for i, name := range names {
			if i == 0 || i >= len(m) {
				continue
			}
			switch name {
			case "path":
				f.File = m[i]
			case "line":
				f.Line, _ = strconv.Atoi(m[i])
			case "col":
				f.Column, _ = strconv.Atoi(m[i])
			case "code":
				f.RuleID = m[i]
			case "message":
				f.Message = m[i]
			case "severity":
				f.Severity = regexSeverity(m[i])
			}
		}
		findings = append(findings, f)
	}
	return findings, nil
}

// regexSeverity normalizes trunk's real 8-value severity vocabulary (note, notice, allow, deny,
// disabled, error, info, warning -- per trunk's own docs) down to this project's existing 3-value
// Finding.Severity convention, the same way every other parser in this package already does via
// its own xxxSeverity helper. "allow"/"deny"/"disabled" describe a severity *override*
// configuration, not something a linter's own text output would literally emit -- included here
// for completeness against the documented vocabulary, not because any real captured output uses
// them.
func regexSeverity(s string) string {
	switch s {
	case "error", "deny":
		return "error"
	case "warning", "allow":
		return "warning"
	default: // "info", "note", "notice", "disabled", or anything else
		return "info"
	}
}
