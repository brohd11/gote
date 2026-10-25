package app

import (
	"image/color"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/brohd11/bubblestack/core"
	"github.com/brohd11/gitstack/repo"
	editor "github.com/brohd11/go-text-edit"
)

// All lines in these fixtures have the same change kind, so every track cell
// should carry that color, regardless of the pane's size or scroll position.
func assertGitScrollbar(t *testing.T, sh *core.Shared, ed *editor.Screen, fg color.Color, glyph string) {
	t.Helper()
	ed.SetFocused(false)
	ed.SetSize(sh, 40, 8)
	rows := strings.Split(ed.View(sh), "\n")
	if len(rows) != 8 {
		t.Fatalf("editor has %d rows, want 8", len(rows))
	}
	cell := lipgloss.NewStyle().Foreground(fg).Render(glyph)
	for row, text := range rows {
		if !strings.HasSuffix(text, cell) {
			t.Fatalf("row %d = %q, want scrollbar suffix %q", row, text, cell)
		}
	}
}

func TestGitScrollbarLifecycle(t *testing.T) {
	s, sh := newHome(t)
	base := strings.Repeat("base\n", 99) + "base"
	changed := strings.Repeat("changed\n", 99) + "changed"
	path, ed := seedDoc(t, s, sh, "a.txt", changed)
	s.openDoc(sh, path)
	s.applyBaseline(baselineMsg{path: path, base: base, state: repo.BaselineOK})
	assertGitScrollbar(t, sh, ed, lipgloss.Color("3"), "┃")

	ed.SetText(base)
	staleSeq := ed.EditSeq()
	s.refreshGutter()
	ed.SetText(base + "!")
	s.refreshGutter()
	s.applyGutterRefresh(gutterRefreshMsg{target: s, path: path, seq: staleSeq})
	assertGitScrollbar(t, sh, ed, lipgloss.Color("3"), "┃") // no repaint from a stale debounce
	ed.SetText(base)
	s.refreshGutter()
	s.applyGutterRefresh(gutterRefreshMsg{target: s, path: path, seq: ed.EditSeq()})
	assertGitScrollbar(t, sh, ed, core.MutedColor, "│")

	ed.SetText(changed)
	s.drawGutter()
	s.setGitGutter(false)
	assertGitScrollbar(t, sh, ed, core.MutedColor, "│")
	s.setGitGutter(true)
	s.applyBaseline(baselineMsg{path: path, base: base, state: repo.BaselineOK})
	assertGitScrollbar(t, sh, ed, lipgloss.Color("3"), "┃")

	otherPath, other := seedDoc(t, s, sh, "b.txt", changed)
	s.openDoc(sh, otherPath)
	assertGitScrollbar(t, sh, other, core.MutedColor, "│")
	s.applyBaseline(baselineMsg{path: path, base: base, state: repo.BaselineOK})
	assertGitScrollbar(t, sh, other, core.MutedColor, "│") // late result for the previous document
	for _, state := range []repo.Baseline{repo.BaselineAbsent, repo.BaselineNone, repo.BaselineIgnored} {
		s.applyBaseline(baselineMsg{path: otherPath, state: state})
		glyph := "│"
		if state == repo.BaselineAbsent {
			glyph = "┃"
		}
		assertGitScrollbar(t, sh, other, core.MutedColor, glyph)
	}
}

func TestGitScrollbarSeparateGroups(t *testing.T) {
	s, sh := newHome(t)
	base := strings.Repeat("base\n", 99) + "base"
	changed := strings.Repeat("changed\n", 99) + "changed"
	a, first := seedDoc(t, s, sh, "a.txt", changed)
	b, second := seedDoc(t, s, sh, "b.txt", changed)
	s.openDoc(sh, a)
	s.applyBaseline(baselineMsg{path: a, base: base, state: repo.BaselineOK})
	s.openDoc(sh, b)
	s.applyBaseline(baselineMsg{path: b, base: "changed", state: repo.BaselineOK})
	s.moveTab(sh, 1)
	if len(s.groups()) != 2 {
		t.Fatal("fixture did not create two editor groups")
	}
	assertGitScrollbar(t, sh, first, lipgloss.Color("3"), "┃")
	assertGitScrollbar(t, sh, second, lipgloss.Color("2"), "┃")
	s.setGitGutter(false)
	assertGitScrollbar(t, sh, first, lipgloss.Color("3"), "┃")
	assertGitScrollbar(t, sh, second, core.MutedColor, "│")
}
