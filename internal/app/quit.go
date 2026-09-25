package app

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
)

// QuitGate implements core.QuitGater: quit immediately when everything is saved, otherwise
// confirm with a list of dirty docs (y discards, esc/n cancels). It answers even from under
// a pushed modal.
func (s *homeScreen) QuitGate(sh *core.Shared) (core.Action, bool) {
	s.closeCompletion()
	dirty := s.dirtyDocs(sh)
	if len(dirty) == 0 {
		return core.Action{}, false
	}
	return core.Push(quitPopup(dirty)), true
}

// quitPopup builds the dirty-quit confirm; OnQuit keeps q/ctrl+c as a force-quit while it
// is up (rather than stacking another popup).
func quitPopup(dirty []string) *components.DialogScreen {
	return dirtyPopup(dirty, "quitting", func(*core.Shared) core.Action { return core.Async(tea.Quit) })
}

// dirtyPopup is the discard confirm shared by quitting and vault switches.
func dirtyPopup(dirty []string, consequence string, onYes func(*core.Shared) core.Action) *components.DialogScreen {
	body := "unsaved changes in:\n\n  " + strings.Join(dirty, "\n  ") +
		"\n\n" + consequence + " discards them.\n(q/ctrl+c force-quits)"
	popup := &components.DialogScreen{
		Title:   "unsaved changes",
		Render:  func(*core.Shared) string { return body },
		OnYes:   onYes,
		Help:    components.DefaultHelpKeys,
		Overlay: true,
	}
	popup.OnQuit = func(*core.Shared) (core.Action, bool) { return core.Async(tea.Quit), true }
	return popup
}

// dirtyDocs lists the buffers with unsaved changes, the live editor first. The fallback
// covers a pathless editor dirtied before promotion.
func (s *homeScreen) dirtyDocs(sh *core.Shared) []string {
	var names []string
	if s.editor != nil && s.editor.Dirty() {
		names = append(names, s.previewName())
	}
	c := Of(sh)
	for _, doc := range c.OpenDocs() {
		ed, ok := c.buffer(doc.ID)
		if ok && ed != nil && ed != s.editor && ed.Dirty() {
			names = append(names, doc.Name)
		}
	}
	return names
}
