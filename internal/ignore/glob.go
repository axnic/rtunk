package ignore

import (
	"regexp"
	"strings"
)

// globToRegexp compiles a gitignore-style glob into an anchored regexp.
// path/filepath.Match doesn't support "**" (any-depth match), which
// SPECS.md §9.2's examples rely on ("**/generated/**"), so this hand-rolls
// the small glob->regexp translation instead of adding a doublestar dependency.
func globToRegexp(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteByte('^')
	for i := 0; i < len(pattern); {
		switch {
		case strings.HasPrefix(pattern[i:], "**"):
			b.WriteString(".*")
			i += 2
		case pattern[i] == '*':
			b.WriteString("[^/]*")
			i++
		case pattern[i] == '?':
			b.WriteString("[^/]")
			i++
		default:
			b.WriteString(regexp.QuoteMeta(string(pattern[i])))
			i++
		}
	}
	b.WriteByte('$')
	return regexp.Compile(b.String())
}
