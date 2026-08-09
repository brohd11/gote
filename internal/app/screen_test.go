package app

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/brohd11/bubblestack/core"

	tea "github.com/charmbracelet/bubbletea"
)

// newHome builds the real home screen against an empty temp store — the same
// wiring Run assembles, minus the bubbletea program.
func newHome(t *testing.T) (*homeScreen, *core.Shared) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	sh := core.NewShared(New("test", DefaultConfig(), false, "", 0))
	s := NewHomeScreen(sh).(*homeScreen)
	s.Init(sh)
	s.SetSize(sh, 100, 30)
	return s, sh
}

// ansiSGR matches the color escapes the render is dressed in, so assertions can be
// made against the plain text underneath.
var ansiSGR = regexp.MustCompile("\x1b\\[[0-9;]*m")

func stripANSI(s string) string { return ansiSGR.ReplaceAllString(s, "") }

// focusedPane names which pane holds focus, read off the help bar: the sidebar
// lists contribute their select/filter hints (ListPanel.PanelHelp) while the
// editor's ScreenPanel contributes none, so "filter" present means a list is
// focused and absent means the editor is. It is the only focus signal the app
// package can see — ModularScreen's index is another package's unexported field.
func focusedPane(s *homeScreen, sh *core.Shared) string {
	if strings.Contains(s.HelpView(sh), "filter") {
		return "list"
	}
	return "editor"
}

// TestHomePaneNavigation walks gote's actual grid — [[docs, open], [editor]], so
// flat order [docs, open, editor] — with the pane cycle, and confirms the editor
// types a tab once focus is on it. This is the whole point of the change: before
// it, the editor's capture ate every key including tab, so the pane could only be
// left through the ctrl+x exit hook and tab did nothing at all.
func TestHomePaneNavigation(t *testing.T) {
	s, sh := newHome(t)

	shiftRight := tea.KeyMsg{Type: tea.KeyShiftRight}
	shiftLeft := tea.KeyMsg{Type: tea.KeyShiftLeft}

	if got := focusedPane(s, sh); got != "list" {
		t.Fatalf("focus should start on the docs list, got %s", got)
	}
	s.Update(sh, shiftRight) // docs → open, still a list
	if got := focusedPane(s, sh); got != "list" {
		t.Fatalf("the first step should land on the open list, got %s", got)
	}
	s.Update(sh, shiftRight) // open → editor
	if got := focusedPane(s, sh); got != "editor" {
		t.Fatalf("the second step should reach the editor pane, got %s", got)
	}

	// The editor now owns tab. Read the result off the render, which is all the app
	// package can see of the buffer: a tab expands to editorTabWidth (4) spaces, and
	// the trailing rune proves the tab landed between them rather than being dropped.
	s.Update(sh, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hi")})
	s.Update(sh, tea.KeyMsg{Type: tea.KeyTab})
	s.Update(sh, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("X")})
	v := s.View(sh)
	if strings.Contains(v, "\t") {
		t.Fatal("the rendered screen must never carry a raw tab")
	}
	if !strings.Contains(stripANSI(v), "hi    X") {
		t.Fatalf("tab should have typed a tab between hi and X; render:\n%s", stripANSI(v))
	}

	s.Update(sh, shiftLeft) // back out of the capturing pane, keyboard-only
	if got := focusedPane(s, sh); got != "list" {
		t.Fatalf("shift+left must escape the capturing editor pane, got %s", got)
	}
	// The cycle wraps: one more forward step from the open list returns to the
	// editor, and the buffer typed into it is still there.
	s.Update(sh, shiftRight)
	if got := focusedPane(s, sh); got != "editor" {
		t.Fatalf("the cycle should return to the editor, got %s", got)
	}
	if !strings.Contains(stripANSI(s.View(sh)), "hi    X") {
		t.Fatal("the editor buffer should survive leaving and re-entering the pane")
	}
}

// TestHomePaneNavigationWithoutSidebar: ctrl+b leaves a single-pane grid, where the
// pane keys have nowhere to go. They must stay consumed rather than falling through
// to the editor, and ctrl+x must still be the way back to the sidebar.
func TestHomePaneNavigationWithoutSidebar(t *testing.T) {
	s, sh := newHome(t)

	s.Update(sh, tea.KeyMsg{Type: tea.KeyCtrlB})
	if s.sidebar {
		t.Fatal("ctrl+b should hide the sidebar")
	}
	if got := focusedPane(s, sh); got != "editor" {
		t.Fatalf("the lone editor pane should hold focus, got %s", got)
	}
	s.Update(sh, tea.KeyMsg{Type: tea.KeyShiftLeft})
	if got := focusedPane(s, sh); got != "editor" {
		t.Fatalf("a pane key with nowhere to go should be a no-op, got %s", got)
	}
}

// TestEditorExitClosesDoc: the editor's exit hook (every ctrl+x path — clean, saved,
// discarded) closes the current doc: the open set loses it and the pane swaps to the
// next open doc, or to a fresh scratch buffer when none remain. Driven through the
// router-facing Update, so the hook's pane swap runs inside the editor child's own
// Update — where ScreenPanel's bookkeeping used to clobber it.
func TestEditorExitClosesDoc(t *testing.T) {
	s, sh := newHome(t)
	c := Of(sh)
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	b := filepath.Join(dir, "b.txt")

	s.openDoc(sh, a)
	s.openDoc(sh, b) // current: b; open order [a, b]; the editor pane holds focus

	s.Update(sh, tea.KeyMsg{Type: tea.KeyCtrlX}) // clean buffer: closes b
	s.Receive(sh, ReseedMsg{})                   // the router applies the hook's broadcast
	if _, ok := c.Open[b]; ok {
		t.Fatal("the exited doc must leave the open set")
	}
	if s.currentPath != a {
		t.Fatalf("the pane should switch to the remaining doc %q, got %q", a, s.currentPath)
	}
	if v := stripANSI(s.View(sh)); strings.Contains(v, "b.txt") || !strings.Contains(v, "• a.txt") {
		t.Fatalf("the pane should show %q's editor and the dot should follow it, render:\n%s", a, v)
	}

	s.modular.FocusSlot(s.editorSlot()) // the exit focused the docs list; go back
	s.Update(sh, tea.KeyMsg{Type: tea.KeyCtrlX}) // closes a: none remain
	s.Receive(sh, ReseedMsg{})
	if s.currentPath != "" || len(c.OpenDocs()) != 0 {
		t.Fatalf("the last exit should clear everything: path %q, open %v", s.currentPath, c.OpenDocs())
	}
	if !strings.Contains(stripANSI(s.View(sh)), "Editor") {
		t.Fatalf("the pane should show a fresh scratch editor, render:\n%s", stripANSI(s.View(sh)))
	}
}
