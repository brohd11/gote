package app

import (
	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/components/editor"
	"github.com/brohd11/bubblestack/core"

	tea "charm.land/bubbletea/v2"
)

// The editor pane's lifecycle: the options homeScreen instances it with, its right-click
// menu, and the three ways a buffer leaves — saved under a new path, closed with ctrl+x,
// or released back to the pane layout.

// editorOpts is the hook set every editor in this screen is built with — the pane's
// three ways out and back: ctrl+x closes the buffer, esc hands the keys back, ctrl+s
// writes it and stays. Path is filled in by whoever constructs the editor (Ctx.OpenDoc
// for a doc, left empty for the scratch buffer).
func (s *homeScreen) editorOpts() editor.Opts {
	return editor.Opts{
		HideTitle:       s.tabsVisible(),
		OnExit:          s.editorExit,
		OnRelease:       s.editorRelease,
		OnSaved:         s.editorSaved,
		Search:          true,
		ContextMenu:     true,
		ContextItems:    s.editorContextItems,
		IndentGuides:    s.indentGuides,
		ResolveLanguage: editorLanguageForPath,
	}
}

// installScratch puts a fresh, untracked pathless editor in the pane. It previews its
// unsaved_N identity immediately so the title is stable if the first edit promotes it;
// the Open row itself is deferred until that edit or ctrl+n.
func (s *homeScreen) installScratch(c *Ctx) {
	id, name := c.newUnsavedIdentity()
	opts := s.editorOpts()
	opts.Title, opts.Crumb = name, name
	s.currentID, s.currentPath, s.currentName = id, "", name
	c.SetActive(id)
	s.editor = editor.New(opts)
}

// newUnsavedBuffer implements ctrl+n in the multi-document workspace. The untouched
// startup scratch already is the new blank buffer, so its first ctrl+n promotes it. From
// any retained buffer a new named editor is created, registered, shown and focused.
func (s *homeScreen) newUnsavedBuffer(sh *core.Shared) core.Action {
	c := Of(sh)
	closePreview := s.closeFullPreview()
	if _, tracked := c.buffer(s.currentID); !tracked && s.currentPath == "" && !s.editor.Dirty() {
		c.trackUnsaved(s.currentID, s.currentName, s.editor)
	} else {
		s.installScratch(c)
		c.trackUnsaved(s.currentID, s.currentName, s.editor)
		s.configureSignColumns()
	}
	s.gutter = gutter{}
	s.openPanel.SetItems(openDocItems(c, s.currentID))
	cmd := tea.Batch(s.paneChild(), s.enforcePreview(), s.modular.FocusSlot(s.editorSlot()))
	return core.Seq(closePreview, core.Async(cmd))
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
	s.openPanel.SetItems(openDocItems(c, s.currentID))
}

// editorContextItems are gote's rows on the editor's right-click menu, below the
// clipboard verbs: the view toggles that otherwise only exist as chords. They are built
// fresh on every press, which is what lets Disabled track the current document — the
// preview is a markdown reader and refuses everything else, the same gate ctrl+p uses.
// Each Pick pops the menu itself (the component's convention). No Hints: the menu
// dispatches no accelerators, so a key-shaped hint would be a promise it doesn't keep.
func (s *homeScreen) editorContextItems(sh *core.Shared) []components.MenuItem {
	return append(s.editorViewItems(sh), s.editorLanguageItems(sh)...)
}

// editorLanguageItems are the pointer-driven half of the language-server features. A
// right press has already moved the caret to the clicked cell
// (EditorScreen.pressContext), so every row here acts on what was clicked — which is how
// gote answers "hover the mouse" without the framework streaming a motion event per cell
// crossed, and how the two gestures stay reachable in a terminal that eats modified
// clicks before gote sees them.
//
// They are OMITTED rather than muted when no server can answer. Five permanently grey
// rows on every markdown file would nearly double a menu that is meant to be scanned in
// one glance, and unlike the preview rows — which go live the moment the document
// changes — these say nothing useful about a document with no language server at all.
func (s *homeScreen) editorLanguageItems(sh *core.Shared) []components.MenuItem {
	if !s.lspFeatureReady(sh) {
		return nil
	}
	request := func(label string, kind lspRequestKind) components.MenuItem {
		return components.MenuItem{Label: label, Pick: func(sh *core.Shared) core.Action {
			return core.Seq(core.Pop(), s.requestAt(sh, kind))
		}}
	}
	return []components.MenuItem{
		request("Hover info", lspReqHover),
		request("Go to definition", lspReqDefinition),
		request("Find references", lspReqReferences),
		request("Format document", lspReqFormat),
	}
}

func (s *homeScreen) editorViewItems(sh *core.Shared) []components.MenuItem {
	outlineLabel := "Show outline"
	if s.outlineVisible {
		outlineLabel = "Hide outline"
	}
	return []components.MenuItem{
		{Label: "Toggle preview", Disabled: !s.previewable(), Pick: func(*core.Shared) core.Action {
			return core.Seq(core.Pop(), s.cyclePreview())
		}},
		{Label: "Full preview", Disabled: !s.previewable(), Pick: func(*core.Shared) core.Action {
			return core.Seq(core.Pop(), s.toggleFullPreview())
		}},
		{Label: "Toggle wrap", Pick: func(*core.Shared) core.Action {
			s.editor.ToggleWrap()
			return core.Pop()
		}},
		{Label: "Toggle line numbers", Pick: func(*core.Shared) core.Action {
			s.editor.ToggleLineNums()
			return core.Pop()
		}},
		{Label: outlineLabel, Pick: func(*core.Shared) core.Action {
			return core.Seq(core.Pop(), s.toggleOutline(sh))
		}},
		{Label: "Toggle diagnostics panel", Pick: func(*core.Shared) core.Action {
			return core.Seq(core.Pop(), s.toggleBottom(sh))
		}},
		{Label: "Toggle diagnostics gutter", Pick: func(*core.Shared) core.Action {
			s.setDiagnosticsGutter(!s.diagnosticsGutter)
			return core.Pop()
		}},
		{Label: "Toggle git gutter", Pick: func(*core.Shared) core.Action {
			// Seq rather than a bare Pop: turning the column on hands back a baseline
			// read, and the router collects the cmd lane of every Action in a Seq.
			return core.Seq(core.Pop(), core.Async(s.setGitGutter(!s.gitGutter)))
		}},
		{Label: "Restart language servers", Pick: func(sh *core.Shared) core.Action {
			return core.Seq(core.Pop(), s.restartLanguageServers(sh))
		}},
	}
}

// editorSaved is the editor pane's OnSaved hook (ctrl+s): the buffer stays exactly
// where it is, and gote catches up with where it now lives. A save-as points the same
// editor at a new path, which the open set is keyed by — so without the rekey the map
// would still answer to the old name and ctrl+x would close a doc that no longer
// exists. The reseed does the rest: Receive rebuilds the Docs list (a file saved
// somewhere new shows up in it) and the Open list (the row renames, and stays selected
// because currentPath moved with it). No SetChild — the pane's child never changed.
func (s *homeScreen) editorSaved(sh *core.Shared, path string) core.Action {
	c := Of(sh)
	if s.currentPath == "" {
		// A first save can happen before typing (and therefore before automatic
		// promotion). Retain it briefly so rekey can preserve the normal Open slot.
		c.trackUnsaved(s.currentID, s.currentName, s.editor)
	}
	c.RekeyDoc(s.currentID, path, s.editor)
	// Both paths, because a save is the only thing that can change a file's first line
	// under gote: the old name may have just lost a shebang, the new one may have gained
	// one, and a save-as onto an existing path inherits whatever was cached for it.
	forgetSniffedLanguage(s.currentPath)
	forgetSniffedLanguage(path)
	s.currentID, s.currentPath, s.currentName = path, path, docName(path)
	c.SetActive(path)
	if c.lsp != nil {
		c.lsp.DidSave(path)
		s.formatOnSave(sh)
	}
	// Re-read the baseline rather than keep the one in hand: a save-as makes this a
	// different file to git (very likely one HEAD has never seen), and even a plain save
	// may follow a commit that moved HEAD out from under the markers.
	s.gutter = gutter{}
	// A save-as can rename markdown out of markdown under an open preview.
	return core.Seq(core.Async(s.enforcePreview()), core.PropagateAll(ReseedMsg{}))
}

// editorExit is the editor pane's OnExit hook (ctrl+x — clean, saved, or discarded;
// every path closes the buffer): the doc leaves the open set and the pane swaps to
// the next open doc, or to a fresh scratch buffer when none remain. Focus returns to
// the docs list, unhiding the sidebar first when needed. This is the "done with this
// buffer" gesture, not an escape hatch — esc (editorRelease) and shift+tab both leave
// the pane at any time, and unhiding the sidebar is what makes ctrl+x meaningful with
// it hidden, where there is no other pane to move to. The reseed refreshes both lists:
// the close shows in Open, and a save-as'd file shows in Docs.
//
// Minimal mode makes ctrl+x quit outright — nano's exit, and the only one available:
// there is no list to go back to, and the router clamps pops so the root screen can
// never be popped off. The editor's own save prompt has already run by the time this
// is reached, so a dirty buffer still gets its (y)es/(n)o/(c)ancel.
func (s *homeScreen) editorExit(sh *core.Shared) core.Action {
	if s.minimal {
		return core.Async(tea.Quit)
	}
	c := Of(sh)
	cmd := s.showBuffer(c, c.CloseDoc(s.currentID))
	if !s.sidebar {
		s.setSidebar(true)
	}
	focus := s.modular.FocusSlot(0)
	s.refreshPreview()
	return core.Seq(core.Async(tea.Batch(cmd, focus)), core.PropagateAll(ReseedMsg{}))
}

// showBuffer points the editor pane at id — an already-open buffer, since the caller took
// it from the open set — or at a fresh scratch buffer when id is empty and no doc remains.
// Shared by ctrl+x and the docs list's delete: both take a document away from the pane
// and have to leave it showing something. enforcePreview runs after the swap, so the
// layout is rebuilt around the new buffer (openDoc's ordering); the returned cmd is the
// child's Init and has to reach bubbletea.
//
// Seeding is narrower than openDoc's: a doc this can be handed is already in the open
// set, and the "" case is a scratch buffer with no file. The one exception is a restored
// buffer that has never been switched to — in the open set, but its file still unread.
func (s *homeScreen) showBuffer(c *Ctx, id string) tea.Cmd {
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
	return tea.Batch(cmd, s.enforcePreview())
}

// editorRelease is the editor pane's OnRelease hook (esc): hand the keys back to the pane
// they came from without touching the buffer. The editor captures every printable key, so
// leaving it otherwise costs the shift+tab pane chord or ctrl+x — and ctrl+x CLOSES the
// doc, which is not what "let me go back to the list" should mean. Screen.Update claims esc
// in the other direction, so the pair is one toggle: esc leaves the editor for the outline,
// the dock or whichever pane last had the keys, and esc there comes straight back.
//
// The sidebar is unhidden for the same reason ctrl+x does it — with it hidden there is no
// other pane to hand focus to — but only when the remembered pane is not one of the panes
// still on screen. A dock or a preview column is somewhere to go, and opening the sidebar
// over it would be answering a key nobody pressed.
func (s *homeScreen) editorRelease(*core.Shared) core.Action {
	if s.panelSlot(s.lastPane) == noFocus && !s.sidebar {
		s.setSidebar(true)
	}
	return core.Async(s.modular.FocusSlot(s.releaseSlot()))
}

// releaseSlot is where esc hands the keys: back to the pane that last held them, or the
// layout's first slot when that pane is not in the current layout — the outline was closed,
// the dock was hidden, a vault swapped the sidebar out from under it. Slot 0 is the docs
// pane whenever the sidebar is up (buildModular emits the side column first), which is what
// esc always did and is still the right answer with nothing to remember.
func (s *homeScreen) releaseSlot() int {
	if slot := s.panelSlot(s.lastPane); slot != noFocus {
		return slot
	}
	return 0
}
