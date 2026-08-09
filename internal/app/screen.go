package app

import (
	"strings"

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
	currentPath string // the doc the editor pane is showing; "" = the scratch buffer
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
	s.docsPanel = components.NewListPanel(docRows(c), "Docs", components.ListPanelOpts{
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
		s.docsPanel.SetItems(docRows(c))
		s.openPanel.SetItems(docItems(c.OpenDocs(), s.currentPath))
		return core.Action{}
	}
	return core.OnThemeChange(payload)
}

// pickDoc routes the docs list's rows: the action row opens the new-file line
// edit; a doc row opens (or switches to) that doc in the editor pane.
func (s *homeScreen) pickDoc(sh *core.Shared, it list.Item) core.Action {
	if _, ok := it.(newFileItem); ok {
		return s.newFile(sh)
	}
	di, ok := it.(docItem)
	if !ok {
		return core.Action{}
	}
	return s.openDoc(sh, di.doc.Path)
}

// openDoc switches the editor pane to path (creating or reusing its buffer, so
// unsaved edits survive) and moves focus to it.
func (s *homeScreen) openDoc(sh *core.Shared, path string) core.Action {
	c := Of(sh)
	ed := c.OpenDoc(path, s.editorExit)
	s.currentPath = path
	s.openPanel.SetItems(docItems(c.OpenDocs(), s.currentPath))
	cmd := s.editorPanel.SetChild(ed)
	s.modular.FocusSlot(s.editorSlot())
	return core.Async(cmd)
}

// newFile pushes the floating line edit over the selected docs row. Anchor math:
// the docs panel is column 0 row 0 of the layout, so its outer top-left is
// (0, BodyY); its border takes one row, and the LineEdit anchor sits one row
// above the covered row (its own top border) — the two cancel, so the anchor is
// BodyY + the list-relative row. x=0 and width=sidebarWidth land the box's
// borders exactly on the panel's own.
func (s *homeScreen) newFile(sh *core.Shared) core.Action {
	l := s.docsPanel.List()
	row, ok := components.ListItemRow(l, l.Index())
	if !ok {
		row = 0 // the selected row is on-page by construction; never die on it
	}
	edit := components.NewLineEdit("name (a/b.md nests dirs)", 0, sh.BodyY()+row, sidebarWidth,
		s.createFile, nil)
	edit.Crumb = "new file"
	edit.Help = []key.Binding{} // the hint row wraps at sidebar width; keep the box slim
	return core.Push(edit)
}

// createFile is the line edit's OnDone: resolve the typed name against the doc
// store (the scan root in scan mode), write the file (making parent dirs for
// names containing "/"), then reseed the list and open the file in the editor.
// Blank input cancels quietly. Errors surface as a popup — gote has no status
// pane — swapped in over the line edit so the overlay's stack depth holds.
func (s *homeScreen) createFile(sh *core.Shared, name string) core.Action {
	if strings.TrimSpace(name) == "" {
		return core.Pop()
	}
	c := Of(sh)
	base := c.ScanDir
	if c.Mode == ModeHome {
		dir, err := Dir()
		if err != nil {
			return core.Replace(errPopup("new file", err))
		}
		base = dir
	}
	path, err := newDocPath(base, name, c.Ext)
	if err != nil {
		return core.Replace(errPopup("new file", err))
	}
	if err := createDoc(path); err != nil {
		return core.Replace(errPopup("new file", err))
	}
	return core.Seq(core.Pop(), core.PropagateAll(ReseedMsg{}), s.openDoc(sh, path))
}

// errPopup builds the error dialog a failed new-file submit is replaced with.
func errPopup(title string, err error) *components.DialogScreen {
	return components.CreatePopup(title, err.Error(), core.Pop())
}

// editorExit is the editor pane's OnExit hook (ctrl+x — clean, saved, or discarded;
// every path closes the buffer): the doc leaves the open set and the pane swaps to
// the next open doc, or to a fresh scratch buffer when none remain. Focus returns to
// the docs list, unhiding the sidebar first when needed. This is the "done with this
// buffer" gesture, not an escape hatch — shift+← leaves the pane at any time, and
// unhiding the sidebar is what makes ctrl+x meaningful with it hidden, where there is
// no other pane to move to. The reseed refreshes both lists: the close shows in Open,
// and a save-as'd file shows in Docs.
func (s *homeScreen) editorExit(sh *core.Shared) core.Action {
	c := Of(sh)
	next := c.CloseDoc(s.currentPath)
	var cmd tea.Cmd
	if next != "" {
		s.currentPath = next
		cmd = s.editorPanel.SetChild(c.OpenDoc(next, s.editorExit))
	} else {
		s.currentPath = ""
		cmd = s.editorPanel.SetChild(components.NewEditorScreen(components.EditorOpts{
			OnExit: s.editorExit,
		}))
	}
	if !s.sidebar {
		s.setSidebar(true)
	}
	s.modular.FocusSlot(0)
	return core.Seq(core.Async(cmd), core.PropagateAll(ReseedMsg{}))
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
