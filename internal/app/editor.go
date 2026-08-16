package app

import (
	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"

	tea "github.com/charmbracelet/bubbletea"
)

// The editor pane's lifecycle: the options homeScreen instances it with, its right-click
// menu, and the three ways a buffer leaves — saved under a new path, closed with ctrl+x,
// or released back to the pane layout.

// editorOpts is the hook set every editor in this screen is built with — the pane's
// three ways out and back: ctrl+x closes the buffer, esc hands the keys back, ctrl+s
// writes it and stays. Path is filled in by whoever constructs the editor (Ctx.OpenDoc
// for a doc, left empty for the scratch buffer).
func (s *homeScreen) editorOpts() components.EditorOpts {
	return components.EditorOpts{
		OnExit:       s.editorExit,
		OnRelease:    s.editorRelease,
		OnSaved:      s.editorSaved,
		Search:       true,
		ContextMenu:  true,
		ContextItems: s.editorContextItems,
	}
}

// editorContextItems are gote's rows on the editor's right-click menu, below the
// clipboard verbs: the view toggles that otherwise only exist as chords. They are built
// fresh on every press, which is what lets Disabled track the current document — the
// preview is a markdown reader and refuses everything else, the same gate ctrl+p uses.
// Each Pick pops the menu itself (the component's convention). No Hints: the menu
// dispatches no accelerators, so a key-shaped hint would be a promise it doesn't keep.
func (s *homeScreen) editorContextItems(*core.Shared) []components.MenuItem {
	return []components.MenuItem{
		{Label: "Toggle preview", Disabled: !s.previewable(), Pick: func(*core.Shared) core.Action {
			return core.Seq(core.Pop(), s.cyclePreview())
		}},
		{Label: "Full preview", Disabled: !s.previewable(), Pick: func(*core.Shared) core.Action {
			return core.Seq(core.Pop(), core.Push(s.previewScreen(s.editor.Text)))
		}},
		{Label: "Toggle wrap", Pick: func(*core.Shared) core.Action {
			s.editor.ToggleWrap()
			return core.Pop()
		}},
		{Label: "Toggle line numbers", Pick: func(*core.Shared) core.Action {
			s.editor.ToggleLineNums()
			return core.Pop()
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
	Of(sh).RekeyDoc(s.currentPath, path, s.editor)
	s.currentPath = path
	s.enforcePreview() // a save-as can rename markdown out of markdown under an open pane
	return core.PropagateAll(ReseedMsg{})
}

// editorExit is the editor pane's OnExit hook (ctrl+x — clean, saved, or discarded;
// every path closes the buffer): the doc leaves the open set and the pane swaps to
// the next open doc, or to a fresh scratch buffer when none remain. Focus returns to
// the docs list, unhiding the sidebar first when needed. This is the "done with this
// buffer" gesture, not an escape hatch — esc (editorRelease) and shift+← both leave
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
	next := c.CloseDoc(s.currentPath)
	var cmd tea.Cmd
	if next != "" {
		s.currentPath = next
		s.editor = c.OpenDoc(next, s.editorOpts())
	} else {
		s.currentPath = ""
		s.editor = components.NewEditorScreen(s.editorOpts())
	}
	cmd = s.editorPanel.SetChild(s.editor)
	if !s.sidebar {
		s.setSidebar(true)
	}
	s.enforcePreview()
	focus := s.modular.FocusSlot(0)
	s.refreshPreview()
	return core.Seq(core.Async(tea.Batch(cmd, focus)), core.PropagateAll(ReseedMsg{}))
}

// editorRelease is the editor pane's OnRelease hook (esc): hand the keys back to the
// docs list without touching the buffer. The editor captures every printable key, so
// leaving it otherwise costs the shift+← pane chord or ctrl+x — and ctrl+x CLOSES the
// doc, which is not what "let me go back to the list" should mean. The sidebar is
// unhidden first for the same reason ctrl+x does it: with it hidden there is no other
// pane to hand focus to.
func (s *homeScreen) editorRelease(*core.Shared) core.Action {
	if !s.sidebar {
		s.setSidebar(true)
	}
	return core.Async(s.modular.FocusSlot(0))
}
