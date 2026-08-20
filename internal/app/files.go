package app

import (
	"path/filepath"
	"strings"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

// Document operations driven from the docs list: opening, creating and renaming files, and
// the row-anchored line edit both the new-file and rename boxes are built from.

// pickDoc routes the docs list's rows: the action row opens the new-file line
// edit; a doc row opens (or switches to) that doc in the editor pane.
func (s *homeScreen) pickDoc(sh *core.Shared, it list.Item) core.Action {
	if _, ok := it.(newFileItem); ok {
		return s.newFile(sh)
	}
	di, ok := it.(docItem)
	if !ok {
		return core.Action{}
	}
	return s.openDoc(sh, di.doc.Path)
}

// openDoc switches the editor pane to path and moves focus to it. An already-open doc is
// a switch, not an open: Ctx.OpenDoc hands back the existing editor and the pane swap
// leaves it exactly as it stands — unsaved edits, cursor, scroll and undo history all
// intact — because EditorScreen reads its file only on the first Init.
func (s *homeScreen) openDoc(sh *core.Shared, path string) core.Action {
	c := Of(sh)
	ed := c.OpenDoc(path, s.editorOpts())
	s.currentPath = path
	s.editor = ed
	s.openPanel.SetItems(docItems(c.OpenDocs(), s.currentPath))
	cmd := s.editorPanel.SetChild(ed)
	// After SetChild, so the layout setPreview rebuilds is sized around the new buffer.
	s.enforcePreview()
	focus := s.modular.FocusSlot(s.editorSlot())
	return core.Async(tea.Batch(cmd, focus))
}

// rowLineEdit builds a floating line edit sitting exactly over the selected docs row —
// the shape both the new-file and rename boxes take. Anchor math: the docs panel is
// column 0 row 0 of the layout, so its outer top-left is (0, BodyY); RowY gives the row
// WITHIN the panel (its border, and its filter line when one is live), and the LineEdit
// anchor sits one row above the row it covers, since it draws its own top border there.
// The panel owns that offset rather than this file assuming it: the filter line makes it
// vary, and it used to cancel against the border exactly. x=0 and width=sidebarWidth
// land the box's borders exactly on the panel's own.
func (s *homeScreen) rowLineEdit(sh *core.Shared, placeholder string,
	onDone func(*core.Shared, string) core.Action) *components.LineEditScreen {
	row, ok := s.docsPanel.RowY(s.docsPanel.List().Index())
	if !ok {
		row = 1 // the selected row is on-page by construction; never die on it
	}
	edit := components.NewLineEdit(placeholder, 0, sh.BodyY()+row-1, sidebarWidth, onDone, nil)
	edit.Help = []key.Binding{} // the hint row wraps at sidebar width; keep the box slim
	return edit
}

// newFile pushes the row-anchored line edit that names a document into being.
func (s *homeScreen) newFile(sh *core.Shared) core.Action {
	return core.Push(s.rowLineEdit(sh, "name (a/b nests dirs)", s.createFile))
}

// createFile is the line edit's OnDone: resolve the typed name against the doc
// store (the scan root in scan mode), write the file (making parent dirs for
// names containing "/"), then reseed the list and open the file in the editor.
// Blank input cancels quietly. Errors surface as a popup — gote has no status
// pane — swapped in over the line edit so the overlay's stack depth holds.
func (s *homeScreen) createFile(sh *core.Shared, name string) core.Action {
	if strings.TrimSpace(name) == "" {
		return core.Pop()
	}
	c := Of(sh)
	base := c.ScanDir
	if c.Mode == ModeHome {
		dir, err := DocsDir()
		if err != nil {
			return core.Replace(errPopup("new file", err))
		}
		base = dir
	}
	path, err := newDocPath(base, name, c.NewExt)
	if err != nil {
		return core.Replace(errPopup("new file", err))
	}
	if err := createDoc(path); err != nil {
		return core.Replace(errPopup("new file", err))
	}
	return core.Seq(core.Pop(), core.PropagateAll(ReseedMsg{}), s.openDoc(sh, path))
}

// docsKey is the docs panel's OnKey (ListPanelOpts.OnKey): ctrl+r renames the selected
// doc, ctrl+d deletes it. The hook fires only while the panel is focused and only when it
// is not running a /-filter, so neither the editor nor a filter query can lose either
// chord. Reporting false hands the key back to the list — which is what leaves the
// "+ new file" row inert for both, since an action row has no file to act on.
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

// renameFile pushes the same row-anchored line edit newFile does, prefilled with the
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
// line edit, so the overlay's stack depth holds (createFile's precedent).
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
	if ed, open := c.Doc(doc.Path); open && ed != nil {
		ed.SetPath(path)
		c.RekeyDoc(doc.Path, path, ed)
		if s.currentPath == doc.Path {
			s.currentPath = path
			s.enforcePreview() // a rename can take a file out of markdown under a live pane
		}
	}
	return core.Seq(core.Pop(), core.PropagateAll(ReseedMsg{}))
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
			act = core.Async(s.showDoc(c, next))
			s.refreshPreview()
		}
	}
	return core.Seq(core.Pop(), act, core.PropagateAll(ReseedMsg{}))
}

// errPopup builds the error dialog a failed new-file submit is replaced with.
func errPopup(title string, err error) *components.DialogScreen {
	return components.CreatePopup(title, err.Error(), core.Pop())
}
