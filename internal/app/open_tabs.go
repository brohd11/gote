package app

import (
	"path/filepath"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
)

var (
	previousDocumentKey = key.NewBinding(key.WithKeys("alt+9"), key.WithHelp("alt+9", "previous document"))
	nextDocumentKey     = key.NewBinding(key.WithKeys("alt+0"), key.WithHelp("alt+0", "next document"))
)

// documentTabBar records its rendered bounds so the host can route pointer
// input without introducing a keyboard-focus stop for the bar.
type documentTabBar struct {
	*components.TabBar
	group      *editorGroup
	x, y, w, h int
	mouseDown  bool
}

func (p *documentTabBar) SetPaneOrigin(x, y int) { p.x, p.y = x, y }
func (p *documentTabBar) SetSize(w, h int) {
	p.w, p.h = w, h
	p.TabBar.SetSize(w, h)
}

func (p *documentTabBar) View(bool) string {
	return p.TabBar.View(p.group != nil && p.group.editorPanel != nil && p.group.editorPanel.Focused())
}

func (s *homeScreen) tabsVisible() bool { return !s.minimal }

func (s *homeScreen) readerTitle() string {
	if s.tabsVisible() {
		return ""
	}
	return s.previewName() + " · preview"
}

func (s *homeScreen) panelSlot(panel components.Panel) int {
	if slot, ok := s.panelSlots[panel]; ok {
		return slot
	}
	return noFocus
}

func (s *homeScreen) focusedPane() components.Panel {
	for panel := range s.panelSlots {
		if f, ok := panel.(components.Focusable); ok && f.Focused() {
			return panel
		}
	}
	return s.editorPanel
}

// togglePanes closes the floating popups, applies change, rebuilds the layout and returns
// focus to the pane that had it (the editor if that pane is gone).
func (s *homeScreen) togglePanes(sh *core.Shared, change func()) tea.Cmd {
	focus := s.focusedPane()
	s.saveResize(s.modular.ResizeState())
	s.closeCompletion()
	s.closeHover()
	s.closeSignature()
	s.closeGitDiff()
	change()
	s.rebuildModular(sh, noFocus)
	if s.panelSlot(focus) == noFocus {
		focus = s.editorPanel
	}
	return s.modular.FocusSlot(s.panelSlot(focus))
}

func (s *homeScreen) refreshOpenTabs(sh *core.Shared) {
	for _, g := range s.groups() {
		s.withGroup(g, func() tea.Cmd { s.refreshGroupTabs(sh); return nil })
	}
}

func (s *homeScreen) groupDocs(c *Ctx) []DocFile {
	docs := make([]DocFile, 0, len(s.tabs))
	for _, id := range s.tabs {
		if doc, ok := c.bufferInfo(id); ok {
			docs = append(docs, doc)
		}
	}
	return docs
}

func (s *homeScreen) refreshGroupTabs(sh *core.Shared) {
	if !s.tabsVisible() {
		return
	}
	c := Of(sh)
	docs := s.groupDocs(c)
	names := make(map[string]int)
	for _, doc := range docs {
		names[doc.Name]++
	}
	items := make([]components.TabItem, 0, len(docs))
	for _, doc := range docs {
		label := doc.Name
		if names[doc.Name] > 1 && doc.Path != "" {
			label += " · " + filepath.Dir(doc.Path)
		}
		var marks []string
		if ed, ok := c.buffer(doc.ID); ok && ed != nil && ed.ChangeMark() != "" {
			marks = append(marks, strings.TrimSpace(ed.ChangeMark()))
		}
		if doc.ID == s.currentID && s.fullPreview != nil {
			marks = append(marks, "[P]")
		}
		marker := ""
		if len(marks) > 0 {
			marker = " " + strings.Join(marks, " ")
		}
		items = append(items, components.TabItem{ID: doc.ID, Label: label, Marker: marker})
	}
	s.openTabs.SetItems(items)
	s.openTabs.SetActive(s.currentID)
}

func (s *homeScreen) stepDocument(sh *core.Shared, delta int) core.Action {
	docs := s.groupDocs(Of(sh))
	if len(docs) == 0 {
		return core.Action{}
	}
	index := slices.IndexFunc(docs, func(doc DocFile) bool { return doc.ID == s.currentID })
	if index < 0 {
		index = 0
		if delta < 0 {
			index = len(docs) - 1
		}
	} else {
		index = (index + delta + len(docs)) % len(docs)
	}
	if docs[index].ID == s.currentID {
		return core.Action{}
	}
	return s.activateTab(sh, docs[index].ID)
}

func (s *homeScreen) activateTab(sh *core.Shared, id string) core.Action {
	s.closeCompletion()
	s.closeHover()
	s.closeSignature()
	return s.switchBuffer(sh, id)
}

func (s *homeScreen) documentTabInput(sh *core.Shared, msg tea.Msg) (core.Action, bool) {
	if s.minimal {
		return core.Action{}, false
	}
	if k, ok := msg.(tea.KeyPressMsg); ok {
		for _, g := range s.groups() {
			g.openTabs.mouseDown = false
		}
		if s.modular.Resizing() {
			return core.Action{}, false
		}
		bare := !s.modular.Filtering()
		switch {
		case core.MatchKey(k.String(), previousDocumentKey) || (bare && k.String() == "["):
			return s.stepDocument(sh, -1), true
		case core.MatchKey(k.String(), nextDocumentKey) || (bare && k.String() == "]"):
			return s.stepDocument(sh, 1), true
		}
	}
	mm, ok := msg.(tea.MouseMsg)
	if !ok || !s.tabsVisible() {
		return core.Action{}, false
	}
	for _, g := range s.groups() {
		if act, handled := s.groupTabInput(sh, msg, mm, g); handled {
			return act, true
		}
	}
	return core.Action{}, false
}

func (s *homeScreen) groupTabInput(sh *core.Shared, msg tea.Msg, mm tea.MouseMsg, g *editorGroup) (core.Action, bool) {
	p := g.openTabs
	m := mm.Mouse()
	if _, press := msg.(tea.MouseClickMsg); press {
		p.mouseDown = false
	} else if p.mouseDown {
		if _, release := msg.(tea.MouseReleaseMsg); release {
			p.mouseDown = false
		}
		return core.Action{}, true
	}
	// A drag started in the editor or on a divider retains its owner when it
	// crosses the bar. In particular its release must reach ModularScreen.
	switch msg.(type) {
	case tea.MouseMotionMsg, tea.MouseReleaseMsg:
		return core.Action{}, false
	}
	if m.X < p.x || m.X >= p.x+p.w || m.Y != p.y || p.h == 0 {
		return core.Action{}, false
	}
	if click, ok := msg.(tea.MouseClickMsg); ok {
		p.mouseDown = true
		if click.Button == tea.MouseLeft && click.Mod == 0 {
			s.refreshOpenTabs(sh)
			if id, _ := p.Click(m.X-p.x, m.Y-p.y); id != "" {
				s.activateGroup(g)
				return s.activateTab(sh, id), true
			}
		}
	}
	return core.Action{}, true
}
