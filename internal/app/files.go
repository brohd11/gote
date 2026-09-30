package app

import (
	"path/filepath"
	"strings"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
)

// Document operations driven from the docs list: opening, renaming and deleting files.

// filePanelOpts wires the folder view with the flat list's verbs (open, rename, delete);
// the panel walks folders and gote handles file rows.
func (s *homeScreen) filePanelOpts(c *Ctx) components.FilePanelOpts {
	root := docsRoot(c)
	return components.FilePanelOpts{
		Dir:        root,
		Root:       root,
		Border:     true,                      // as both sidebar lists are: with three panes up, the focused one must show
		Compact:    true,                      // a 30-cell column has no room for the standard delegate's second line
		Colors:     components.FileColorsDirs, // folders apart from documents at a glance in a narrow column
		DensityKey: densityKey,
		UpKey:      upKey,
		TitleColor: s.fileTitleColor,
		KeepColor:  true, // git state is what the reader wants on the row they are pointing at; the selection bar still says which row that is
		Selection:  sidebarSelection,
		OnDir:      func(*core.Shared, string) core.Action { return core.Async(s.refreshDocsGit()) },
		Include:    s.includeDoc(c),
		OnSelect:   func(sh *core.Shared, e components.FileEntry) core.Action { return s.openDoc(sh, e.Path) },
		OnKey:      s.fileKey,
		OnError:    func(_ *core.Shared, err error) core.Action { return core.Push(errPopup("open folder", err)) },
	}
}

// descendFolder also handles "..", which FilePanel's OnKey hook deliberately skips.
// Documents have nothing to descend into; Enter still opens them.
func (s *homeScreen) descendFolder(sh *core.Shared) core.Action {
	e, ok := s.filePanel.Selected()
	if !ok || !e.IsDir {
		return core.Action{}
	}
	return s.filePanel.SetDir(sh, e.Path)
}

// toggleHidden refreshes the current folder without changing its directory or focus.
func (s *homeScreen) toggleHidden() core.Action {
	s.showHidden = !s.showHidden
	s.filePanel.Refresh()
	if s.showHidden {
		return core.SetStatus("hidden files shown")
	}
	return core.SetStatus("hidden files off")
}

// fileKey is the folder view's ctrl+r rename and ctrl+d delete. Root is the entry's own
// directory, so the rename box opens on a bare name.
func (s *homeScreen) fileKey(sh *core.Shared, k string, e components.FileEntry) (core.Action, bool) {
	if e.IsDir {
		return core.Action{}, false
	}
	doc := DocFile{Name: e.Name, Path: e.Path, Root: e.Dir}
	switch {
	case core.MatchKey(k, renameKey):
		return s.renameFile(sh, doc), true
	case core.MatchKey(k, deleteKey):
		return s.deleteFile(sh, doc), true
	}
	return core.Action{}, false
}

// pickDoc opens (or switches to) the selected document in the editor pane.
func (s *homeScreen) pickDoc(sh *core.Shared, it list.Item) core.Action {
	di, ok := it.(docItem)
	if !ok {
		return core.Action{}
	}
	if di.doc.Path == "" {
		return s.switchBuffer(sh, di.identity())
	}
	return s.openDoc(sh, di.doc.Path)
}

// openDoc switches the editor pane to path and focuses it. An open doc is just switched
// to, keeping its edits, caret and history (editors read their file only once).
func (s *homeScreen) openDoc(sh *core.Shared, path string) core.Action {
	c := Of(sh)
	if owner := c.groupFor(path); owner != nil {
		s.activateGroup(owner)
	}
	s.invalidateDocumentTools()
	// Asked before OpenDoc, which registers the doc. A newly opened doc must be seeded by hand
	// while the reader holds the pane (seedForPreview).
	_, was := c.Doc(path)
	ed := c.OpenDoc(path, s.editorOpts(c))
	s.seedForPreview(ed, path, !was || c.unread(path))
	s.currentID, s.currentPath, s.currentName = path, path, docName(path)
	c.SetActive(s.currentID)
	s.editor = ed
	s.configureSignColumns()
	// paneChild rather than SetChild: with the reader up, a pick opens INTO the preview —
	// the pane keeps a reader, rebuilt around the doc that was just picked.
	cmd := s.paneChild()
	// After the swap, so the layout enforcePreview rebuilds is sized around the new buffer.
	cmd = tea.Batch(cmd, s.enforcePreview())
	focus := s.modular.FocusSlot(s.editorSlot())
	return core.Async(tea.Batch(cmd, focus))
}

// switchBuffer shows an already-retained buffer, including a pathless unsaved one.
func (s *homeScreen) switchBuffer(sh *core.Shared, id string) core.Action {
	c := Of(sh)
	if owner := c.groupFor(id); owner != nil {
		s.activateGroup(owner)
	}
	s.invalidateDocumentTools()
	doc, ok := c.bufferInfo(id)
	if !ok {
		return core.Action{}
	}
	ed, _ := c.buffer(id)
	s.currentID, s.currentPath, s.currentName = doc.ID, doc.Path, doc.Name
	c.SetActive(s.currentID)
	s.editor = ed
	s.configureSignColumns()
	cmd := tea.Batch(s.paneChild(), s.enforcePreview())
	focus := s.modular.FocusSlot(s.editorSlot())
	return core.Async(tea.Batch(cmd, focus))
}

// rowLineEdit builds a line edit over the selected docs row for renaming. The docs panel
// is at (0, BodyY) below the header; RowY gives the row within the panel, and the anchor is one row above
// (the box draws its own top border). The box spans the sidebar width.
func (s *homeScreen) rowLineEdit(sh *core.Shared, placeholder string,
	onDone func(*core.Shared, string) core.Action) *components.LineEditScreen {
	pane := s.docsPane()
	row, ok := pane.RowY(pane.List().Index())
	if !ok {
		row = 1 // the selected row is on-page by construction; never die on it
	}
	edit := components.NewLineEdit(placeholder, 0, sh.BodyY()+s.headerHeight()+row-1, s.sidebarPaneWidth(), false, onDone, nil)
	return edit
}

// docsKey is the docs panel's OnKey: ctrl+r renames, ctrl+d deletes. It only fires while
// the panel is focused and not filtering.
func (s *homeScreen) docsKey(sh *core.Shared, k string, it list.Item) (core.Action, bool) {
	di, ok := it.(docItem)
	if !ok {
		return core.Action{}, false
	}
	switch {
	case core.MatchKey(k, renameKey):
		return s.renameFile(sh, di.doc), true
	case core.MatchKey(k, deleteKey):
		return s.deleteFile(sh, di.doc), true
	}
	return core.Action{}, false
}

// renameFile opens a rename box prefilled with the doc's path relative to its root, so
// editing the directory part moves the file.
func (s *homeScreen) renameFile(sh *core.Shared, doc DocFile) core.Action {
	rel := docRel(doc)
	edit := s.rowLineEdit(sh, "new name",
		func(sh *core.Shared, name string) core.Action { return s.submitRename(sh, doc, rel, name) })
	edit.SetValue(rel)
	return core.Push(edit)
}

// submitRename moves the file to the typed path (relative to the doc's root); blank or
// unchanged input cancels. Errors replace the box with a popup. An open doc is repointed
// three ways: the editor (SetPath), the open set (RekeyDoc) and the pane's current path.
// The buffer and its history are untouched.
func (s *homeScreen) submitRename(sh *core.Shared, doc DocFile, rel, name string) core.Action {
	name = strings.TrimSpace(name)
	if name == "" || name == rel {
		return core.Pop()
	}
	c := Of(sh)
	path, err := newDocPath(doc.Root, name, c.NewExt)
	if err != nil {
		return core.Replace(errPopup("rename", err))
	}
	if path == doc.Path {
		return core.Pop()
	}
	if err := renameDoc(doc.Path, path); err != nil {
		return core.Replace(errPopup("rename", err))
	}
	act := core.Action{}
	if ed, open := c.Doc(doc.Path); open && ed != nil {
		highlight := ed.SetPath(path)
		c.RekeyDoc(doc.Path, path, ed)
		act.Cmd = tea.Batch(act.Cmd, highlight)
		if s.editor == ed {
			s.currentID, s.currentPath, s.currentName = path, path, docName(path)
			c.SetActive(path)
			// A rename can take a file out of markdown under a live preview.
			act.Cmd = tea.Batch(act.Cmd, s.enforcePreview())
		}
	}
	return core.Seq(core.Pop(), act, core.PropagateAll(ReseedMsg{}))
}

// docRel names a doc relative to its root (so two notes.md are distinguishable), falling
// back to its base name.
func docRel(doc DocFile) string {
	rel, err := filepath.Rel(doc.Root, doc.Path)
	if err != nil {
		return doc.Name
	}
	return rel
}

// deleteFile asks for confirmation (a y/n overlay, unlike rename's silent submit, since
// delete destroys). An open doc gets a second line: its buffer closes too.
func (s *homeScreen) deleteFile(sh *core.Shared, doc DocFile) core.Action {
	body := "delete " + docRel(doc) + "?"
	if _, open := Of(sh).Doc(doc.Path); open {
		body += "\n\nits open buffer closes; unsaved changes are lost"
	}
	return core.Push(&components.DialogScreen{
		Title:   "delete",
		Render:  func(*core.Shared) string { return body },
		OnYes:   func(sh *core.Shared) core.Action { return s.submitDelete(sh, doc) },
		Help:    components.DefaultHelpKeys,
		Overlay: true,
	})
}

// submitDelete removes the file and catches up: an open doc leaves the open set, and the
// pane moves off it as alt+w would. Errors replace the confirm with a popup.
func (s *homeScreen) submitDelete(sh *core.Shared, doc DocFile) core.Action {
	if err := deleteDoc(doc.Path); err != nil {
		return core.Replace(errPopup("delete", err))
	}
	c := Of(sh)
	act := core.Action{}
	if _, open := c.Doc(doc.Path); open {
		next := c.CloseDoc(doc.Path)
		if doc.Path == s.currentPath {
			act = core.Async(s.showBuffer(c, next))
			s.refreshPreview()
		}
	}
	return core.Seq(core.Pop(), act, core.PropagateAll(ReseedMsg{}))
}

// errPopup builds the error dialog for a failed document operation.
func errPopup(title string, err error) *components.DialogScreen {
	return components.CreatePopup(title, err.Error(), core.Pop())
}
