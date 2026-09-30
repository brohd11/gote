package app

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
)

// rightClickDocRow right-clicks the docs row whose title contains name, as a user would.
func rightClickDocRow(t *testing.T, model tea.Model, s *homeScreen, sh *core.Shared, name string) tea.Model {
	t.Helper()
	_ = model.(core.Router).View()
	pane := s.docsPane()
	for i, it := range pane.List().Items() {
		if !strings.Contains(it.FilterValue(), name) {
			continue
		}
		row, ok := pane.RowY(i)
		if !ok {
			t.Fatalf("row %q is off-page", name)
		}
		// Not pumped: the router applies the menu's push directly, and pumping would also
		// run the focused list's marquee ticks, real timers.
		next, _ := model.Update(tea.MouseClickMsg{X: 8, Y: sh.BodyY() + s.headerHeight() + row, Button: tea.MouseRight})
		return next
	}
	t.Fatalf("no docs row %q", name)
	return model
}

// TestDocsContextMenu: in every file view a right click on a doc row raises Open, Rename
// and Delete, each doing what its key (or enter) does on that row.
func TestDocsContextMenu(t *testing.T) {
	for _, view := range []fileView{fileViewFlat, fileViewFolder, fileViewGrouped} {
		t.Run(map[fileView]string{fileViewFlat: "flat", fileViewFolder: "folder", fileViewGrouped: "grouped"}[view], func(t *testing.T) {
			root := scanTree(t)
			model, s, sh := newHomeRouter(t, Options{Mode: ModeScan, Dir: root})
			defer Of(sh).close()
			s.setFileView(view)
			top := func() core.Screen { return model.(core.Router).Top() }

			model = rightClickDocRow(t, model, s, sh, "notes.md")
			menu, ok := top().(*components.MenuScreen)
			if !ok {
				t.Fatalf("a right click on a doc should raise the menu, top is %T", top())
			}
			if got := strings.Join(menuLabels(menu.Items()), " | "); got != "Open | Rename | Delete" {
				t.Fatalf("docs menu = %q", got)
			}

			model = chooseMenuRow(t, model, "Rename")
			edit, ok := top().(*components.LineEditScreen)
			if !ok || edit.Value() != "notes.md" {
				t.Fatalf("Rename should raise the prefilled rename box, top is %T", top())
			}
			model, _ = model.Update(keyMsg("esc"))

			model = rightClickDocRow(t, model, s, sh, "notes.md")
			model = chooseMenuRow(t, model, "Delete")
			if _, ok := top().(*components.DialogScreen); !ok {
				t.Fatalf("Delete should raise the confirm, top is %T", top())
			}
			model, _ = model.Update(keyMsg("esc"))

			model = rightClickDocRow(t, model, s, sh, "notes.md")
			model = chooseMenuRow(t, model, "Open")
			if top() != s || s.currentPath != filepath.Join(root, "notes.md") || !s.editorPanel.Focused() {
				t.Fatalf("Open should open the doc and focus the editor (top %T, path %q)", top(), s.currentPath)
			}
		})
	}
}

// TestDocsContextMenuSkipsFolders: folders have no Open/Rename/Delete (their keys do
// nothing either), so a right click there raises no menu.
func TestDocsContextMenuSkipsFolders(t *testing.T) {
	for _, view := range []fileView{fileViewFolder, fileViewGrouped} {
		root := scanTree(t)
		model, s, sh := newHomeRouter(t, Options{Mode: ModeScan, Dir: root})
		s.setFileView(view)
		model = rightClickDocRow(t, model, s, sh, "sub")
		if top := model.(core.Router).Top(); top != s {
			t.Fatalf("view %d: a right click on a folder should raise nothing, top is %T", view, top)
		}
		Of(sh).close()
	}
}
