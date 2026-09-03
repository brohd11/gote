package app

import (
	"strings"
	"testing"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// panelRows splits a rendered panel into its display rows.
func panelRows(t *testing.T, out string) []string {
	t.Helper()
	if out == "" {
		t.Fatal("the panel rendered nothing")
	}
	return strings.Split(out, "\n")
}

// assertOpaquePanel is the whole readability contract: a border so the panel reads as
// foreground, and equal-width rows so it is opaque. core.Composite punches a hole per line
// at that line's own width, so a ragged panel lets the editor show through beside every
// short line — which is exactly how the unboxed tooltip looked.
func assertOpaquePanel(t *testing.T, out string) {
	t.Helper()
	rows := panelRows(t, out)
	if len(rows) < 3 {
		t.Fatalf("panel has %d rows, want content between two border rows:\n%s", len(rows), out)
	}
	width := ansi.StringWidth(rows[0])
	for i, row := range rows {
		if got := ansi.StringWidth(row); got != width {
			t.Errorf("row %d is %d cells wide, want %d — a ragged row is a transparent row:\n%s",
				i, got, width, stripANSI(out))
		}
	}
	if !strings.Contains(rows[0], "╭") || !strings.Contains(rows[len(rows)-1], "╯") {
		t.Errorf("the panel drew no border:\n%s", stripANSI(out))
	}
}

func TestHoverPanelIsBoxedAndOpaque(t *testing.T) {
	s, _ := newHome(t) // 100x30, sidebar up
	s.applyHover(&lspRequestResult{
		kind: lspReqHover, path: s.currentPath,
		hover: "```go\nfunc Reveal(p EditorPosition) bool\n```\n\nMoves the caret and scrolls it into view.",
	})
	if s.hover.popup == nil {
		t.Fatal("a hover result should have installed a tooltip")
	}
	assertOpaquePanel(t, s.hover.popup.View())

	// Empty content installs nothing rather than an empty box.
	s.applyHover(&lspRequestResult{kind: lspReqHover, path: s.currentPath, hover: "   "})
	if s.hover.popup != nil {
		t.Error("blank hover content should leave no tooltip")
	}
}

// TestCaretPanelShiftsInsteadOfPinningLeft is the regression test for the reported bug: a
// panel wider than the caret's column used to right-align to a negative x, which the
// compositor clamped to column 0 — the tooltip appeared at the far left of the screen,
// nowhere near the symbol.
func TestCaretPanelShiftsInsteadOfPinningLeft(t *testing.T) {
	const frameW, frameH, leftBound = 100, 30, 30
	place := caretPanel(80, 10, leftBound, false)

	// A 60-cell panel cannot start at column 80, so it slides left exactly far enough.
	x, y := place(frameW, frameH, 60, 8)
	if x != frameW-60 {
		t.Errorf("a wide panel placed at x=%d, want it shifted to %d — not pinned left", x, frameW-60)
	}
	if x < leftBound {
		t.Errorf("the panel crossed onto the sidebar at x=%d (bound %d)", x, leftBound)
	}
	if y != 11 {
		t.Errorf("y=%d, want the row below the caret", y)
	}

	// A panel that fits starts exactly at the caret.
	if x, _ := place(frameW, frameH, 15, 4); x != 80 {
		t.Errorf("a fitting panel placed at x=%d, want the caret's column 80", x)
	}

	// Near the left edge the panel still starts at the caret, never left of the editor.
	if x, _ := caretPanel(31, 10, leftBound, false)(frameW, frameH, 40, 4); x != 31 {
		t.Errorf("x=%d, want the caret's column 31", x)
	}
	if x, _ := caretPanel(0, 10, leftBound, false)(frameW, frameH, 40, 4); x != leftBound {
		t.Errorf("x=%d, want the editor's left edge %d", x, leftBound)
	}
	// Honoring the left bound must never push the box off the right edge.
	if x, _ := caretPanel(0, 10, leftBound, false)(frameW, frameH, 95, 4); x != frameW-95 {
		t.Errorf("x=%d, want %d — fitting on screen outranks the left bound", x, frameW-95)
	}
}

func TestCaretPanelPicksTheSideThatFits(t *testing.T) {
	const frameW, frameH = 100, 30

	// Hover prefers below, and flips above at the bottom edge.
	if _, y := caretPanel(10, 5, 0, false)(frameW, frameH, 20, 8); y != 6 {
		t.Errorf("hover y=%d, want the row below the caret", y)
	}
	if _, y := caretPanel(10, 28, 0, false)(frameW, frameH, 20, 8); y != 20 {
		t.Errorf("hover at the bottom y=%d, want it above the caret at 20", y)
	}

	// Signature prefers above, so it does not collide with a completion list below the
	// same caret, and drops below at the top edge.
	if _, y := caretPanel(10, 12, 0, true)(frameW, frameH, 20, 4); y != 8 {
		t.Errorf("signature y=%d, want it above the caret at 8", y)
	}
	if _, y := caretPanel(10, 1, 0, true)(frameW, frameH, 20, 4); y != 2 {
		t.Errorf("signature at the top y=%d, want it below the caret at 2", y)
	}

	// A panel taller than the frame still lands on screen rather than at a negative row.
	if _, y := caretPanel(10, 5, 0, true)(frameW, 6, 20, 40); y != 0 {
		t.Errorf("an oversized panel placed at y=%d, want 0", y)
	}
}

func TestSignaturePanelIsBoxedAndOpaque(t *testing.T) {
	s, _ := newHome(t)
	s.applySignature(&lspRequestResult{
		kind: lspReqSignature, path: s.currentPath,
		signature: &lspSignature{
			Label:      "Reveal(p EditorPosition) bool",
			Doc:        "Moves the caret and scrolls it into view.",
			Parameters: []lspParameter{{Label: "p EditorPosition", Start: 7, End: 23}},
			Active:     0,
		},
	})
	if s.signature.popup == nil {
		t.Fatal("a signature result should have installed a hint")
	}
	assertOpaquePanel(t, s.signature.popup.View())
	if !strings.Contains(stripANSI(s.signature.popup.View()), "p EditorPosition") {
		t.Errorf("the active parameter is missing:\n%s", stripANSI(s.signature.popup.View()))
	}
}

// TestPanelWidthLeavesRoomForTheBox: the chrome is budgeted BEFORE the content is wrapped,
// so a rendered panel always fits between the editor's left edge and the frame.
func TestPanelWidthLeavesRoomForTheBox(t *testing.T) {
	s, _ := newHome(t) // w=100, sidebar up ⇒ editorLeft 30
	if got := s.editorLeft(); got != sidebarWidth {
		t.Fatalf("editorLeft with the sidebar up = %d, want %d", got, sidebarWidth)
	}
	width := s.panelWidth(hoverMaxWidth)
	if rendered := width + panelChrome; rendered > s.w-s.editorLeft() {
		t.Errorf("a %d-cell panel does not fit the %d cells right of the editor's edge",
			rendered, s.w-s.editorLeft())
	}
	// Hiding the sidebar gives the panel the whole frame, capped by hoverMaxWidth.
	s.setSidebar(false)
	if got := s.editorLeft(); got != 0 {
		t.Errorf("editorLeft with the sidebar hidden = %d, want 0", got)
	}
	if got := s.panelWidth(hoverMaxWidth); got != hoverMaxWidth {
		t.Errorf("panelWidth = %d, want it capped at %d", got, hoverMaxWidth)
	}
	// A pathologically narrow frame still yields a usable floor rather than a negative.
	s.w = 10
	if got := s.panelWidth(hoverMaxWidth); got < 1 {
		t.Errorf("panelWidth on a narrow frame = %d, want a positive floor", got)
	}
}

// TestHoverRendersOverTheEditor drives the real View path: the tooltip must appear in the
// composited frame, and to the right of the sidebar rather than on top of it.
func TestHoverRendersOverTheEditor(t *testing.T) {
	s, sh := newHome(t)
	path, ed := seedDoc(t, s, sh, "wide.py", strings.Repeat("value = 1\n", 40))
	s.openDoc(sh, path)
	s.modular.FocusSlot(s.editorSlot())
	s.SetSize(sh, 100, 30)
	_ = s.View(sh) // publish the pane origins CursorAnchor needs
	ed.Reveal(components.EditorPosition{Line: 3, Column: 8})

	s.applyHover(&lspRequestResult{kind: lspReqHover, path: path, hover: "value is an int"})
	out := stripANSI(s.View(sh))
	if !strings.Contains(out, "value is an int") {
		t.Fatalf("the tooltip did not reach the frame:\n%s", out)
	}
	for _, row := range strings.Split(out, "\n") {
		if idx := strings.Index(row, "╭"); idx >= 0 {
			if idx < s.editorLeft() {
				t.Errorf("the tooltip's left edge is at column %d, left of the editor's %d:\n%s",
					idx, s.editorLeft(), out)
			}
			return
		}
	}
	t.Errorf("no panel border found in the frame:\n%s", out)
}

// hoverResult is the answer a server would send for the symbol at the caret.
func hoverResult(path string) *lspRequestResult {
	return &lspRequestResult{kind: lspReqHover, path: path, hover: "value is an int"}
}

// TestDismissHoverOnlyOnNewIntent: a press, wheel notch, key or paste retires the tooltip;
// a release or a drag-motion does not, because those end a gesture rather than start one.
// The release case is the bug — see dismissHoverOn's comment.
func TestDismissHoverOnlyOnNewIntent(t *testing.T) {
	s, _ := newHome(t)
	install := func() {
		s.applyHover(hoverResult(s.currentPath))
		if s.hover.popup == nil {
			t.Fatal("failed to install a tooltip")
		}
	}

	for name, msg := range map[string]tea.Msg{
		"release": tea.MouseReleaseMsg{X: 4, Y: 4, Button: tea.MouseLeft},
		"motion":  tea.MouseMotionMsg{X: 4, Y: 4, Button: tea.MouseLeft},
	} {
		install()
		s.dismissHoverOn(msg)
		if s.hover.popup == nil {
			t.Errorf("%s closed the tooltip; it is the tail of a gesture, not a new intent", name)
		}
	}

	for name, msg := range map[string]tea.Msg{
		"press": tea.MouseClickMsg{X: 4, Y: 4, Button: tea.MouseLeft},
		"wheel": tea.MouseWheelMsg{X: 4, Y: 4, Button: tea.MouseWheelDown},
		"key":   keyMsg("x"),
		"paste": tea.PasteMsg{Content: "text"},
	} {
		install()
		s.dismissHoverOn(msg)
		if s.hover.popup != nil {
			t.Errorf("%s left the tooltip up; it is a new intent and should retire it", name)
		}
	}
}

// TestHoverSurvivesATrailingRelease drives the real router. The context menu's Hover row
// fires on the PRESS and pops the menu with it, so the matching RELEASE reaches the home
// screen a moment later — after the server has answered. Ordered here the way it loses.
func TestHoverSurvivesATrailingRelease(t *testing.T) {
	model, s, sh := newHomeRouter(t, Options{})
	path, ed := seedDoc(t, s, sh, "wide.go", strings.Repeat("value := 1\n", 30))
	s.openDoc(sh, path)
	s.modular.FocusSlot(s.editorSlot())
	_ = view(model)
	ed.Reveal(components.EditorPosition{Line: 3, Column: 4})
	_ = view(model)

	// The server's answer lands first — the race the trailing release used to win.
	s.applyHover(hoverResult(path))
	if s.hover.popup == nil {
		t.Fatal("the hover result installed no tooltip")
	}
	if !strings.Contains(view(model), "value is an int") {
		t.Fatal("the tooltip is not in the frame before the release")
	}

	model, _ = model.Update(tea.MouseReleaseMsg{X: 40, Y: 8, Button: tea.MouseLeft})
	if out := view(model); !strings.Contains(out, "value is an int") {
		t.Errorf("the trailing release closed the tooltip:\n%s", out)
	}

	// A genuinely new gesture still retires it.
	model, _ = model.Update(tea.MouseClickMsg{X: 40, Y: 8, Button: tea.MouseLeft})
	if out := view(model); strings.Contains(out, "value is an int") {
		t.Errorf("a fresh press left the tooltip up:\n%s", out)
	}
}

// TestContextMenuHoverRowKeepsItsTooltip is the reported bug end to end: right-click the
// editor, pick Hover info, let the answer arrive, then deliver the release that belongs to
// the click that picked the row.
func TestContextMenuHoverRowKeepsItsTooltip(t *testing.T) {
	model, s, sh := newHomeRouter(t, Options{})
	path, ed := seedDoc(t, s, sh, "menu.go", strings.Repeat("value := 1\n", 30))
	s.openDoc(sh, path)
	s.modular.FocusSlot(s.editorSlot())
	_ = view(model)
	ed.Reveal(components.EditorPosition{Line: 3, Column: 4})
	_ = view(model)

	model, _ = model.Update(tea.MouseClickMsg{X: 45, Y: 8, Button: tea.MouseRight})
	menu, ok := model.(core.Router).Top().(*components.MenuScreen)
	if !ok {
		t.Fatalf("a right click should raise the editor menu, top is %T", model.(core.Router).Top())
	}
	_ = menu
	// Click the row where it is actually drawn, which is what a user does — and what makes
	// this exercise MenuScreen's press-activates-and-pops path rather than a keyboard pick.
	x, y := rowCellIn(t, view(model), "Hover info")
	model, _ = model.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	if _, stillUp := model.(core.Router).Top().(*components.MenuScreen); stillUp {
		t.Fatalf("clicking a row should have popped the menu:\n%s", view(model))
	}

	// The server answers before the user lifts the button — the losing order.
	s.applyHover(hoverResult(path))
	model, _ = model.Update(tea.MouseReleaseMsg{X: 45, Y: 9, Button: tea.MouseLeft})
	if out := view(model); !strings.Contains(out, "value is an int") {
		t.Errorf("the release trailing the menu click closed the tooltip:\n%s", out)
	}
}

// rowCellIn locates a menu row's label in a rendered frame and returns a cell inside it.
// The menu hit-tests in absolute terminal cells against its own placement, so the frame's
// own coordinates are the ones to click.
func rowCellIn(t *testing.T, frame, label string) (int, int) {
	t.Helper()
	for y, line := range strings.Split(frame, "\n") {
		if x := strings.Index(line, label); x >= 0 {
			return x, y
		}
	}
	t.Fatalf("no row labelled %q in the frame:\n%s", label, frame)
	return 0, 0
}
