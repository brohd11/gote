package app

import (
	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/components/editor"
	"github.com/brohd11/bubblestack/core"

	tea "charm.land/bubbletea/v2"
)

// The editor pane's lifecycle: its options, its right-click menu, and how a buffer
// leaves (save-as, alt+w, release).

// editorOpts is the hook set every editor here gets: alt+w closes the buffer, esc
// releases the keys, ctrl+s saves and stays. BaseDir is docsRoot's directory, not the
// cwd, and is read per call because a vault switch moves it.
func (s *homeScreen) editorOpts(c *Ctx) editor.Opts {
	defaults := c.Config.modeDefaults(c.Mode)
	return editor.Opts{
		TrackDiskChanges: true,
		Wrap:             defaults.Wrap,
		LineNumbers:      defaults.LineNumbers,
		BaseDir:          docsRoot(c),
		HideTitle:        s.tabsVisible(),
		OnExit:           s.editorExit,
		OnRelease:        s.editorRelease,
		OnSaved:          s.editorSaved,
		Search:           true,
		ContextMenu:      true,
		MenuStyle:        menuStyle,
		ContextItems:     s.editorContextItems,
		OnSignClick:      s.gitSignClick,
		IndentGuides:     s.indentGuides,
		ResolveLanguage:  editorLanguageForPath,
	}
}

// installScratch puts a fresh untracked scratch editor in the pane. Its tab waits
// for the first edit or ctrl+n.
func (s *homeScreen) installScratch(c *Ctx) {
	id, name := c.newUnsavedIdentity()
	opts := s.editorOpts(c)
	opts.Title, opts.Crumb = name, name
	s.currentID, s.currentPath, s.currentName = id, "", name
	c.SetActive(id)
	s.editor = c.newEditor(opts)
}

// newUnsavedBuffer implements ctrl+n: the untouched startup scratch is promoted, otherwise
// a new named buffer is created and focused.
func (s *homeScreen) newUnsavedBuffer(sh *core.Shared) core.Action {
	c := Of(sh)
	s.invalidateDocumentTools()
	if _, tracked := c.buffer(s.currentID); !tracked && s.currentPath == "" && !s.editor.Dirty() {
		c.trackUnsaved(s.currentID, s.currentName, s.editor)
	} else {
		s.installScratch(c)
		c.trackUnsaved(s.currentID, s.currentName, s.editor)
		s.configureSignColumns()
	}
	s.gutter = gutter{}
	cmd := tea.Batch(s.paneChild(), s.enforcePreview(), s.modular.FocusSlot(s.editorSlot()))
	return core.Async(cmd)
}

// promoteEditedScratch retains the startup/after-close scratch as soon as an actual
// mutation makes it dirty. Navigation and no-op editing leave it outside Open.
func (s *homeScreen) promoteEditedScratch(sh *core.Shared) {
	if s.minimal || s.currentPath != "" || s.editor == nil || !s.editor.Dirty() {
		return
	}
	c := Of(sh)
	if _, tracked := c.buffer(s.currentID); tracked {
		return
	}
	c.trackUnsaved(s.currentID, s.currentName, s.editor)
}

// editorContextItems are gote's rows on the editor's right-click menu, after its clipboard
// rows: the language-server requests that act on what was clicked (a right press has
// already moved the caret there). Everything else is a key chord or the menu bar. Minimal
// mode keeps only Hover info: definition and references can navigate away from the one
// file it edits. Omitted when no server can answer, to keep the menu short. Picks pop the
// menu; no Hints, since the menu dispatches no accelerators.
func (s *homeScreen) editorContextItems(sh *core.Shared) []components.MenuItem {
	if !s.lspFeatureReady(sh) {
		return nil
	}
	request := func(label string, kind lspRequestKind) components.MenuItem {
		return components.MenuItem{Label: label, Pick: func(sh *core.Shared) core.Action {
			return core.Seq(core.Pop(), s.requestAt(sh, kind))
		}}
	}
	items := []components.MenuItem{request("Hover info", lspReqHover)}
	if s.minimal {
		return items
	}
	return append(items,
		request("Go to definition", lspReqDefinition),
		request("Find references", lspReqReferences))
}

// editorSaved is the ctrl+s hook: the buffer stays and gote catches up. A save-as rekeys
// the open set (or alt+w would close a stale path), and the reseed updates both lists.
func (s *homeScreen) editorSaved(sh *core.Shared, path string) core.Action {
	s.invalidateDocumentTools()
	c := Of(sh)
	if s.currentPath == "" {
		// A first save can happen before typing (and therefore before automatic
		// promotion). Retain it briefly so rekey can preserve the normal Open slot.
		c.trackUnsaved(s.currentID, s.currentName, s.editor)
	}
	oldPath := s.currentPath
	c.RekeyDoc(s.currentID, path, s.editor)
	// Forget both paths: a save can add or remove a shebang, and a save-as onto an existing
	// path would inherit its cache.
	forgetSniffedLanguage(oldPath)
	forgetSniffedLanguage(path)
	s.currentID, s.currentPath, s.currentName = path, path, docName(path)
	c.SetActive(path)
	if c.lsp != nil {
		c.lsp.DidSave(path)
		s.formatOnSave(sh)
	}
	// Re-read the git baseline: a save-as is a different file to git, and HEAD may have moved.
	s.gutter = gutter{}
	// A save-as can rename markdown out of markdown under an open preview.
	return core.Seq(core.Async(s.enforcePreview()), core.PropagateAll(ReseedMsg{}))
}

// editorExit is the alt+w hook (after the editor's own save prompt): the doc leaves the
// open set, the pane shows the next open doc (or a scratch buffer), and focus returns to
// the docs list, unhiding the sidebar if needed. In minimal mode alt+w quits, as in
// nano.
func (s *homeScreen) editorExit(sh *core.Shared) core.Action {
	if s.minimal {
		return core.Async(tea.Quit)
	}
	c := Of(sh)
	split := len(c.groups) > 1
	cmd := s.showBuffer(c, c.CloseDoc(s.currentID))
	if split {
		return core.Seq(core.Async(tea.Batch(cmd, s.reconcileGroups(sh), s.rebuildGroups(sh))), core.PropagateAll(ReseedMsg{}))
	}
	if !s.sidebar {
		s.setSidebar(true)
	}
	focus := s.modular.FocusSlot(s.firstSlot())
	s.refreshPreview()
	return core.Seq(core.Async(tea.Batch(cmd, focus)), core.PropagateAll(ReseedMsg{}))
}

// showBuffer points the pane at open buffer id, or a scratch buffer for "", used by alt+w
// and delete. enforcePreview runs after the swap; the returned Init cmd must be emitted.
// Only a restored, never-read buffer needs seeding.
func (s *homeScreen) showBuffer(c *Ctx, id string) tea.Cmd {
	if c.activeGroup == s.editorGroup && id != s.currentID {
		s.invalidateDocumentTools()
	}
	s.nextID = ""
	if doc, ok := c.bufferInfo(id); ok {
		s.currentID, s.currentPath, s.currentName = doc.ID, doc.Path, doc.Name
		c.SetActive(doc.ID)
		s.editor, _ = c.buffer(id)
		s.seedForPreview(s.editor, doc.Path, doc.Path != "" && c.unread(doc.Path))
	} else {
		s.installScratch(c)
	}
	// Gutter visibility belongs to the pane, not an individual buffer.
	s.configureSignColumns()
	cmd := s.paneChild()
	if c.activeGroup != s.editorGroup {
		return cmd
	}
	return tea.Batch(cmd, s.enforcePreview())
}

// editorRelease is the esc hook: hand the keys back to the pane that last had them without
// touching the buffer (alt+w would close it). With Screen.Update's esc it forms one
// toggle. The sidebar is unhidden only when no other remembered pane is on screen.
func (s *homeScreen) editorRelease(*core.Shared) core.Action {
	if s.panelSlot(s.lastPane) == noFocus && !s.sidebar {
		s.setSidebar(true)
	}
	return core.Async(s.modular.FocusSlot(s.releaseSlot()))
}

// releaseSlot is where esc sends the keys: the pane that last held them, or slot 0 (the
// docs pane when the sidebar is up) if that pane is gone.
func (s *homeScreen) releaseSlot() int {
	if slot := s.panelSlot(s.lastPane); slot != noFocus {
		return slot
	}
	return s.firstSlot()
}
