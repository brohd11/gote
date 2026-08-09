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
	previewKey = key.NewBinding(key.WithKeys("ctrl+p"), key.WithHelp("ctrl+p", "preview"))
)

// The preview modes this screen can be in. The third rung of the ctrl+p cycle — the
// full-width overlay — is a pushed DocScreen and so lives on the router's stack, not
// here. The pane and the overlay are two different readings of the same render (one
// to write against, one to read), and which earns a permanent home is still open, so
// the key offers both rather than the choice being made here.
const (
	previewOff  = iota // editor only
	previewPane        // a live pane beside the editor
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
	modular      *components.ModularScreen
	docsPanel    *components.ListPanel
	openPanel    *components.ListPanel
	editorPanel  *components.ScreenPanel
	previewPanel *components.ScrollContainer
	editor       *components.EditorScreen // the editor pane's live child (ScreenPanel exposes none)
	currentPath  string                   // the doc the editor pane is showing; "" = the scratch buffer
	sidebar      bool
	preview      int          // previewOff/previewPane; the overlay lives on the router's stack
	previewSrc   string       // the buffer text the pane was last rendered from
	previewW     int          // the width it was last rendered at (a resize must re-wrap)
	sh           *core.Shared // stashed by Init/SetSize for rebuilds and the crumb
	w, h         int
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
	s.editor = components.NewEditorScreen(components.EditorOpts{
		OnExit:    s.editorExit,
		OnRelease: s.editorRelease,
	})
	s.editorPanel = components.NewScreenPanel(s.editor)
	s.previewPanel = components.NewScrollContainer("preview")
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
		if core.MatchKey(k, previewKey) {
			return s, s.cyclePreview()
		}
	}
	_, act := s.modular.Update(sh, msg)
	s.refreshPreview()
	return s, act
}

// cyclePreview steps ctrl+p through the preview modes: off → the live pane → the
// full-width overlay → off. The overlay is a pushed screen rather than a fourth
// state here, so the pane is torn down before it goes up and popping the overlay
// (its own ctrl+p, or esc) lands back at off, closing the cycle.
func (s *homeScreen) cyclePreview() core.Action {
	switch s.preview {
	case previewOff:
		s.setPreview(previewPane)
		return core.Action{}
	case previewPane:
		s.setPreview(previewOff)
		return core.Push(s.previewScreen())
	}
	return core.Action{}
}

// previewScreen is the full-width overlay: a DocScreen re-rendering the live buffer
// at whatever width it is given. esc pops it through core.Keys.Back; ctrl+p is added
// so the key that opened it also closes it.
func (s *homeScreen) previewScreen() *components.DocScreen {
	return components.NewDocScreen(components.DocOpts{
		Title:  "preview · " + s.previewName(),
		Crumb:  "preview",
		Render: func(width int) string { return components.RenderMarkdown(s.editor.Text(), width) },
		OnKey: func(_ *core.Shared, k string) (core.Action, bool) {
			if core.MatchKey(k, previewKey) {
				return core.Pop(), true
			}
			return core.Action{}, false
		},
	})
}

// previewName labels the preview with the doc being edited, or the scratch buffer.
func (s *homeScreen) previewName() string {
	if s.currentPath == "" {
		return "scratch"
	}
	return docName(s.currentPath)
}

// setPreview shows or hides the side pane, rebuilding the layout around it. The
// rebuild auto-focuses the first slot, so focus is put back on the editor: ctrl+p is
// a view toggle, not a navigation.
func (s *homeScreen) setPreview(mode int) {
	if s.preview == mode {
		return
	}
	s.modular.SetFocused(false)
	s.preview = mode
	s.modular = s.buildModular()
	if s.w > 0 {
		s.modular.SetSize(s.sh, s.w, s.h)
	}
	s.modular.FocusSlot(s.editorSlot())
	s.previewSrc, s.previewW = "", 0 // the pane is new (or gone); the next refresh must not skip
	s.refreshPreview()
}

// refreshPreview re-renders the side pane when the buffer changed under it, or when
// the pane changed width (the render is wrapped to it). It runs from Update — once per
// message — rather than from View, which would re-render the whole document on every
// frame, mouse motion included.
func (s *homeScreen) refreshPreview() {
	if s.preview != previewPane || s.editor == nil {
		return
	}
	src, width := s.editor.Text(), s.previewPanel.TextWidth()
	if src == s.previewSrc && width == s.previewW {
		return
	}
	s.previewSrc, s.previewW = src, width
	s.previewPanel.SetLines(strings.Split(components.RenderMarkdown(src, width), "\n"))
}

func (s *homeScreen) View(sh *core.Shared) string     { return s.modular.View(sh) }
func (s *homeScreen) HelpView(sh *core.Shared) string { return s.modular.HelpView(sh) }

func (s *homeScreen) SetSize(sh *core.Shared, width, bodyHeight int) {
	s.sh = sh
	s.w, s.h = width, bodyHeight
	s.modular.SetSize(sh, width, bodyHeight)
	s.refreshPreview() // the pane's new width re-wraps the render
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
	ed := c.OpenDoc(path, s.editorExit, s.editorRelease)
	s.currentPath = path
	s.editor = ed
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
// buffer" gesture, not an escape hatch — esc (editorRelease) and shift+← both leave
// the pane at any time, and unhiding the sidebar is what makes ctrl+x meaningful with
// it hidden, where there is no other pane to move to. The reseed refreshes both lists:
// the close shows in Open, and a save-as'd file shows in Docs.
func (s *homeScreen) editorExit(sh *core.Shared) core.Action {
	c := Of(sh)
	next := c.CloseDoc(s.currentPath)
	var cmd tea.Cmd
	if next != "" {
		s.currentPath = next
		s.editor = c.OpenDoc(next, s.editorExit, s.editorRelease)
	} else {
		s.currentPath = ""
		s.editor = components.NewEditorScreen(components.EditorOpts{
			OnExit:    s.editorExit,
			OnRelease: s.editorRelease,
		})
	}
	cmd = s.editorPanel.SetChild(s.editor)
	if !s.sidebar {
		s.setSidebar(true)
	}
	s.modular.FocusSlot(0)
	s.refreshPreview()
	return core.Seq(core.Async(cmd), core.PropagateAll(ReseedMsg{}))
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

// buildModular lays the current combination out: the sidebar column is optional
// (ctrl+b) and the preview column is optional (ctrl+p), so the grid is assembled
// rather than picked from a fixed set. The preview column is appended AFTER the
// editor, which is what keeps editorSlot's indexes valid whether or not it is up; the
// editor and preview both flex (ColWidths 0) and so split whatever the fixed sidebar
// leaves.
func (s *homeScreen) buildModular() *components.ModularScreen {
	opts := components.ModularOpts{
		Help: []key.Binding{sidebarKey, previewKey, actionsKey},
	}
	var cols [][]components.Slot
	var widths []int
	if s.sidebar {
		cols = append(cols, []components.Slot{
			{Panel: s.docsPanel, Weight: 1},
			{Panel: s.openPanel, Weight: 1},
		})
		widths = append(widths, sidebarWidth)
	}
	cols = append(cols, []components.Slot{{Panel: s.editorPanel, Weight: 1}})
	widths = append(widths, 0)
	if s.preview == previewPane {
		cols = append(cols, []components.Slot{{Panel: s.previewPanel, Weight: 1}})
		widths = append(widths, 0)
	}
	if s.sidebar {
		opts.ColWidths = widths // all-flex needs no entry at all
	}
	return components.NewModularScreen(cols, opts)
}
