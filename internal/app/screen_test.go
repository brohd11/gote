package app

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// newHome builds the real home screen against an empty temp store — the same
// wiring Run assembles, minus the bubbletea program.
func newHome(t *testing.T) (*homeScreen, *core.Shared) {
	t.Helper()
	return newHomeWith(t, Options{})
}

// newHomeWith is newHome for a specific launch mode (see TestMinimalMode).
func newHomeWith(t *testing.T, opts Options) (*homeScreen, *core.Shared) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	sh := core.NewShared(New("test", DefaultConfig(), opts))
	s := NewHomeScreen(sh).(*homeScreen)
	s.Init(sh)
	s.SetSize(sh, 100, 30)
	return s, sh
}

// newHomeRouter builds the same screen through the real router. Tests that exercise
// pushed overlays need this path: calling homeScreen.Update directly returns the
// navigation Action but cannot apply it to a stack.
func newHomeRouter(t *testing.T, opts Options) (tea.Model, *homeScreen, *core.Shared) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	sh := core.NewShared(New("test", DefaultConfig(), opts))
	r := core.NewRouter(sh, []core.TabEntry{
		{Title: "Editor", New: func(sh *core.Shared) core.Screen { return NewHomeScreen(sh) }},
	})
	r.Init()
	var model tea.Model = r
	model, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return model, model.(core.Router).Top().(*homeScreen), sh
}

// ansiSGR matches the color escapes the render is dressed in, so assertions can be
// made against the plain text underneath.
var ansiSGR = regexp.MustCompile("\x1b\\[[0-9;]*m")

func stripANSI(s string) string { return ansiSGR.ReplaceAllString(s, "") }

// typeMarkdownBullets enters n list items using the editor's Markdown continuation:
// the first line supplies the marker and each Enter supplies the next one. Clear the
// final unused marker so the fixture ends with the same blank line as manually typed
// source did before continuation existed.
func typeMarkdownBullets(s *homeScreen, sh *core.Shared, n int) {
	if n <= 0 {
		return
	}
	s.Update(sh, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("- item")})
	for i := 0; i < n; i++ {
		s.Update(sh, tea.KeyMsg{Type: tea.KeyEnter})
		if i+1 < n {
			s.Update(sh, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("item")})
		}
	}
	s.Update(sh, tea.KeyMsg{Type: tea.KeyBackspace})
	s.Update(sh, tea.KeyMsg{Type: tea.KeyBackspace})
}

// focusedPane names which pane holds focus, read off the help bar: the sidebar
// lists contribute their select hint (ListPanel.PanelHelp) while the editor's
// ScreenPanel contributes none, so "select" present means a list is focused and
// absent means the editor is. Nothing else on gote's bar (panes / back / ? more)
// carries the word. It is the only focus signal the app package can see —
// ModularScreen's index is another package's unexported field.
func focusedPane(s *homeScreen, sh *core.Shared) string {
	if strings.Contains(s.HelpView(sh), "select") {
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

	paneNext := tea.KeyMsg{Type: tea.KeyShiftTab}

	if got := focusedPane(s, sh); got != "list" {
		t.Fatalf("focus should start on the docs list, got %s", got)
	}
	s.Update(sh, paneNext) // docs → open, still a list
	if got := focusedPane(s, sh); got != "list" {
		t.Fatalf("the first step should land on the open list, got %s", got)
	}
	s.Update(sh, paneNext) // open → editor
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

	// Forward off the editor wraps to the docs list: with the cycle forward-only, the
	// wrap IS the way out of a capturing pane, keyboard-only.
	s.Update(sh, paneNext)
	if got := focusedPane(s, sh); got != "list" {
		t.Fatalf("the cycle must escape the capturing editor pane, got %s", got)
	}
	// Round again to the editor, and the buffer typed into it is still there.
	s.Update(sh, paneNext)
	s.Update(sh, paneNext)
	if got := focusedPane(s, sh); got != "editor" {
		t.Fatalf("the cycle should return to the editor, got %s", got)
	}
	if !strings.Contains(stripANSI(s.View(sh)), "hi    X") {
		t.Fatal("the editor buffer should survive leaving and re-entering the pane")
	}
}

// TestHomeShiftTabLeavesEditor is the gote-side regression for shift+tab joining
// PaneNext: on the editor pane it must move focus like shift+→ instead of typing the
// tab its standalone key handler still maps it to. Bare tab keeps typing — that
// split is the whole reason the alias could be given up.
func TestHomeShiftTabLeavesEditor(t *testing.T) {
	s, sh := newHome(t)

	shiftTab := tea.KeyMsg{Type: tea.KeyShiftTab}
	s.Update(sh, shiftTab) // docs → open
	s.Update(sh, shiftTab) // open → editor
	if got := focusedPane(s, sh); got != "editor" {
		t.Fatalf("two shift+tab steps should reach the editor pane, got %s", got)
	}

	s.Update(sh, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hi")})
	s.Update(sh, shiftTab) // must escape, not indent
	if got := focusedPane(s, sh); got != "list" {
		t.Fatalf("shift+tab must escape the capturing editor pane, got %s", got)
	}

	// Back to the editor (the cycle wraps through both lists) and type on: the two
	// runes must be adjacent. A tab would have expanded to editorTabWidth spaces
	// between them, exactly as TestHomePaneNavigation asserts for bare tab.
	s.Update(sh, shiftTab)
	s.Update(sh, shiftTab)
	if got := focusedPane(s, sh); got != "editor" {
		t.Fatalf("the cycle should return to the editor, got %s", got)
	}
	s.Update(sh, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("X")})
	v := stripANSI(s.View(sh))
	if strings.Contains(v, "hi    X") {
		t.Fatalf("shift+tab typed a tab instead of moving panes; render:\n%s", v)
	}
	if !strings.Contains(v, "hiX") {
		t.Fatalf("the buffer should read hiX; render:\n%s", v)
	}
}

// TestThemeChangeKeepsEditor drives the framework's real theme broadcast through
// the router. gote's root is stateful: rebuilding it used to leave the replacement
// ScreenPanel uninitialized, so both the editor and its unsaved buffer disappeared
// while the ordinary list panels kept rendering.
func TestThemeChangeKeepsEditor(t *testing.T) {
	prev := core.CurrentTheme()
	t.Cleanup(func() { core.SetTheme(prev) })
	t.Setenv("HOME", t.TempDir())

	sh := core.NewShared(New("test", DefaultConfig(), Options{}))
	r := core.NewRouter(sh, []core.TabEntry{
		{Title: "Editor", New: func(sh *core.Shared) core.Screen { return NewHomeScreen(sh) }},
	})
	r.Init()
	var model tea.Model = r
	model, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyShiftTab}) // docs -> open
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyShiftTab}) // open -> editor
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("unsaved theme text")})

	if !strings.Contains(stripANSI(model.View()), "unsaved theme text") {
		t.Fatal("test setup did not render the unsaved editor buffer")
	}

	next := "mono"
	if prev == next {
		next = "godot"
	}
	model, _ = model.Update(core.ApplyTheme(next))

	view := stripANSI(model.View())
	if !strings.Contains(view, "unsaved theme text") {
		t.Fatalf("theme change must keep the live editor and its unsaved buffer, render:\n%s", view)
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
	s.Update(sh, tea.KeyMsg{Type: tea.KeyShiftTab})
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
	if _, ok := c.open.byPath[b]; ok {
		t.Fatal("the exited doc must leave the open set")
	}
	if s.currentPath != a {
		t.Fatalf("the pane should switch to the remaining doc %q, got %q", a, s.currentPath)
	}
	if v := stripANSI(s.View(sh)); strings.Contains(v, "b.txt") || !strings.Contains(v, "• a.txt") {
		t.Fatalf("the pane should show %q's editor and the dot should follow it, render:\n%s", a, v)
	}

	s.modular.FocusSlot(s.editorSlot())          // the exit focused the docs list; go back
	s.Update(sh, tea.KeyMsg{Type: tea.KeyCtrlX}) // closes a: none remain
	s.Receive(sh, ReseedMsg{})
	if s.currentPath != "" || len(c.OpenDocs()) != 0 {
		t.Fatalf("the last exit should clear everything: path %q, open %v", s.currentPath, c.OpenDocs())
	}
	if !strings.Contains(stripANSI(s.View(sh)), "Editor") {
		t.Fatalf("the pane should show a fresh scratch editor, render:\n%s", stripANSI(s.View(sh)))
	}
}

// TestEditorEscReleasesFocus: esc hands the keys back to the docs list WITHOUT
// closing the buffer — the distinction from ctrl+x, which closes it. The editor
// captures every printable key, so before the OnRelease hook the only ways out were
// the shift+← pane chord and that destructive ctrl+x.
func TestEditorEscReleasesFocus(t *testing.T) {
	s, sh := newHome(t)
	c := Of(sh)
	path := filepath.Join(t.TempDir(), "a.txt")

	s.openDoc(sh, path)
	if got := focusedPane(s, sh); got != "editor" {
		t.Fatalf("opening a doc should focus the editor, got %s", got)
	}

	s.Update(sh, tea.KeyMsg{Type: tea.KeyEsc})
	if got := focusedPane(s, sh); got != "list" {
		t.Fatalf("esc should hand focus to the docs list, got %s", got)
	}
	if _, ok := c.open.byPath[path]; !ok {
		t.Fatal("esc must not close the buffer")
	}
	if s.currentPath != path {
		t.Fatalf("esc must not change the current doc, got %q", s.currentPath)
	}
}

// TestEditorEscUnhidesSidebar: with the sidebar hidden the editor is the only pane,
// so releasing focus has to bring back somewhere to release it TO — the same reason
// the ctrl+x path unhides.
func TestEditorEscUnhidesSidebar(t *testing.T) {
	s, sh := newHome(t)
	s.openDoc(sh, filepath.Join(t.TempDir(), "a.txt"))
	s.Update(sh, tea.KeyMsg{Type: tea.KeyCtrlB})
	if s.sidebar {
		t.Fatal("ctrl+b should have hidden the sidebar")
	}

	s.Update(sh, tea.KeyMsg{Type: tea.KeyEsc})
	if !s.sidebar {
		t.Fatal("esc should unhide the sidebar so there is a pane to focus")
	}
	if got := focusedPane(s, sh); got != "list" {
		t.Fatalf("esc should land on the docs list, got %s", got)
	}
}

// TestEscOverExitPromptStillCancels: the editor's own save/discard/cancel prompt
// claims esc first, so ctrl+x on a dirty buffer keeps its cancel instead of the
// release hook firing over it.
func TestEscOverExitPromptStillCancels(t *testing.T) {
	s, sh := newHome(t)
	s.openDoc(sh, filepath.Join(t.TempDir(), "a.txt"))
	s.Update(sh, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("dirty")})
	s.Update(sh, tea.KeyMsg{Type: tea.KeyCtrlX}) // dirty ⇒ the prompt, not an exit
	if !strings.Contains(stripANSI(s.View(sh)), "Save modified buffer?") {
		t.Fatal("a dirty ctrl+x should raise the exit prompt")
	}

	s.Update(sh, tea.KeyMsg{Type: tea.KeyEsc})
	if v := stripANSI(s.View(sh)); strings.Contains(v, "Save modified buffer?") {
		t.Fatal("esc should cancel the prompt")
	}
	if got := focusedPane(s, sh); got != "editor" {
		t.Fatalf("cancelling the prompt must not also release the pane, got %s", got)
	}
}

// TestPreviewCycle walks ctrl+p through its two rungs: off → the live side pane →
// off. Both are layout changes, so neither shows up as a navigation Action.
func TestPreviewCycle(t *testing.T) {
	s, sh := newHome(t)
	s.openDoc(sh, filepath.Join(t.TempDir(), "a.md"))

	ctrlP := tea.KeyMsg{Type: tea.KeyCtrlP}

	for _, step := range []struct {
		mode    int
		legend  bool // is the pane's border title on screen?
		summary string
	}{
		{previewPane, true, "the first ctrl+p opens the pane"},
		{previewOff, false, "the second closes the cycle"},
	} {
		_, act := s.Update(sh, ctrlP)
		if s.preview != step.mode {
			t.Fatalf("%s: mode is %d, want %d", step.summary, s.preview, step.mode)
		}
		// Every rung is a layout change now — nothing touches the router's stack.
		if got := msgType(act); got != "" {
			t.Fatalf("%s: expected no navigation, got %s", step.summary, got)
		}
		v := stripANSI(s.View(sh))
		if got := strings.Contains(v, "preview"); got != step.legend {
			t.Fatalf("%s: pane legend on screen = %v, want %v, render:\n%s",
				step.summary, got, step.legend, v)
		}
		if got := focusedPane(s, sh); got != "editor" {
			t.Fatalf("%s: toggling the preview must not move focus, got %s", step.summary, got)
		}
	}
}

// TestPreviewReopenRerenders: reopening the pane over an UNEDITED buffer must still
// draw. The refresh skips when the source and width are unchanged, which they are
// across a close/open — so setPreview has to clear that cache or the pane comes back
// blank.
func TestPreviewReopenRerenders(t *testing.T) {
	s, sh := newHome(t)
	s.openDoc(sh, filepath.Join(t.TempDir(), "a.md"))
	s.Update(sh, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("# Title")})

	s.Update(sh, tea.KeyMsg{Type: tea.KeyCtrlP}) // open
	if v := stripANSI(s.View(sh)); !strings.Contains(v, "Title") {
		t.Fatalf("the pane should have rendered, got:\n%s", v)
	}
	s.Update(sh, tea.KeyMsg{Type: tea.KeyCtrlP}) // close
	s.Update(sh, tea.KeyMsg{Type: tea.KeyCtrlP}) // open again, same buffer
	if v := stripANSI(s.View(sh)); !strings.Contains(v, "Title") {
		t.Fatalf("the reopened pane should have rendered without an edit, got:\n%s", v)
	}
}

// msgType names an Action's control message, the only handle a consumer package has
// on one (the nav message types are core-internal).
func msgType(act core.Action) string {
	if act.Msg == nil {
		return ""
	}
	return reflect.TypeOf(act.Msg).String()
}

// TestHomeWrapAndLineNums: alt+z toggles soft wrap and ctrl+l the line-number gutter on
// the editor. The editor owns the state; the home screen just forwards the keys.
//
// Every toggle is followed by a render, which is the assertion that matters: the flags
// flipping proves nothing on its own, and a version of this test that only checked them
// passed while the app died on the first frame after the key.
func TestHomeWrapAndLineNums(t *testing.T) {
	s, sh := newHome(t)
	s.openDoc(sh, filepath.Join(t.TempDir(), "a.txt"))
	s.Update(sh, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(strings.Repeat("w", 400))})
	render := func(what string) {
		t.Helper()
		if v := s.View(sh); v == "" {
			t.Fatalf("%s: empty render", what)
		}
	}
	if s.editor.WrapMode() || s.editor.LineNumMode() {
		t.Fatal("both toggles should start off")
	}

	altZ := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}, Alt: true}
	s.Update(sh, altZ)
	if !s.editor.WrapMode() {
		t.Fatal("alt+z should turn wrap on")
	}
	render("wrapped")
	s.Update(sh, altZ)
	if s.editor.WrapMode() {
		t.Fatal("a second alt+z should turn wrap off")
	}
	render("unwrapped")

	s.Update(sh, tea.KeyMsg{Type: tea.KeyCtrlL})
	if !s.editor.LineNumMode() {
		t.Fatal("ctrl+l should turn line numbers on")
	}
	render("numbered")
	s.Update(sh, tea.KeyMsg{Type: tea.KeyCtrlL})
	if s.editor.LineNumMode() {
		t.Fatal("a second ctrl+l should turn line numbers off")
	}
	render("unnumbered")
}

// TestHomeLeavesCtrlWToTheEditor: ctrl+w is the editor's delete-word-back, so the home
// screen must not claim it for anything — which is why wrap is on alt+z.
func TestHomeLeavesCtrlWToTheEditor(t *testing.T) {
	s, sh := newHome(t)
	s.openDoc(sh, filepath.Join(t.TempDir(), "a.txt"))
	s.Update(sh, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("alpha beta")})
	s.Update(sh, tea.KeyMsg{Type: tea.KeyCtrlW})
	if got, want := s.editor.Text(), "alpha "; got != want {
		t.Fatalf("ctrl+w through the home screen = %q, want %q (delete-word-back)", got, want)
	}
	if s.editor.WrapMode() {
		t.Fatal("ctrl+w must not toggle wrap")
	}
}

// TestHomeLeavesClipboardChordsToTheEditor: alt+c/alt+x/alt+v are the editor's clipboard
// verbs, so gote's own alt keys (alt+z wrap, alt+p full preview) must not grow into them.
// The cut is the observable half — the clipboard write itself is async and would shell out
// to pbcopy, so the returned command is left unrun.
func TestHomeLeavesClipboardChordsToTheEditor(t *testing.T) {
	s, sh := newHome(t)
	s.openDoc(sh, filepath.Join(t.TempDir(), "a.txt"))
	s.Update(sh, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("alpha beta")})

	_, act := s.Update(sh, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}, Alt: true})
	if act.Cmd == nil {
		t.Fatal("alt+x should reach the editor and return its clipboard command")
	}
	if got := s.editor.Text(); got != "" {
		t.Fatalf("alt+x through the home screen left %q, want the line cut", got)
	}
	if s.editor.WrapMode() {
		t.Fatal("a clipboard chord must not toggle wrap")
	}
}

// TestHomeEditorSearch pins gote's opt-in wiring and the per-buffer ownership of
// queries. Each open path retains its own EditorScreen, so switching documents must
// swap both the text and its active search rather than leaking one global filter.
func TestHomeEditorSearch(t *testing.T) {
	model, s, sh := newHomeRouter(t, Options{})
	drive := func(msg tea.Msg) { model, _ = model.Update(msg) }
	a := filepath.Join(t.TempDir(), "a.txt")
	b := filepath.Join(t.TempDir(), "b.txt")

	if !s.editorOpts().Search {
		t.Fatal("every editor built by gote should enable shared editor search")
	}
	if opts := s.editorOpts(); !opts.ContextMenu || opts.ContextItems == nil {
		t.Fatalf("every editor built by gote should enable the right-click menu and contribute rows: %+v", opts)
	}
	s.openDoc(sh, a)
	drive(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("alpha body")})
	drive(tea.KeyMsg{Type: tea.KeyCtrlF})
	if overlay := stripANSI(model.View()); !strings.Contains(overlay, "╭") || !strings.Contains(overlay, "find:") {
		t.Fatalf("ctrl+f should show the shared rounded line-edit overlay:\n%s", overlay)
	}
	drive(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("alpha")})
	drive(tea.KeyMsg{Type: tea.KeyEnter})
	if view := stripANSI(model.View()); !strings.Contains(view, "a.txt (*)") || strings.Contains(view, "a.txt (*) · find:") || strings.Index(view, "find: alpha") < strings.Index(view, "a.txt (*)") {
		t.Fatalf("first buffer should retain its query below the editor, not in its title:\n%s", view)
	}

	s.openDoc(sh, b)
	if view := stripANSI(model.View()); strings.Contains(view, "find: alpha") {
		t.Fatalf("a new buffer inherited the previous buffer's search:\n%s", view)
	}
	drive(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("beta body")})
	drive(tea.KeyMsg{Type: tea.KeyCtrlF})
	drive(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("beta")})
	drive(tea.KeyMsg{Type: tea.KeyEnter})

	s.openDoc(sh, a)
	view := stripANSI(model.View())
	if !strings.Contains(view, "find: alpha") || strings.Contains(view, "find: beta") {
		t.Fatalf("switching back should restore a's search only:\n%s", view)
	}
	if help := s.helpText(); !strings.Contains(help, "ctrl+f") || !strings.Contains(help, "search") {
		t.Fatalf("gote shortcut help should advertise editor search:\n%s", help)
	}
}

// TestHelpOverlayIsTheCompleteReference pins the split the bar and the overlay now make:
// the bar names only the way in ("? more"), so every app key has to be written in the
// overlay or it is written nowhere. It also pins the notation — one modifier spelling
// across gote's own chords and the editor's, which is the point of listing them together.
func TestHelpOverlayIsTheCompleteReference(t *testing.T) {
	_, s, sh := newHomeRouter(t, Options{})

	help := s.helpText()
	for _, want := range []string{
		"panes", "back", "select", // navigation, the hints the bar still shows
		"filter", // navigation too, but off the bar — the overlay is its only home
		"ctrl+b", "sidebar", "actions", // moved off the bar
		"ctrl+r", "rename", "ctrl+d", "delete", // the docs list's own keys, also off the bar
		"alt+p", "alt+z", // gote's alt chords
		"alt+c", "alt+v", "alt+backspace", // the editor's, via HelpBindings
	} {
		if !strings.Contains(help, want) {
			t.Fatalf("the ? overlay is the only place these keys are written; missing %q:\n%s", want, help)
		}
	}
	if strings.Contains(help, "⌥") {
		t.Fatalf("alt chords should be spelled \"alt+\" throughout, not ⌥:\n%s", help)
	}

	// The bar keeps the pointer and drops everything the overlay covers. Checked with the
	// docs list focused, the state that used to carry the most: rename plus all three.
	bar := stripANSI(s.HelpView(sh))
	if !strings.Contains(bar, "more") {
		t.Fatalf("the bar should still point at the overlay:\n%s", bar)
	}
	for _, gone := range []string{"sidebar", "actions", "rename"} {
		if strings.Contains(bar, gone) {
			t.Fatalf("%q belongs in the overlay, not the bar:\n%s", gone, bar)
		}
	}
}

func TestMinimalEditorSearchKeepsTitleRow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "single.md")
	model, _, _ := newHomeRouter(t, Options{Mode: ModeFile, File: path})
	drive := func(msg tea.Msg) { model, _ = model.Update(msg) }
	drive(tea.KeyMsg{Type: tea.KeyCtrlF})
	drive(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("needle")})
	drive(tea.KeyMsg{Type: tea.KeyEnter})
	view := stripANSI(model.View())
	if !strings.Contains(view, "single.md") || strings.Contains(view, "single.md · find:") {
		t.Fatalf("chrome-less file mode should keep an ordinary editor title:\n%s", view)
	}
	lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
	findRow := -1
	for i, line := range lines {
		if strings.Contains(line, "find: needle") {
			findRow = i
			break
		}
	}
	if findRow != len(lines)-2 || findRow < 1 || !strings.Contains(lines[findRow-1], "╭") || !strings.Contains(lines[findRow+1], "╰") {
		t.Fatalf("chrome-less file mode should retain a rounded search bar at the bottom:\n%s", view)
	}
}

// TestPreviewPaneTracksEdits: the side pane re-renders as the buffer changes, which
// is the whole reason it exists next to the editor rather than over it.
func TestPreviewPaneTracksEdits(t *testing.T) {
	s, sh := newHome(t)
	s.openDoc(sh, filepath.Join(t.TempDir(), "a.md"))
	s.Update(sh, tea.KeyMsg{Type: tea.KeyCtrlP})

	s.Update(sh, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("# Title")})
	v := stripANSI(s.View(sh))
	// The editor shows the source and the preview the render, so the marker appears
	// once (the editor) and the bare text twice.
	if strings.Count(v, "# Title") != 1 {
		t.Fatalf("the preview should render the heading, not echo its marker:\n%s", v)
	}
	if strings.Count(v, "Title") < 2 {
		t.Fatalf("the pane should show the rendered heading, render:\n%s", v)
	}
}

// TestPreviewFollowsEditorScroll pins the three properties the sync guarantees at the
// ends and in between: the top of the render is visible when the editor is at the top,
// the pane is bottomed out when the editor is, and scrolling down never moves the pane
// back up (the eased handoffs must not jitter). The pane opens synced to wherever the
// editor already is.
func TestPreviewFollowsEditorScroll(t *testing.T) {
	s, sh := newHome(t)
	s.openDoc(sh, filepath.Join(t.TempDir(), "a.md"))

	// Bullets, not bare prose: the custom reader joins consecutive paragraph lines
	// into one re-flowed block, which would leave the pane too short to scroll.
	// Typed with real Enters — rune input carries no newlines, and a file load is a
	// cmd no test runs.
	typeMarkdownBullets(s, sh, 200)
	s.Update(sh, tea.KeyMsg{Type: tea.KeyCtrlP})

	// Typing left the caret (and so the view) at the buffer's end; the pane opens
	// synced to that, not parked at the top.
	if s.editor.TopLine() == 0 {
		t.Fatal("a 200-line buffer should have scrolled the editor off the top")
	}
	if got := s.previewPanel.ScrollOffset(); got == 0 {
		t.Fatal("the pane should open synced to the editor's scrolled position")
	}

	// Wheel coords must land inside the editor pane: a mouse press is hit-tested
	// against the slot rects, not broadcast. The sidebar owns x<30, the preview the
	// right half of what remains.
	wheel := func(btn tea.MouseButton, n int) {
		for ; n > 0; n-- {
			s.Update(sh, tea.MouseMsg{Action: tea.MouseActionPress, Button: btn, X: 45, Y: 15})
		}
	}
	wheel(tea.MouseButtonWheelUp, 200) // browse all the way back to the start
	if off, _, _ := s.editor.ScrollSpan(); off != 0 {
		t.Fatalf("wheeling up should reach the buffer's top, got offset %d", off)
	}
	if got := s.previewPanel.ScrollOffset(); got != 0 {
		t.Fatalf("the render's first row must be visible with the editor at the top, got offset %d", got)
	}

	// Down one tick at a time, watching for a step backwards.
	last := 0
	for i := 0; i < 250; i++ {
		wheel(tea.MouseButtonWheelDown, 1)
		got := s.previewPanel.ScrollOffset()
		if got < last {
			off, _, _ := s.editor.ScrollSpan()
			t.Fatalf("scrolling down moved the pane back up: %d → %d (editor offset %d)", last, got, off)
		}
		last = got
	}

	off, maxOff, _ := s.editor.ScrollSpan()
	if off != maxOff {
		t.Fatalf("the editor should be bottomed out after 250 ticks, got offset %d of %d", off, maxOff)
	}
	if got, want := s.previewPanel.ScrollOffset(), s.previewPanel.MaxScrollOffset(); got != want {
		t.Fatalf("the pane must bottom out with the editor: offset %d, want %d", got, want)
	}
}

// TestPreviewScrollsByHand: the pane is the user's between editor scrolls. A wheel over
// it (which focuses it, ModularScreen hit-tests the press) or the nav keys once it holds
// focus move it, and the position STICKS — the regression being pinned is that the
// router re-lays out after every message (core.Router.Update), so a SetSize that
// re-asserted the sync unconditionally undid every hand scroll in the tick that made it.
// The editor scrolling again takes the pane back over, and so does a real resize.
func TestPreviewScrollsByHand(t *testing.T) {
	s, sh := newHome(t)
	s.openDoc(sh, filepath.Join(t.TempDir(), "a.md"))
	typeMarkdownBullets(s, sh, 60)
	s.Update(sh, tea.KeyMsg{Type: tea.KeyCtrlP})

	// The preview column: the sidebar owns x<30 and the editor and preview split what is
	// left of the 100 cells, putting the pane's left edge at 65.
	synced := s.previewPanel.ScrollOffset()
	if synced == 0 {
		t.Fatal("typing should have left the pane synced away from the top")
	}
	for i := 0; i < 4; i++ {
		s.Update(sh, tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp, X: 80, Y: 15})
	}
	if !s.previewPanel.Focused() {
		t.Fatal("a press over the pane should focus it")
	}
	byHand := s.previewPanel.ScrollOffset()
	if byHand >= synced {
		t.Fatalf("wheeling the pane up should have moved it off %d, got %d", synced, byHand)
	}

	// What the router does after every single message, and then an unrelated message.
	s.SetSize(sh, 100, 30)
	if got := s.previewPanel.ScrollOffset(); got != byHand {
		t.Fatalf("a re-layout at the same size must not move the pane: %d → %d", byHand, got)
	}
	s.Update(sh, tea.KeyMsg{Type: tea.KeyCtrlL}) // line numbers: nothing to do with the pane
	if got := s.previewPanel.ScrollOffset(); got != byHand {
		t.Fatalf("an unrelated message must not move the pane: %d → %d", byHand, got)
	}

	// The keyboard path: shift+tab walks focus onto the pane, the nav keys scroll it.
	s.modular.FocusSlot(s.editorSlot())
	s.Update(sh, tea.KeyMsg{Type: tea.KeyShiftTab})
	if !s.previewPanel.Focused() {
		t.Fatal("shift+tab from the editor should focus the pane")
	}
	s.Update(sh, tea.KeyMsg{Type: tea.KeyUp})
	if got := s.previewPanel.ScrollOffset(); got != byHand-1 {
		t.Fatalf("up on the focused pane should scroll it one row: %d → %d", byHand, got)
	}

	// The editor scrolling again re-takes it.
	s.Update(sh, tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp, X: 45, Y: 15})
	if got := s.previewPanel.ScrollOffset(); got == byHand-1 {
		t.Fatal("scrolling the editor should put the pane back under the sync")
	}
}

// TestPreviewScrollIsExactNotProportional pins the drift the map exists to remove. In
// a document that does not render line-for-line — headings take a blank row the source
// has not got, fences grow rules, a hard-wrapped paragraph collapses into fewer rows —
// a proportional estimate lands rows away from the line the editor is showing. Through
// the body of the document (more than one editor screenful from either end, where the
// eased ends take over) the pane's CENTER row must carry the editor's CENTER line, and
// the estimate must be wrong somewhere, or the test would prove nothing.
func TestPreviewScrollIsExactNotProportional(t *testing.T) {
	s, sh := newHome(t)
	s.openDoc(sh, filepath.Join(t.TempDir(), "a.md"))

	// Typed with real Enters — rune input carries no newlines, and a file load is a cmd
	// no test runs.
	type_ := func(text string) {
		s.Update(sh, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text)})
		s.Update(sh, tea.KeyMsg{Type: tea.KeyEnter})
		// This fixture wants a blank line after its bullet. Enter now supplies the
		// next marker, so remove that marker before typing the intended blank line.
		if strings.HasPrefix(text, "- ") {
			s.Update(sh, tea.KeyMsg{Type: tea.KeyBackspace})
			s.Update(sh, tea.KeyMsg{Type: tea.KeyBackspace})
		}
	}
	for i := 0; i < 20; i++ {
		type_(fmt.Sprintf("## Section %d", i))
		type_("")
		type_(fmt.Sprintf("prose %d hard-wrapped by its author", i)) // these three lines
		type_("across three source lines that the")                  // re-flow into fewer
		type_("render folds back together")                          // rows than they take
		type_("")
		type_(fmt.Sprintf("- bullet %d", i))
		type_("")
		type_("```")
		type_(fmt.Sprintf("code(%d)", i)) // the fence grows a rule above and below
		type_("```")
		type_("")
	}
	s.Update(sh, tea.KeyMsg{Type: tea.KeyCtrlP})

	wheel := func(btn tea.MouseButton, n int) {
		for ; n > 0; n-- {
			s.Update(sh, tea.MouseMsg{Action: tea.MouseActionPress, Button: btn, X: 45, Y: 15})
		}
	}
	wheel(tea.MouseButtonWheelUp, 400) // back to the top

	src := strings.Split(s.previewSrc, "\n")
	lines := strings.Split(stripANSI(components.RenderMarkdown(s.previewSrc, s.previewPanel.TextWidth())), "\n")
	// The markers are the editor's, not the render's: what survives into the pane is the
	// text after them. Only headings and bullets answer — the prose lines re-flow, so a
	// source line is not a row of the render and matching one against the other proves
	// nothing about the mapping.
	text := func(line string) string {
		for _, marker := range []string{"## ", "- "} {
			if strings.HasPrefix(line, marker) {
				return strings.TrimPrefix(line, marker)
			}
		}
		return ""
	}

	checked, drifted := 0, 0
	for step := 0; step < 30; step++ {
		wheel(tea.MouseButtonWheelDown, 4)
		off, maxOff, editorRows := s.editor.ScrollSpan()
		if off < editorRows || maxOff-off < editorRows {
			continue // inside an eased end: the endpoints, not the anchor, decide here
		}
		checked++

		// The pane's middle row is the row the editor's middle line rendered to.
		center := s.editor.CenterLine()
		row := s.previewPanel.ScrollOffset() + s.previewPanel.VisibleRows()/2
		if row >= len(lines) {
			t.Fatalf("pane center row %d is past the render's %d rows", row, len(lines))
		}
		if want := s.previewMap[center]; row != want {
			t.Fatalf("editor center line %d (%q): pane center row %d, want %d",
				center, src[center], row, want)
		}
		// And that row really does carry the line — checked on the headings and bullets,
		// which are short enough that the render never wraps them onto a second row.
		if want := text(src[center]); want != "" && !strings.Contains(lines[row], want) {
			t.Fatalf("editor center line %d (%q): pane center row %d = %q, want the row carrying it",
				center, src[center], row, lines[row])
		}
		if est := center * max(len(lines)-1, 0) / max(len(src)-1, 1); est != row {
			drifted++
		}
	}
	if checked == 0 {
		t.Fatal("every sample landed in an eased end: this document is too short to exercise the anchor")
	}
	if drifted == 0 {
		t.Fatal("the proportional estimate agreed everywhere: this document does not exercise the map")
	}
}

// altP is the full-screen reader's key. A terminal cannot deliver ctrl+shift+p — v1
// bubbletea attaches shift to navigation keys only — so alt+p is what opens the reader.
var altP = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p"), Alt: true}

// TestPreviewOverlayRendersBuffer: the reader renders the LIVE buffer, not a snapshot
// taken when it was built.
func TestPreviewOverlayRendersBuffer(t *testing.T) {
	s, sh := newHome(t)
	s.openDoc(sh, filepath.Join(t.TempDir(), "a.md"))
	s.Update(sh, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("- bullet")})

	doc := s.previewScreen()
	doc.SetSize(sh, 80, 24)
	if v := stripANSI(doc.View(sh)); !strings.Contains(v, "• bullet") {
		t.Fatalf("the reader should render the live buffer, got:\n%s", v)
	}
	if !strings.Contains(doc.Title, "a.md") {
		t.Errorf("the reader should name the doc, got %q", doc.Title)
	}
}

// TestPreviewOverlayCloses: alt+p and esc both pop the reader, which is why the home
// screen tracks only the pane — an esc it never sees cannot desync it.
func TestPreviewOverlayCloses(t *testing.T) {
	s, sh := newHome(t)
	s.openDoc(sh, filepath.Join(t.TempDir(), "a.md"))
	s.Update(sh, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("# Title")})

	for _, key := range []tea.KeyMsg{altP, {Type: tea.KeyEsc}} {
		doc := s.previewScreen()
		if _, a := doc.Update(sh, key); msgType(a) != "core.popMsg" {
			t.Errorf("%s should pop the reader, got %s", key, msgType(a))
		}
	}
}

// TestPreviewOverlayKeepsWrapper: DocScreen.Update answers with its own pointer, so
// without the wrapper's override the router would store that back and the reader would
// lose its chrome mask on the first keystroke it received.
func TestPreviewOverlayKeepsWrapper(t *testing.T) {
	s, sh := newHome(t)
	doc := s.previewScreen()

	next, _ := doc.Update(sh, tea.KeyMsg{Type: tea.KeyDown})
	wrapped, ok := next.(*previewDoc)
	if !ok {
		t.Fatalf("the reader should stay a *previewDoc, got %T", next)
	}
	// The mask is the thing the wrapper exists to carry, so check it through the value
	// Update handed back rather than the one we still hold.
	if mask := wrapped.ChromeMask(); mask != chromeMask(false) {
		t.Errorf("the reader lost its chrome mask across Update, got %+v", mask)
	}
}

// TestReaderWearsTheEditorsChrome: the home screen and the full-screen reader pushed over
// it must mask identically in each launch mode. Reader-only chrome (or chrome only the
// editor has) makes alt+p read as leaving the app rather than as a way of looking at the
// document already open — which is exactly what the reader's own hand-tuned mask used to
// do. They share chromeMask; this is what keeps them from drifting apart again.
func TestReaderWearsTheEditorsChrome(t *testing.T) {
	for _, minimal := range []bool{false, true} {
		home := (&homeScreen{minimal: minimal}).ChromeMask()
		reader := (&previewDoc{minimal: minimal}).ChromeMask()
		if home != reader {
			t.Errorf("minimal=%v: home masks %+v but the reader masks %+v", minimal, home, reader)
		}
	}
}

// TestFullPreviewKey: alt+p pushes the reader over a markdown doc, and does nothing at
// all over a file its renderer would mangle — the same gate ctrl+p uses.
func TestFullPreviewKey(t *testing.T) {
	s, sh := newHome(t)
	dir := t.TempDir()

	s.openDoc(sh, filepath.Join(dir, "a.md"))
	if _, a := s.Update(sh, altP); msgType(a) != "core.pushMsg" {
		t.Errorf("alt+p should push the reader on a markdown doc, got %q", msgType(a))
	}
	s.openDoc(sh, filepath.Join(dir, "a.go"))
	if _, a := s.Update(sh, altP); msgType(a) != "" {
		t.Errorf("alt+p should do nothing on a .go file, got %q", msgType(a))
	}
}

// TestLaunchPreviewLoadsTheBuffer: a --preview launch SEEDS the editor rather than
// leaving it to EditorScreen.Init's async read. That read comes back as a message, the
// router delivers a message only to the top screen, and this launch pushes the reader on
// top — so the load used to land there and be dropped, leaving an empty buffer aimed at a
// file that is not empty (esc out of the reader and the document was gone).
//
// Nothing here pumps a cmd: newHomeWith calls Init and discards what it returns, so a
// buffer with content in it can only have been seeded synchronously. The reader renders
// that same buffer, which is what makes the two impossible to disagree.
func TestLaunchPreviewLoadsTheBuffer(t *testing.T) {
	file := filepath.Join(t.TempDir(), "note.md")
	if err := os.WriteFile(file, []byte("- from disk"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, sh := newHomeWith(t, Options{Mode: ModeFile, File: file, Preview: true})

	if got := s.editor.Text(); got != "- from disk" {
		t.Fatalf("the launch should have seeded the buffer, got %q", got)
	}
	if s.editor.Dirty() {
		t.Error("a seeded buffer is a load, not an edit: it must be clean")
	}
	doc := s.previewScreen()
	doc.SetSize(sh, 80, 24)
	if v := stripANSI(doc.View(sh)); !strings.Contains(v, "• from disk") {
		t.Fatalf("the launch reader should render the document, got:\n%s", v)
	}
}

// TestLaunchPreviewEscapeKeepsDocument pins the user-visible outcome through the router,
// the only path that actually applies the launch push to a stack: esc out of the reader
// and the document is still in the editor behind it.
//
// It does NOT reproduce the original bug, and can't: pumpModel walks a batch in order, so
// the editor's read is delivered before the push, while real bubbletea runs the two
// concurrently and the instant push closure beats the file I/O every time. That asymmetry
// is exactly why the fix does not depend on ordering at all — TestLaunchPreviewLoadsTheBuffer
// is what pins it, by proving the buffer is seeded with no cmd pumped whatsoever.
func TestLaunchPreviewEscapeKeepsDocument(t *testing.T) {
	file := filepath.Join(t.TempDir(), "note.md")
	if err := os.WriteFile(file, []byte("# Heading\n\nbody text"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Built by hand rather than through newHomeRouter: the launch push rides Init's cmd
	// batch, so it only reaches the stack if that batch is pumped (as TestLaunchPreview
	// does). The screen is captured on the way past — once the reader is on top, Top() is
	// the reader, not the editor underneath it.
	t.Setenv("HOME", t.TempDir())
	sh := core.NewShared(New("test", DefaultConfig(), Options{Mode: ModeFile, File: file, Preview: true}))
	var s *homeScreen
	r := core.NewRouter(sh, []core.TabEntry{
		{Title: "Editor", New: func(sh *core.Shared) core.Screen {
			s = NewHomeScreen(sh).(*homeScreen)
			return s
		}},
	})
	var model tea.Model = r
	model = pumpModel(model, r.Init())
	model, _ = model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	if _, ok := model.(core.Router).Top().(*previewDoc); !ok {
		t.Fatalf("-P should boot into the reader, got %T", model.(core.Router).Top())
	}
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if _, ok := model.(core.Router).Top().(*homeScreen); !ok {
		t.Fatalf("esc should pop back to the editor, got %T", model.(core.Router).Top())
	}
	if got := s.editor.Text(); got != "# Heading\n\nbody text" {
		t.Fatalf("the editor behind the reader lost the document: %q", got)
	}
}

// TestLaunchPreview: -P on a markdown file boots into the reader — the rendered document
// with gote's chrome masked away — while every launch that opens no document ignores it.
func TestLaunchPreview(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "note.md")
	if err := os.WriteFile(file, []byte("# Heading\n\nbody text"), 0o644); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "note.go")
	if err := os.WriteFile(other, []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}

	frame := func(opts Options) string {
		t.Helper()
		sh := core.NewShared(New("test", DefaultConfig(), opts))
		sh.Chrome = &core.Chrome{Breadcrumb: core.NewBreadcrumbPane()}
		r := core.NewRouter(sh, []core.TabEntry{
			{Title: "Editor", New: func(sh *core.Shared) core.Screen { return NewHomeScreen(sh) }},
		})
		// Threaded, not pumped: the launch push rides the same batch as the editor's file
		// read, and a push changes the STACK — which a discarded model copy would drop.
		var m tea.Model = r
		m = pumpModel(m, r.Init())
		m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		return stripANSI(m.View())
	}

	// The renderer is the tell: it eats the "#", where the editor would show it raw.
	preview := frame(Options{Mode: ModeFile, File: file, Preview: true})
	if !strings.Contains(preview, "Heading") || strings.Contains(preview, "# Heading") {
		t.Fatalf("-P should boot into the rendered document, frame:\n%s", preview)
	}
	// A ModeFile launch is chrome-free, and the reader pushed over it matches: no
	// breadcrumb, no help bar of gote's and none of its own either.
	if strings.Contains(preview, "more") || strings.Contains(preview, "docs") {
		t.Fatalf("the reader should mask gote's breadcrumb and help bar, frame:\n%s", preview)
	}
	if strings.Contains(preview, "esc back") {
		t.Fatalf("a minimal launch's reader should grow no help bar of its own, frame:\n%s", preview)
	}
	// Only minimal drops it — pushed from the ordinary launch, the exit hint stays.
	if mask := (&previewDoc{minimal: false}).ChromeMask(); mask.Help {
		t.Fatal("the ordinary launch's reader should keep its help bar — it is the exit")
	}
	// And the breadcrumb goes the same way: masked with the editor's in minimal mode,
	// kept with it otherwise, so the reader never wears chrome the screen it was opened
	// over isn't wearing.
	if mask := (&previewDoc{minimal: false}).ChromeMask(); mask.Breadcrumb {
		t.Error("the ordinary launch's reader should keep the breadcrumb")
	}
	if mask := (&previewDoc{minimal: true}).ChromeMask(); !mask.Breadcrumb {
		t.Error("a minimal launch's reader should mask the breadcrumb")
	}

	// Everything that opens no markdown document launches exactly as it always did.
	for name, opts := range map[string]Options{
		"non-markdown file": {Mode: ModeFile, File: other, Preview: true},
		"no file at all":    {Preview: true},
	} {
		if got := frame(opts); strings.Contains(got, "Heading") {
			t.Errorf("%s should launch normally, frame:\n%s", name, got)
		}
	}
}

// TestEditorPaneFillsColumn: a short document used to leave the editor column ragged
// — the editor's body is only as wide as its longest line unless the scrollbar forces
// the padding — so the slot carries ExpandH and every rendered row is the full width.
func TestEditorPaneFillsColumn(t *testing.T) {
	s, sh := newHome(t)
	s.openDoc(sh, filepath.Join(t.TempDir(), "a.md"))
	s.Update(sh, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hi")}) // far shorter than the pane

	// Measured in display cells, not bytes: the sidebar's box-drawing runes are
	// multi-byte and would make every row look a different length.
	rows := strings.Split(s.View(sh), "\n")
	width := 0
	for _, r := range rows {
		if w := lipgloss.Width(r); w > width {
			width = w
		}
	}
	for i, r := range rows {
		if w := lipgloss.Width(r); w != width {
			t.Fatalf("row %d is %d cells wide, not the full %d — the grid is ragged:\n%s",
				i, w, width, stripANSI(s.View(sh)))
		}
	}
}

// TestEditorSavedRekeys: ctrl+s to a NEW path leaves the same buffer in the same pane
// and re-files it under that path — the open set is keyed by path, so without this the
// map would still answer to the name the doc no longer has.
func TestEditorSavedRekeys(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.md")
	s, sh := newHome(t)
	s.openDoc(sh, old)
	ed := s.editor

	renamed := filepath.Join(dir, "renamed.md")
	s.editorSaved(sh, renamed)

	if s.currentPath != renamed {
		t.Fatalf("the screen should follow the buffer to %q, got %q", renamed, s.currentPath)
	}
	if s.editor != ed {
		t.Fatal("a save must not swap the pane's editor")
	}
	c := Of(sh)
	if c.open.byPath[renamed] != ed {
		t.Fatal("the new path should resolve to the same buffer")
	}
	if _, ok := c.open.byPath[old]; ok {
		t.Fatal("the old path must leave the open set")
	}
	// The close path keys off currentPath, so the rename is what keeps ctrl+x working.
	if next := c.CloseDoc(s.currentPath); next != "" {
		t.Fatalf("closing the only doc should leave nothing, got %q", next)
	}
}

// TestMinimalMode: a file argument boots the editor alone — the file loaded, the
// sidebar gone and locked out (ctrl+b is a no-op, where it toggles in every other
// mode), and every chrome element masked so the buffer owns the terminal.
func TestMinimalMode(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "note.md")
	if err := os.WriteFile(file, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	s, sh := newHomeWith(t, Options{Mode: ModeFile, File: file})

	if !s.minimal || s.sidebar {
		t.Fatalf("a file launch should be minimal with no sidebar, got minimal=%v sidebar=%v", s.minimal, s.sidebar)
	}
	if s.currentPath != file {
		t.Fatalf("the editor should boot on %q, got %q", file, s.currentPath)
	}
	if Of(sh).open.byPath[file] != s.editor {
		t.Fatal("the minimal buffer should be registered in the open set, as a picked doc is")
	}
	if mask := s.ChromeMask(); !mask.Help || !mask.Breadcrumb {
		t.Fatalf("minimal mode should mask the chrome, got %+v", mask)
	}

	s.Update(sh, tea.KeyMsg{Type: tea.KeyCtrlB})
	if s.sidebar {
		t.Fatal("ctrl+b must not bring the sidebar back in minimal mode")
	}
	// The editor pane is the only slot, so it is slot 0 and holds focus.
	if got := focusedPane(s, sh); got != "editor" {
		t.Fatalf("focus should be on the editor, got %s", got)
	}
	if act := s.editorExit(sh); act.Cmd == nil {
		t.Fatal("ctrl+x should quit in minimal mode (the root screen cannot be popped)")
	}
}

// TestNonMinimalKeepsChrome: the mask is opt-in per launch, not a global — the ordinary
// launch must still draw its breadcrumb and help bar. Status is the one exception, masked
// in every mode because the screen paints it itself (see TestStatusCostsNoRows).
func TestNonMinimalKeepsChrome(t *testing.T) {
	s, _ := newHome(t)
	if mask := s.ChromeMask(); mask != (core.ChromeMask{Status: true}) {
		t.Fatalf("the doc-list launch should mask nothing but the status row, got %+v", mask)
	}
}

// TestMinimalFrame drives the REAL router — the thing that reads ChromeMask — and
// checks the whole frame, not just the mask value: the file's content is on screen,
// none of the chrome is, and the editor got every row of the terminal. Asserting on
// the mask alone would pass even if the screen never reached the top of a stack.
func TestMinimalFrame(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "note.md")
	if err := os.WriteFile(file, []byte("minimal content"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())

	const rows = 24
	frame := func(opts Options) string {
		sh := core.NewShared(New("test", DefaultConfig(), opts))
		sh.Chrome = &core.Chrome{Breadcrumb: core.NewBreadcrumbPane()}
		r := core.NewRouter(sh, []core.TabEntry{
			{Title: "Editor", New: func(sh *core.Shared) core.Screen { return NewHomeScreen(sh) }},
		})
		// The file read is async — the editor's Init returns the cmd that does it — so
		// the frame is only worth looking at once the resulting message has been fed
		// back in. pump does that; without it the buffer would still be empty.
		pump(r, r.Init())
		r.Update(tea.WindowSizeMsg{Width: 80, Height: rows})
		return stripANSI(r.View())
	}

	minimal := frame(Options{Mode: ModeFile, File: file})
	if !strings.Contains(minimal, "minimal content") {
		t.Fatalf("the file should be loaded into the editor, frame:\n%s", minimal)
	}
	if strings.Contains(minimal, "docs") || strings.Contains(minimal, "Docs") {
		t.Fatalf("no sidebar or breadcrumb should be drawn, frame:\n%s", minimal)
	}
	// "? more" is the bar's own entry (the app keys live in the ? overlay now), so it is
	// the string that proves the bar is there — or, here, that it is not.
	if strings.Contains(minimal, "more") || strings.Contains(minimal, "panes") {
		t.Fatalf("the help bar should be masked away, frame:\n%s", minimal)
	}

	// The masked chrome is rows the editor gets instead. The frame fills the terminal
	// either way, so the proof is WHOSE rows the edges are: minimal mode opens on the
	// editor's own title bar and ends on a buffer row, where the ordinary launch spends
	// its first row on the breadcrumb and its last on the help bar.
	minLines := strings.Split(minimal, "\n")
	if len(minLines) != rows {
		t.Fatalf("minimal mode should fill the terminal: %d rows, want %d", len(minLines), rows)
	}
	if !strings.Contains(minLines[0], "note.md") {
		t.Fatalf("the top row should be the editor's, got %q", minLines[0])
	}

	ordinary := frame(Options{})
	ordLines := strings.Split(ordinary, "\n")
	if !strings.Contains(ordinary, "Docs") {
		t.Fatalf("the ordinary launch should still draw its sidebar, frame:\n%s", ordinary)
	}
	if !strings.Contains(ordLines[0], "docs") {
		t.Fatalf("the ordinary launch's top row should be the breadcrumb, got %q", ordLines[0])
	}
	if !strings.Contains(ordLines[len(ordLines)-1], "more") {
		t.Fatalf("the ordinary launch's last row should be the help bar, got %q", ordLines[len(ordLines)-1])
	}
}

// TestStatusCostsNoRows is the regression this whole status.go exists for. The router
// draws its status line as a row taken OFF the body (belowChrome feeds bodyHeightFor),
// so a clipboard message used to shove every pane up a line and drop it back five
// seconds later. gote masks that row and paints the message in space the frame already
// spends: the help bar's blank padding row, or — where the help bar is masked too — the
// body's own last row. The proof is that nothing above the row it lands on moves.
func TestStatusCostsNoRows(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "note.md")
	if err := os.WriteFile(file, []byte("minimal content"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())

	const rows, cols = 24, 80
	// render draws one frame through the REAL router, with status set before the resize
	// so the layout is computed with it in hand — the ordering that used to lose a row.
	render := func(opts Options, status string) []string {
		t.Helper()
		sh := core.NewShared(New("test", DefaultConfig(), opts))
		sh.Chrome = &core.Chrome{Breadcrumb: core.NewBreadcrumbPane(), Status: components.NewStatusLine()}
		r := core.NewRouter(sh, []core.TabEntry{
			{Title: "Editor", New: func(sh *core.Shared) core.Screen { return NewHomeScreen(sh) }},
		})
		pump(r, r.Init()) // the editor's file read is async; without this the buffer is empty
		if status != "" {
			sh.WriteStatus(status)
		}
		r.Update(tea.WindowSizeMsg{Width: cols, Height: rows})
		return strings.Split(stripANSI(r.View()), "\n")
	}

	for _, tc := range []struct {
		name string
		opts Options
		// lands is the row the message is expected to take: the help bar's padding row
		// for a launch that has a help bar, the very last row for one that doesn't.
		lands int
	}{
		{"ordinary launch", Options{}, rows - 2},
		{"minimal launch", Options{Mode: ModeFile, File: file}, rows - 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			quiet := render(tc.opts, "")
			noisy := render(tc.opts, "copied 12 characters")

			if len(quiet) != rows || len(noisy) != rows {
				t.Fatalf("both frames should fill the terminal: quiet %d rows, noisy %d, want %d",
					len(quiet), len(noisy), rows)
			}
			if !strings.Contains(noisy[tc.lands], "copied 12 characters") {
				t.Fatalf("the message should land on row %d, got %q\nframe:\n%s",
					tc.lands, noisy[tc.lands], strings.Join(noisy, "\n"))
			}
			// Every row the message did NOT land on must be untouched — that is the
			// eyesore: panes climbing a line and dropping back.
			for i := range quiet {
				if i == tc.lands {
					continue
				}
				if quiet[i] != noisy[i] {
					t.Errorf("row %d moved when the status appeared:\n quiet %q\n noisy %q", i, quiet[i], noisy[i])
				}
			}

			// A message wider than the terminal must be cut, not wrapped: core's status
			// style sets no width, and a wrapped line costs the row this all saves.
			long := render(tc.opts, strings.Repeat("save failed: /very/long/path ", 6))
			if len(long) != rows {
				t.Fatalf("an over-wide message must be clamped, not wrapped: %d rows, want %d", len(long), rows)
			}
		})
	}
}

// pumpModel is pump for a flow whose messages move the navigation STACK. Router is a
// value model, so a push only survives if the model each Update returns is threaded
// into the next one; pump's screen-level mutations survive a discarded copy, a push
// does not.
func pumpModel(m tea.Model, cmd tea.Cmd) tea.Model {
	for depth := 0; cmd != nil && depth < 8; depth++ {
		msg := cmd()
		if msg == nil {
			return m
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				m = pumpModel(m, c)
			}
			return m
		}
		m, cmd = m.Update(msg)
	}
	return m
}

// pump runs cmd — flattening the batches Init and Update hand back — and feeds every
// resulting message into the router, standing in for the bubbletea event loop.
func pump(r core.Router, cmd tea.Cmd) {
	for depth := 0; cmd != nil && depth < 8; depth++ {
		msg := cmd()
		if msg == nil {
			return
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				pump(r, c)
			}
			return
		}
		_, cmd = r.Update(msg)
	}
}

// TestQuitGate: a clean screen lets the quit through (handled=false); a dirty
// buffer intercepts it with a push (the confirm popup) and dirtyDocs names it —
// the scratch buffer here, which lives outside the ctx's open set.
func TestQuitGate(t *testing.T) {
	s, sh := newHome(t)

	if _, handled := s.QuitGate(sh); handled {
		t.Fatal("a clean home screen should let the quit through")
	}

	// Focus the editor pane and dirty the scratch buffer.
	s.Update(sh, tea.KeyMsg{Type: tea.KeyShiftTab})
	s.Update(sh, tea.KeyMsg{Type: tea.KeyShiftTab})
	s.Update(sh, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})

	if names := s.dirtyDocs(sh); len(names) != 1 || names[0] != "scratch" {
		t.Fatalf("dirtyDocs should name the scratch buffer, got %v", names)
	}
	act, handled := s.QuitGate(sh)
	if !handled || act.Msg == nil {
		t.Fatal("a dirty buffer should intercept the quit with a confirm popup push")
	}

	// The popup must force-quit on q/ctrl+c rather than re-trigger the gate
	// below it (which would stack popup upon popup).
	if _, handled := quitPopup([]string{"scratch"}).QuitGate(sh); !handled {
		t.Fatal("the quit popup should answer the quit gate itself (force-quit)")
	}
}

func TestVaultSwitchGatesDirtyBufferThenResetsSession(t *testing.T) {
	s, sh := newHome(t)
	c := Of(sh)
	vault := t.TempDir()
	if err := os.WriteFile(filepath.Join(vault, "vault.md"), []byte("vault"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.Config.Vaults["notes"] = VaultConfig{Path: vault, Open: []string{}}

	oldPath := filepath.Join(t.TempDir(), "old.md")
	s.openDoc(sh, oldPath)
	oldEditor := s.editor
	s.Update(sh, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("unsaved")})
	s.preview = previewPane

	if act := s.requestVaultSwitch(sh, "notes"); msgType(act) != "core.pushMsg" {
		t.Fatalf("dirty switch should push the unsaved popup, got %s", msgType(act))
	}
	if c.Mode == ModeVault || s.editor != oldEditor || c.open.byPath[oldPath] != oldEditor {
		t.Fatal("requesting a dirty switch mutated the session before confirmation")
	}

	act := s.activateVault(sh, "notes") // the dirty popup's confirm action
	if msgType(act) != "core.seqMsg" {
		t.Fatalf("confirmed switch should reset navigation, got %s", msgType(act))
	}
	if c.Mode != ModeVault || c.VaultName != "notes" || c.ScanDir != vault {
		t.Fatalf("active vault = mode %v name %q dir %q", c.Mode, c.VaultName, c.ScanDir)
	}
	if len(c.open.byPath) != 0 || len(c.open.order) != 0 || len(c.open.roots) != 0 {
		t.Fatalf("confirmed switch left open state: %v %v %v", c.open.byPath, c.open.order, c.open.roots)
	}
	if s.editor == oldEditor || s.editor.Dirty() || s.currentPath != "" {
		t.Fatal("confirmed switch should install a fresh clean scratch editor")
	}
	if s.preview != previewOff || !s.sidebar || s.minimal {
		t.Fatalf("switch layout = preview %d sidebar %v minimal %v", s.preview, s.sidebar, s.minimal)
	}
	view := stripANSI(s.View(sh))
	if !strings.Contains(view, "vault.md") || strings.Contains(view, "unsaved") {
		t.Fatalf("switched view did not show only the new vault:\n%s", view)
	}
	if got := s.CrumbLabel(false); got != "vault: notes" {
		t.Fatalf("vault crumb = %q", got)
	}
}

func TestVaultSwitchRejectsMissingFolderWithoutClosing(t *testing.T) {
	s, sh := newHome(t)
	c := Of(sh)
	c.Config.Vaults["gone"] = VaultConfig{Path: filepath.Join(t.TempDir(), "gone"), Open: []string{}}
	oldEditor := s.editor

	if act := s.requestVaultSwitch(sh, "gone"); msgType(act) != "core.pushMsg" {
		t.Fatalf("missing vault should push an error popup, got %s", msgType(act))
	}
	if c.Mode != ModeHome || s.editor != oldEditor {
		t.Fatal("invalid vault switch changed the live session")
	}
}

// TestHelpKey: ? pushes the shortcut overlay while nothing captures text, types a
// literal ? into the editor when it does, and alt+? summons the overlay even from
// inside the editor.
func TestHelpKey(t *testing.T) {
	s, sh := newHome(t)

	if _, act := s.Update(sh, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")}); act.Msg == nil {
		t.Fatal("? should push the help overlay when nothing is capturing text")
	}

	// Into the editor pane: ? is text now, so the buffer goes dirty.
	s.Update(sh, tea.KeyMsg{Type: tea.KeyShiftTab})
	s.Update(sh, tea.KeyMsg{Type: tea.KeyShiftTab})
	s.Update(sh, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	if !s.editor.Dirty() {
		t.Fatal("? should type into the editor, not open help")
	}

	if _, act := s.Update(sh, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?"), Alt: true}); act.Msg == nil {
		t.Fatal("alt+? should push the help overlay even from the editor")
	}
}

// scanHome seeds a scan root with the named docs and builds the home screen over it.
func scanHome(t *testing.T, names ...string) (*homeScreen, *core.Shared) {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("body"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return newHomeWith(t, Options{Mode: ModeScan, Dir: dir})
}

// filterList drives a list into an applied filter for query. bubbles computes its
// matches in a command, so each keystroke's command is run and fed back — under a
// deadline, because the same batch carries the filter cursor's blink TIMER.
func filterList(t *testing.T, l *list.Model, query string) {
	t.Helper()
	var run func(tea.Cmd)
	run = func(cmd tea.Cmd) {
		if cmd == nil {
			return
		}
		// Under a deadline, and flattened: the filter's matches and the cursor's blink
		// TIMER travel in the same batch, so a pump that runs commands straight through
		// stalls for the blink interval on every keystroke.
		done := make(chan tea.Msg, 1)
		go func() { done <- cmd() }()
		var msg tea.Msg
		select {
		case msg = <-done:
		case <-time.After(50 * time.Millisecond):
			return
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				run(c)
			}
			return
		}
		if msg != nil {
			*l, _ = l.Update(msg)
		}
	}
	*l, _ = l.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	for _, r := range query {
		var cmd tea.Cmd
		*l, cmd = l.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		run(cmd)
	}
	var cmd tea.Cmd
	*l, cmd = l.Update(tea.KeyMsg{Type: tea.KeyEnter})
	run(cmd)
}

// rowTitles names the docs list's visible rows.
func rowTitles(l *list.Model) []string {
	out := []string{}
	for _, it := range l.VisibleItems() {
		if t, ok := it.(interface{ Title() string }); ok {
			out = append(out, t.Title())
		}
	}
	return out
}

// TestFilterDropsNewFileRow: the "+ new file" row is an action, not a document, so a
// query must not rank it among the documents. It used to answer to "new", "ne" and
// "ile" — and bubbles orders matches by fuzzy rank, so it could land anywhere in them.
func TestFilterDropsNewFileRow(t *testing.T) {
	s, _ := scanHome(t, "news.md")
	l := s.docsPanel.List()
	if len(rowTitles(l)) != 2 {
		t.Fatalf("setup: want the action row and one doc, got %v", rowTitles(l))
	}

	filterList(t, l, "ne")
	for _, it := range l.VisibleItems() {
		if _, ok := it.(newFileItem); ok {
			t.Error(`the "+ new file" row must not survive a filter`)
		}
	}
	if got := rowTitles(l); len(got) != 1 || got[0] != "news.md" {
		t.Fatalf("the matching document should be the only row, got %v", got)
	}
}

// TestRefreshKeepsFilter is the reported sequence: filter the docs list, accept it,
// then run Actions ▸ ⟳ Refresh (which is a ReseedMsg broadcast). The reseed used to
// wipe the match set and leave the sidebar blank.
func TestRefreshKeepsFilter(t *testing.T) {
	s, sh := scanHome(t, "notes.md", "todo.md")
	l := s.docsPanel.List()

	filterList(t, l, "note")
	if got := rowTitles(l); len(got) != 1 || got[0] != "notes.md" {
		t.Fatalf("setup: want the one matching doc, got %v", got)
	}

	s.Receive(sh, ReseedMsg{})

	if l.FilterState() == list.Unfiltered {
		t.Fatal("a refresh must not drop the filter")
	}
	if got := rowTitles(l); len(got) != 1 || got[0] != "notes.md" {
		t.Fatalf("the filtered rows should survive the refresh, got %v", got)
	}
	if l.SelectedItem() == nil {
		t.Fatal("a refreshed filtered list must still have a selection")
	}
}

// renameFixture seeds a scan root with one doc and drives the real router to the docs
// list with that doc selected — row 0 is always the "+ new file" action, so one down
// arrow is what puts the selection on a document.
func renameFixture(t *testing.T, name, body string) (tea.Model, *homeScreen, *core.Shared, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	model, s, sh := newHomeRouter(t, Options{Mode: ModeScan, Dir: dir})
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	return model, s, sh, dir
}

// pressRename sends ctrl+r and returns the line edit it pushed, if any.
func pressRename(model tea.Model) (tea.Model, *components.LineEditScreen) {
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	edit, _ := model.(core.Router).Top().(*components.LineEditScreen)
	return model, edit
}

// TestRenamePrompt: ctrl+r on a doc row raises the line edit prefilled with the doc's
// path relative to its scan root — the same shape "+ new file" takes, which is what
// makes the box a move as well as a rename. The "+ new file" row has no path to
// rename, so the key falls through to the list there and nothing is pushed.
func TestRenamePrompt(t *testing.T) {
	model, _, sh, _ := renameFixture(t, filepath.Join("sub", "todo.md"), "")

	model, edit := pressRename(model)
	if edit == nil {
		t.Fatal("ctrl+r on a doc row should push the rename line edit")
	}
	want := filepath.Join("sub", "todo.md")
	if got := stripANSI(edit.View(sh)); !strings.Contains(got, want) {
		t.Fatalf("the box should be prefilled with %q, got:\n%s", want, got)
	}

	// esc back to the list, up onto "+ new file", and the key is inert there.
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyUp})
	if _, edit := pressRename(model); edit != nil {
		t.Fatal("ctrl+r on the + new file row should do nothing")
	}
}

// TestRenameMovesFile: submitting the box moves the file on disk and reseeds the docs
// list, and a name typed without an extension picks up the default one just as it does
// in the new-file box.
func TestRenameMovesFile(t *testing.T) {
	model, _, sh, dir := renameFixture(t, "old.md", "body")

	model, edit := pressRename(model)
	if edit == nil {
		t.Fatal("no rename box")
	}
	edit.SetValue("new") // no extension: NewExt appends .md
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if _, err := os.Stat(filepath.Join(dir, "old.md")); !os.IsNotExist(err) {
		t.Fatal("the old path should be gone")
	}
	b, err := os.ReadFile(filepath.Join(dir, "new.md"))
	if err != nil || string(b) != "body" {
		t.Fatalf("the file should have moved with its contents, got %q err %v", b, err)
	}
	files := Of(sh).Files
	if len(files) != 1 || files[0].Name != "new.md" {
		t.Fatalf("the docs list should have reseeded onto new.md, got %+v", files)
	}
	if _, ok := model.(core.Router).Top().(*homeScreen); !ok {
		t.Fatal("a successful rename should pop back to the home screen")
	}
}

// TestSaveAsConfirmFlow is the router-level guard for the save-as confirm: the box is
// seeded with the buffer's own path, so a changed name used to move the document on a
// bare enter. Driven through the real stack because that is what the confirm relies on —
// it is pushed OVER the box, so no leaves the typed name intact to fix.
func TestSaveAsConfirmFlow(t *testing.T) {
	model, _, _, dir := renameFixture(t, "old.md", "body")

	// Open the doc into the editor pane, then ctrl+s.
	model, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = pumpModel(model, cmd)
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	edit, _ := model.(core.Router).Top().(*components.LineEditScreen)
	if edit == nil {
		t.Fatal("ctrl+s should raise the save-as box")
	}

	moved := filepath.Join(dir, "moved.md")
	edit.SetValue(moved)
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if _, ok := model.(core.Router).Top().(*components.DialogScreen); !ok {
		t.Fatalf("a changed name should raise the confirm, got %T", model.(core.Router).Top())
	}
	if _, err := os.Stat(moved); !os.IsNotExist(err) {
		t.Fatalf("nothing may be written before the y, stat err = %v", err)
	}

	// No returns to the box with the typed name still in it — the reason to say no is
	// usually a typo, and retyping the whole path would be the wrong penalty for one.
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if _, ok := model.(core.Router).Top().(*components.LineEditScreen); !ok {
		t.Fatalf("esc should return to the save-as box, got %T", model.(core.Router).Top())
	}

	// Enter again with nothing retyped, then y: the name survived the cancel (the write
	// below lands on it), both overlays come off, and the file appears.
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model, cmd = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = pumpModel(model, cmd)
	if _, ok := model.(core.Router).Top().(*homeScreen); !ok {
		t.Fatalf("a confirmed save-as should land back on the home screen, got %T", model.(core.Router).Top())
	}
	if b, err := os.ReadFile(moved); err != nil || string(b) != "body" {
		t.Fatalf("the new path should hold the buffer: %q, err = %v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "old.md")); err != nil || string(b) != "body" {
		t.Fatalf("save-as leaves the old file alone: %q, err = %v", b, err)
	}
}

// pressDelete sends ctrl+d and returns the confirm it pushed, if any.
func pressDelete(model tea.Model) (tea.Model, *components.DialogScreen) {
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	dlg, _ := model.(core.Router).Top().(*components.DialogScreen)
	return model, dlg
}

// TestDeletePrompt: ctrl+d on a doc row raises the confirm naming the doc, and esc backs
// out of it leaving the file alone — the whole point of putting a confirm in front of the
// one docs-list verb that destroys something. The "+ new file" row has no file to delete,
// so the key falls through to the list there and nothing is pushed.
func TestDeletePrompt(t *testing.T) {
	model, _, sh, dir := renameFixture(t, filepath.Join("sub", "todo.md"), "body")

	model, dlg := pressDelete(model)
	if dlg == nil {
		t.Fatal("ctrl+d on a doc row should push the delete confirm")
	}
	if got := stripANSI(dlg.View(sh)); !strings.Contains(got, filepath.Join("sub", "todo.md")) {
		t.Fatalf("the confirm should name the doc, got:\n%s", got)
	}

	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if _, ok := model.(core.Router).Top().(*homeScreen); !ok {
		t.Fatal("esc should back out of the confirm")
	}
	if _, err := os.Stat(filepath.Join(dir, "sub", "todo.md")); err != nil {
		t.Fatalf("a cancelled delete must leave the file alone: %v", err)
	}

	// Up onto "+ new file", where the key is inert.
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyUp})
	if _, dlg := pressDelete(model); dlg != nil {
		t.Fatal("ctrl+d on the + new file row should do nothing")
	}
}

// TestDeleteRemovesFile: confirming removes the file from disk and reseeds the docs list
// off it, popping back to the home screen the way a rename does.
func TestDeleteRemovesFile(t *testing.T) {
	model, _, sh, dir := renameFixture(t, "old.md", "body")

	model, dlg := pressDelete(model)
	if dlg == nil {
		t.Fatal("no delete confirm")
	}
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})

	if _, err := os.Stat(filepath.Join(dir, "old.md")); !os.IsNotExist(err) {
		t.Fatalf("the file should be gone, stat err = %v", err)
	}
	if files := Of(sh).Files; len(files) != 0 {
		t.Fatalf("the docs list should have reseeded empty, got %+v", files)
	}
	if _, ok := model.(core.Router).Top().(*homeScreen); !ok {
		t.Fatal("a completed delete should pop back to the home screen")
	}
}

// TestDeleteOpenDoc: deleting the doc the editor pane is showing takes it out of the open
// set and moves the pane off it — onto the next open doc, and onto a fresh scratch buffer
// when that was the only one. A file with no buffer left holding it is the point: without
// the close, the Open list would keep a row for a document that no longer exists.
func TestDeleteOpenDoc(t *testing.T) {
	model, s, sh, dir := renameFixture(t, "old.md", "body")

	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter}) // open it
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})   // back to the list
	ed := s.editor

	model, dlg := pressDelete(model)
	if dlg == nil {
		t.Fatal("no delete confirm")
	}
	if got := stripANSI(dlg.View(sh)); !strings.Contains(got, "unsaved changes are lost") {
		t.Fatalf("an open doc's confirm should say the buffer closes, got:\n%s", got)
	}
	r := model.(core.Router)
	model, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	pump(r, cmd)

	if _, err := os.Stat(filepath.Join(dir, "old.md")); !os.IsNotExist(err) {
		t.Fatalf("the file should be gone, stat err = %v", err)
	}
	c := Of(sh)
	if _, open := c.Doc(filepath.Join(dir, "old.md")); open {
		t.Fatal("the deleted doc must leave the open set")
	}
	if len(c.OpenDocs()) != 0 {
		t.Fatalf("the Open list should be empty, got %+v", c.OpenDocs())
	}
	if s.editor == ed || s.currentPath != "" {
		t.Fatalf("the pane should have swapped to a scratch buffer, currentPath = %q", s.currentPath)
	}
	if _, ok := model.(core.Router).Top().(*homeScreen); !ok {
		t.Fatal("a completed delete should pop back to the home screen")
	}
}

// TestRenameOpenDoc: renaming a doc that is OPEN moves the three places its path is
// held — the ctx's open set, the screen's current doc, and the editor itself — without
// touching the buffer, so unsaved edits survive and the next save lands on the new
// path rather than re-creating the old one.
func TestRenameOpenDoc(t *testing.T) {
	model, s, sh, dir := renameFixture(t, "old.md", "body")

	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})                     // open it
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("!")}) // dirty it
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})                       // back to the list
	ed := s.editor

	model, edit := pressRename(model)
	if edit == nil {
		t.Fatal("no rename box")
	}
	edit.SetValue("new.md")
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})

	renamed := filepath.Join(dir, "new.md")
	if s.currentPath != renamed {
		t.Fatalf("currentPath = %q, want %q", s.currentPath, renamed)
	}
	if s.editor != ed {
		t.Fatal("a rename must not swap the pane's editor")
	}
	if !ed.Dirty() || !strings.Contains(ed.Text(), "!") {
		t.Fatal("the buffer and its unsaved edits must survive the rename")
	}
	if got := ed.CrumbLabel(false); got != "new.md" {
		t.Fatalf("the editor should have followed the file, crumb = %q", got)
	}
	c := Of(sh)
	if c.open.byPath[renamed] != ed {
		t.Fatal("the new path should resolve to the same buffer")
	}
	if _, ok := c.open.byPath[filepath.Join(dir, "old.md")]; ok {
		t.Fatal("the old path must leave the open set")
	}
	if len(c.open.order) != 1 || c.open.order[0] != renamed {
		t.Fatalf("OpenOrder should hold only the new path, got %v", c.open.order)
	}

	// And clicking the renamed row afterwards is still just a switch — the sequence the
	// bug showed up in, where the re-read landed on a file that no longer held the edits.
	r := model.(core.Router)
	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	pump(r, cmd)
	if s.editor != ed || !ed.Dirty() || !strings.Contains(ed.Text(), "!") {
		t.Fatalf("activating the renamed row should switch to the live buffer, got %q", ed.Text())
	}
}

// TestRenameRefusals: a rename that would clobber another document, and one that tries
// to escape the doc store, both surface an error popup and leave the disk alone. The
// popup replaces the line edit rather than stacking on it, so the overlay depth holds.
func TestRenameRefusals(t *testing.T) {
	model, _, _, dir := renameFixture(t, "old.md", "body")
	occupied := filepath.Join(dir, "taken.md")
	if err := os.WriteFile(occupied, []byte("theirs"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ name, value string }{
		{"clobber", "taken.md"},
		{"escape", filepath.Join("..", "elsewhere.md")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, edit := pressRename(model)
			if edit == nil {
				t.Fatal("no rename box")
			}
			edit.SetValue(tc.value)
			m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})

			if _, ok := m.(core.Router).Top().(*components.DialogScreen); !ok {
				t.Fatalf("a refused rename should raise an error popup, got %T", m.(core.Router).Top())
			}
			if b, err := os.ReadFile(filepath.Join(dir, "old.md")); err != nil || string(b) != "body" {
				t.Fatalf("the source must be untouched, got %q err %v", b, err)
			}
			if b, err := os.ReadFile(occupied); err != nil || string(b) != "theirs" {
				t.Fatalf("the target must be untouched, got %q err %v", b, err)
			}
			m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc}) // dismiss for the next case
			model = m
		})
	}
}

// TestRenameUnchangedCancels: enter on the untouched prefill is a no-op, not a rename
// onto the file's own path (which renameDoc would refuse as occupied).
func TestRenameUnchangedCancels(t *testing.T) {
	model, _, _, dir := renameFixture(t, "old.md", "body")

	model, edit := pressRename(model)
	if edit == nil {
		t.Fatal("no rename box")
	}
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if _, ok := model.(core.Router).Top().(*homeScreen); !ok {
		t.Fatalf("an unchanged name should just pop, got %T", model.(core.Router).Top())
	}
	if b, err := os.ReadFile(filepath.Join(dir, "old.md")); err != nil || string(b) != "body" {
		t.Fatalf("the file must be untouched, got %q err %v", b, err)
	}
}

// TestReselectOpenDocKeepsBuffer: activating a doc that is already open is a SWITCH, not
// an open. The pane swaps back to the buffer it already holds, so unsaved edits, the
// cursor and the undo history are all still there — the file is read once, on first open.
// The cmds must be pumped for this to mean anything: the read only happens when the cmd
// SetChild returns is actually run, which is why the bug survived the other rename tests.
func TestReselectOpenDocKeepsBuffer(t *testing.T) {
	model, s, sh, _ := renameFixture(t, "old.md", "body")
	r := model.(core.Router)

	model, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter}) // open it
	pump(r, cmd)
	ed := s.editor
	if got := ed.Text(); got != "body" {
		t.Fatalf("the first open should load the file, buffer = %q", got)
	}

	model, cmd = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("!")}) // dirty it
	pump(r, cmd)
	model, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEsc}) // back to the list
	pump(r, cmd)

	_, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter}) // activate the same row again
	pump(r, cmd)

	if s.editor != ed {
		t.Fatal("re-selecting an open doc should keep its editor instance")
	}
	if got := ed.Text(); got != "!body" {
		t.Fatalf("the unsaved edit should have survived the switch, buffer = %q", got)
	}
	if !ed.Dirty() {
		t.Fatal("the dirty flag should have survived the switch")
	}
	if c := Of(sh); len(c.open.byPath) != 1 {
		t.Fatalf("re-selecting must not open a second buffer, open = %d", len(c.open.byPath))
	}
}

// TestHomeEditorContextItems: gote's rows on the shared editor's right-click menu. The
// preview row is gated the same way ctrl+p is, so it must go muted for a document the
// preview refuses.
func TestHomeEditorContextItems(t *testing.T) {
	s, sh := newHome(t)

	rows := s.editorContextItems(sh)
	want := []string{"Toggle preview", "Full preview", "Toggle wrap", "Toggle line numbers"}
	if len(rows) != len(want) {
		t.Fatalf("editorContextItems returned %d rows, want %d", len(rows), len(want))
	}
	for i, label := range want {
		if rows[i].Label != label {
			t.Errorf("row %d is %q, want %q", i, rows[i].Label, label)
		}
		if rows[i].Hint != "" {
			t.Errorf("row %d carries hint %q; the menu dispatches no accelerators", i, rows[i].Hint)
		}
	}

	// The scratch buffer is previewable; a .txt doc is not. Both preview rows are gated.
	if rows[0].Disabled || rows[1].Disabled {
		t.Error("the scratch buffer is markdown-previewable, so the rows should be live")
	}
	s.currentPath = filepath.Join(t.TempDir(), "notes.txt")
	muted := s.editorContextItems(sh)
	if !muted[0].Disabled || !muted[1].Disabled {
		t.Error("the preview rows should be muted for a document the preview refuses")
	}

	before := s.editor.WrapMode()
	if act := rows[2].Pick(sh); act.Msg == nil {
		t.Error("a row's Pick must pop the menu itself")
	}
	if s.editor.WrapMode() == before {
		t.Error("the wrap row should have toggled the editor's wrap mode")
	}
}

// TestHomeEditorRightClickMenu is the end-to-end path a user actually takes: a right
// click inside the editor column raises the menu over the still-drawn layout, and it is
// the editor pane — not the sidebar — that claims the gesture.
func TestHomeEditorRightClickMenu(t *testing.T) {
	model, _, _ := newHomeRouter(t, Options{})
	drive := func(msg tea.Msg) { model, _ = model.Update(msg) }
	right := func(x, y int) tea.MouseMsg {
		return tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonRight, X: x, Y: y}
	}

	// Render once before clicking, as bubbletea does: ModularScreen publishes each pane's
	// absolute origin from View, and that origin is what turns the pane-relative click
	// back into the anchor the overlay is placed at.
	_ = model.View()

	drive(right(45, 15)) // the editor column
	menu, ok := model.(core.Router).Top().(*components.MenuScreen)
	if !ok {
		t.Fatalf("a right click in the editor should raise the menu, top is %T", model.(core.Router).Top())
	}
	view := stripANSI(model.View())
	for _, want := range []string{"Copy", "Cut", "Paste", "Toggle wrap"} {
		if !strings.Contains(view, want) {
			t.Errorf("the menu should offer %q:\n%s", want, view)
		}
	}
	// It has to land just under the pointer, not merely exist: same column, one row down
	// so the clicked text stays readable. The editor receives the click pane-relative, so
	// a menu placed at the sidebar's left edge is the failure this catches.
	if x, y := menu.OverlayPos(0, 0); x != 45 || y != 16 {
		t.Errorf("the menu placed at (%d,%d), want (45,16) — the click column, one row below:\n%s", x, y, view)
	}

	drive(tea.KeyMsg{Type: tea.KeyEsc})
	if _, ok := model.(core.Router).Top().(*homeScreen); !ok {
		t.Fatalf("esc should dismiss the menu, top is %T", model.(core.Router).Top())
	}

	// The sidebar is not the editor: its panels never claimed the right button.
	drive(right(5, 15))
	if _, ok := model.(core.Router).Top().(*homeScreen); !ok {
		t.Fatalf("a right click in the sidebar should raise nothing, top is %T", model.(core.Router).Top())
	}
}

// TestHomeEditorMenuQuitGate is the stacking bug the menu's QuitGate exists to prevent:
// ctrl+c runs ahead of the Filtering gate, so without one the router's walk would reach
// homeScreen and draw the unsaved-changes confirm on top of the still-open context menu.
// The first press must close the menu and stop; only the second raises the confirm.
func TestHomeEditorMenuQuitGate(t *testing.T) {
	model, _, _ := newHomeRouter(t, Options{})
	drive := func(msg tea.Msg) { model, _ = model.Update(msg) }
	// The sidebar holds focus at startup, so click into the editor column before typing.
	drive(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: 45, Y: 15})
	drive(tea.MouseMsg{Action: tea.MouseActionRelease, Button: tea.MouseButtonNone, X: 45, Y: 15})
	drive(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("unsaved work")}) // dirty the scratch buffer
	_ = model.View()

	drive(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonRight, X: 45, Y: 15})
	if _, ok := model.(core.Router).Top().(*components.MenuScreen); !ok {
		t.Fatalf("the right click should have raised the menu, top is %T", model.(core.Router).Top())
	}

	drive(tea.KeyMsg{Type: tea.KeyCtrlC})
	if _, ok := model.(core.Router).Top().(*homeScreen); !ok {
		t.Fatalf("ctrl+c should close the menu and stop there, top is %T", model.(core.Router).Top())
	}
	if view := stripANSI(model.View()); strings.Contains(view, "unsaved changes") {
		t.Errorf("the confirm should not appear until the menu is gone:\n%s", view)
	}

	drive(tea.KeyMsg{Type: tea.KeyCtrlC})
	if _, ok := model.(core.Router).Top().(*components.DialogScreen); !ok {
		t.Fatalf("the second ctrl+c should raise the dirty-buffer confirm, top is %T", model.(core.Router).Top())
	}
	if view := stripANSI(model.View()); !strings.Contains(view, "unsaved changes") {
		t.Errorf("the confirm should name the unsaved buffer:\n%s", view)
	}
}

// openMarks names each Open-list row as the sidebar prints it: the title with its flag.
func openMarks(s *homeScreen) []string {
	out := []string{}
	for _, it := range s.openPanel.List().VisibleItems() {
		row, ok := it.(docItem)
		if !ok {
			continue
		}
		out = append(out, row.Title()+row.Mark())
	}
	return out
}

// TestOpenListFlagsDirtyBuffers is the point of the feature: the sidebar is the only place
// unsaved work in a buffer that is NOT on screen is visible. The flag has to track the
// buffer live — nothing rebuilds the open list per keystroke — and it has to survive the
// pane switching to another doc.
func TestOpenListFlagsDirtyBuffers(t *testing.T) {
	s, sh := newHome(t)
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	b := filepath.Join(dir, "b.txt")

	s.openDoc(sh, a)
	s.openDoc(sh, b)
	if got := openMarks(s); !reflect.DeepEqual(got, []string{"a.txt", "• b.txt"}) {
		t.Fatalf("setup: two clean buffers should carry no flags, got %v", got)
	}

	s.Update(sh, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if !s.editor.Dirty() {
		t.Fatal("setup: typing should have dirtied the buffer on screen")
	}
	// No reseed, no SetItems: the row answers from the buffer on every render.
	if got := openMarks(s); !reflect.DeepEqual(got, []string{"a.txt", "• b.txt (*)"}) {
		t.Fatalf("the edited buffer should be flagged without a rebuild, got %v", got)
	}

	s.openDoc(sh, a)
	if got := openMarks(s); !reflect.DeepEqual(got, []string{"• a.txt", "b.txt (*)"}) {
		t.Fatalf("a dirty buffer must stay flagged once the pane leaves it, got %v", got)
	}
	// And it reaches the pixels: the flag is pinned past the suffix column, at the right
	// of everything the row prints.
	for _, line := range strings.Split(stripANSI(s.View(sh)), "\n") {
		if strings.Contains(line, "b.txt") && !strings.Contains(strings.TrimRight(line, " │"), "(*)") {
			t.Fatalf("the flag should reach the rendered sidebar row: %q", line)
		}
	}
}

// TestOpenListFlagClearsWhenBufferGoesClean: the flag is the buffer's own dirty state, so
// it drops the moment the buffer is clean again — here by undoing back to the loaded
// text — with no rebuild of the list in between.
func TestOpenListFlagClearsWhenBufferGoesClean(t *testing.T) {
	s, sh := newHome(t)
	path := filepath.Join(t.TempDir(), "a.txt")

	s.openDoc(sh, path)
	s.Update(sh, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if got := openMarks(s); !reflect.DeepEqual(got, []string{"• a.txt (*)"}) {
		t.Fatalf("setup: the edited buffer should be flagged, got %v", got)
	}

	s.Update(sh, tea.KeyMsg{Type: tea.KeyCtrlZ})
	if s.editor.Dirty() {
		t.Fatal("setup: undoing the only edit should leave the buffer clean")
	}
	if got := openMarks(s); !reflect.DeepEqual(got, []string{"• a.txt"}) {
		t.Fatalf("a clean buffer must lose its flag, got %v", got)
	}
}
