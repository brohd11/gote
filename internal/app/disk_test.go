package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
)

func writeDiskDoc(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func openDiskFixture(t *testing.T) (tea.Model, *homeScreen, *core.Shared, string) {
	t.Helper()
	model, s, sh, dir := renameFixture(t, "first.md", "first")
	Of(sh).close()
	Of(sh).lsp = nil
	s.gitGutter = false
	model, cmd := model.Update(keyMsg("enter"))
	model = pumpModel(model, cmd)
	return model, s, sh, dir
}

func refreshThroughActions(t *testing.T, model tea.Model) tea.Model {
	t.Helper()
	model, cmd := model.Update(keyMsg("ctrl+alt+a"))
	return choosePickerRow(t, pumpModel(model, cmd), "⟳ Refresh")
}

func TestActionsRefreshChecksRetainedDocuments(t *testing.T) {
	for _, state := range []string{"clean", "dirty", "deleted"} {
		t.Run(state, func(t *testing.T) {
			model, s, sh, dir := openDiskFixture(t)
			first := s.editor
			secondPath := filepath.Join(dir, "second.md")
			writeDiskDoc(t, secondPath, "second")
			model = pumpModel(model, s.openDoc(sh, secondPath).Cmd)
			second, activeID := s.editor, s.currentID
			if state == "dirty" {
				model, _ = model.Update(keyMsg("!"))
			}
			filterList(t, s.docsPanel.List(), "first")
			writeDiskDoc(t, filepath.Join(dir, "first.md"), "external first")
			writeDiskDoc(t, filepath.Join(dir, "first-new.md"), "new file")
			if state == "deleted" {
				if err := os.Remove(secondPath); err != nil {
					t.Fatal(err)
				}
			} else {
				writeDiskDoc(t, secondPath, "external second")
			}
			refreshThroughActions(t, model)
			if first.Text() != "external first" || first.Dirty() {
				t.Fatal("refresh missed the inactive clean buffer")
			}
			if s.editor != second || s.currentID != activeID {
				t.Fatal("refresh changed the active document")
			}
			rows := rowTitles(s.docsPanel.List())
			if len(rows) != 2 || s.docsPanel.List().SelectedItem() == nil {
				t.Fatalf("refresh lost the filter or failed to rescan the list: %v", rows)
			}
			for _, row := range rows {
				if !strings.Contains(row, "first") {
					t.Fatalf("refresh lost the filter: %v", rows)
				}
			}
			switch state {
			case "clean":
				if second.Text() != "external second" || second.Dirty() || second.DiskChanged() {
					t.Fatal("refresh did not reload the active clean buffer")
				}
			case "dirty":
				if second.Text() != "!second" || !second.Dirty() || second.ChangeMark() != " (!*)" {
					t.Fatal("refresh lost local edits or failed to mark the conflict")
				}
			case "deleted":
				if second.Text() != "second" || second.Dirty() || second.ChangeMark() != " (!)" {
					t.Fatal("refresh lost the deleted file's buffer or failed to mark it")
				}
			}
		})
	}
}

func TestActionsRefreshUpdatesPreviews(t *testing.T) {
	for _, mode := range []string{"side", "full", "single-file"} {
		t.Run(mode, func(t *testing.T) {
			var model tea.Model
			var s *homeScreen
			var sh *core.Shared
			var path string
			if mode == "single-file" {
				path = filepath.Join(t.TempDir(), "file.md")
				writeDiskDoc(t, path, "original")
				model, s, sh = newHomeRouter(t, Options{Mode: ModeFile, File: path, Preview: true})
				Of(sh).close()
				Of(sh).lsp = nil
				s.gitGutter = false
			} else {
				model, s, sh, _ = openDiskFixture(t)
				path = s.currentPath
				if mode == "full" {
					model = pumpModel(model, s.toggleFullPreview().Cmd)
				} else {
					s.cyclePreview()
				}
			}
			writeDiskDoc(t, path, "updated preview text")
			refreshThroughActions(t, model)
			var view string
			if mode == "side" {
				view = s.previewPanel.View(false)
			} else {
				view = s.fullPreview.View(sh)
			}
			if s.editor.Text() != "updated preview text" || !strings.Contains(stripANSI(view), "updated preview text") {
				t.Fatalf("refresh left the buffer or preview stale: %s", view)
			}
		})
	}
}

func TestFocusRefreshesInactiveDocumentsBeneathDialog(t *testing.T) {
	model, s, sh, dir := openDiskFixture(t)
	first := s.editor
	secondPath := filepath.Join(dir, "second.md")
	writeDiskDoc(t, secondPath, "second")
	model = pumpModel(model, s.openDoc(sh, secondPath).Cmd)
	second := s.editor
	model, cmd := model.Update(keyMsg("ctrl+s"))
	model = pumpModel(model, cmd)
	dialog := model.(core.Router).Top()
	writeDiskDoc(t, filepath.Join(dir, "first.md"), "external first")
	writeDiskDoc(t, secondPath, "external second")
	model, cmd = model.Update(tea.FocusMsg{})
	model = pumpModel(model, cmd)
	if first.Text() != "external first" || second.Text() != "external second" {
		t.Fatal("focus missed active/inactive document under dialog")
	}
	if model.(core.Router).Top() != dialog || s.editor != second {
		t.Fatal("refresh disturbed dialog or active editor")
	}
}

func TestFocusMarksDirtyDocsAndTabs(t *testing.T) {
	model, s, sh, dir := openDiskFixture(t)
	model, _ = model.Update(keyMsg("!"))
	writeDiskDoc(t, filepath.Join(dir, "first.md"), "external")
	model, cmd := model.Update(tea.FocusMsg{})
	model = pumpModel(model, cmd)
	if s.editor.Text() != "!first" {
		t.Fatal("focus overwrote edits")
	}
	if view := stripANSI(s.View(sh)); !strings.Contains(view, "(!*)") {
		t.Fatalf("tabs missing conflict marker: %s", view)
	}
	model, _ = model.Update(keyMsg("ctrl+s"))
	model, cmd = model.Update(keyMsg("enter"))
	model = pumpModel(model, cmd)
	if _, ok := model.(core.Router).Top().(*components.DialogScreen); !ok {
		t.Fatal("save missing acknowledgement")
	}
	model, cmd = model.Update(keyMsg("y"))
	model = pumpModel(model, cmd)
	if s.editor.ChangeMark() != "" {
		t.Fatal("successful overwrite kept marker")
	}
	b, _ := os.ReadFile(s.currentPath)
	if string(b) != "!first" {
		t.Fatal("save did not write buffer")
	}
}

func TestFocusRefreshesBothPreviewModes(t *testing.T) {
	for _, full := range []bool{false, true} {
		name := "side"
		if full {
			name = "full"
		}
		t.Run(name, func(t *testing.T) {
			model, s, sh, dir := openDiskFixture(t)
			if full {
				model = pumpModel(model, s.toggleFullPreview().Cmd)
			} else {
				s.cyclePreview()
			}
			writeDiskDoc(t, filepath.Join(dir, "first.md"), "updated preview text")
			model, cmd := model.Update(tea.FocusMsg{})
			model = pumpModel(model, cmd)
			var view string
			if full {
				view = s.fullPreview.View(sh)
			} else {
				view = s.previewPanel.View(false)
			}
			if !strings.Contains(stripANSI(view), "updated preview text") {
				t.Fatalf("preview stale: %s", view)
			}
		})
	}
}

func TestSingleFilePreviewLoadTracksDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.md")
	writeDiskDoc(t, path, "original")
	model, s, sh := newHomeRouter(t, Options{Mode: ModeFile, File: path, Preview: true})
	Of(sh).close()
	Of(sh).lsp = nil
	s.gitGutter = false
	if s.editor.Text() != "original" || s.fullPreview == nil {
		t.Fatal("preview launch not seeded")
	}
	writeDiskDoc(t, path, "changed")
	model, cmd := model.Update(tea.FocusMsg{})
	pumpModel(model, cmd)
	if s.editor.Text() != "changed" || !strings.Contains(stripANSI(s.fullPreview.View(sh)), "changed") {
		t.Fatal("single-file preview did not reload")
	}
}

func TestRemovedDocumentIgnoresPendingDiskRead(t *testing.T) {
	model, s, sh, dir := openDiskFixture(t)
	ed := s.editor
	writeDiskDoc(t, filepath.Join(dir, "first.md"), "external")
	msg := ed.CheckDiskChanges()()
	model, cmd := model.Update(keyMsg("ctrl+x"))
	model = pumpModel(model, cmd)
	model, cmd = model.Update(msg)
	pumpModel(model, cmd)
	if ed.Text() != "first" || len(Of(sh).OpenDocs()) != 0 {
		t.Fatal("removed buffer received stale disk result")
	}
}

func TestSaveCompletesAfterSwitchWithoutRekeyingActiveDoc(t *testing.T) {
	model, s, sh, dir := openDiskFixture(t)
	first, firstPath := s.editor, s.currentPath
	model, _ = model.Update(keyMsg("!"))
	model, _ = model.Update(keyMsg("ctrl+s"))
	model, check := model.Update(keyMsg("enter"))
	// The pre-save check pops the filename prompt and returns the delayed write.
	model, write := model.Update(check())
	secondPath := filepath.Join(dir, "second.md")
	writeDiskDoc(t, secondPath, "second")
	model = pumpModel(model, s.openDoc(sh, secondPath).Cmd)
	second := s.editor
	model = pumpModel(model, write)
	if s.editor != second || s.currentPath != secondPath {
		t.Fatal("late save changed active document")
	}
	if got, ok := Of(sh).Doc(firstPath); !ok || got != first || first.Dirty() {
		t.Fatal("late save lost original buffer")
	}
	b, _ := os.ReadFile(firstPath)
	if string(b) != "!first" {
		t.Fatal("late save wrote wrong content")
	}
}

func TestReloadReconcilesLSPWithoutAnotherKey(t *testing.T) {
	for _, trigger := range []string{"focus", "refresh"} {
		t.Run(trigger, func(t *testing.T) {
			model, s, sh, dir := openDiskFixture(t)
			path := filepath.Join(dir, "main.py")
			writeDiskDoc(t, path, "old_name = 1\n")
			model = pumpModel(model, s.openDoc(sh, path).Cmd)
			manager := newLSPManager(DefaultConfig(), "test")
			// Exercise UI reconciliation without launching a server or waiting for its events.
			manager.once.Do(func() {})
			Of(sh).lsp = manager
			s.lspWaiting = true
			defer func() { Of(sh).lsp = nil }()
			manager.Reconcile(Of(sh))
			previous := manager.desired[path].version
			writeDiskDoc(t, path, "new_name = 2\n")
			if trigger == "refresh" {
				refreshThroughActions(t, model)
			} else {
				model, cmd := model.Update(tea.FocusMsg{})
				pumpModel(model, cmd)
			}
			if got := manager.desired[path]; got.text != "new_name = 2\n" || got.version <= previous {
				t.Fatalf("LSP snapshot stale: %+v", got)
			}
		})
	}
}
