package app

import (
	"os"
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
	// The reader gets its own key rather than a third rung on ctrl+p: reading the whole
	// document is a mode you sit in, not a state you cycle past on the way back to the
	// editor. It covers the EDITOR, not the app — the sidebar stays beside it.
	// alt+p and not ctrl+shift+p — bubbletea v1 attaches shift only to navigation keys, so
	// a terminal delivers ctrl+shift+p as a bare "P" typed into the buffer.
	fullPreviewKey = key.NewBinding(key.WithKeys("alt+p"), key.WithHelp("alt+p", "full preview"))
	// alt+z, not ctrl+w: ctrl+w is the editor's own delete-word-back (and readline's),
	// and intercepting it here would swallow it before the editor ever sees it.
	wrapKey     = key.NewBinding(key.WithKeys("alt+z"), key.WithHelp("alt+z", "wrap"))
	lineNumsKey = key.NewBinding(key.WithKeys("ctrl+l"), key.WithHelp("ctrl+l", "line nums"))
	gutterKey   = key.NewBinding(key.WithKeys("alt+g"), key.WithHelp("alt+g", "git gutter"))
	helpKey     = key.NewBinding(key.WithKeys("?", "alt+?"), key.WithHelp("?", "more"))
	// The docs list's own key, not the screen's: it acts on the selected row, so it
	// belongs to the panel that has one (ListPanelOpts.OnKey) and must not fire from
	// the editor. ctrl+r is free everywhere — gote, the editor, and the router's globals.
	renameKey = key.NewBinding(key.WithKeys("ctrl+r"), key.WithHelp("ctrl+r", "rename"))
	// The docs list's other row key. ctrl+d is NOT free the way ctrl+r is — it is the
	// editor's forward-delete — but it never has to be: docsKey fires only while the docs
	// panel is focused and not running a /-filter, so the editor keeps its own chord.
	deleteKey = key.NewBinding(key.WithKeys("ctrl+d"), key.WithHelp("ctrl+d", "delete"))
	// The flat/explorer switch. A screen key rather than the docs panel's own, because it
	// has to work in BOTH views — a key that only the flat list carried could turn the
	// explorer on and never off. alt+t is free of gote's chords, of the editor's word
	// motions (alt+b/f/d) and of core's alt+wasd arrows.
	flatKey = key.NewBinding(key.WithKeys("alt+t"), key.WithHelp("alt+t", "flat/folder view"))
	// The explorer's own row density (components.FilePanelOpts.DensityKey), so it fires
	// only while that panel is focused and not running a /-filter. alt+r, not alt+d/f: the
	// editor moves by words on those.
	densityKey = key.NewBinding(key.WithKeys("alt+r"), key.WithHelp("alt+r", "row density"))
)

// The preview modes ctrl+p cycles through. The render shows up as a pane beside the
// editor rather than as an overlay over it: side by side is the shape a preview
// actually gets used in, and it is the only shape that lets it be read against the
// SOURCE. The reader alt+p opens is off this cycle entirely — it is the editor pane's
// child rather than a column (homeScreen.fullPreview), and the two are mutually
// exclusive: alt+p folds a live pane away and puts it back on the way out.
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
	filePanel     *components.FilePanel // the folder view alt+t swaps into the docs slot
	openPanel     *components.CompactListPanel
	editorPanel   *components.ScreenPanel
	previewPanel  *components.ScrollContainer // the live preview pane
	editor        *components.EditorScreen    // the editor pane's live buffer (ScreenPanel exposes none)
	fullPreview   *components.DocScreen       // alt+p: the reader IN the editor pane; nil = the editor is
	currentPath   string                      // the doc the editor pane is showing; "" = the scratch buffer
	sidebar       bool
	flat          bool         // the docs slot shows the flat scan (true) or the folder explorer
	minimal       bool         // ModeFile: the editor alone, all chrome masked, sidebar unreachable
	gitGutter     bool         // alt+g: draw change markers against HEAD (see gitgutter.go)
	gutter        gutter       // the baseline and last-drawn markers behind them
	launchPreview bool         // --preview: open the reader from Init, once
	preview       int          // previewOff/previewPane
	previewPrior  int          // the ctrl+p mode alt+p folded away, restored when the reader closes
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
	// Which view the sidebar opens on is the config's (folder_view); alt+t moves it from
	// there and nothing writes the choice back.
	s := &homeScreen{sidebar: !minimal, minimal: minimal, flat: !c.Config.FolderView,
		gitGutter: gutterDefault(c.Config, c.Mode)}
	// Border on both sidebar lists: with three panes on screen the focused one has
	// to be visible, and the editor pane is framed automatically (ScreenPanel borders
	// a core.Borderer child).
	// No Help: that field only feeds the bar, and rename is documented in the ? overlay
	// with the rest. The binding stays live — OnKey (docsKey) is what fires it.
	s.docsPanel = components.NewCompactListPanel(docRows(c), "Docs", components.ListPanelOpts{
		OnSelect: s.pickDoc,
		OnKey:    s.docsKey,
		Border:   true,
	})
	s.openPanel = components.NewCompactListPanel(nil, "Open", components.ListPanelOpts{
		OnSelect: s.pickDoc,
		Border:   true,
	})
	// Built alongside the flat list rather than on first use: both panels outlive the
	// ModularScreen that holds them, and a layout rebuild does not Init what it builds
	// (see rebuildModular), so a panel that deferred its first read would swap in empty.
	s.filePanel = components.NewFilePanel(s.filePanelOpts(c))
	// The minimal editor goes through the ctx like any picked doc, so the buffer is
	// registered under its path and a save-as rekeys it the same way. ScreenPanel.Init
	// forwards Init to its child, so the file read fires at startup unprompted.
	if minimal {
		s.currentPath = c.FilePath
		s.editor = c.OpenDoc(c.FilePath, s.editorOpts())
	} else {
		s.editor = components.NewEditorScreen(s.editorOpts())
	}
	s.editor.ShowSigns(s.gitGutter)
	// --preview needs a document to read, and ModeFile is the only launch that opens one
	// here — so a vault or scan launch never sets this, which is how the flag comes to be
	// silently ignored for every target that is not a single markdown file.
	s.launchPreview = c.Preview && minimal && s.previewable()
	s.editorPanel = components.NewScreenPanel(s.editor)
	s.previewPanel = components.NewScrollContainer("preview")
	// A bare "preview" on the edge, matching the two bordered list panels beside it —
	// the pane's keys are in the help bar (PanelHelp) where the rest of the screen's are.
	s.previewPanel.SetKeyHints(false)
	s.modular = s.buildModular()
	return s
}

// Init stashes Shared, boots the layout, and — for a --preview launch — seeds the editor
// with the file and puts the reader in the editor pane over it.
//
// The seeding is why the read happens HERE rather than being left to EditorScreen.Init:
// that read is asynchronous and comes back as a message the router hands to the top
// screen, which routes it to the pane's child — and this launch is about to make the
// READER that child, so the load would land nowhere, leaving an empty buffer aimed at the
// file, which the first save would then truncate. Seeding first means Init finds the
// buffer loaded and dispatches nothing, so there is no message to lose rather than one
// that is harmlessly dropped (seedForPreview does the same for a doc opened later).
//
// It also makes the reader and the buffer the same bytes by construction: the reader
// renders the live buffer, as every alt+p does, rather than a second independently-read
// copy of the file.
//
// The swap runs before modular.Init on purpose — a SetChild before the panel is
// initialized is silent, and the host's own Init starts the child that is there.
func (s *homeScreen) Init(sh *core.Shared) tea.Cmd {
	s.sh = sh
	if s.launchPreview {
		s.launchPreview = false
		s.editor.SetText(fileText(s.currentPath))
		s.fullPreview = s.previewScreen()
		s.modular = s.buildModular() // rebuilt so the bar names the way back to the editor
		_ = s.editorPanel.SetChild(s.fullPreview)
	}
	return s.modular.Init(sh)
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
		if core.MatchKey(k, flatKey) {
			s.setFlat(!s.flat)
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
			return s, s.toggleFullPreview()
		}
		// esc closes the reader, as it did back when the reader was a pushed screen and
		// esc popped it. Claimed HERE, ahead of the panes: the pane child would answer
		// esc with a Pop the router clamps away at the root, and a list's /-filter still
		// needs its own esc (Filtering).
		if s.fullPreview != nil && core.MatchKey(k, core.Keys.Back) && !s.modular.Filtering() {
			return s, s.closeFullPreview()
		}
		if core.MatchKey(k, wrapKey) {
			s.editor.ToggleWrap()
			return s, core.Action{}
		}
		if core.MatchKey(k, lineNumsKey) {
			s.editor.ToggleLineNums()
			return s, core.Action{}
		}
		if core.MatchKey(k, gutterKey) {
			s.setGitGutter(!s.gitGutter)
			return s, core.Action{}
		}
	}
	_, act := s.modular.Update(sh, msg)
	s.refreshPreview()
	s.syncPreviewScroll()
	// Batched into the cmd lane rather than folded in with core.Seq: Seq builds an
	// Action carrying only a control message, which would drop whatever cmd the panes
	// just returned (the editor's clipboard writes, a list's own async work).
	act.Cmd = tea.Batch(act.Cmd, s.refreshGutter())
	return s, act
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
	Of(sh).EachDoc(func(path string, ed *components.EditorScreen) {
		if ed != nil && ed != s.editor && ed.Dirty() {
			names = append(names, docName(path))
		}
	})
	return names
}

// chromeMask is gote's ONE chrome rule, shared by this screen and the full-screen reader
// pushed over it (previewDoc): minimal mode hides every persistent element, giving the
// body the whole terminal in steady state; an ordinary launch keeps the breadcrumb and
// the help bar. The router asks the top screen per render, so a pushed overlay is
// unaffected and no state is left to restore.
//
// Status is masked in BOTH modes — not because gote has no status line, but because the
// router draws it as a row taken off the body, which makes every pane jump when a
// clipboard result appears and jump back when it clears. The screens paint it themselves
// (View/HelpView, see status.go) in space the frame already spends.
//
// The reader shares this rather than tuning a mask of its own: chrome it showed or hid
// differently from the screen it was opened over would read as a different app.
func chromeMask(minimal bool) core.ChromeMask {
	if minimal {
		return core.FullscreenMask()
	}
	return core.ChromeMask{Status: true}
}

func (s *homeScreen) ChromeMask() core.ChromeMask { return chromeMask(s.minimal) }

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
		s.filePanel.Refresh()
		s.openPanel.SetItems(openDocItems(c, s.currentPath))
		return core.Action{}
	}
	if msg, ok := payload.(baselineMsg); ok {
		s.applyBaseline(msg)
		return core.Action{}
	}
	if msg, ok := payload.(SwitchVaultMsg); ok {
		return s.requestVaultSwitch(sh, msg.Name)
	}
	if _, ok := payload.(core.MsgThemeChanged); ok {
		core.StyleList(s.docsPanel.List())
		core.StyleList(s.filePanel.List())
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
	s.fullPreview = nil // the vault's scratch buffer is the editor, not a reader over it
	cmd := s.editorPanel.SetChild(s.editor)
	s.docsPanel.SetItems(docRows(c))
	// Rebuilt, not re-pointed: the new vault brings a new root as well as a new directory,
	// and the explorer's floor is fixed at construction.
	s.filePanel = components.NewFilePanel(s.filePanelOpts(c))
	s.openPanel.SetItems(nil)
	s.preview, s.previewPrior = previewOff, previewOff
	s.resetPreviewCache()
	s.minimal = false
	s.sidebar = true
	// The launch mode this screen was built for is gone; a vault is the full editor, so
	// the auto default has to be asked again rather than carrying ModeFile's answer over.
	s.setGitGutter(gutterDefault(c.Config, ModeVault))
	focus := s.rebuildModular(sh, 0)
	return core.Seq(core.Async(tea.Batch(cmd, focus)), core.ResetToRoot())
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

// setFlat swaps the docs slot between the flat scan list and the folder explorer. Like
// setSidebar it goes through rebuildModular, because which panel sits in the slot is a
// layout fact; both panels are kept, so each keeps its cursor across a round trip.
//
// Minimal mode returns here for setSidebar's reason: there is no sidebar to swap a panel
// into, and this is the only door the flag can change through.
func (s *homeScreen) setFlat(flat bool) {
	if s.minimal || flat == s.flat {
		return
	}
	s.flat = flat
	s.rebuildModular(s.sh, noFocus)
}

// docsPane is the panel currently filling the docs slot. The two views differ in what they
// list, not in what the screen asks of them: a footprint to lay out, and the row geometry
// the rename/new-file box anchors to.
type docsPane interface {
	components.Panel
	RowY(int) (int, bool)
	List() *list.Model
}

func (s *homeScreen) docsPane() docsPane {
	if s.flat {
		return s.docsPanel
	}
	return s.filePanel
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
//
// It returns the focused panel's on-focus cmd (components.FocusNotifier) for the caller
// to emit. Only for an explicit focus: the noFocus path is auto-focused by the new
// ModularScreen's constructor, which has no cmd lane, and re-Initing the rebuilt screen
// to drain one is exactly what this wrapper exists to avoid (it would re-read the
// editor's file over a dirty buffer). A panel that cares picks itself up on its next
// message instead.
func (s *homeScreen) rebuildModular(sh *core.Shared, focus int) tea.Cmd {
	s.modular.SetFocused(false)
	s.modular = s.buildModular()
	if s.w > 0 {
		s.modular.SetSize(sh, s.w, s.h)
	}
	if focus != noFocus {
		return s.modular.FocusSlot(focus)
	}
	return nil
}

// buildModular lays the current combination out: the sidebar column is optional
// (ctrl+b) and the preview column is optional (ctrl+p), so the grid is assembled
// rather than picked from a fixed set. The preview column is appended AFTER the
// editor, which is what keeps editorSlot's indexes valid whether or not it is up; the
// editor and preview both flex (ColWidths 0) and so split whatever the fixed sidebar
// leaves.
func (s *homeScreen) buildModular() *components.ModularScreen {
	opts := components.ModularOpts{
		// One entry, and it is the pointer at all the others: every app key gote has
		// is documented in the ? overlay (helpText), so the bar names the way in
		// rather than reprinting a handful of them beside the framework's own
		// pane/back/select hints. The keys themselves are untouched.
		Help: []key.Binding{helpKey},
	}
	if s.fullPreview != nil {
		// The one exception, and it earns the cell: a reader sitting where the editor was
		// has to say how to get the editor back. A ScreenPanel contributes no PanelHelp of
		// its own, so the reader's own hints never reach this bar.
		opts.Help = append([]key.Binding{fullPreviewKey}, opts.Help...)
	}
	var cols [][]components.Slot
	var widths []int
	if s.sidebar {
		cols = append(cols, []components.Slot{
			{Panel: s.docsPane(), Weight: 1},
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
