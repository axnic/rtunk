// Package report renders diagnostics to the terminal and computes the exit
// code (fail-on threshold, SPECS.md §7.6 — hold-the-line new/existing/fixed
// classification not implemented yet in the POC, every diagnostic counts as new).
package report

import (
	"fmt"
	"io"

	"gitlab.dnm.radiofrance.fr/alexandre.nicolaie/rtunk/internal/diagnostic"
)

// Print writes one line per diagnostic and returns the process exit code:
// non-zero if at least one diagnostic meets or exceeds failOn.
func Print(w io.Writer, diags []diagnostic.Diagnostic, failOn diagnostic.Severity) int {
	exit := 0
	for _, d := range diags {
		fmt.Fprintf(w, "%s:%d:%d: %s: %s (%s)\n", d.Path, d.Line, d.Col, d.Severity, d.Message, d.LinterName)
		if d.Severity >= failOn {
			exit = 1
		}
	}
	if len(diags) == 0 {
		fmt.Fprintln(w, "no issues found")
	} else {
		fmt.Fprintf(w, "%d issue(s) found\n", len(diags))
	}
	return exit
}
