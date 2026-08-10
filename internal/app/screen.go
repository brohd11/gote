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
// capture gate and toggles even while typing in the editor; "a" and "?" are
// intercepted only when nothing is capturing, so typed text and /-filters never
// lose the letter (alt+? is the modified alias that summons help from anywhere,
// the editor included).
var (
	sidebarKey = key.NewBinding(key.WithKeys("ctrl+b"), key.WithHelp("ctrl+b", "sidebar"))
	actionsKey = key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "actions"))
	previewKey = key.NewBinding(key.WithKeys("ctrl+p"), key.WithHelp("ctrl+p", "preview"))
	// alt+z, not ctrl+w: ctrl+w is the editor's own delete-word-back (and readline's),
	// and intercepting it here would swallow it before the editor ever sees it.
	wrapKey     = key.NewBinding(key.WithKeys("alt+z"), key.WithHelp("alt+z", "wrap"))
	lineNumsKey = key.NewBinding(key.WithKeys("ctrl+l"), key.WithHelp("ctrl+l", "line nums"))
	helpKey     = key.NewBinding(key.WithKeys("?", "alt+?"), key.WithHelp("?", "more"))
)

// The preview modes ctrl+p cycles through. Both renderers show up as a pane beside
// the editor rather than as an overlay over it: side by side is the shape a preview
// actually gets used in, and it is the only shape that lets the two be compared
// against the SOURCE as well as against each other. The full-width overlay is still
// built (previewScreen) but parked off the cycle.
const (
	previewOff     = iota // editor only
	previewPane           // the custom reader, live, beside the editor
	previewGlamour        // the same, rendered by glamour
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
	previewPanel *components.ScrollContainer // the custom reader's pane
	glamourPanel *components.ScrollContainer // glamour's pane; separate so each keeps its own scroll
	editor       *components.EditorScreen    // the editor pane's live child (ScreenPanel exposes none)
	currentPath  string                      // the doc the editor pane is showing; "" = the scratch buffer
	sidebar      bool
	minimal      bool         // ModeFile: the editor alone, all chrome masked, sidebar unreachable
	preview      int          // previewOff/previewPane/previewGlamour
	previewSrc   string       // the buffer text the pane was last rendered from
	previewW     int          // the width it was last rendered at (a resize must re-wrap)
	sh           *core.Shared // stashed by Init/SetSize for rebuilds and the crumb
	w, h         int
}

var _ core.Screen = (*homeScreen)(nil)
var _ core.Filterer = (*homeScreen)(nil)
var _ core.Receiver = (*homeScreen)(nil)
var _ core.Crumber = (*homeScreen)(nil)
var _ core.ChromeMasker = (*homeScreen)(nil)
var _ core.QuitGater = (*homeScreen)(nil)

// NewHomeScreen builds the root screen: the docs list seeded from the ctx, an empty
// open-docs list, and the editor pane starting on a scratch buffer.
//
// ModeFile builds the same screen minimally instead: the sidebar starts hidden and
// stays unreachable, the chrome is masked away (ChromeMask), and the editor boots on
// the file gote was given rather than on a scratch buffer. It is the same screen
// because everything that makes the editor pane work — the save/rekey bookkeeping in
// editorOpts, the ctrl+p preview panes — is wired here and is wanted there too.
func NewHomeScreen(sh *core.Shared) core.Screen {
	c := Of(sh)
	minimal := c.Mode == ModeFile
	s := &homeScreen{sidebar: !minimal, minimal: minimal}
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
	// The minimal editor goes through the ctx like any picked doc, so the buffer is
	// registered under its path and a save-as rekeys it the same way. ScreenPanel.Init
	// forwards Init to its child, so the file read fires at startup unprompted.
	if minimal {
		s.currentPath = c.FilePath
		s.editor = c.OpenDoc(c.FilePath, s.editorOpts())
	} else {
		s.editor = components.NewEditorScreen(s.editorOpts())
	}
	s.editorPanel = components.NewScreenPanel(s.editor)
	s.previewPanel = components.NewScrollContainer("preview")
	s.glamourPanel = components.NewScrollContainer("glamour")
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
		if core.MatchKey(k, helpKey) && (!s.modular.Filtering() || k == "alt+?") {
			return s, core.Push(s.helpScreen())
		}
		if core.MatchKey(k, previewKey) {
			return s, s.cyclePreview()
		}
		if core.MatchKey(k, wrapKey) {
			s.editor.ToggleWrap()
			return s, core.Action{}
		}
		if core.MatchKey(k, lineNumsKey) {
			s.editor.ToggleLineNums()
			return s, core.Action{}
		}
	}
	_, act := s.modular.Update(sh, msg)
	s.refreshPreview()
	return s, act
}

// cyclePreview steps ctrl+p through the panes: off → the custom reader → glamour →
// off. Every rung is a layout change, so nothing here touches the router's stack and
// there is no navigation state to keep in sync.
func (s *homeScreen) cyclePreview() core.Action {
	switch s.preview {
	case previewOff:
		s.setPreview(previewPane)
	case previewPane:
		s.setPreview(previewGlamour)
	case previewGlamour:
		s.setPreview(previewOff)
		// The parked overlay hangs off this rung: uncomment to make the cycle
		// off → pane → glamour pane → full-width overlay → off again.
		// return core.Push(s.previewScreen(false))
	}
	return core.Action{}
}

// previewScreen is the PARKED full-width overlay over the live buffer — built and
// tested, but off the ctrl+p cycle while the side-by-side panes are being lived with.
// Re-enable it from the previewGlamour rung of cyclePreview.
//
// Two of them chain: the custom reader REPLACES itself with the glamour one
// (pop-then-push in a single action) and the glamour one pops, so the router's stack
// only ever holds one and esc unwinds to the editor from either.
func (s *homeScreen) previewScreen(useGlamour bool) *components.DocScreen {
	title, render, next := "preview · ", components.RenderMarkdown, func() core.Action {
		return core.Replace(s.previewScreen(true))
	}
	if useGlamour {
		title, render, next = "glamour · ", glamourRender, func() core.Action { return core.Pop() }
	}
	return components.NewDocScreen(components.DocOpts{
		Title:  title + s.previewName(),
		Crumb:  strings.TrimSuffix(title, " · "),
		Render: func(width int) string { return render(s.editor.Text(), width) },
		OnKey: func(_ *core.Shared, k string) (core.Action, bool) {
			if core.MatchKey(k, previewKey) {
				return next(), true
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

// setPreview swaps which preview pane (if any) sits beside the editor, rebuilding the
// layout around it. The rebuild auto-focuses the first slot, so focus is put back on
// the editor: ctrl+p is a view toggle, not a navigation.
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
	// Clearing the skip-cache is what makes switching RENDERERS work: the buffer has
	// not changed, so without this the new pane would never draw.
	s.previewSrc, s.previewW = "", 0
	s.refreshPreview()
}

// previewPane answers the pane and renderer for the current mode, or nil when the
// preview is off.
func (s *homeScreen) previewTarget() (*components.ScrollContainer, func(string, int) string) {
	switch s.preview {
	case previewPane:
		return s.previewPanel, components.RenderMarkdown
	case previewGlamour:
		return s.glamourPanel, glamourRender
	}
	return nil, nil
}

// refreshPreview re-renders the live pane when the buffer changed under it, or when
// the pane changed width (the render is wrapped to it). It runs from Update — once per
// message — rather than from View, which would re-render the whole document on every
// frame, mouse motion included.
func (s *homeScreen) refreshPreview() {
	panel, render := s.previewTarget()
	if panel == nil || s.editor == nil {
		return
	}
	src, width := s.editor.Text(), panel.TextWidth()
	if src == s.previewSrc && width == s.previewW {
		return
	}
	s.previewSrc, s.previewW = src, width
	panel.SetLines(strings.Split(render(src, width), "\n"))
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

// QuitGate implements core.QuitGater: q and ctrl+c quit instantly when every
// buffer is clean; with unsaved changes they push a confirm popup listing the
// dirty docs — y quits anyway (discarding them), esc/n cancels. The router
// consults the stack top-down, so the gate still answers from under a pushed
// modal (the save-as/new-file line edit, the help overlay).
func (s *homeScreen) QuitGate(sh *core.Shared) (core.Action, bool) {
	dirty := s.dirtyDocs(sh)
	if len(dirty) == 0 {
		return core.Action{}, false
	}
	return core.Push(quitPopup(dirty)), true
}

// quitPopup builds the dirty-quit confirm. OnQuit keeps q/ctrl+c as the
// force-quit while the popup is on top: without it the router's stack walk
// would find this screen's gate below the popup and stack another one.
func quitPopup(dirty []string) *components.DialogScreen {
	body := "unsaved changes in:\n\n  " + strings.Join(dirty, "\n  ") +
		"\n\nquitting discards them.\n(q/ctrl+c force-quits)"
	popup := components.CreatePopup("unsaved changes", body, core.Async(tea.Quit), components.DefaultHelpKeys...)
	popup.OnQuit = func(*core.Shared) (core.Action, bool) { return core.Async(tea.Quit), true }
	return popup
}

// dirtyDocs names every open buffer with unsaved changes: the live editor first
// (it is the only home of the scratch buffer, which isn't in the open set), then
// each dirty doc in the ctx's open set, in opening order.
func (s *homeScreen) dirtyDocs(sh *core.Shared) []string {
	var names []string
	if s.editor != nil && s.editor.Dirty() {
		names = append(names, s.previewName()) // "scratch" or the current doc's name
	}
	c := Of(sh)
	for _, path := range c.OpenOrder {
		if ed := c.Open[path]; ed != nil && ed != s.editor && ed.Dirty() {
			names = append(names, docName(path))
		}
	}
	return names
}

// ChromeMask hides every router-drawn element in minimal mode, giving the editor the
// whole terminal: the breadcrumb and help bar are the only ones gote has up, and a
// nano replacement should be the file and nothing else. The router asks the top screen
// per render, so a pushed overlay (the save-as box) is unaffected and no state is left
// to restore.
func (s *homeScreen) ChromeMask() core.ChromeMask {
	if s.minimal {
		return core.FullscreenMask()
	}
	return core.ChromeMask{}
}

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
	ed := c.OpenDoc(path, s.editorOpts())
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

// editorOpts is the hook set every editor in this screen is built with — the pane's
// three ways out and back: ctrl+x closes the buffer, esc hands the keys back, ctrl+s
// writes it and stays. Path is filled in by whoever constructs the editor (Ctx.OpenDoc
// for a doc, left empty for the scratch buffer).
func (s *homeScreen) editorOpts() components.EditorOpts {
	return components.EditorOpts{
		OnExit:    s.editorExit,
		OnRelease: s.editorRelease,
		OnSaved:   s.editorSaved,
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
//
// Minimal mode returns here: this is the single door the sidebar can come back through
// (ctrl+b, and the unhide branches of editorExit/editorRelease), so refusing it here is
// what makes "the editor alone" hold without a guard at every call site.
func (s *homeScreen) setSidebar(visible bool) {
	if s.minimal {
		return
	}
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
		// The bar stays lean on purpose: preview/wrap/line-nums still work, but
		// they are documented in the ? overlay (helpKey) instead of the bar.
		Help: []key.Binding{sidebarKey, actionsKey, helpKey},
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
	// ExpandH on the editor: its body is only as wide as its longest line unless the
	// scrollbar forces the padding, so a short doc would leave the column ragged
	// against the preview's border (or the terminal edge).
	cols = append(cols, []components.Slot{{Panel: s.editorPanel, Weight: 1, ExpandH: true}})
	widths = append(widths, 0)
	if panel, _ := s.previewTarget(); panel != nil {
		cols = append(cols, []components.Slot{{Panel: panel, Weight: 1}})
		widths = append(widths, 0)
	}
	if s.sidebar {
		opts.ColWidths = widths // all-flex needs no entry at all
	}
	return components.NewModularScreen(cols, opts)
}
