package render

import "strings"

// LiveEnabled reports whether the live view is drawn: stderr is a terminal, --no-progress is not
// set and TERM is not "dumb" (an unset TERM is not dumb).
func LiveEnabled(stderrIsTTY, noProgress bool, term string) bool {
	return stderrIsTTY && !noProgress && term != "dumb"
}

// IsUTF8Locale looks at the first non-empty of LC_ALL, LC_CTYPE and LANG (in that precedence) and
// reports whether it names a UTF-8 charset. No locale at all is not UTF-8.
func IsUTF8Locale(lcAll, lcCtype, lang string) bool {
	for _, v := range []string{lcAll, lcCtype, lang} {
		if v != "" {
			v = strings.ToLower(v)
			return strings.Contains(v, "utf-8") || strings.Contains(v, "utf8")
		}
	}
	return false
}
