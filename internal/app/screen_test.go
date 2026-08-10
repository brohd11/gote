package app

import (
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

// TestPreviewCycle walks ctrl+p through its three rungs: off → the live side pane →
// the pushed overlay → off. The overlay is a screen on the router's stack, so the
// cycle's third step shows up as a push Action rather than as state here.
func TestPreviewCycle(t *testing.T) {
	s, sh := newHome(t)
	s.openDoc(sh, filepath.Join(t.TempDir(), "a.md"))

	ctrlP := tea.KeyMsg{Type: tea.KeyCtrlP}

	for _, step := range []struct {
		mode    int
		legend  string // the pane's border title, or "" when the preview is off
		absent  string // a legend that must NOT be on screen
		summary string
	}{
		{previewPane, "preview", "glamour", "the first ctrl+p opens the custom reader"},
		{previewGlamour, "glamour", "preview", "the second swaps it for glamour"},
		{previewOff, "", "preview", "the third closes the cycle"},
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
		if step.legend != "" && !strings.Contains(v, step.legend) {
			t.Fatalf("%s: %q should be on screen, render:\n%s", step.summary, step.legend, v)
		}
		if strings.Contains(v, step.absent) {
			t.Fatalf("%s: %q should be gone, render:\n%s", step.summary, step.absent, v)
		}
		if got := focusedPane(s, sh); got != "editor" {
			t.Fatalf("%s: toggling the preview must not move focus, got %s", step.summary, got)
		}
	}
}

// TestPreviewSwapRerenders: switching renderers on an UNEDITED buffer must still draw.
// The refresh skips when the source and width are unchanged, which they are across a
// mode swap — so setPreview has to clear that cache or the new pane comes up blank.
func TestPreviewSwapRerenders(t *testing.T) {
	s, sh := newHome(t)
	s.openDoc(sh, filepath.Join(t.TempDir(), "a.md"))
	s.Update(sh, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("# Title")})

	s.Update(sh, tea.KeyMsg{Type: tea.KeyCtrlP}) // custom
	if v := stripANSI(s.View(sh)); !strings.Contains(v, "Title") {
		t.Fatalf("the custom pane should have rendered, got:\n%s", v)
	}
	s.Update(sh, tea.KeyMsg{Type: tea.KeyCtrlP}) // glamour, same buffer
	v := stripANSI(s.View(sh))
	if !strings.Contains(v, "glamour") {
		t.Fatalf("the glamour pane should be up, got:\n%s", v)
	}
	if !strings.Contains(v, "Title") {
		t.Fatalf("the glamour pane should have rendered without an edit, got:\n%s", v)
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

// TestPreviewOverlayRendersBuffer: the overlay renders the LIVE buffer, not a snapshot
// taken when it was built.
func TestPreviewOverlayRendersBuffer(t *testing.T) {
	s, sh := newHome(t)
	s.openDoc(sh, filepath.Join(t.TempDir(), "a.md"))
	s.Update(sh, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("- bullet")})

	doc := s.previewScreen(false)
	doc.SetSize(sh, 80, 24)
	if v := stripANSI(doc.View(sh)); !strings.Contains(v, "• bullet") {
		t.Fatalf("the overlay should render the live buffer, got:\n%s", v)
	}
	if !strings.Contains(doc.Title, "a.md") {
		t.Errorf("the overlay should name the doc, got %q", doc.Title)
	}
}

// TestPreviewOverlayChain: the two overlays chain on ctrl+p — the custom reader
// REPLACES itself with the glamour one, which then pops, so the fourth press lands
// back on the editor. esc bails out of either directly, which is why the home screen
// tracks only the pane: an esc it never sees cannot desync it.
func TestPreviewOverlayChain(t *testing.T) {
	s, sh := newHome(t)
	s.openDoc(sh, filepath.Join(t.TempDir(), "a.md"))
	s.Update(sh, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("# Title")})

	custom := s.previewScreen(false)
	if _, a := custom.Update(sh, tea.KeyMsg{Type: tea.KeyCtrlP}); msgType(a) != "core.replaceMsg" {
		t.Fatalf("ctrl+p on the custom overlay should hand off to glamour, got %s", msgType(a))
	}

	glam := s.previewScreen(true)
	if !strings.HasPrefix(glam.Title, "glamour · ") {
		t.Errorf("the two overlays must be tellable apart by title, got %q", glam.Title)
	}
	if _, a := glam.Update(sh, tea.KeyMsg{Type: tea.KeyCtrlP}); msgType(a) != "core.popMsg" {
		t.Fatalf("ctrl+p on the glamour overlay should close the cycle, got %s", msgType(a))
	}

	for _, doc := range []*components.DocScreen{s.previewScreen(false), s.previewScreen(true)} {
		if _, a := doc.Update(sh, tea.KeyMsg{Type: tea.KeyEsc}); msgType(a) != "core.popMsg" {
			t.Errorf("esc should pop %q", doc.Title)
		}
	}
}

// TestGlamourRendersBuffer: the alternative renderer produces the document's text,
// and a failure surfaces as page content rather than as an empty page (DocOpts.Render
// has no error channel).
func TestGlamourRendersBuffer(t *testing.T) {
	s, sh := newHome(t)
	s.openDoc(sh, filepath.Join(t.TempDir(), "a.md"))
	s.Update(sh, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("# Heading")})

	doc := s.previewScreen(true)
	doc.SetSize(sh, 80, 24)
	v := stripANSI(doc.View(sh))
	if !strings.Contains(v, "Heading") {
		t.Fatalf("glamour should render the buffer, got:\n%s", v)
	}
	if strings.Contains(v, "glamour: ") {
		t.Fatalf("glamour reported an error:\n%s", v)
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

// TestGlamourStyleTuning pins gote's two deviations from stock glamour, both of which
// are invisible in a diff of the render and obvious in the pane: no document margin
// (the ScrollContainer already draws a border and pads a column, so glamour's own two
// were doubling up) and, unless showLinkURLs is flipped back on, no raw URLs.
func TestGlamourStyleTuning(t *testing.T) {
	out := glamourRender("Some words with a [label](https://example.com/page) in them.\n", 60)

	for _, line := range strings.Split(stripANSI(out), "\n") {
		if strings.TrimSpace(line) != "" && strings.HasPrefix(line, "  ") {
			t.Fatalf("the document margin should be gone, got line %q", line)
		}
	}
	if strings.HasPrefix(out, "\n") || strings.HasSuffix(out, "\n") {
		t.Fatal("the render should not open or close on a blank line")
	}

	plain := stripANSI(out)
	if !strings.Contains(plain, "label") {
		t.Fatalf("the link's text should survive, got:\n%s", plain)
	}
	if showLinkURLs != strings.Contains(plain, "https://example.com/page") {
		t.Fatalf("showLinkURLs = %v disagrees with the render:\n%s", showLinkURLs, plain)
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
