package app

import (
	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

// sidebarWidth is the fixed cell width of the docs/open column; the editor flexes.
const sidebarWidth = 30

// The home screen's own keys. ctrl+b carries a modifier, so it passes the router's
// capture gate and toggles even while typing in the editor; "a" is intercepted only
// when nothing is capturing, so typed text and /-filters never lose the letter.
var (
	sidebarKey = key.NewBinding(key.WithKeys("ctrl+b"), key.WithHelp("ctrl+b", "sidebar"))
	actionsKey = key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "actions"))
)

// ReseedMsg is gote's "reload the doc list" broadcast: the Actions ▸ Refresh row and
// the mode toggle raise it; the home screen reseeds and rebuilds its lists on it.
type ReseedMsg struct{}

// homeScreen is gote's root screen: a ModularScreen with a hideable sidebar (the docs
// and open-docs lists) beside the editor pane. The wrapper owns the panels and swaps
// its internal ModularScreen on the sidebar toggle — the panels are shared across
// rebuilds, so list state and editor buffers survive, and the wrapper stays the same
// instance so the router never re-Inits it (a re-Init would re-run the editor's file
// load over a dirty buffer).
type homeScreen struct {
	modular     *components.ModularScreen
	docsPanel   *components.ListPanel
	openPanel   *components.ListPanel
	editorPanel *components.ScreenPanel
	sidebar     bool
	sh          *core.Shared // stashed by Init/SetSize for rebuilds and the crumb
	w, h        int
}

var _ core.Screen = (*homeScreen)(nil)
var _ core.Filterer = (*homeScreen)(nil)
var _ core.Receiver = (*homeScreen)(nil)
var _ core.Crumber = (*homeScreen)(nil)

// NewHomeScreen builds the root screen: the docs list seeded from the ctx, an empty
// open-docs list, and the editor pane starting on a scratch buffer.
func NewHomeScreen(sh *core.Shared) core.Screen {
	s := &homeScreen{sidebar: true}
	c := Of(sh)
	// Border on both sidebar lists: with three panes on screen the focused one has
	// to be visible, and the editor pane is framed automatically (ScreenPanel borders
	// a core.Borderer child).
	s.docsPanel = components.NewListPanel(docItems(c.Files), "Docs", components.ListPanelOpts{
		OnSelect: s.pickDoc,
		Border:   true,
	})
	s.openPanel = components.NewListPanel(nil, "Open", components.ListPanelOpts{
		OnSelect: s.pickDoc,
		Border:   true,
	})
	s.editorPanel = components.NewScreenPanel(components.NewEditorScreen(components.EditorOpts{
		OnExit: s.editorExit,
	}))
	s.modular = s.buildModular()
	return s
}

func (s *homeScreen) Init(sh *core.Shared) tea.Cmd {
	s.sh = sh
	return s.modular.Init(sh)
}

// Update intercepts the wrapper's own keys, then delegates to the current modular
// screen. The returned screen is always the wrapper — the modular swap happens in
// place, never as a screen replacement.
func (s *homeScreen) Update(sh *core.Shared, msg tea.Msg) (core.Screen, core.Action) {
	if km, ok := msg.(tea.KeyMsg); ok {
		k := km.String()
		if core.MatchKey(k, sidebarKey) {
			s.setSidebar(!s.sidebar)
			return s, core.Action{}
		}
		if core.MatchKey(k, actionsKey) && !s.modular.Filtering() {
			return s, core.Push(actionsMenu(sh))
		}
	}
	_, act := s.modular.Update(sh, msg)
	return s, act
}

func (s *homeScreen) View(sh *core.Shared) string     { return s.modular.View(sh) }
func (s *homeScreen) HelpView(sh *core.Shared) string { return s.modular.HelpView(sh) }

func (s *homeScreen) SetSize(sh *core.Shared, width, bodyHeight int) {
	s.sh = sh
	s.w, s.h = width, bodyHeight
	s.modular.SetSize(sh, width, bodyHeight)
}

// Filtering proxies the modular screen's capture state: the router must leave global
// single-key shortcuts alone while the editor types or a list filters.
func (s *homeScreen) Filtering() bool { return s.modular.Filtering() }

// CrumbLabel contributes the mode segment: the store, or the scanned directory.
func (s *homeScreen) CrumbLabel(short bool) string {
	if s.sh != nil && Of(s.sh).Mode == ModeScan {
		return "scan: " + Of(s.sh).ScanDir
	}
	return "docs"
}

// Receive handles app-level broadcasts: a ReseedMsg reloads both lists from a fresh
// seed; anything else falls through to the theme-change handler.
func (s *homeScreen) Receive(sh *core.Shared, payload any) core.Action {
	if _, ok := payload.(ReseedMsg); ok {
		c := Of(sh)
		c.Seed()
		s.docsPanel.SetItems(docItems(c.Files))
		s.openPanel.SetItems(docItems(c.OpenDocs()))
		return core.Action{}
	}
	return core.OnThemeChange(payload)
}

// pickDoc opens (or switches to) the selected doc in the editor pane and moves focus
// to it. The doc's editor is reused across picks, so its unsaved buffer survives.
func (s *homeScreen) pickDoc(sh *core.Shared, it list.Item) core.Action {
	di, ok := it.(docItem)
	if !ok {
		return core.Action{}
	}
	c := Of(sh)
	ed := c.OpenDoc(di.doc.Path, s.editorExit)
	s.openPanel.SetItems(docItems(c.OpenDocs()))
	cmd := s.editorPanel.SetChild(ed)
	s.modular.FocusSlot(s.editorSlot())
	return core.Async(cmd)
}

// editorExit is the editor pane's OnExit hook (ctrl+x, after the save prompt): focus
// returns to the docs list, unhiding the sidebar first when needed — the editor's
// capture swallows tab, so this hook is the keyboard's way out of the pane.
func (s *homeScreen) editorExit(sh *core.Shared) core.Action {
	if !s.sidebar {
		s.setSidebar(true)
	}
	s.modular.FocusSlot(0)
	return core.Action{}
}

// editorSlot is the editor pane's flat slot index in the current layout.
func (s *homeScreen) editorSlot() int {
	if s.sidebar {
		return 2
	}
	return 0
}

// setSidebar rebuilds the internal modular screen with or without the sidebar
// column. The old screen's focused panel is blurred before it is discarded, and the
// new one (which auto-focuses its first focusable slot — the docs list, or the lone
// editor) gets the current size.
func (s *homeScreen) setSidebar(visible bool) {
	s.modular.SetFocused(false)
	s.sidebar = visible
	s.modular = s.buildModular()
	if s.w > 0 {
		s.modular.SetSize(s.sh, s.w, s.h)
	}
}

func (s *homeScreen) buildModular() *components.ModularScreen {
	opts := components.ModularOpts{
		Help: []key.Binding{sidebarKey, actionsKey},
	}
	editor := components.Slot{Panel: s.editorPanel, Weight: 1}
	if !s.sidebar {
		return components.NewModularScreen([][]components.Slot{{editor}}, opts)
	}
	opts.ColWidths = []int{sidebarWidth, 0}
	return components.NewModularScreen([][]components.Slot{
		{
			{Panel: s.docsPanel, Weight: 1},
			{Panel: s.openPanel, Weight: 1},
		},
		{editor},
	}, opts)
}
