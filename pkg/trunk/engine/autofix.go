package engine

import (
	"os"
	"sort"

	"github.com/xunleii/rtunk/pkg/trunk/output"
)

// ApplyInlineFixes writes every non-overlapping output.Finding.Fix in findings to disk, grouped by
// File. Within one file, fixes are applied highest-Range-first so an earlier replacement's byte
// offsets stay valid for the next; a fix whose Range overlaps one already applied in this file, or
// whose Range no longer fits the file's current content (stale -- the file changed since the tool
// produced it), is left untouched: its Finding is unresolved this pass, and a repeated `check
// --fix` picks it up once the conflicting fix has landed and pass 1 re-reports it (the same
// fixed-point-over-repeated-runs shape a tool's own native --fix has). Returns the absolute paths
// actually rewritten, deduplicated and sorted.
func ApplyInlineFixes(findings []output.Finding) ([]string, error) {
	byFile := map[string][]*output.InlineFix{}
	for i := range findings {
		if findings[i].Fix == nil {
			continue
		}
		byFile[findings[i].File] = append(byFile[findings[i].File], findings[i].Fix)
	}

	var changed []string
	for file, fixes := range byFile {
		content, err := os.ReadFile(file)
		if err != nil {
			return changed, err
		}
		sort.Slice(fixes, func(i, j int) bool { return fixes[i].Range[0] > fixes[j].Range[0] })

		out := content
		applied := false
		appliedFrom := len(content) + 1 // nothing applied yet -- any range is "before" this
		for _, fix := range fixes {
			start, end := fix.Range[0], fix.Range[1]
			if start < 0 || end > len(content) || start > end || end > appliedFrom {
				continue // out of bounds, or overlaps a fix already applied (higher offset)
			}
			out = append(out[:start:start], append([]byte(fix.Text), out[end:]...)...)
			appliedFrom = start
			applied = true
		}
		if !applied {
			continue
		}
		if err := os.WriteFile(file, out, 0o644); err != nil {
			return changed, err
		}
		changed = append(changed, file)
	}
	sort.Strings(changed)
	return changed, nil
}
