package app

import (
	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const (
	bottomDiagnostics = "diagnostics"
	bottomSearch      = "search"
	// Each TabBar cell spends one padding column on either side of its ASCII label.
	bottomTabsWidth = 2 + len("Diag") + 2 + len("Search")
)

// bottomDock is one ModularScreen panel with two retained children. Each child draws
// its ordinary full frame; the dock lays its compact selector over the bottom border,
// leaving the descriptive top title and the child's viewport geometry untouched.
type bottomDock struct {
	tabs          *components.TabBar
	diagnostics   *diagnosticsPanel
	search        *searchPanel
	active        string
	focused       bool
	width, height int
}

var _ components.Panel = (*bottomDock)(nil)
var _ components.Focusable = (*bottomDock)(nil)
var _ components.PanelUpdater = (*bottomDock)(nil)
var _ components.PanelHelper = (*bottomDock)(nil)

func newBottomDock(diagnostics *diagnosticsPanel, search *searchPanel) *bottomDock {
	p := &bottomDock{
		tabs: components.NewTabBar(), diagnostics: diagnostics, search: search,
		active: bottomDiagnostics,
	}
	p.tabs.SetItems([]components.TabItem{
		{ID: bottomDiagnostics, Label: "Diag"},
		{ID: bottomSearch, Label: "Search"},
	})
	p.tabs.SetActive(p.active)
	return p
}

func (p *bottomDock) activePanel() components.Panel {
	if p.active == bottomSearch {
		return p.search
	}
	return p.diagnostics
}

func (p *bottomDock) selectTab(id string) {
	if (id != bottomDiagnostics && id != bottomSearch) || id == p.active {
		return
	}
	if p.focused {
		p.activePanel().(components.Focusable).Blur()
	}
	p.active = id
	p.tabs.SetActive(id)
	if p.focused {
		p.activePanel().(components.Focusable).Focus()
	}
}

func (p *bottomDock) stepTab(delta int) {
	if delta < 0 {
		p.selectTab(bottomDiagnostics)
		return
	}
	p.selectTab(bottomSearch)
}

func (p *bottomDock) SetSize(width, height int) {
	p.width, p.height = width, height
	p.tabs.SetSize(min(max(width-2, 0), bottomTabsWidth), 1)
	p.diagnostics.SetSize(width, height)
	p.search.SetSize(width, height)
}

func (p *bottomDock) View(focused bool) string {
	background := p.activePanel().View(focused)
	row := lipgloss.Height(background) - 1
	if row < 0 || p.width < 2 {
		return background
	}
	return core.Composite(background, p.tabs.View(false), 1, row)
}

func (p *bottomDock) Focus() {
	p.focused = true
	p.activePanel().(components.Focusable).Focus()
}

func (p *bottomDock) Blur() {
	p.focused = false
	p.activePanel().(components.Focusable).Blur()
}

func (p *bottomDock) Focused() bool { return p.focused }

func (p *bottomDock) UpdatePanel(sh *core.Shared, msg tea.Msg) (core.Action, bool) {
	if !p.focused {
		return core.Action{}, false
	}
	if click, ok := msg.(tea.MouseClickMsg); ok && click.Y == p.height-1 {
		if click.Button == tea.MouseLeft && click.Mod == 0 {
			if id, handled := p.tabs.Click(click.X-1, 0); handled {
				p.selectTab(id)
			}
		}
		return core.Action{}, true
	}
	if km, ok := msg.(tea.KeyPressMsg); ok {
		switch {
		case km.String() == "left":
			p.stepTab(-1)
			return core.Action{}, true
		case km.String() == "right":
			p.stepTab(1)
			return core.Action{}, true
		}
	}
	return p.activePanel().(components.PanelUpdater).UpdatePanel(sh, msg)
}

func (p *bottomDock) PanelHelp() []key.Binding {
	if h, ok := p.activePanel().(components.PanelHelper); ok {
		return h.PanelHelp()
	}
	return nil
}
