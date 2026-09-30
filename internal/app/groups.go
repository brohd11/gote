package app

import (
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/components/editor"
	"github.com/brohd11/bubblestack/core"
)

const maxEditorGroups = 4

var (
	moveTabLeftKey  = key.NewBinding(key.WithKeys("ctrl+t"), key.WithHelp("ctrl+t", "move tab left"))
	moveTabRightKey = key.NewBinding(key.WithKeys("alt+t"), key.WithHelp("alt+t", "move tab right / split"))
)

// editorGroup owns presentation and membership, never a second copy of a buffer.
// Ctx retains these objects so the session can be saved after the router exits.
// homeScreen embeds ONLY the active group; shared tools read through that pointer.
type editorGroup struct {
	readerEditor                        *editor.Screen
	tabs                                []string
	weight                              float64
	currentID, currentPath, currentName string
	editor                              *editor.Screen
	editorPanel                         *groupPanel
	openTabs                            *documentTabBar
	fullPreview                         *components.DocScreen // the current doc's reader, while it is in Reader
	gitGutter, diagnosticsGutter        bool
	gutter                              gutter
	needsRefresh                        bool
	nextID                              string
}

// Focus runs synchronously, before ModularScreen delivers the click to its child.
// An async focus notification would be too late for context-menu callbacks.
type groupPanel struct {
	*components.ScreenPanel
	host       *homeScreen
	group      *editorGroup
	x, y, w, h int // the editor's bounds, right of any separator
	// separator draws a │ down the first column, dividing this group from the one on its
	// left. The tab bar above draws the same column.
	separator bool
}

func (p *groupPanel) Focus() {
	if !p.host.buildingGroups {
		p.host.activateGroup(p.group)
	}
	p.ScreenPanel.Focus()
}

func (p *groupPanel) sepW() int {
	if p.separator {
		return 1
	}
	return 0
}

func (p *groupPanel) SetPaneOrigin(x, y int) {
	p.x, p.y = x+p.sepW(), y
	p.ScreenPanel.SetPaneOrigin(p.x, y)
}

func (p *groupPanel) SetSize(w, h int) {
	p.w, p.h = max(0, w-p.sepW()), h
	p.ScreenPanel.SetSize(p.w, h)
}

func (p *groupPanel) View(focused bool) string {
	body := p.ScreenPanel.View(focused)
	if p.sepW() == 0 {
		return body
	}
	sep := groupSeparator()
	lines := strings.Split(body, "\n")
	var b strings.Builder
	for i := 0; i < p.h; i++ {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(sep)
		if i < len(lines) {
			b.WriteString(lines[i])
		}
	}
	return b.String()
}

// groupSeparator is the divider cell between editor groups: chrome, so it keeps the
// border color whichever group has focus.
func groupSeparator() string {
	return lipgloss.NewStyle().Foreground(core.BorderColor).Render("│")
}

func (p *groupPanel) UpdatePanel(sh *core.Shared, msg tea.Msg) (core.Action, bool) {
	// ModularScreen broadcasts non-key messages. Bracketed paste is user input,
	// whereas addressed load/save/highlight results must still reach every editor.
	if _, paste := msg.(tea.PasteMsg); paste && !p.Focused() {
		return core.Action{}, true
	}
	// Pane-local coordinates count the separator; the editor's start after it.
	if mm, ok := msg.(tea.MouseMsg); ok && p.sepW() > 0 {
		msg = shiftMouseX(mm, -p.sepW())
	}
	return p.ScreenPanel.UpdatePanel(sh, msg)
}

// shiftMouseX moves a mouse message dx columns, keeping its concrete type.
func shiftMouseX(mm tea.MouseMsg, dx int) tea.Msg {
	m := mm.Mouse()
	m.X += dx
	switch mm.(type) {
	case tea.MouseClickMsg:
		return tea.MouseClickMsg(m)
	case tea.MouseReleaseMsg:
		return tea.MouseReleaseMsg(m)
	case tea.MouseWheelMsg:
		return tea.MouseWheelMsg(m)
	case tea.MouseMotionMsg:
		return tea.MouseMotionMsg(m)
	}
	return mm
}

func (s *homeScreen) groups() []*editorGroup {
	if s.sh != nil && len(Of(s.sh).groups) > 0 {
		return Of(s.sh).groups
	}
	return []*editorGroup{s.editorGroup}
}

func (c *Ctx) groupFor(id string) *editorGroup {
	for _, g := range c.groups {
		if slices.Contains(g.tabs, id) {
			return g
		}
	}
	return nil
}

func (c *Ctx) assignGroup(id string) {
	if c.activeGroup != nil && c.groupFor(id) == nil {
		c.activeGroup.tabs = append(c.activeGroup.tabs, id)
	}
}

func (g *editorGroup) remove(id string) string {
	i := slices.Index(g.tabs, id)
	if i < 0 {
		return ""
	}
	g.tabs = slices.Delete(g.tabs, i, i+1)
	if len(g.tabs) == 0 {
		return ""
	}
	next := g.tabs[min(i, len(g.tabs)-1)]
	if g.currentID == id {
		g.nextID = next
	}
	return next
}

func (c *Ctx) rekeyGroups(oldID, newID string, ed *editor.Screen) {
	owner := c.groupFor(oldID)
	if owner == nil {
		owner = c.activeGroup
	}
	for _, g := range c.groups {
		// Preserve the source slot; a save-as may displace a buffer in another group.
		out := make([]string, 0, len(g.tabs))
		for _, id := range g.tabs {
			if id == oldID && g == owner {
				out = append(out, newID)
			} else if id != newID {
				out = append(out, id)
			}
		}
		g.tabs = out
		if g.currentID == oldID && g == owner {
			g.currentID, g.currentPath, g.currentName, g.editor = newID, newID, docName(newID), ed
			g.needsRefresh = true
			g.readerEditor = nil
		}
	}
}

func (s *homeScreen) initGroupPanels(c *Ctx) {
	for _, g := range c.groups {
		if g.openTabs == nil {
			g.openTabs = &documentTabBar{TabBar: components.NewTabBar()}
		}
		g.openTabs.group = g
		if g.editorPanel == nil {
			g.editorPanel = &groupPanel{ScreenPanel: components.NewScreenPanel(g.editor), host: s, group: g}
		}
	}
}

func (s *homeScreen) isEditorPanel(p components.Panel) bool {
	_, ok := p.(*groupPanel)
	return ok
}

func (s *homeScreen) invalidateDocumentTools() {
	s.closeCompletion()
	s.closeHover()
	s.closeSignature()
	s.closeGitDiff()
	s.lspRequestID = 0
	s.outlineRequestID = 0
	s.outlineGeneration++
	s.outlineScheduledPath = ""
	s.outlineRequestedPath = ""
	s.semanticGen++
	s.semanticPath = ""
	s.pendingJump, s.pendingRange = nil, nil
}

func (s *homeScreen) activateGroup(g *editorGroup) {
	if g == nil || g == s.editorGroup {
		return
	}
	s.invalidateDocumentTools()
	s.editorGroup = g
	if s.sh != nil {
		c := Of(s.sh)
		c.activeGroup = g
		c.SetActive(g.currentID)
	}
	s.resetPreviewCache()
}

// withGroup is only for background presentation maintenance. It does not activate a
// document, change focus, or issue interactive language requests.
func (s *homeScreen) withGroup(g *editorGroup, fn func() tea.Cmd) tea.Cmd {
	active := s.editorGroup
	s.editorGroup = g
	defer func() { s.editorGroup = active }()
	return fn()
}

func (s *homeScreen) refreshVisibleGroups(sh *core.Shared) tea.Cmd {
	var cmds []tea.Cmd
	for _, g := range Of(sh).groups {
		cmds = append(cmds, s.withGroup(g, func() tea.Cmd {
			s.applyRestore(Of(sh))
			s.refreshDiagnosticSigns()
			return s.refreshGutter()
		}))
	}
	return tea.Batch(cmds...)
}

// tabMoveUnavailable is shared by the shortcuts and the group submenu, so their
// boundary and split-creation rules cannot drift.
func (s *homeScreen) tabMoveUnavailable(delta int) string {
	if s.minimal {
		return "editor groups are unavailable in single-file mode"
	}
	if s.modular.Resizing() {
		return "finish resizing first"
	}
	if !slices.Contains(s.tabs, s.currentID) {
		return "no tab to move"
	}
	groups := s.groups()
	j := slices.Index(groups, s.editorGroup) + delta
	if j < 0 {
		return "already in the leftmost editor group"
	}
	if j == len(groups) {
		if len(groups) == maxEditorGroups {
			return "at most four editor groups"
		}
		if len(s.tabs) < 2 {
			return "open a second tab before splitting"
		}
	}
	return ""
}

func (s *homeScreen) moveTab(sh *core.Shared, delta int) core.Action {
	if s.minimal || s.modular.Resizing() {
		return core.Action{}
	}
	if reason := s.tabMoveUnavailable(delta); reason != "" {
		return core.SetStatus(reason)
	}
	c := Of(sh)
	src := s.editorGroup
	j := slices.Index(c.groups, src) + delta
	creating := j == len(c.groups)
	s.saveResize(s.modular.ResizeState())
	s.invalidateDocumentTools()
	id := src.currentID
	var cmds []tea.Cmd
	if creating {
		src.weight /= 2
		g := &editorGroup{weight: src.weight, gitGutter: src.gitGutter, diagnosticsGutter: src.diagnosticsGutter,
			editor: src.editor, currentID: id, currentPath: src.currentPath, currentName: src.currentName}
		c.groups = append(c.groups, g)
		s.initGroupPanels(c)
	}
	dst := c.groups[j]
	next := src.remove(id)
	dst.tabs = append(dst.tabs, id)
	// Detach the moved editor before initializing its destination panel.
	cmds = append(cmds, s.showBuffer(c, next))
	s.activateGroup(dst)
	cmds = append(cmds, s.showBuffer(c, id))
	if creating {
		cmds = append(cmds, dst.editorPanel.Init(sh))
	}
	cmds = append(cmds, s.reconcileGroups(sh))
	cmds = append(cmds, s.rebuildGroups(sh))
	return s.finishHomeUpdate(sh, core.Async(tea.Batch(cmds...)))
}

func (s *homeScreen) rebuildGroups(sh *core.Shared) tea.Cmd {
	target := s.editorGroup
	s.rebuildLayout(sh)
	s.groupLayoutDirty = false
	return s.modular.FocusSlot(s.panelSlot(target.editorPanel))
}

func (s *homeScreen) closeEditorGroup(sh *core.Shared) core.Action {
	c := Of(sh)
	if s.minimal || len(c.groups) < 2 {
		return core.SetStatus("only one editor group")
	}
	s.saveResize(s.modular.ResizeState())
	src := s.editorGroup
	i := slices.Index(c.groups, src)
	j := i - 1
	if j < 0 {
		j = 1
	}
	dst := c.groups[j]
	id := src.currentID
	dst.tabs = append(dst.tabs, src.tabs...)
	src.tabs = nil
	s.activateGroup(dst)
	cmd := s.showBuffer(c, id)
	return s.finishHomeUpdate(sh, core.Async(tea.Batch(cmd, s.reconcileGroups(sh), s.rebuildGroups(sh))))
}

// reconcileGroups repairs presentation after registry changes, including asynchronous
// saves, save-as collisions, and deleting a document in an inactive group.
func (s *homeScreen) reconcileGroups(sh *core.Shared) tea.Cmd {
	c := Of(sh)
	if len(c.groups) == 0 {
		return nil
	}
	var cmds []tea.Cmd
	changed := false
	for _, g := range slices.Clone(c.groups) {
		g.tabs = slices.DeleteFunc(g.tabs, func(id string) bool { _, ok := c.buffer(id); return !ok })
		if len(g.tabs) == 0 && len(c.groups) > 1 {
			i := slices.Index(c.groups, g)
			g.editorPanel.Blur()
			c.groups = slices.Delete(c.groups, i, i+1)
			if s.editorGroup == g {
				s.activateGroup(c.groups[max(0, i-1)])
			}
			changed = true
			continue
		}
		if !slices.Contains(g.tabs, g.currentID) && len(g.tabs) > 0 {
			id := g.tabs[0]
			if slices.Contains(g.tabs, g.nextID) {
				id = g.nextID
			}
			cmds = append(cmds, s.withGroup(g, func() tea.Cmd { return s.showBuffer(c, id) }))
		} else if g.needsRefresh {
			cmds = append(cmds, s.withGroup(g, func() tea.Cmd {
				g.needsRefresh = false
				g.gutter = gutter{}
				return s.paneChild()
			}))
		} else if g.currentPath != "" {
			if _, ok := c.buffer(g.currentID); !ok {
				cmds = append(cmds, s.withGroup(g, func() tea.Cmd { return s.showBuffer(c, "") }))
			}
		}
	}
	c.activeGroup = s.editorGroup
	c.SetActive(s.currentID)
	if changed {
		sum := 0.0
		for _, g := range c.groups {
			sum += g.weight
		}
		if sum > 0 {
			for _, g := range c.groups {
				g.weight /= sum
			}
		}
		cmds = append(cmds, s.rebuildGroups(sh))
	}
	return tea.Batch(cmds...)
}
