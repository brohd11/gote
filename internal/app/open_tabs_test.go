package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
	"github.com/charmbracelet/x/ansi"
)

func TestDocumentTabColorsFollowGroupFocus(t *testing.T) {
	s, sh := groupHome(t)
	a, _ := seedDoc(t, s, sh, "a.md", "# A")
	b, _ := seedDoc(t, s, sh, "b.md", "# B")
	s.openDoc(sh, b)
	s.moveTab(sh, 1)
	left, right := s.groups()[0], s.groups()[1]
	if left.currentID != a {
		t.Fatal("split lost first document")
	}
	assertTabs := func(want *editorGroup) {
		t.Helper()
		s.View(sh)
		for _, g := range s.groups() {
			background := core.MutedColor
			if g == want {
				background = core.FocusedColor
			}
			label := " " + g.currentName
			if g.fullPreview != nil {
				label += " [P]"
			}
			label += " "
			styled := lipgloss.NewStyle().Foreground(core.OnFocusedColor).Background(background).Bold(true).Render(label)
			if got := g.openTabs.View(false); !strings.Contains(got, styled) {
				t.Fatalf("group %s has incorrect focus color: %q", g.currentName, got)
			}
		}
	}
	assertTabs(right)
	s.modular.FocusSlot(s.panelSlot(left.editorPanel))
	assertTabs(left)
	s.modular.FocusSlot(s.panelSlot(s.docsPane()))
	assertTabs(nil)
	s.modular.FocusSlot(s.panelSlot(right.editorPanel))
	s.setPreview(previewPane)
	s.modular.FocusSlot(s.panelSlot(s.previewPanel))
	assertTabs(nil)
	s.toggleBottom(sh)
	s.modular.FocusSlot(s.panelSlot(s.bottom))
	assertTabs(nil)
	s.modular.FocusSlot(s.panelSlot(right.editorPanel))
	s.toggleFullPreview()
	assertTabs(right)
	s.modular.FocusSlot(s.panelSlot(left.editorPanel))
	assertTabs(left)
}

func tabTestHome(t *testing.T) (tea.Model, *homeScreen, *core.Shared) {
	t.Helper()
	model, s, sh := newHomeRouter(t, Options{})
	Of(sh).close()
	Of(sh).lsp = nil
	s.gitGutter = false
	return model, s, sh
}

func TestOpenDocsViewConfig(t *testing.T) {
	for _, value := range []string{"", "list", "tabs", "unknown"} {
		t.Run(value, func(t *testing.T) {
			cfg := writeConfig(t, "open_docs_view: "+value+"\n")
			cfg.AutoLSP = false
			c := New("test", cfg, Options{})
			defer c.close()
			sh := core.NewShared(c)
			s := NewHomeScreen(sh).(*homeScreen)
			if !s.tabsVisible() {
				t.Fatal("legacy view setting disabled tabs")
			}
		})
	}
}

func TestDocumentKeysThroughRouter(t *testing.T) {
	model, s, sh := tabTestHome(t)
	// Filtering requires a document; an empty Docs pane has no action row.
	dir, err := DocsDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.Receive(sh, ReseedMsg{})
	s.newUnsavedBuffer(sh)
	firstID, firstEditor := s.currentID, s.editor
	model, _ = model.Update(keyMsg("A"))
	position := firstEditor.CursorPosition()
	s.newUnsavedBuffer(sh)
	secondID := s.currentID
	model, _ = model.Update(keyMsg("alt+9"))
	if s.currentID != firstID || s.editor != firstEditor || !s.editorPanel.Focused() {
		t.Fatal("Alt+9 did not switch from the editor")
	}
	if s.editor.CursorPosition() != position || s.editor.Text() != "A" {
		t.Fatal("switch reset buffer or cursor")
	}
	model, _ = model.Update(keyMsg("alt+9"))
	if s.currentID != secondID {
		t.Fatal("previous did not wrap")
	}
	model, _ = model.Update(keyMsg("alt+0"))
	if s.currentID != firstID {
		t.Fatal("next did not wrap")
	}
	model, _ = model.Update(keyMsg("["))
	model, _ = model.Update(keyMsg("]"))
	if s.currentID != firstID || !strings.Contains(s.editor.Text(), "[]") {
		t.Fatal("brackets were stolen from editor")
	}
	s.modular.FocusSlot(s.panelSlot(s.docsPane()))
	model, _ = model.Update(keyMsg("]"))
	if s.currentID != secondID {
		t.Fatal("bare bracket did not navigate outside text capture")
	}
	s.modular.FocusSlot(s.panelSlot(s.docsPane()))
	model, _ = model.Update(keyMsg("/"))
	if !s.modular.Filtering() {
		t.Fatal("test filter was not started")
	}
	model, _ = model.Update(keyMsg("["))
	if s.currentID != secondID || !s.modular.Filtering() {
		t.Fatal("bracket escaped filter")
	}
}

func TestOpenTabsGeometryAndFocus(t *testing.T) {
	_, s, sh := tabTestHome(t)
	s.newUnsavedBuffer(sh)
	s.Update(sh, keyMsg("A"))
	ed, id, position := s.editor, s.currentID, s.editor.CursorPosition()
	s.sidebarW = 34
	s.setPreview(previewPane)
	s.toggleBottom(sh)
	s.View(sh)
	if s.openTabs.h != 1 || s.openTabs.x != 34 || s.openTabs.y != sh.BodyY()+headerRows {
		t.Fatalf("tab geometry = %+v", s.openTabs)
	}
	// Pane traversal skips the non-focusable bar.
	s.modular.FocusSlot(s.panelSlot(s.docsPane()))
	s.Update(sh, keyMsg("shift+tab"))
	if !s.editorPanel.Focused() {
		t.Fatal("tab bar became a keyboard focus stop")
	}
	s.setSidebar(false)
	s.View(sh)
	if s.openTabs.x != 0 || s.openTabs.w != 100 || s.openTabs.h != 1 || s.editorSlot() != s.firstSlot()+1 {
		t.Fatal("hidden sidebar also hid or misplaced tabs")
	}
	if s.editor != ed || s.currentID != id || s.editor.CursorPosition() != position || s.editor.Text() != "A" {
		t.Fatal("layout rebuild lost editor state")
	}
	s.Update(sh, keyMsg("ctrl+z"))
	if s.editor.Text() != "" {
		t.Fatal("layout rebuild lost undo history")
	}
}

func TestOpenTabsMouseAndPreview(t *testing.T) {
	model, s, sh := tabTestHome(t)
	s.newUnsavedBuffer(sh)
	firstID, firstName := s.currentID, s.currentName
	s.Update(sh, keyMsg("A"))
	s.newUnsavedBuffer(sh)
	secondID := s.currentID
	s.toggleFullPreview()
	model.View()
	row := ansi.Strip(s.openTabs.View(false))
	if strings.Count(row, "[P]") != 1 || !strings.Contains(row, "(*)") {
		t.Fatalf("markers = %q", row)
	}
	if s.fullPreview.Title != "" {
		t.Fatal("reader repeats filename header")
	}
	x := s.openTabs.x + strings.Index(row, firstName)
	model, _ = model.Update(tea.MouseClickMsg{X: x, Y: s.openTabs.y, Button: tea.MouseLeft})
	if s.currentID != firstID || s.currentID == secondID || s.fullPreview == nil || !s.editorPanel.Focused() {
		t.Fatal("tab click failed to activate document in reader")
	}
	// Release and motion from the tab must not start a gesture in the new child.
	model, _ = model.Update(tea.MouseMotionMsg{X: 50, Y: 10, Button: tea.MouseLeft})
	model, _ = model.Update(tea.MouseReleaseMsg{X: 50, Y: 10, Button: tea.MouseLeft})
	if s.openTabs.mouseDown {
		t.Fatal("tab gesture did not end")
	}
	// An editor-owned gesture crossing the bar is passed through.
	if _, handled := s.documentTabInput(sh, tea.MouseReleaseMsg{X: x, Y: s.openTabs.y, Button: tea.MouseLeft}); handled {
		t.Fatal("bar swallowed another pane's release")
	}

}

func TestOpenTabsSavedNamesAndClose(t *testing.T) {
	_, s, sh := tabTestHome(t)
	root := t.TempDir()
	for _, dir := range []string{"one", "two"} {
		path := filepath.Join(root, dir, "same.md")
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("# doc"), 0600); err != nil {
			t.Fatal(err)
		}
		s.openDoc(sh, path)
	}
	s.SetSize(sh, 300, 30)
	s.View(sh)
	row := ansi.Strip(s.openTabs.View(false))
	if !strings.Contains(row, "one") || !strings.Contains(row, "two") {
		t.Fatalf("duplicate names lack context: %q", row)
	}
	s.editorExit(sh)
	s.View(sh)
	row = ansi.Strip(s.openTabs.View(false))
	if strings.Count(row, "same.md") != 1 || strings.Contains(row, " · ") {
		t.Fatalf("closed tab or stale duplicate context remains: %q", row)
	}
	before := s.currentID
	s.stepDocument(sh, 1)
	if s.currentID != before {
		t.Fatal("single-document navigation changed identity")
	}
}

func TestOpenTabsHideEditorTitlesAcrossBufferSwitches(t *testing.T) {
	model, s, sh := tabTestHome(t)
	s.newUnsavedBuffer(sh)
	firstID := s.currentID
	s.newUnsavedBuffer(sh)
	secondID := s.currentID
	for _, id := range []string{firstID, secondID} {
		s.switchBuffer(sh, id)
		model.View()
		if strings.Contains(ansi.Strip(s.editor.View(sh)), s.currentName) {
			t.Fatal("retained editor still displays filename in tab mode")
		}
		// The first content row sits directly beneath the tab strip.
		model, _ = model.Update(tea.MouseClickMsg{X: s.openTabs.x + 2, Y: s.openTabs.y + 1, Button: tea.MouseLeft})
		_, y, visible := s.editor.CursorAnchor()
		if !visible || y != s.openTabs.y+1 {
			t.Fatal("first-row click or popup anchor retained the old title offset")
		}
	}
}

func TestOpenTabsActionsAndMinimal(t *testing.T) {
	_, s, sh := tabTestHome(t)
	for _, name := range pickerLabels(s.editorSettingsMenu()) {
		if strings.Contains(name, "Show open documents as") {
			t.Fatal("obsolete view toggle remains in Actions")
		}
	}
	s.minimal = true
	if s.tabsVisible() {
		t.Fatal("minimal launch showed tabs")
	}
	if _, handled := s.documentTabInput(sh, keyMsg("alt+0")); handled {
		t.Fatal("minimal launch intercepted document keys")
	}
	// Keep the generic component's interface explicit at its integration boundary.
	var _ components.Panel = s.openTabs
	var _ components.PaneOriginer = s.openTabs
}
