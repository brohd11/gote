package app

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"

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
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyShiftRight}) // docs -> open
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyShiftRight}) // open -> editor
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
	if _, ok := c.Open[path]; !ok {
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
	s.openDoc(sh, a)
	drive(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("alpha body")})
	drive(tea.KeyMsg{Type: tea.KeyCtrlF})
	if overlay := stripANSI(model.View()); !strings.Contains(overlay, "╭") || !strings.Contains(overlay, "find:") {
		t.Fatalf("ctrl+f should show the shared rounded line-edit overlay:\n%s", overlay)
	}
	drive(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("alpha")})
	drive(tea.KeyMsg{Type: tea.KeyEnter})
	if view := stripANSI(model.View()); !strings.Contains(view, "a.txt [+]") || strings.Contains(view, "a.txt [+] · find:") || strings.Index(view, "find: alpha") < strings.Index(view, "a.txt [+]") {
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

	// The keyboard path: shift+right walks focus onto the pane, the nav keys scroll it.
	s.modular.FocusSlot(s.editorSlot())
	s.Update(sh, tea.KeyMsg{Type: tea.KeyShiftRight})
	if !s.previewPanel.Focused() {
		t.Fatal("shift+right from the editor should focus the pane")
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

// TestPreviewOverlayRendersBuffer: the overlay renders the LIVE buffer, not a snapshot
// taken when it was built.
func TestPreviewOverlayRendersBuffer(t *testing.T) {
	s, sh := newHome(t)
	s.openDoc(sh, filepath.Join(t.TempDir(), "a.md"))
	s.Update(sh, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("- bullet")})

	doc := s.previewScreen()
	doc.SetSize(sh, 80, 24)
	if v := stripANSI(doc.View(sh)); !strings.Contains(v, "• bullet") {
		t.Fatalf("the overlay should render the live buffer, got:\n%s", v)
	}
	if !strings.Contains(doc.Title, "a.md") {
		t.Errorf("the overlay should name the doc, got %q", doc.Title)
	}
}

// TestPreviewOverlayCloses: ctrl+p and esc both pop the parked overlay, which is why
// the home screen tracks only the pane — an esc it never sees cannot desync it.
func TestPreviewOverlayCloses(t *testing.T) {
	s, sh := newHome(t)
	s.openDoc(sh, filepath.Join(t.TempDir(), "a.md"))
	s.Update(sh, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("# Title")})

	for _, key := range []tea.KeyMsg{{Type: tea.KeyCtrlP}, {Type: tea.KeyEsc}} {
		doc := s.previewScreen()
		if _, a := doc.Update(sh, key); msgType(a) != "core.popMsg" {
			t.Errorf("%s should pop the overlay, got %s", key, msgType(a))
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
	if c.Open[renamed] != ed {
		t.Fatal("the new path should resolve to the same buffer")
	}
	if _, ok := c.Open[old]; ok {
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
	if Of(sh).Open[file] != s.editor {
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
// launch must still draw its breadcrumb and help bar.
func TestNonMinimalKeepsChrome(t *testing.T) {
	s, _ := newHome(t)
	if mask := s.ChromeMask(); mask != (core.ChromeMask{}) {
		t.Fatalf("the doc-list launch should mask nothing, got %+v", mask)
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
	if strings.Contains(minimal, "sidebar") || strings.Contains(minimal, "preview") {
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
	if !strings.Contains(ordLines[len(ordLines)-1], "sidebar") {
		t.Fatalf("the ordinary launch's last row should be the help bar, got %q", ordLines[len(ordLines)-1])
	}
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
	s.Update(sh, tea.KeyMsg{Type: tea.KeyShiftRight})
	s.Update(sh, tea.KeyMsg{Type: tea.KeyShiftRight})
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
	if c.Mode == ModeVault || s.editor != oldEditor || c.Open[oldPath] != oldEditor {
		t.Fatal("requesting a dirty switch mutated the session before confirmation")
	}

	act := s.activateVault(sh, "notes") // the dirty popup's confirm action
	if msgType(act) != "core.seqMsg" {
		t.Fatalf("confirmed switch should reset navigation, got %s", msgType(act))
	}
	if c.Mode != ModeVault || c.VaultName != "notes" || c.ScanDir != vault {
		t.Fatalf("active vault = mode %v name %q dir %q", c.Mode, c.VaultName, c.ScanDir)
	}
	if len(c.Open) != 0 || len(c.OpenOrder) != 0 || len(c.OpenRoots) != 0 {
		t.Fatalf("confirmed switch left open state: %v %v %v", c.Open, c.OpenOrder, c.OpenRoots)
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
	s.Update(sh, tea.KeyMsg{Type: tea.KeyShiftRight})
	s.Update(sh, tea.KeyMsg{Type: tea.KeyShiftRight})
	s.Update(sh, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	if !s.editor.Dirty() {
		t.Fatal("? should type into the editor, not open help")
	}

	if _, act := s.Update(sh, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?"), Alt: true}); act.Msg == nil {
		t.Fatal("alt+? should push the help overlay even from the editor")
	}
}
