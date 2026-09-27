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
// fixed-point-over-repeated-runs shape a tool's own native --fix has). A file that no longer
// exists (removed since the tool ran) is skipped, not an error -- the rest of findings still get
// applied. Range is interpreted as UTF-16 code units, not bytes: today's one real producer
// (ESLint's own `--format json`) indexes its `fix.range` into the JS source string, which is
// UTF-16 (ECMA-262), so a byte-offset read would misplace every fix in a file with any non-ASCII
// text before it. Returns the absolute paths actually rewritten, deduplicated and sorted.
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
		if os.IsNotExist(err) {
			continue // removed since the checking pass ran -- nothing to fix
		}
		if err != nil {
			return changed, err
		}
		sort.SliceStable(fixes, func(i, j int) bool { return fixes[i].Range[0] > fixes[j].Range[0] })

		out := content
		applied := false
		appliedFrom := len(content) + 1 // nothing applied yet -- any offset is "before" this
		for _, fix := range fixes {
			start, startOK := utf16ByteOffset(content, fix.Range[0])
			end, endOK := utf16ByteOffset(content, fix.Range[1])
			if !startOK || !endOK || start > end || end > appliedFrom {
				continue // out of bounds/mid-surrogate-pair, or overlaps a fix already applied
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

// utf16ByteOffset converts units, a UTF-16 code unit offset (ESLint's own range unit -- JS
// strings are UTF-16), into the equivalent byte offset into content (UTF-8). ok is false when
// units falls outside content's total UTF-16 length, or lands in the middle of a surrogate pair
// (a 4-byte UTF-8 rune, which counts as 2 UTF-16 units) -- neither is a valid split point.
func utf16ByteOffset(content []byte, units int) (offset int, ok bool) {
	if units < 0 {
		return 0, false
	}
	count := 0
	for i, r := range string(content) {
		if count == units {
			return i, true
		}
		if count > units {
			return 0, false
		}
		if r > 0xFFFF {
			count += 2
		} else {
			count++
		}
	}
	if count == units {
		return len(content), true
	}
	return 0, false
}
