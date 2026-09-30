package app

import (
	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

const (
	bottomDiagnostics = "diagnostics"
	bottomSearch      = "search"
)

// bottomDock is one panel holding two children, each drawing its own frame. Its tabs are
// in the status bar's control spot (statusbar.go); left/right switch them from here.
type bottomDock struct {
	diagnostics   *diagnosticsPanel
	search        *searchPanel
	active        string
	focused       bool
	width, height int
	x, y          int // absolute origin, from ModularScreen (PaneOriginer)
}

var _ components.Panel = (*bottomDock)(nil)
var _ components.Focusable = (*bottomDock)(nil)
var _ components.PanelUpdater = (*bottomDock)(nil)
var _ components.PanelHelper = (*bottomDock)(nil)

func newBottomDock(diagnostics *diagnosticsPanel, search *searchPanel) *bottomDock {
	return &bottomDock{diagnostics: diagnostics, search: search, active: bottomDiagnostics}
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
	p.diagnostics.SetSize(width, height)
	p.search.SetSize(width, height)
}

func (p *bottomDock) SetPaneOrigin(x, y int) { p.x, p.y = x, y }

func (p *bottomDock) View(focused bool) string { return p.activePanel().View(focused) }

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
