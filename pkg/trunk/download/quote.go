package download

import "strings"

// QuoteOne wraps s in single quotes for safe inclusion in a shell command line, escaping any
// embedded single quote as close-quote, escaped literal quote, reopen-quote (the standard POSIX
// single-quote escaping trick). Shared by pkg/trunk/engine and pkg/trunk/actions for their own
// ${...} template-variable substitution.
func QuoteOne(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// QuoteAll is QuoteOne applied to every element of ss.
func QuoteAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = QuoteOne(s)
	}
	return out
}
