package app

import (
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
)

// The docs sidebar's right-click menu: Open, Rename, Delete on the clicked row, in every
// file view. The click has already selected the row, so Rename and Delete run exactly as
// their keys do (the rename box anchors on the selected row). The menu opens at the
// pointer, recorded by noteRightClick, since the list hooks carry no coordinates.

// noteRightClick records where the last right press landed, for the menu's anchor.
func (s *homeScreen) noteRightClick(msg tea.Msg) {
	if c, ok := msg.(tea.MouseClickMsg); ok && c.Button == tea.MouseRight {
		s.rightClickX, s.rightClickY = c.X, c.Y
	}
}

// docMenu raises the menu for doc at the pointer. A pathless unsaved buffer can only be
// opened, as its keys can only open it.
func (s *homeScreen) docMenu(doc DocFile, open func(*core.Shared) core.Action) core.Action {
	unsaved := doc.Path == ""
	return core.Push(components.NewMenu(components.MenuOpts{
		Anchor: components.AnchorAt(s.rightClickX, s.rightClickY),
		Style:  menuStyle,
		Items: []components.MenuItem{
			{Label: "Open", Key: 'o', Pick: menuPick(1, open)},
			{Label: "Rename", Key: 'r', Hint: hint(renameKey), Disabled: unsaved, Pick: menuPick(1, func(sh *core.Shared) core.Action {
				return s.renameFile(sh, doc)
			})},
			{Label: "Delete", Key: 'd', Hint: hint(deleteKey), Disabled: unsaved, Pick: menuPick(1, func(sh *core.Shared) core.Action {
				return s.deleteFile(sh, doc)
			})},
		},
	}))
}

// docsPointer is the flat list's OnPointer: a right click on a doc row raises the menu.
func (s *homeScreen) docsPointer(_ *core.Shared, it list.Item, right bool) (core.Action, bool) {
	di, ok := it.(docItem)
	if !ok || !right {
		return core.Action{}, false
	}
	return s.docMenu(di.doc, func(sh *core.Shared) core.Action { return s.pickDoc(sh, it) }), true
}

// filePointer is the folder view's OnPointer. Files get the menu; a right click on a
// folder does nothing, as ctrl+r and ctrl+d do nothing there (fileKey).
func (s *homeScreen) filePointer(_ *core.Shared, e components.FileEntry, right bool) (core.Action, bool) {
	if !right {
		return core.Action{}, false
	}
	if e.IsDir {
		return core.Action{}, true
	}
	doc := DocFile{Name: e.Name, Path: e.Path, Root: e.Dir}
	return s.docMenu(doc, func(sh *core.Shared) core.Action { return s.openDoc(sh, e.Path) }), true
}

// groupedPointer is the grouped view's OnPointer: doc leaves get the menu, folder headings
// nothing.
func (s *homeScreen) groupedPointer(_ *core.Shared, node components.TreeNode, right bool) (core.Action, bool) {
	if !right {
		return core.Action{}, false
	}
	item, ok := node.Item.(groupedDocItem)
	if !ok {
		return core.Action{}, true
	}
	return s.docMenu(item.doc, func(sh *core.Shared) core.Action { return s.pickDoc(sh, item.docItem) }), true
}
