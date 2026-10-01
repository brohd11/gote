package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
	"github.com/charmbracelet/x/ansi"
)

func TestJoinRule(t *testing.T) {
	for _, tc := range []struct{ name, above, below, want string }{
		{"plain", "", "text", "────────"},
		{"separator", "", "ab│cd", "──┬─────"},
		{"box corners stay flat", "", "┌──┐", "────────"},
		{"joined borders", "", "├─┼─┘", "┌─┬─┬───"},
		{"corners at the ends", "", "│      │", "┌──────┐"},
		{"wide rune keeps columns", "", "界│", "──┬─────"},
		{"styled row", "", "\x1b[31mab\x1b[0m│", "──┬─────"},
		{"clipped past width", "", "12345678│", "────────"},
		{"shared edge", "│  │   │", "│ │    │", "├─┬┴───┤"},
		{"crossing", "│  │   │", "│  │   │", "├──┼───┤"},
		{"closing a column", "│  │", "", "└──┴────"},
		{"box bottom above stays flat", "└──┘", "", "────────"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ansi.Strip(joinRule(8, tc.above, tc.below)); got != tc.want {
				t.Fatalf("joinRule(%q, %q) = %q, want %q", tc.above, tc.below, got, tc.want)
			}
		})
	}
}

// TestHeaderRuleJoinsGroupDivider: the header is a fixed leaf above the panes, and its rule
// takes a ┬ where a split's divider meets it.
func TestHeaderRuleJoinsGroupDivider(t *testing.T) {
	s, sh := groupHome(t)
	seedDoc(t, s, sh, "a.md", "# A")
	b, _ := seedDoc(t, s, sh, "b.md", "# B")
	s.openDoc(sh, b)
	s.moveTab(sh, 1)
	right := s.groups()[1]
	lines := strings.Split(ansi.Strip(s.View(sh)), "\n")
	if !strings.Contains(lines[0], s.CrumbLabel(false)) {
		t.Fatalf("header row = %q", lines[0])
	}
	col := right.openTabs.x // the separator's column, in terminal cells
	if rule := []rune(lines[headerRows-1]); col >= len(rule) || rule[col] != '┬' {
		t.Fatalf("rule has no junction at column %d: %q", col, lines[headerRows-1])
	}
	if right.openTabs.y != sh.BodyY()+headerRows {
		t.Fatalf("panes start at %d, want below the %d header rows", right.openTabs.y, headerRows)
	}
}

// TestDockRuleJoinsColumns: with the dock open the sidebar drops its bottom edge and every
// column above closes onto the dock's top rule; closing the dock gives the edge back.
func TestDockRuleJoinsColumns(t *testing.T) {
	s, sh := groupHome(t)
	seedDoc(t, s, sh, "a.md", "# A")
	b, _ := seedDoc(t, s, sh, "b.md", "# B")
	s.openDoc(sh, b)
	s.moveTab(sh, 1)
	s.toggleBottom(sh)
	lines := strings.Split(ansi.Strip(s.View(sh)), "\n")
	r := s.bottom.y - sh.BodyY()
	rule := []rune(lines[r])
	sidebarEdge := s.sidebarPaneWidth() - 1
	sep := s.groups()[1].openTabs.x
	if rule[0] != '├' || rule[sidebarEdge] != '┴' || rule[sep] != '┴' || rule[len(rule)-1] != '┤' && rule[len(rule)-1] != '┐' {
		t.Fatalf("dock rule = %q (sidebar edge %d, separator %d)", lines[r], sidebarEdge, sep)
	}
	if strings.Contains(lines[r-1], "└") {
		t.Fatalf("the sidebar should leave its bottom to the dock rule: %q", lines[r-1])
	}

	s.toggleBottom(sh)
	if view := ansi.Strip(s.View(sh)); !strings.Contains(view, "└") {
		t.Fatalf("closing the dock should give the sidebar its bottom edge back:\n%s", view)
	}
}

func TestHeaderMenuRow(t *testing.T) {
	s, sh := newHome(t)
	lines := strings.Split(ansi.Strip(s.View(sh)), "\n")
	if !strings.HasPrefix(lines[0], " File  Edit  View  Options │") || !strings.Contains(lines[0], s.CrumbLabel(false)) {
		t.Fatalf("header row = %q", lines[0])
	}
	sep := len([]rune(" File  Edit  View  Options "))
	if rule := []rune(lines[1]); rule[sep] != '┴' && rule[sep] != '┼' {
		t.Fatalf("the menu separator should tee into the rule at %d: %q", sep, lines[1])
	}
}

// TestHeaderCapturesMouse: presses and wheel notches over the header stop there — no pane
// takes focus or moves — while a menu label opens its dropdown under the rule.
func TestHeaderCapturesMouse(t *testing.T) {
	model, s, sh := newHomeRouter(t, Options{})
	id, _ := seedDoc(t, s, sh, "a.md", "one\ntwo\nthree\n")
	s.openDoc(sh, id)
	top := sh.BodyY()
	// Both focus states: an unfocused editor must not take focus from a header click, and
	// a focused one must not move its cursor.
	for _, focus := range []components.Panel{s.editorPanel, s.docsPane()} {
		// Park the cursor off row 0, where a leaked click on the header row would move it.
		s.modular.FocusSlot(s.panelSlot(s.editorPanel))
		model, _ = model.Update(keyMsg("down"))
		model, _ = model.Update(keyMsg("down"))
		s.modular.FocusSlot(s.panelSlot(focus))
		_ = model.(core.Router).View()
		pos := s.editor.CursorPosition()
		if pos.Line == 0 {
			t.Fatal("fixture: the cursor should be off row 0")
		}
		for _, msg := range []tea.Msg{
			tea.MouseClickMsg{X: 60, Y: top, Button: tea.MouseLeft},     // the crumb, over the editor
			tea.MouseReleaseMsg{X: 60, Y: top, Button: tea.MouseLeft},   // passes, but finds no gesture
			tea.MouseClickMsg{X: 60, Y: top + 1, Button: tea.MouseLeft}, // the rule
			tea.MouseReleaseMsg{X: 60, Y: top + 1, Button: tea.MouseLeft},
			tea.MouseWheelMsg{X: 60, Y: top, Button: tea.MouseWheelDown},
		} {
			model, _ = model.Update(msg)
			if model.(core.Router).Top() != s || s.focusedPane() != focus || s.editor.CursorPosition() != pos {
				t.Fatalf("%T over the header reached the panes (focus %T)", msg, focus)
			}
		}
	}
	s.modular.FocusSlot(s.panelSlot(s.docsPane()))

	s.View(sh)
	var viewMenu statusSpan
	for _, span := range s.header.menuSpans {
		if span.id == "view" {
			viewMenu = span
		}
	}
	model, _ = model.Update(tea.MouseClickMsg{X: viewMenu.x0, Y: top, Button: tea.MouseLeft})
	menu, ok := model.(core.Router).Top().(*components.MenuScreen)
	if !ok {
		t.Fatalf("clicking View should open a menu, top is %T", model.(core.Router).Top())
	}
	if s.focusedPane() != s.docsPane() {
		t.Fatal("opening a menu must not move pane focus")
	}
	if !strings.Contains(ansi.Strip(menu.View(sh)), "Preview") {
		t.Fatal("the View menu's first row is missing")
	}
	// The box's top border lies on the rule: label, border, items — no doubled line.
	rows := strings.Split(stripANSI(view(model)), "\n")
	if !strings.Contains(rows[top+headerRows-1], "╭") || !strings.Contains(rows[top+headerRows], "Preview") {
		t.Fatalf("menu should open on the rule:\n%s", strings.Join(rows[:top+headerRows+2], "\n"))
	}
}

// TestHeaderLetsDragsThrough: a gesture begun in a pane keeps its motion over the header.
func TestHeaderLetsDragsThrough(t *testing.T) {
	s, sh := newHome(t)
	if _, handled := s.headerInput(sh, tea.MouseMotionMsg{X: 50, Y: sh.BodyY(), Button: tea.MouseLeft}); handled {
		t.Fatal("motion over the header must pass to the pane holding the gesture")
	}
	if _, handled := s.headerInput(sh, tea.MouseReleaseMsg{X: 50, Y: sh.BodyY(), Button: tea.MouseLeft}); handled {
		t.Fatal("a release over the header must pass to the pane holding the gesture")
	}
}

// tabRuleRow is the body row the tab rule draws on, as a slice of cells.
func tabRuleRow(t *testing.T, s *homeScreen, sh *core.Shared) (rule, below []rune) {
	t.Helper()
	lines := strings.Split(ansi.Strip(s.View(sh)), "\n")
	r := s.openTabs.y + 1 - sh.BodyY()
	if r+1 >= len(lines) {
		t.Fatalf("no tab rule row %d in %d lines", r, len(lines))
	}
	return []rune(lines[r]), []rune(lines[r+1])
}

// TestTabRuleJoinsFrames: the rule under the tab bar is one line with the legend rules of
// the sidebar and the side preview beside it: ┼ where it crosses each frame edge.
func TestTabRuleJoinsFrames(t *testing.T) {
	s, sh := groupHome(t)
	a, _ := seedDoc(t, s, sh, "a.md", "# A")
	s.openDoc(sh, a)
	s.toggleSidePreview(sh)
	rule, below := tabRuleRow(t, s, sh)
	edge := s.sidebarPaneWidth() - 1
	if rule[edge] != '┼' {
		t.Fatalf("the sidebar's legend rule should cross into the tab rule at %d: %q", edge, string(rule))
	}
	if !strings.ContainsRune(string(rule[edge+1:]), '┼') || rule[len(rule)-1] != '┤' {
		t.Fatalf("the preview's legend rule should continue the tab rule: %q", string(rule))
	}
	if strings.ContainsRune(string(below), '─') {
		t.Fatalf("a second rule under the tab rule: %q", string(below))
	}
	if !strings.Contains(string(rule[edge+1:]), "──") {
		t.Fatalf("rule = %q", string(rule))
	}
}

// TestTabRuleCrossesGroupDivider: split groups share one rule, and the divider crosses it.
func TestTabRuleCrossesGroupDivider(t *testing.T) {
	s, sh := groupHome(t)
	seedDoc(t, s, sh, "a.md", "# A")
	b, _ := seedDoc(t, s, sh, "b.md", "# B")
	s.openDoc(sh, b)
	s.moveTab(sh, 1)
	rule, _ := tabRuleRow(t, s, sh)
	if col := s.groups()[1].openTabs.x; rule[col] != '┼' {
		t.Fatalf("no crossing at the divider %d: %q", col, string(rule))
	}
}

// TestTabRuleCapturesMouse: a press on the rule row is the bar's — the editor below neither
// moves its cursor nor loses focus.
func TestTabRuleCapturesMouse(t *testing.T) {
	model, s, sh := newHomeRouter(t, Options{})
	id, _ := seedDoc(t, s, sh, "a.md", "one\ntwo\nthree\n")
	s.openDoc(sh, id)
	s.modular.FocusSlot(s.panelSlot(s.editorPanel))
	model, _ = model.Update(keyMsg("down"))
	model, _ = model.Update(keyMsg("down"))
	_ = model.(core.Router).View()
	pos := s.editor.CursorPosition()
	y := s.openTabs.y + 1
	for _, msg := range []tea.Msg{
		tea.MouseClickMsg{X: s.openTabs.x + 5, Y: y, Button: tea.MouseLeft},
		tea.MouseReleaseMsg{X: s.openTabs.x + 5, Y: y, Button: tea.MouseLeft},
	} {
		model, _ = model.Update(msg)
		if s.focusedPane() != s.editorPanel || s.editor.CursorPosition() != pos {
			t.Fatalf("%T on the tab rule reached the editor", msg)
		}
	}
}
