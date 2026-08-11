package app

import (
	"math"
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

// The preview modes ctrl+p cycles through. The render shows up as a pane beside the
// editor rather than as an overlay over it: side by side is the shape a preview
// actually gets used in, and it is the only shape that lets it be read against the
// SOURCE. The full-width overlay is still built (previewScreen) but parked off the
// cycle.
const (
	previewOff  = iota // editor only
	previewPane        // the custom reader, live, beside the editor
)

// ReseedMsg is gote's "reload the doc list" broadcast: the Actions ▸ Refresh row
// raises it, and the home screen reseeds and rebuilds its lists on receipt.
type ReseedMsg struct{}

// homeScreen is gote's root screen: a ModularScreen with a hideable sidebar (the docs
// and open-docs lists) beside the editor pane. The wrapper owns the panels and swaps
// its internal ModularScreen on the sidebar toggle — the panels are shared across
// rebuilds, so list state and editor buffers survive, and the wrapper stays the same
// instance so the router never re-Inits it (a re-Init would re-run the editor's file
// load over a dirty buffer).
type homeScreen struct {
	modular      *components.ModularScreen
	docsPanel    *components.CompactListPanel
	openPanel    *components.CompactListPanel
	editorPanel  *components.ScreenPanel
	previewPanel *components.ScrollContainer // the live preview pane
	editor       *components.EditorScreen    // the editor pane's live child (ScreenPanel exposes none)
	currentPath  string                      // the doc the editor pane is showing; "" = the scratch buffer
	sidebar      bool
	minimal      bool         // ModeFile: the editor alone, all chrome masked, sidebar unreachable
	preview      int          // previewOff/previewPane
	previewSrc   string       // the buffer text the pane was last rendered from
	previewW     int          // the width it was last rendered at (a resize must re-wrap)
	previewMap   []int        // that render's source line → pane row map (RenderMarkdownMapped)
	previewAt    int          // the editor scroll offset the pane was last synced to; -1 re-syncs
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
	s.docsPanel = components.NewCompactListPanel(docRows(c), "Docs", components.ListPanelOpts{
		OnSelect: s.pickDoc,
		Border:   true,
	})
	s.openPanel = components.NewCompactListPanel(nil, "Open", components.ListPanelOpts{
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
	s.syncPreviewScroll()
	return s, act
}

// cyclePreview steps ctrl+p through the panes: off → the reader → off. Every rung is
// a layout change, so nothing here touches the router's stack and there is no
// navigation state to keep in sync.
func (s *homeScreen) cyclePreview() core.Action {
	switch s.preview {
	case previewOff:
		s.setPreview(previewPane)
	case previewPane:
		s.setPreview(previewOff)
		// The parked overlay hangs off this rung: uncomment to make the cycle
		// off → pane → full-width overlay → off again.
		// return core.Push(s.previewScreen())
	}
	return core.Action{}
}

// previewScreen is the PARKED full-width overlay over the live buffer — built and
// tested, but off the ctrl+p cycle while the side-by-side pane is being lived with.
// Re-enable it from the previewPane rung of cyclePreview.
//
// It renders the LIVE buffer rather than a snapshot, so it is the same document the
// pane shows; ctrl+p and esc both pop it, which is why the home screen tracks only
// the pane — an esc it never sees cannot desync it.
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
	// Clearing the skip-cache is what makes REOPENING the pane work: the buffer has not
	// changed since it was last up, so without this it would come back blank.
	s.previewSrc, s.previewW, s.previewMap = "", 0, nil
	s.previewAt = -1 // the pane re-syncs to wherever the editor is scrolled
	s.refreshPreview()
	s.syncPreviewScroll()
}

// previewTarget answers the live pane, or nil when the preview is off.
func (s *homeScreen) previewTarget() *components.ScrollContainer {
	if s.preview == previewPane {
		return s.previewPanel
	}
	return nil
}

// refreshPreview re-renders the live pane when the buffer changed under it, or when
// the pane changed width (the render is wrapped to it). It runs from Update — once per
// message — rather than from View, which would re-render the whole document on every
// frame, mouse motion included.
//
// The mapped renderer costs nothing over the plain one and is what makes the scroll
// sync exact, so the map is kept with the render it belongs to.
func (s *homeScreen) refreshPreview() {
	panel := s.previewTarget()
	if panel == nil || s.editor == nil {
		return
	}
	src, width := s.editor.Text(), panel.TextWidth()
	if src == s.previewSrc && width == s.previewW {
		return
	}
	out, mapped := components.RenderMarkdownMapped(src, width)
	s.previewSrc, s.previewW, s.previewMap = src, width, mapped
	s.previewAt = -1 // the rows the last sync was computed against are gone
	panel.SetLines(strings.Split(out, "\n"))
}

// syncPreviewScroll scrolls the live pane to follow the editor. The two views do not
// hold the same number of rows for the same text — the render joins paragraphs, gives
// every heading a blank line and grows rules around fences — so a single alignment
// cannot be right everywhere. This one is right where it matters and eases where it
// cannot be:
//
//   - Through the body of the document the editor's MIDDLE line sits at the pane's
//     middle row, on the row RenderMarkdownMapped says that line rendered to. Aligning
//     the middles (rather than the tops) keeps the correspondence readable across the
//     whole pane, and leaves a half-pane of slack at each end for the two views to
//     disagree in.
//   - Within one editor screenful of either end the target eases into the end itself:
//     offset 0 at the top, the pane's last page at the bottom. Without that the pane
//     could never reach the bottom at all — the render outruns the source, so the
//     editor bottoms out while the anchor still points a screenful short.
//
// The sync fires only when the editor's scroll offset actually moves, so a manually
// scrolled pane is left alone until the editor scrolls again.
func (s *homeScreen) syncPreviewScroll() {
	panel := s.previewTarget()
	if panel == nil || s.editor == nil {
		return
	}
	off, maxOff, editorRows := s.editor.ScrollSpan()
	if off == s.previewAt {
		return
	}
	s.previewAt = off
	if len(s.previewMap) == 0 {
		return
	}

	// The anchor: the center line's row, placed at the pane's middle. The min guards a
	// map from a render one keystroke behind the buffer.
	center := s.editor.CenterLine()
	anchored := s.previewMap[min(center, len(s.previewMap)-1)] - panel.VisibleRows()/2

	// ends is where the pane must be when the editor is AT an end; w is how much of it
	// to apply — nothing through the body, all of it at either extreme, smoothstepped
	// between so the handoff has no kink.
	w, ends := 1.0, 0.0
	if maxOff > 0 {
		band := float64(max(editorRows, 1))
		w = 1 - min(float64(min(off, maxOff-off))/band, 1)
		w = w * w * (3 - 2*w)
		ends = float64(off) / float64(maxOff) * float64(panel.MaxScrollOffset())
	}
	// ScrollTo clamps, so nothing here has to.
	panel.ScrollTo(int(math.Round((1-w)*float64(anchored) + w*ends)))
}

func (s *homeScreen) View(sh *core.Shared) string     { return s.modular.View(sh) }
func (s *homeScreen) HelpView(sh *core.Shared) string { return s.modular.HelpView(sh) }

func (s *homeScreen) SetSize(sh *core.Shared, width, bodyHeight int) {
	s.sh = sh
	resized := width != s.w || bodyHeight != s.h
	s.w, s.h = width, bodyHeight
	s.modular.SetSize(sh, width, bodyHeight)
	s.refreshPreview() // the pane's new width re-wraps the render
	if resized {
		// A height-only resize leaves the render alone but moves both viewports under
		// it, so the sync has to run again on an unchanged editor offset. Only on a
		// REAL resize: the router re-lays out after every message (core.Router.Update),
		// so forcing it every tick would undo a hand-scrolled pane in the same tick the
		// user scrolled it.
		s.previewAt = -1
		s.syncPreviewScroll()
	}
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

// quitPopup builds the dirty-quit confirm. OnQuit keeps q/ctrl+c as the force-quit
// while the popup is on top: without it the router's stack walk would find this
// screen's gate below the popup and stack another one.
func quitPopup(dirty []string) *components.DialogScreen {
	return dirtyPopup(dirty, "quitting", func(*core.Shared) core.Action { return core.Async(tea.Quit) })
}

// dirtyPopup is the shared discard gate for quitting and vault switches. It keeps
// the same compact list and confirm/cancel controls in both places; only the action
// named in the warning and run on confirmation differs.
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

// ChromeMask hides persistent router chrome in minimal mode, giving the editor the
// whole terminal in steady state. The status line remains eligible but has zero height
// unless transient feedback (such as a clipboard result) is present. The router asks
// the top screen per render, so a pushed overlay is unaffected and no state is left to
// restore.
func (s *homeScreen) ChromeMask() core.ChromeMask {
	if s.minimal {
		mask := core.FullscreenMask()
		mask.Status = false // normally zero-height; clipboard feedback may appear briefly
		return mask
	}
	return core.ChromeMask{}
}

// CrumbLabel contributes the active store, ad-hoc scan, or named vault.
func (s *homeScreen) CrumbLabel(short bool) string {
	if s.sh != nil {
		c := Of(s.sh)
		switch c.Mode {
		case ModeScan:
			return "scan: " + c.ScanDir
		case ModeVault:
			return "vault: " + c.VaultName
		}
	}
	return "docs"
}

// Receive handles app-level broadcasts: a ReseedMsg reloads both lists from a fresh
// seed; a theme change restyles the two live list models in place. gote's root owns
// stateful editor instances, so it must not use core.OnThemeChange: that helper
// rebuilds the root and would discard the scratch buffer and the pane's live wiring.
// The editor and panel frames read theme colors while rendering; only bubbles lists
// cache themed styles and need an explicit refresh here.
func (s *homeScreen) Receive(sh *core.Shared, payload any) core.Action {
	if _, ok := payload.(ReseedMsg); ok {
		c := Of(sh)
		c.Seed()
		s.docsPanel.SetItems(docRows(c))
		s.openPanel.SetItems(docItems(c.OpenDocs(), s.currentPath))
		return core.Action{}
	}
	if msg, ok := payload.(SwitchVaultMsg); ok {
		return s.requestVaultSwitch(sh, msg.Name)
	}
	if _, ok := payload.(core.MsgThemeChanged); ok {
		core.StyleList(s.docsPanel.List())
		core.StyleList(s.openPanel.List())
	}
	return core.Action{}
}

// requestVaultSwitch validates the target before consulting dirty state. A broken
// saved path must never make the user discard work for a switch that cannot happen.
func (s *homeScreen) requestVaultSwitch(sh *core.Shared, name string) core.Action {
	if _, err := vaultPath(Of(sh).Config, name); err != nil {
		return core.Push(errPopup("open vault", err))
	}
	if dirty := s.dirtyDocs(sh); len(dirty) > 0 {
		return core.Push(dirtyPopup(dirty, "switching vaults", func(sh *core.Shared) core.Action {
			return s.activateVault(sh, name)
		}))
	}
	return s.activateVault(sh, name)
}

// activateVault performs the destructive half of a confirmed switch: the context's
// open set is cleared, the pane gets a fresh scratch editor, and every vault-specific
// view state is rebuilt before navigation returns to the root.
func (s *homeScreen) activateVault(sh *core.Shared, name string) core.Action {
	c := Of(sh)
	if err := c.SwitchVault(name); err != nil {
		return core.Replace(errPopup("open vault", err))
	}
	s.currentPath = ""
	s.editor = components.NewEditorScreen(s.editorOpts())
	cmd := s.editorPanel.SetChild(s.editor)
	s.docsPanel.SetItems(docRows(c))
	s.openPanel.SetItems(nil)
	s.preview = previewOff
	s.previewSrc, s.previewW, s.previewMap = "", 0, nil
	s.previewAt = -1
	s.minimal = false
	s.sidebar = true
	s.modular.SetFocused(false)
	s.modular = s.buildModular()
	if s.w > 0 {
		s.modular.SetSize(sh, s.w, s.h)
	}
	s.modular.FocusSlot(0)
	return core.Seq(core.Async(cmd), core.ResetToRoot())
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
	row, ok := components.CompactListItemRow(l, l.Index())
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
		Search:    true,
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
	if panel := s.previewTarget(); panel != nil {
		cols = append(cols, []components.Slot{{Panel: panel, Weight: 1}})
		widths = append(widths, 0)
	}
	if s.sidebar {
		opts.ColWidths = widths // all-flex needs no entry at all
	}
	return components.NewModularScreen(cols, opts)
}
