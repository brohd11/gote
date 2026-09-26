package app

import (
	"fmt"
	"strings"
)

const diffContext = 3

type diffLine struct {
	kind byte // ' ', '-', '+', or '\\' for a missing newline notice
	text string
}

type diffHunk struct {
	lineChange
	lines []diffLine
}

// fileLines retains line endings and omits the editor's synthetic EOF line.
func fileLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.SplitAfter(text, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// diffHunks shares the gutter's line matching, with newline-sensitive lines for
// display. Nearby edits join when their three-line context ranges touch.
func diffHunks(base, buf string) []diffHunk {
	old, current := fileLines(base), fileLines(buf)
	changes := lineChanges(old, current)
	var hunks []diffHunk
	for first := 0; first < len(changes); {
		last := first
		for last+1 < len(changes) && changes[last+1].newStart-changes[last].newEnd <= 2*diffContext {
			last++
		}
		a, b := changes[first], changes[last]
		before := min(diffContext, a.oldStart, a.newStart)
		after := min(diffContext, len(old)-b.oldEnd, len(current)-b.newEnd)
		h := diffHunk{lineChange: lineChange{
			oldStart: a.oldStart - before, oldEnd: b.oldEnd + after,
			newStart: a.newStart - before, newEnd: b.newEnd + after,
		}}
		appendLines := func(kind byte, lines []string) {
			for _, line := range lines {
				h.lines = append(h.lines, diffLine{kind, strings.TrimSuffix(line, "\n")})
				if !strings.HasSuffix(line, "\n") {
					h.lines = append(h.lines, diffLine{'\\', "No newline at end of file"})
				}
			}
		}
		at := h.oldStart
		for _, c := range changes[first : last+1] {
			appendLines(' ', old[at:c.oldStart])
			appendLines('-', old[c.oldStart:c.oldEnd])
			appendLines('+', current[c.newStart:c.newEnd])
			at = c.oldEnd
		}
		appendLines(' ', old[at:h.oldEnd])
		hunks = append(hunks, h)
		first = last + 1
	}
	return hunks
}

func (h diffHunk) contains(line, bufferLines int) bool {
	if line >= h.newStart && line < h.newEnd {
		return true
	}
	// Pure deletions in an empty file and the editor's trailing empty line have
	// no physical line in the patch. They still carry deletion/newline markers.
	return line == h.newEnd && line == bufferLines-1
}

func (h diffHunk) header() string {
	rangeText := func(start, end int) string {
		if start == end {
			return fmt.Sprintf("%d,0", start)
		}
		return fmt.Sprintf("%d,%d", start+1, end-start)
	}
	return fmt.Sprintf("@@ -%s +%s @@", rangeText(h.oldStart, h.oldEnd), rangeText(h.newStart, h.newEnd))
}
