package app

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/brohd11/bubblestack/core"
)

func TestGroupedFilesUseScanAndOneLevelGroups(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"README.md", "docs/z.md", "docs/a.md", "docs/something/a.md",
		"docs/something/deeper/skip.md", "empty/skip.txt", ".hidden/skip.md", "vendor/skip.md"} {
		writeSearchFile(t, root, name, "text\n")
	}
	cfg := DefaultConfig()
	cfg.FileView, cfg.Extensions = "grouped", []string{"md"}
	s, sh := newHomeCfg(t, cfg, Options{Mode: ModeScan, Dir: root, Depth: 2, DepthSet: true})
	defer Of(sh).close()
	if s.docsPane() != s.groupedPanel || s.focusedPane() != s.groupedPanel {
		t.Fatal("grouped startup did not select and focus the grouped panel")
	}
	want := []string{".", "README.md", "docs", "a.md", "z.md", "docs/something", "a.md"}
	if got := rowTitles(s.groupedPanel.List()); !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
	files := map[string]bool{}
	for _, node := range s.groupedDocNodes(Of(sh)) {
		for _, child := range node.Children {
			item := child.Item.(groupedDocItem)
			if len(child.Children) != 0 || item.SuffixText() != "" {
				t.Fatal("file has nested rows or a directory suffix")
			}
			if files[child.ID] {
				t.Fatal("duplicate basenames share an ID")
			}
			files[child.ID] = true
		}
	}
	if len(files) != len(Of(sh).Files) {
		t.Fatal("grouped view differs from the scan")
	}
}

func TestGroupedFilesRetainFoldsFilterAndSelection(t *testing.T) {
	root := scanTree(t)
	s, sh := newScanHome(t, root)
	defer Of(sh).close()
	s.setFileView(fileViewGrouped)
	p := s.groupedPanel
	p.Select("dir:" + filepath.Join(root, "sub"))
	s.Update(sh, keyMsg("space"))
	if hasRow(rowTitles(p.List()), "deep.md") {
		t.Fatal("Space did not fold the group")
	}
	writeSearchFile(t, root, "sub/later.md", "later")
	s.Receive(sh, ReseedMsg{})
	for range 3 {
		s.Update(sh, keyMsg("alt+f"))
	}
	if hasRow(rowTitles(p.List()), "later.md") || hasRow(rowTitles(p.List()), "deep.md") {
		t.Fatal("reseed or cycling lost the fold")
	}
	if node, _ := p.Selected(); node.ID != "dir:"+filepath.Join(root, "sub") {
		t.Fatal("reseed or cycling lost the selected group")
	}
	s.Update(sh, keyMsg("/")) // exposes collapsed descendants before matching
	p.List().SetFilterText("deep")
	if got := rowTitles(p.List()); !reflect.DeepEqual(got, []string{"deep.md"}) {
		t.Fatalf("collapsed file missing from search: %v", got)
	}
	for range 3 {
		s.Update(sh, keyMsg("alt+f"))
	}
	s.Receive(sh, ReseedMsg{})
	if p.List().FilterValue() != "deep" {
		t.Fatal("cycling or reseeding lost the applied filter")
	}
	s.Update(sh, keyMsg("esc"))
	if hasRow(rowTitles(p.List()), "deep.md") {
		t.Fatal("clearing search lost the saved fold")
	}
	s.Update(sh, keyMsg("right"))
	p.Select("file:" + filepath.Join(root, "sub", "later.md"))
	s.Receive(sh, ReseedMsg{})
	if node, _ := p.Selected(); node.ID != "file:"+filepath.Join(root, "sub", "later.md") {
		t.Fatal("reseed lost selected file identity")
	}
}

func TestGroupedFilesOpenAndFolderActions(t *testing.T) {
	for _, mouse := range []bool{false, true} {
		t.Run(map[bool]string{false: "enter", true: "click"}[mouse], func(t *testing.T) {
			root := scanTree(t)
			model, s, sh := newHomeRouter(t, Options{Mode: ModeScan, Dir: root})
			defer Of(sh).close()
			s.setFileView(fileViewGrouped)
			s.groupedPanel.Select("dir:" + filepath.Join(root, "sub"))
			before := s.currentID
			for _, k := range []string{"ctrl+r", "ctrl+d"} {
				model, _ = model.Update(keyMsg(k))
				if model.(core.Router).Top() != s || s.currentID != before {
					t.Fatal("folder heading ran a file action")
				}
			}
			for _, expanded := range []bool{false, true} {
				var msg tea.Msg = keyMsg("enter")
				if mouse {
					row, _ := s.groupedPanel.RowY(s.groupedPanel.List().Index())
					msg = tea.MouseClickMsg{X: 8, Y: sh.BodyY() + row, Button: tea.MouseLeft}
				}
				model, _ = model.Update(msg)
				if hasRow(rowTitles(s.groupedPanel.List()), "deep.md") != expanded {
					t.Fatalf("folder activation should toggle expansion to %v", expanded)
				}
				if node, _ := s.groupedPanel.Selected(); node.ID != "dir:"+filepath.Join(root, "sub") ||
					s.focusedPane() != s.groupedPanel || s.currentID != before {
					t.Fatal("folding moved selection, focus, or the editor")
				}
			}
			path := filepath.Join(root, "sub", "deep.md")
			s.groupedPanel.Select("file:" + path)
			var msg tea.Msg = keyMsg("enter")
			if mouse {
				row, ok := s.groupedPanel.RowY(s.groupedPanel.List().Index())
				if !ok {
					t.Fatal("selected file is off-page")
				}
				msg = tea.MouseClickMsg{X: 8, Y: sh.BodyY() + row, Button: tea.MouseLeft}
			}
			model, _ = model.Update(msg)
			if s.currentPath != path || !s.editorPanel.Focused() {
				t.Fatal("file activation did not open the document and focus its editor")
			}
		})
	}
}

func TestGroupedFilesRenameAndDelete(t *testing.T) {
	model, s, sh, root := renameFixture(t, filepath.Join("sub", "old.md"), "body")
	defer Of(sh).close()
	s.setFileView(fileViewGrouped)
	s.groupedPanel.Select("file:" + filepath.Join(root, "sub", "old.md"))
	row, _ := s.groupedPanel.RowY(s.groupedPanel.List().Index())
	model, edit := pressRename(model)
	if edit == nil || edit.Value() != filepath.Join("sub", "old.md") {
		t.Fatal("rename did not preserve root-relative context")
	}
	if x, y, w := edit.Anchor(); x != 0 || y != sh.BodyY()+row-1 || w != s.sidebarPaneWidth() {
		t.Fatalf("rename anchor = %d,%d width %d", x, y, w)
	}
	edit.SetValue("moved/new.md")
	model, _ = model.Update(keyMsg("enter"))
	if got := rowTitles(s.groupedPanel.List()); !reflect.DeepEqual(got, []string{"moved", "new.md"}) {
		t.Fatalf("rename did not rebuild groups: %v", got)
	}
	path := filepath.Join(root, "moved", "new.md")
	if body, err := os.ReadFile(path); err != nil || string(body) != "body" {
		t.Fatalf("renamed contents = %q, %v", body, err)
	}
	s.groupedPanel.Select("file:" + path)
	model, dlg := pressDelete(model)
	if dlg == nil {
		t.Fatal("delete bypassed its confirmation")
	}
	model, _ = model.Update(keyMsg("esc"))
	if _, err := os.Stat(path); err != nil {
		t.Fatal("cancelled deletion removed the file")
	}
	model, _ = pressDelete(model)
	model, _ = model.Update(keyMsg("y"))
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("confirmed delete left file: %v", err)
	}
	if len(s.groupedPanel.List().Items()) != 0 || s.focusedPane() != s.groupedPanel {
		t.Fatal("deleting the last file left a group or lost focus")
	}
}

func TestGroupedFilesVaultSwitch(t *testing.T) {
	s, sh := newScanHome(t, scanTree(t))
	defer Of(sh).close()
	s.setFileView(fileViewGrouped)
	old := s.groupedPanel
	root := t.TempDir()
	writeSearchFile(t, root, "other/new.md", "new")
	Of(sh).Config.Vaults["other"] = VaultConfig{Path: root}
	s.activateVault(sh, "other")
	if s.fileView != fileViewGrouped || s.groupedPanel == old || s.focusedPane() != s.groupedPanel {
		t.Fatal("vault switch did not reset grouped data and preserve the view")
	}
	if got := rowTitles(s.groupedPanel.List()); !reflect.DeepEqual(got, []string{"other", "new.md"}) {
		t.Fatalf("vault switch retained old files: %v", got)
	}
}
