// Package autofix renders a colored unified diff for a proposed
// diagnostic.Fix and prompts Y/n/all/none for whether to apply it —
// trunk-style AUTOFIXES flow (SPECS.md §8.3 `output: rewrite`).
package autofix

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/pmezard/go-difflib/difflib"

	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/internal/diagnostic"
)

// Render returns a colored unified diff of fix.Before -> fix.After.
func Render(fix diagnostic.Fix) (string, error) {
	diff := difflib.UnifiedDiff{
		A:        difflib.SplitLines(string(fix.Before)),
		B:        difflib.SplitLines(string(fix.After)),
		FromFile: fix.Path,
		ToFile:   fix.Path,
		Context:  3,
	}
	text, err := difflib.GetUnifiedDiffString(diff)
	if err != nil {
		return "", fmt.Errorf("diff %s: %w", fix.Path, err)
	}
	return colorize(text), nil
}

func colorize(diffText string) string {
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimRight(diffText, "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
			b.WriteString("\033[1m" + line + "\033[0m\n")
		case strings.HasPrefix(line, "+"):
			b.WriteString("\033[32m" + line + "\033[0m\n")
		case strings.HasPrefix(line, "-"):
			b.WriteString("\033[31m" + line + "\033[0m\n")
		case strings.HasPrefix(line, "@@"):
			b.WriteString("\033[36m" + line + "\033[0m\n")
		default:
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}

// Choice is the user's answer to an apply-this-fix prompt.
type Choice int

const (
	Yes Choice = iota
	No
	All  // apply this and every remaining fix without asking again
	None // skip this and every remaining fix without asking again
)

// Prompt reads one line from r, matching trunk's Y/n/all/none convention
// (blank input = Yes; EOF, e.g. a non-interactive stdin, = No — a safe
// default rather than hanging or erroring).
func Prompt(r *bufio.Reader, w io.Writer, question string) (Choice, error) {
	fmt.Fprintf(w, "%s (Y/n/all/none): ", question)
	line, err := r.ReadString('\n')
	if err != nil {
		if err == io.EOF {
			fmt.Fprintln(w)
			return No, nil
		}
		return No, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", "y", "yes":
		return Yes, nil
	case "all", "a":
		return All, nil
	case "none":
		return None, nil
	default:
		return No, nil
	}
}
