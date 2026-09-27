package app

import (
	"image/color"
	"regexp"
	"strings"
	"testing"

	"charm.land/bubbles/v2/list"
	"github.com/brohd11/bubblestack/core"
)

var sgrFg = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// rowFg is the escape sequence a rendered row paints name in: the last SGR before it. Found
// by position rather than by index, because a selected row carries a left rule ahead of the
// title and an unselected one does not.
func rowFg(t *testing.T, row, name string) string {
	t.Helper()
	at := strings.Index(row, name)
	if at < 0 {
		t.Fatalf("row %q does not contain %q", row, name)
	}
	seqs := sgrFg.FindAllStringIndex(row[:at], -1)
	if len(seqs) == 0 {
		t.Fatalf("row carries no title style: %q", row)
	}
	last := seqs[len(seqs)-1]
	return row[last[0]:last[1]]
}

// TestDocItemKeepsGitColorSelected: the Docs panel's cursor row shows git state rather than
// the highlight color; the panel's left rule marks the selection. Rows without a color
// hook retain the default selection accent.
func TestDocItemKeepsGitColorSelected(t *testing.T) {
	modified := func(string) color.Color { return gitStateColor(gitModified) }
	none := func(string) color.Color { return nil }
	rows := []list.Item{
		docItem{doc: DocFile{Name: "dirty.md", Path: "/x/dirty.md"}, titleColor: modified},
		docItem{doc: DocFile{Name: "clean.md", Path: "/x/clean.md"}, titleColor: none},
		docItem{doc: DocFile{Name: "plain.md", Path: "/x/plain.md"}}, // no titleColor hook
	}
	l := core.NewCompactList(rows, "")
	l.SetSize(30, 10)
	render := func(i int, selected bool) string {
		if selected {
			l.Select(i)
		} else {
			l.Select((i + 1) % len(rows))
		}
		var b strings.Builder
		core.CompactDelegate{}.Render(&b, l, i, rows[i])
		return b.String()
	}

	if got, want := rowFg(t, render(0, true), "dirty.md"), rowFg(t, render(0, false), "dirty.md"); got != want {
		t.Errorf("a modified doc under the cursor should keep its git color: got %q, want %q", got, want)
	}
	if got, want := rowFg(t, render(1, true), "clean.md"), rowFg(t, render(1, false), "clean.md"); got != want {
		t.Errorf("a clean doc under the cursor should read as an unselected one: got %q, want %q", got, want)
	}
	if got, plain := rowFg(t, render(2, true), "plain.md"), rowFg(t, render(2, false), "plain.md"); got == plain {
		t.Errorf("a row without a color hook has no git color to protect and should still highlight: %q", got)
	}
	// Whatever the title does, the cursor is still marked.
	if !strings.HasPrefix(render(0, true), sgrFg.FindString(render(0, true))+"│") {
		t.Errorf("the selected row lost its left rule: %q", render(0, true))
	}
}
