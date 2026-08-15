package app

import (
	"math"
	"os"
	"path/filepath"
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
	// The full-screen reader gets its own key rather than a third rung on ctrl+p: full
	// screen is a place you go, not a state you cycle past on the way back to the editor.
	// alt+p and not ctrl+shift+p — bubbletea v1 attaches shift only to navigation keys, so
	// a terminal delivers ctrl+shift+p as a bare "P" typed into the buffer.
	fullPreviewKey = key.NewBinding(key.WithKeys("alt+p"), key.WithHelp("alt+p", "full preview"))
	// alt+z, not ctrl+w: ctrl+w is the editor's own delete-word-back (and readline's),
	// and intercepting it here would swallow it before the editor ever sees it.
	wrapKey     = key.NewBinding(key.WithKeys("alt+z"), key.WithHelp("alt+z", "wrap"))
	lineNumsKey = key.NewBinding(key.WithKeys("ctrl+l"), key.WithHelp("ctrl+l", "line nums"))
	helpKey     = key.NewBinding(key.WithKeys("?", "alt+?"), key.WithHelp("?", "more"))
	// The docs list's own key, not the screen's: it acts on the selected row, so it
	// belongs to the panel that has one (ListPanelOpts.OnKey) and must not fire from
	// the editor. ctrl+r is free everywhere — gote, the editor, and the router's globals.
	renameKey = key.NewBinding(key.WithKeys("ctrl+r"), key.WithHelp("ctrl+r", "rename"))
)

// The preview modes ctrl+p cycles through. The render shows up as a pane beside the
// editor rather than as an overlay over it: side by side is the shape a preview
// actually gets used in, and it is the only shape that lets it be read against the
// SOURCE. The full-width reader is alt+p's (previewScreen), off this cycle entirely —
// it is a pushed screen with no layout state, so there is nothing here to track.
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
	modular       *components.ModularScreen
	docsPanel     *components.CompactListPanel
	openPanel     *components.CompactListPanel
	editorPanel   *components.ScreenPanel
	previewPanel  *components.ScrollContainer // the live preview pane
	editor        *components.EditorScreen    // the editor pane's live child (ScreenPanel exposes none)
	currentPath   string                      // the doc the editor pane is showing; "" = the scratch buffer
	sidebar       bool
	minimal       bool         // ModeFile: the editor alone, all chrome masked, sidebar unreachable
	launchPreview bool         // --preview: push the reader from Init, once
	preview       int          // previewOff/previewPane
	previewSrc    string       // the buffer text the pane was last rendered from
	previewW      int          // the width it was last rendered at (a resize must re-wrap)
	previewMap    []int        // that render's source line → pane row map (RenderMarkdownMapped)
	previewAt     int          // the editor scroll offset the pane was last synced to; -1 re-syncs
	sh            *core.Shared // stashed by Init/SetSize for rebuilds and the crumb
	w, h          int
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
		OnKey:    s.docsKey,
		Help:     []key.Binding{renameKey},
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
	// --preview needs a document to read, and ModeFile is the only launch that opens one
	// here — so a vault or scan launch never sets this, which is how the flag comes to be
	// silently ignored for every target that is not a single markdown file.
	s.launchPreview = c.Preview && minimal && s.previewable()
	s.editorPanel = components.NewScreenPanel(s.editor)
	s.previewPanel = components.NewScrollContainer("preview")
	s.modular = s.buildModular()
	return s
}

// Init stashes Shared, boots the layout, and — for a --preview launch — pushes the reader
// over it. The push travels as an Action through the cmd queue, which the router resolves
// against the stack the same way it resolves one returned from Update.
//
// The reader renders the file off DISK rather than the editor's buffer, because at this
// point the buffer is still empty: EditorScreen.Init dispatches its read as a cmd, and
// DocScreen renders once and re-renders only on a width change, so a reader built on the
// buffer would race that read and could stay blank. Disk is exactly the content the read
// is about to deliver. Every later alt+p renders the live buffer.
func (s *homeScreen) Init(sh *core.Shared) tea.Cmd {
	s.sh = sh
	cmd := s.modular.Init(sh)
	if !s.launchPreview {
		return cmd
	}
	s.launchPreview = false
	path := s.currentPath
	doc := s.previewScreen(func() string { return fileText(path) })
	return tea.Batch(cmd, func() tea.Msg { return core.Push(doc) })
}

// fileText reads a document for the launch reader. An unreadable path renders as an empty
// page — the same thing the editor about to open behind it will show for a new file.
func fileText(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
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
		if core.MatchKey(k, fullPreviewKey) {
			if !s.previewable() {
				return s, core.Action{}
			}
			return s, core.Push(s.previewScreen(s.editor.Text))
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

// previewable reports whether ctrl+p has anything worth showing. The pane is a
// markdown reader — it joins paragraphs and re-flows to its own width — so pointing it
// at a .go or .json file destroys exactly the indentation that made the file readable.
// The unnamed scratch buffer counts as markdown, which is what it has always been.
func (s *homeScreen) previewable() bool {
	if s.currentPath == "" {
		return true
	}
	switch strings.ToLower(filepath.Ext(s.currentPath)) {
	case ".md", ".markdown":
		return true
	}
	return false
}

// enforcePreview closes a pane the current document no longer earns — the doc switched
// to a non-markdown file, or a save-as renamed the open one out of markdown underneath
// it. Called wherever currentPath moves; a no-op when the pane is already off.
func (s *homeScreen) enforcePreview() {
	if s.preview != previewOff && !s.previewable() {
		s.setPreview(previewOff)
	}
}

// cyclePreview steps ctrl+p through the panes: off → the reader → off. Every rung is
// a layout change, so nothing here touches the router's stack and there is no
// navigation state to keep in sync. On a file the reader would mangle, ctrl+p does
// nothing at all.
func (s *homeScreen) cyclePreview() core.Action {
	if !s.previewable() {
		return core.Action{}
	}
	switch s.preview {
	case previewOff:
		s.setPreview(previewPane)
	case previewPane:
		s.setPreview(previewOff)
	}
	return core.Action{}
}

// previewScreen is alt+p's full-width reader, pushed over whatever the editor is doing.
// alt+p and esc both pop it, which is why the home screen tracks only the pane — an esc
// it never sees cannot desync it, and the pane it was opened over is still there
// underneath on the way back.
//
// src is the document to render, deferred rather than passed as a string because the two
// callers disagree about where it lives: alt+p hands over the editor's live buffer, while
// the --preview launch reads the file off disk (see homeScreen.Init).
func (s *homeScreen) previewScreen(src func() string) *previewDoc {
	return &previewDoc{minimal: s.minimal, DocScreen: components.NewDocScreen(components.DocOpts{
		Title:  "preview · " + s.previewName(),
		Crumb:  "preview",
		Render: func(width int) string { return components.RenderMarkdown(src(), width) },
		OnKey: func(_ *core.Shared, k string) (core.Action, bool) {
			if core.MatchKey(k, fullPreviewKey) {
				return core.Pop(), true
			}
			return core.Action{}, false
		},
	})}
}

// previewDoc is the full-screen reader: bubblestack's read-only DocScreen plus the chrome
// mask that makes it one. The router asks only the TOP screen for its mask, so without
// this the breadcrumb (and, in ModeFile, everything homeScreen.ChromeMask had just
// suppressed) would come back the moment the reader was pushed.
//
// The help bar follows the launch it was opened from: an ordinary launch keeps it (one
// dim line naming the way out is not noise, it is the exit), while a ModeFile launch
// drops it, because the editor underneath has none either — a reader that grew chrome
// its own screen doesn't have would read as a different app. There the exit (esc, or
// alt+p again) goes unlabeled, which is the price of the chrome-free pair.
//
// Status is masked in both cases, as it is on homeScreen: the reader paints the message
// itself so its body never changes height (View/HelpView, see status.go).
type previewDoc struct {
	*components.DocScreen
	minimal bool
	h       int // the body height the router last handed down, for statusOver
}

func (p *previewDoc) ChromeMask() core.ChromeMask {
	mask := core.FullscreenMask()
	mask.Help = p.minimal
	return mask
}

func (p *previewDoc) SetSize(sh *core.Shared, width, bodyHeight int) {
	p.h = bodyHeight
	p.DocScreen.SetSize(sh, width, bodyHeight)
}

func (p *previewDoc) View(sh *core.Shared) string {
	body := p.DocScreen.View(sh)
	if p.minimal {
		return statusOver(sh, body, p.h)
	}
	return body
}

func (p *previewDoc) HelpView(sh *core.Shared) string {
	return statusBar(sh, p.DocScreen.HelpView(sh))
}

// Update keeps the wrapper on the stack. DocScreen.Update answers with its own pointer,
// which the router stores back — dropping this type, and with it the mask, on the first
// keystroke the reader received.
func (p *previewDoc) Update(sh *core.Shared, msg tea.Msg) (core.Screen, core.Action) {
	_, act := p.DocScreen.Update(sh, msg)
	return p, act
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
	s.preview = mode
	s.rebuildModular(s.sh, s.editorSlot())
	s.resetPreviewCache()
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

// View and HelpView both route through the status helpers (status.go): the router's own
// status row is masked away, so this screen is the one that has to find the message a
// home. In minimal mode there is no help bar, so the body's last row takes it.
func (s *homeScreen) View(sh *core.Shared) string {
	body := s.modular.View(sh)
	if s.minimal {
		return statusOver(sh, body, s.h)
	}
	return body
}

func (s *homeScreen) HelpView(sh *core.Shared) string {
	return statusBar(sh, s.modular.HelpView(sh))
}

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
// whole terminal in steady state. The router asks the top screen per render, so a pushed
// overlay is unaffected and no state is left to restore.
//
// Status is masked in BOTH modes — not because gote has no status line, but because the
// router draws it as a row taken off the body, which makes every pane jump when a
// clipboard result appears and jump back when it clears. This screen paints it itself
// (View/HelpView, see status.go) in space the frame already spends.
func (s *homeScreen) ChromeMask() core.ChromeMask {
	if s.minimal {
		return core.FullscreenMask()
	}
	return core.ChromeMask{Status: true}
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
	s.resetPreviewCache()
	s.minimal = false
	s.sidebar = true
	s.rebuildModular(sh, 0)
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

// openDoc switches the editor pane to path and moves focus to it. An already-open doc is
// a switch, not an open: Ctx.OpenDoc hands back the existing editor and the pane swap
// leaves it exactly as it stands — unsaved edits, cursor, scroll and undo history all
// intact — because EditorScreen reads its file only on the first Init.
func (s *homeScreen) openDoc(sh *core.Shared, path string) core.Action {
	c := Of(sh)
	ed := c.OpenDoc(path, s.editorOpts())
	s.currentPath = path
	s.editor = ed
	s.openPanel.SetItems(docItems(c.OpenDocs(), s.currentPath))
	cmd := s.editorPanel.SetChild(ed)
	// After SetChild, so the layout setPreview rebuilds is sized around the new buffer.
	s.enforcePreview()
	s.modular.FocusSlot(s.editorSlot())
	return core.Async(cmd)
}

// rowLineEdit builds a floating line edit sitting exactly over the selected docs row —
// the shape both the new-file and rename boxes take. Anchor math: the docs panel is
// column 0 row 0 of the layout, so its outer top-left is (0, BodyY); its border takes
// one row, and the LineEdit anchor sits one row above the covered row (its own top
// border) — the two cancel, so the anchor is BodyY + the list-relative row. x=0 and
// width=sidebarWidth land the box's borders exactly on the panel's own.
func (s *homeScreen) rowLineEdit(sh *core.Shared, placeholder, crumb string,
	onDone func(*core.Shared, string) core.Action) *components.LineEditScreen {
	l := s.docsPanel.List()
	row, ok := components.CompactListItemRow(l, l.Index())
	if !ok {
		row = 0 // the selected row is on-page by construction; never die on it
	}
	edit := components.NewLineEdit(placeholder, 0, sh.BodyY()+row, sidebarWidth, onDone, nil)
	edit.Crumb = crumb
	edit.Help = []key.Binding{} // the hint row wraps at sidebar width; keep the box slim
	return edit
}

// newFile pushes the row-anchored line edit that names a document into being.
func (s *homeScreen) newFile(sh *core.Shared) core.Action {
	return core.Push(s.rowLineEdit(sh, "name (a/b nests dirs)", "new file", s.createFile))
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
		dir, err := DocsDir()
		if err != nil {
			return core.Replace(errPopup("new file", err))
		}
		base = dir
	}
	path, err := newDocPath(base, name, c.NewExt)
	if err != nil {
		return core.Replace(errPopup("new file", err))
	}
	if err := createDoc(path); err != nil {
		return core.Replace(errPopup("new file", err))
	}
	return core.Seq(core.Pop(), core.PropagateAll(ReseedMsg{}), s.openDoc(sh, path))
}

// docsKey is the docs panel's OnKey (ListPanelOpts.OnKey): ctrl+r renames the selected
// doc. The hook fires only while the panel is focused and only when it is not running a
// /-filter, so neither the editor nor a filter query can lose the chord. Reporting
// false hands the key back to the list — which is what leaves the "+ new file" row
// inert, since an action row has no path to rename.
func (s *homeScreen) docsKey(sh *core.Shared, k string, it list.Item) (core.Action, bool) {
	if !core.MatchKey(k, renameKey) {
		return core.Action{}, false
	}
	di, ok := it.(docItem)
	if !ok {
		return core.Action{}, false
	}
	return s.renameFile(sh, di.doc), true
}

// renameFile pushes the same row-anchored line edit newFile does, prefilled with the
// doc's path relative to its origin root — so editing the directory part moves the file
// as well as renaming it.
func (s *homeScreen) renameFile(sh *core.Shared, doc DocFile) core.Action {
	rel, err := filepath.Rel(doc.Root, doc.Path)
	if err != nil {
		rel = doc.Name // an unrelatable root still renames in place
	}
	edit := s.rowLineEdit(sh, "new name", "rename",
		func(sh *core.Shared, name string) core.Action { return s.submitRename(sh, doc, rel, name) })
	edit.SetValue(rel)
	return core.Push(edit)
}

// submitRename is the rename box's OnDone: resolve the typed path against the doc's
// own root, move the file, then catch the app up with where it now lives. Blank input
// and an unchanged name cancel quietly. Errors surface as a popup swapped in over the
// line edit, so the overlay's stack depth holds (createFile's precedent).
//
// A doc that is OPEN needs three things pointed at the new path, and each is the only
// home of one fact: the editor knows where to save (SetPath, which also moves its title
// and re-picks the highlighter for a changed extension), the ctx keys the open set by
// path (RekeyDoc — the same call a save-as makes), and the screen tracks which doc the
// pane is showing. The buffer itself is never touched, so unsaved edits and undo
// history survive a rename exactly as they survive a save-as.
func (s *homeScreen) submitRename(sh *core.Shared, doc DocFile, rel, name string) core.Action {
	name = strings.TrimSpace(name)
	if name == "" || name == rel {
		return core.Pop()
	}
	c := Of(sh)
	path, err := newDocPath(doc.Root, name, c.NewExt)
	if err != nil {
		return core.Replace(errPopup("rename", err))
	}
	if path == doc.Path {
		return core.Pop()
	}
	if err := renameDoc(doc.Path, path); err != nil {
		return core.Replace(errPopup("rename", err))
	}
	if ed := c.Open[doc.Path]; ed != nil {
		ed.SetPath(path)
		c.RekeyDoc(doc.Path, path, ed)
		if s.currentPath == doc.Path {
			s.currentPath = path
			s.enforcePreview() // a rename can take a file out of markdown under a live pane
		}
	}
	return core.Seq(core.Pop(), core.PropagateAll(ReseedMsg{}))
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
		OnExit:       s.editorExit,
		OnRelease:    s.editorRelease,
		OnSaved:      s.editorSaved,
		Search:       true,
		ContextMenu:  true,
		ContextItems: s.editorContextItems,
	}
}

// editorContextItems are gote's rows on the editor's right-click menu, below the
// clipboard verbs: the view toggles that otherwise only exist as chords. They are built
// fresh on every press, which is what lets Disabled track the current document — the
// preview is a markdown reader and refuses everything else, the same gate ctrl+p uses.
// Each Pick pops the menu itself (the component's convention). No Hints: the menu
// dispatches no accelerators, so a key-shaped hint would be a promise it doesn't keep.
func (s *homeScreen) editorContextItems(*core.Shared) []components.MenuItem {
	return []components.MenuItem{
		{Label: "Toggle preview", Disabled: !s.previewable(), Pick: func(*core.Shared) core.Action {
			return core.Seq(core.Pop(), s.cyclePreview())
		}},
		{Label: "Full preview", Disabled: !s.previewable(), Pick: func(*core.Shared) core.Action {
			return core.Seq(core.Pop(), core.Push(s.previewScreen(s.editor.Text)))
		}},
		{Label: "Toggle wrap", Pick: func(*core.Shared) core.Action {
			s.editor.ToggleWrap()
			return core.Pop()
		}},
		{Label: "Toggle line numbers", Pick: func(*core.Shared) core.Action {
			s.editor.ToggleLineNums()
			return core.Pop()
		}},
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
	s.enforcePreview() // a save-as can rename markdown out of markdown under an open pane
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
	s.enforcePreview()
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
	s.sidebar = visible
	s.rebuildModular(s.sh, noFocus)
}

// noFocus tells rebuildModular to leave focus where the fresh layout auto-places it
// (its first focusable slot) instead of moving it somewhere specific.
const noFocus = -1

// rebuildModular swaps in a layout built from the current sidebar/preview flags. The
// order matters and is why this is one helper rather than four lines at each caller:
// the outgoing screen's focused panel is blurred BEFORE it is discarded (otherwise a
// panel keeps a focus ring it can never clear), the new screen only takes a size once
// one is known, and focus is placed last, after the slots it names exist.
//
// focus is a slot index, or noFocus to accept the auto-focused first slot.
func (s *homeScreen) rebuildModular(sh *core.Shared, focus int) {
	s.modular.SetFocused(false)
	s.modular = s.buildModular()
	if s.w > 0 {
		s.modular.SetSize(sh, s.w, s.h)
	}
	if focus != noFocus {
		s.modular.FocusSlot(focus)
	}
}

// resetPreviewCache drops the memo of what the preview pane last rendered. Clearing it
// is what makes REOPENING the pane work: the buffer has not changed since the pane was
// last up, so the skip-on-unchanged check would otherwise bring it back blank. The
// scroll anchor goes with it so the pane re-syncs to wherever the editor is scrolled.
func (s *homeScreen) resetPreviewCache() {
	s.previewSrc, s.previewW, s.previewMap = "", 0, nil
	s.previewAt = -1
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
