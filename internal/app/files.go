package app

import (
	"path/filepath"
	"strings"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
)

// Document operations driven from the docs list: opening, renaming and deleting files.

// filePanelOpts wires the folder view. It is the same set of verbs the flat docs list
// has — open, rename, delete — pointed at one directory instead of a whole scan:
// the panel owns walking into folders, gote owns what a FILE row means.
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
		KeepColor:  true, // git state is what the reader wants on the row they are pointing at; the frame's rule still says which row that is
		OnDir:      func(*core.Shared, string) core.Action { return core.Async(s.requestDocsGit()) },
		Include:    includeDoc(c),
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

// fileKey is the folder view's row keys, docsKey's counterpart: the same ctrl+r rename and
// ctrl+d delete, over an entry instead of a seeded DocFile. Root is the entry's own
// directory, which is what makes the rename box open on a bare name here — in the flat
// list the name it prefills carries the path down from the scan root, because that is the
// context that list shows.
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

// openDoc switches the editor pane to path and moves focus to it. An already-open doc is
// a switch, not an open: Ctx.OpenDoc hands back the existing editor and the pane swap
// leaves it exactly as it stands — unsaved edits, cursor, scroll and undo history all
// intact — because EditorScreen reads its file only on the first Init.
func (s *homeScreen) openDoc(sh *core.Shared, path string) core.Action {
	c := Of(sh)
	// Asked BEFORE OpenDoc, which memoizes — afterwards every doc looks open. A buffer new
	// to the open set has to be seeded by hand while the reader holds the pane, or its
	// async load reaches nothing (seedForPreview).
	_, was := c.Doc(path)
	ed := c.OpenDoc(path, s.editorOpts())
	s.seedForPreview(ed, path, !was)
	s.currentID, s.currentPath, s.currentName = path, path, docName(path)
	s.editor = ed
	s.configureSignColumns()
	s.openPanel.SetItems(openDocItems(c, s.currentID))
	// paneChild rather than SetChild: with the reader up, a pick opens INTO the preview —
	// the pane keeps a reader, rebuilt around the doc that was just picked.
	cmd := s.paneChild()
	// After the swap, so the layout enforcePreview rebuilds is sized around the new buffer.
	cmd = tea.Batch(cmd, s.enforcePreview())
	focus := s.modular.FocusSlot(s.editorSlot())
	return core.Async(tea.Batch(cmd, focus))
}

// switchBuffer shows an already-retained Open row, including a pathless unsaved one.
func (s *homeScreen) switchBuffer(sh *core.Shared, id string) core.Action {
	c := Of(sh)
	doc, ok := c.bufferInfo(id)
	if !ok {
		return core.Action{}
	}
	ed, _ := c.buffer(id)
	s.currentID, s.currentPath, s.currentName = doc.ID, doc.Path, doc.Name
	s.editor = ed
	s.configureSignColumns()
	s.openPanel.SetItems(openDocItems(c, s.currentID))
	cmd := tea.Batch(s.paneChild(), s.enforcePreview())
	focus := s.modular.FocusSlot(s.editorSlot())
	return core.Async(tea.Batch(cmd, focus))
}

// rowLineEdit builds a floating line edit sitting exactly over the selected docs row —
// for renaming. Anchor math: the docs panel is
// column 0 row 0 of the layout, so its outer top-left is (0, BodyY); RowY gives the row
// WITHIN the panel (its border, and its filter line when one is live), and the LineEdit
// anchor sits one row above the row it covers, since it draws its own top border there.
// The panel owns that offset rather than this file assuming it: the filter line makes it
// vary, and it used to cancel against the border exactly. x=0 and the live sidebar
// width land the box's borders exactly on the panel's own.
func (s *homeScreen) rowLineEdit(sh *core.Shared, placeholder string,
	onDone func(*core.Shared, string) core.Action) *components.LineEditScreen {
	pane := s.docsPane()
	row, ok := pane.RowY(pane.List().Index())
	if !ok {
		row = 1 // the selected row is on-page by construction; never die on it
	}
	edit := components.NewLineEdit(placeholder, 0, sh.BodyY()+row-1, s.sidebarPaneWidth(), onDone, nil)
	edit.Help = []key.Binding{} // the hint row wraps at sidebar width; keep the box slim
	return edit
}

// docsKey is the docs panel's OnKey (ListPanelOpts.OnKey): ctrl+r renames the selected
// doc, ctrl+d deletes it. The hook fires only while the panel is focused and only when it
// is not running a /-filter, so neither the editor nor a filter query can lose either
// chord. Reporting false hands the key back to the list when there is no document.
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

// renameFile pushes a row-anchored line edit prefilled with the
// doc's path relative to its origin root — so editing the directory part moves the file
// as well as renaming it.
func (s *homeScreen) renameFile(sh *core.Shared, doc DocFile) core.Action {
	rel := docRel(doc)
	edit := s.rowLineEdit(sh, "new name",
		func(sh *core.Shared, name string) core.Action { return s.submitRename(sh, doc, rel, name) })
	edit.SetValue(rel)
	return core.Push(edit)
}

// submitRename is the rename box's OnDone: resolve the typed path against the doc's
// own root, move the file, then catch the app up with where it now lives. Blank input
// and an unchanged name cancel quietly. Errors surface as a popup swapped in over the
// line edit, so the overlay's stack depth holds.
//
// A doc that is OPEN needs three things pointed at the new path, and each is the only
// home of one fact: the editor knows where to save (SetPath, which also moves its title
// and re-picks the highlighter for a changed extension), the ctx keys the open set by
// path (RekeyDoc — the same call a save-as makes), and the screen tracks which doc the
// pane is showing. The buffer itself is never touched, so unsaved edits and undo
// history survive a rename exactly as they survive a save-as.
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
		ed.SetPath(path)
		c.RekeyDoc(doc.Path, path, ed)
		if s.currentPath == doc.Path {
			s.currentID, s.currentPath, s.currentName = path, path, docName(path)
			// A rename can take a file out of markdown under a live preview.
			act = core.Async(s.enforcePreview())
		}
	}
	return core.Seq(core.Pop(), act, core.PropagateAll(ReseedMsg{}))
}

// docRel is how a doc is named to the user in the boxes that act on it: its path
// relative to the origin root, so two "notes.md" in different folders of a scan are told
// apart. An unrelatable root falls back to the base name — a doc always has one.
func docRel(doc DocFile) string {
	rel, err := filepath.Rel(doc.Root, doc.Path)
	if err != nil {
		return doc.Name
	}
	return rel
}

// deleteFile raises the delete confirm. A y/n overlay rather than the rename box's
// silent submit, because this is the one docs-list verb that destroys something: rename
// is protected by renameDoc refusing an occupied target, but a delete has nothing to
// refuse. The shape is dirtyPopup's — an overlay DialogScreen with the shared
// confirm/cancel hints — and not CreatePopup, which builds an acknowledgement.
//
// An open doc gets a second line: deleting the file closes its buffer, so unsaved edits
// go with it, and that is worth saying before the y rather than after.
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

// submitDelete is the confirm's OnYes: remove the file, then catch the app up with a
// document that no longer exists. Errors surface as a popup swapped in over the confirm,
// so the overlay's stack depth holds (submitRename's precedent).
//
// A doc that is OPEN has to leave the open set as well as the disk, or the Open list
// would keep a row for a file nothing can save — and when it is also the doc in the
// editor pane, the pane has to move off it, exactly as ctrl+x moves it (showDoc, shared
// with editorExit). Focus needs no touching: the confirm pops back to the docs list,
// which is where the key came from.
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
