package app

import (
	"time"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/components/editor"
	"github.com/brohd11/bubblestack/core"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"go.lsp.dev/protocol"
)

// sidebarWidth is the fixed cell width of the docs column; the editor flexes.
const sidebarWidth = 30

// The home screen's own keys. Panel toggles carry a modifier, so they fire even while
// typing in the editor; "a" and "?" only when nothing captures text (alt+? works
// anywhere). The sidebar keys are alt+\ and alt+| because alt+b and alt+f are the
// editor's word motions as terminals send them.
var (
	bottomKey  = key.NewBinding(key.WithKeys("alt+\\"), key.WithHelp("alt+\\", "bottom panel"))
	sidebarKey = key.NewBinding(key.WithKeys("alt+|"), key.WithHelp("alt+|", "sidebar"))
	previewKey = key.NewBinding(key.WithKeys("ctrl+p"), key.WithHelp("ctrl+p", "preview"))
	// The reader has its own key rather than a third ctrl+p state: it is a mode you stay in.
	// It covers the editor, not the app. alt+p because terminals deliver ctrl+shift+p as "P".
	fullPreviewKey = key.NewBinding(key.WithKeys("alt+p"), key.WithHelp("alt+p", "full preview"))
	// alt+z, not ctrl+w: ctrl+w is the editor's own delete-word-back (and readline's),
	// and intercepting it here would swallow it before the editor ever sees it.
	wrapKey       = key.NewBinding(key.WithKeys("alt+z"), key.WithHelp("alt+z", "wrap"))
	lineNumsKey   = key.NewBinding(key.WithKeys("alt+l"), key.WithHelp("alt+l", "line nums"))
	completionKey = key.NewBinding(key.WithKeys("ctrl+space"), key.WithHelp("ctrl+space", "completion"))
	newBufferKey  = key.NewBinding(key.WithKeys("ctrl+n"), key.WithHelp("ctrl+n", "new unsaved file"))
	helpKey       = key.NewBinding(key.WithKeys("?", "alt+?"), key.WithHelp("?", "more"))
	// A docs-list key (ListPanelOpts.OnKey), acting on the selected row, never from the
	// editor.
	renameKey = key.NewBinding(key.WithKeys("ctrl+r"), key.WithHelp("ctrl+r", "rename"))
	// ctrl+d is also the editor's forward-delete, but docsKey only fires while the docs panel
	// is focused and not filtering.
	deleteKey = key.NewBinding(key.WithKeys("ctrl+d"), key.WithHelp("ctrl+d", "delete"))
	// Scoped to the Docs pane in every view, ahead of row dispatch so empty lists
	// and directory rows work too. The editor keeps alt+f for word movement.
	fileViewKey = key.NewBinding(key.WithKeys("alt+f"), key.WithHelp("alt+f", "flat/folder/grouped view"))
	// The folder view's density key (only while that panel is focused and not filtering);
	// alt+r because alt+d/f move by words.
	densityKey = key.NewBinding(key.WithKeys("alt+r"), key.WithHelp("alt+r", "row density"))
	descendKey = key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "enter selected folder (folder view)"))
	upKey      = key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "up a folder"))
	hiddenKey  = key.NewBinding(key.WithKeys("."), key.WithHelp(".", "show or hide dot files (folder view)"))
	// Language tools share Alt+Shift, keeping plain Alt chords available for
	// editing and navigation. Completion retains Ctrl+Space.
	definitionKey = altShiftKey("g", "go to definition")
	jumpBackKey   = altShiftKey("b", "jump back")
	hoverKey      = altShiftKey("h", "hover info")
	symbolsKey    = altShiftKey("o", "toggle outline")
	referencesKey = altShiftKey("r", "find references")
	formatKey     = altShiftKey("m", "format document")
)

// ctrl+p's preview modes: a side pane beside the editor, so it can be read against the
// source. The alt+p reader is separate (the editor pane's child) and never shown together
// with the side pane.
const (
	previewOff  = iota // editor only
	previewPane        // the custom reader, live, beside the editor
)

// ReseedMsg is gote's "reload the doc list" broadcast: the Actions ▸ Refresh row
// raises it, and the home screen reseeds and rebuilds its lists on receipt.
type ReseedMsg struct{}

// homeScreen is gote's root screen: a ModularScreen with a hideable Docs sidebar beside the editor. It rebuilds its inner ModularScreen on layout toggles but
// keeps the panels (so list state and buffers survive) and stays the same instance, so the
// router never re-Inits it over a dirty buffer.
type homeScreen struct {
	groupLayoutDirty     bool
	*editorGroup         // active group; document actions always read through this pointer
	buildingGroups       bool
	gitDocs              docsGit
	bottomVisible        bool
	header               *headerPanel // the layout's breadcrumb leaf; nil in minimal mode
	statusSpans          []statusSpan // the status bar's clickable items, as last rendered
	bottomFraction       float64
	bottom               *bottomDock
	diagnostics          *diagnosticsPanel
	search               *searchPanel
	searchGeneration     uint64
	searchCancel         func()
	searchQuery          string
	searchPath           string
	modular              *components.ModularScreen
	docsPanel            *components.CompactListPanel
	filePanel            *components.FilePanel // the folder view alt+f swaps into the docs slot
	groupedPanel         *components.TreePanel
	outlinePanel         *components.TreePanel
	panelSlots           map[components.Panel]int
	lastPane             components.Panel            // the non-editor pane esc hands the keys back to
	previewPanel         *components.ScrollContainer // the live preview pane
	sidebar              bool
	sidebarW             int                  // adjusted sidebar width; zero uses sidebarWidth
	sidebarSplits        map[string][]float64 // adjusted vertical shares, keyed by visible pane composition
	editorFlex           float64              // editor's share when the preview flex column is present; zero uses half
	fileView             fileView             // the view occupying the Docs slot
	showHidden           bool                 // session-only dot-file visibility in the folder view
	minimal              bool                 // ModeFile: chrome masked; outline may supply the only side column
	panelToggles         bool                 // the bottom panel and outline may be summoned (single_file_mode.allow_panel_toggle)
	indentGuides         bool                 // config-selected leading-indent visualization for every buffer
	gitDiff              *gitDiffUI           // anchored, scrollable diff; nil also cancels pending opens
	gutterDebounce       time.Duration        // internal test seam; production uses gitGutterDebounce
	launchPreview        bool                 // --preview: open the reader from Init, once
	preview              int                  // previewOff/previewPane
	previewSrc           string               // the buffer text the pane was last rendered from
	previewW             int                  // the width it was last rendered at (a resize must re-wrap)
	previewMap           []int                // that render's source line → pane row map (RenderMarkdownMapped)
	previewAt            int                  // the editor scroll offset the pane was last synced to; -1 re-syncs
	lspWaiting           bool                 // one blocking manager subscription is already in Bubble Tea
	semanticPath         string               // the path the last semantic-token fetch was issued for
	semanticSeq          int                  // and the edit generation it named, so one edit asks once
	semanticGen          int                  // debounce generation: a later edit supersedes a pending tick
	completion           completionUI         // parent-owned, input-transparent LSP completion popup
	hover                hoverUI              // the passive alt+shift+h / context-menu tooltip
	signature            signatureUI          // the passive parameter hint, above the caret
	lspRequestID         uint64               // the on-demand request whose answer this screen is waiting for
	outlineVisible       bool
	outlineNodes         []components.TreeNode
	outlineDataPath      string
	outlineDataSeq       int
	outlineRequestID     uint64
	outlineRequestedPath string
	outlineRequestedSeq  int
	outlineScheduledPath string
	outlineScheduledSeq  int
	outlineGeneration    int
	jumps                []jumpSite // alt+shift+b's back-stack of caret locations (navigate.go)
	pendingJump          *jumpSite  // a jump waiting on its destination buffer's file read
	pendingRange         *protocol.Range
	sh                   *core.Shared // stashed by Init/SetSize for rebuilds and the crumb
	w, h                 int
}

var _ core.Screen = (*homeScreen)(nil)
var _ core.Filterer = (*homeScreen)(nil)
var _ core.Receiver = (*homeScreen)(nil)
var _ core.Crumber = (*homeScreen)(nil)
var _ core.ChromeMasker = (*homeScreen)(nil)
var _ core.QuitGater = (*homeScreen)(nil)

// sidebarSelection is how every sidebar list marks its row: a plain-text bar instead of the
// accent border, and nothing while the pane is unfocused — while typing in the editor the
// sidebar cursor tells the reader nothing.
var sidebarSelection = core.SelectionOpts{Style: core.SelectBackground, NoAccent: true, HideUnfocused: true}

// NewHomeScreen builds the root screen: the docs list, document tabs, and a scratch
// buffer. ModeFile builds the same screen minimally (sidebar unreachable, chrome masked,
// the given file in the editor), reusing the editor pane's wiring.
func NewHomeScreen(sh *core.Shared) core.Screen {
	c := Of(sh)
	minimal := c.Mode == ModeFile
	// Which view the sidebar opens on is the config's; alt+f moves it from
	// there and nothing writes the choice back.
	s := &homeScreen{sh: sh, editorGroup: &editorGroup{weight: 1}, sidebar: !minimal, minimal: minimal, fileView: c.Config.startFileView(),
		sidebarSplits: make(map[string][]float64), outlineDataSeq: -1, outlineScheduledSeq: -1,
		indentGuides: c.Config.IndentGuides,
	}
	s.gitGutter = gutterDefault(c.Config, c.Mode)
	s.diagnosticsGutter = c.lsp != nil && c.Config.modeDefaults(c.Mode).DiagnosticsGutter
	s.panelToggles = !minimal || c.Config.SingleFile.AllowPanelToggle
	s.gutterDebounce = gitGutterDebounce
	c.groups, c.activeGroup = []*editorGroup{s.editorGroup}, s.editorGroup

	// The Docs list is bordered so the focused pane is visible. No Help: the
	// ? overlay documents rename; OnKey still fires it.
	s.docsPanel = components.NewCompactListPanel(s.docRows(c), "Docs", components.ListPanelOpts{
		OnSelect:  s.pickDoc,
		OnKey:     s.docsKey,
		Border:    true,
		Selection: sidebarSelection,
	})
	s.outlinePanel = s.newOutlinePanel()
	s.openTabs = &documentTabBar{TabBar: components.NewTabBar()}
	// Built eagerly: a layout rebuild does not Init its panels, so a deferred first read would
	// swap in empty.
	s.filePanel = components.NewFilePanel(s.filePanelOpts(c))
	s.groupedPanel = s.newGroupedPanel(c)
	// The minimal editor goes through the ctx like any doc, so it is registered and rekeyed
	// normally; ScreenPanel.Init starts the file read.
	if minimal {
		s.currentID = c.FilePath
		s.currentPath = c.FilePath
		s.currentName = docName(c.FilePath)
		s.editor = c.OpenDoc(c.FilePath, s.editorOpts(c))
	} else if !s.restoreSession(c) {
		s.installScratch(c)
	}
	c.SetActive(s.currentID)
	s.configureSignColumns()
	// --preview needs a single markdown document, which only ModeFile opens; other launches
	// ignore it.
	s.launchPreview = c.Preview && minimal && s.previewable()
	s.initGroupPanels(c)
	s.previewPanel = components.NewScrollContainer("preview")
	// A bare "preview" on the edge, matching the bordered Docs panel beside it —
	// the pane's keys are in the help bar (PanelHelp) where the rest of the screen's are.
	s.previewPanel.SetKeyHints(false)
	// Wired once; the hooks are rebuilt per click because the directory a relative link
	// resolves against moves with the open document.
	s.previewPanel.OnLink = func(sh *core.Shared, l components.Link) core.Action {
		return s.previewLinks().Do(sh, l)
	}
	s.diagnostics = newDiagnosticsPanel(s.activateDiagnostic)
	s.search = newSearchPanel(s.activateSearchResult)
	// Titleless and unlit: the dock's top row is a rule the panes above join
	// (joinDockRule), and its tabs and focus show in the status bar.
	for _, p := range []*components.ScrollContainer{s.diagnostics.ScrollContainer, s.search.ScrollContainer} {
		p.SetFrame(components.SideFrame{Top: components.TopBox, Bottom: true})
	}
	s.bottom = newBottomDock(s.diagnostics, s.search)
	s.modular = s.buildModular()
	return s
}

// Init stashes Shared, builds the layout and, for a --preview launch, seeds the editor
// with the file and puts the reader over it. The read happens here because the editor's
// async load would go to the reader and be lost, leaving an empty buffer (seedForPreview
// covers later opens). The swap precedes modular.Init, which then starts the new child.
func (s *homeScreen) Init(sh *core.Shared) (initCmd tea.Cmd) {
	defer func() { initCmd = tea.Batch(initCmd, s.syncDocsGit()) }()
	s.sh = sh
	if s.launchPreview {
		s.launchPreview = false
		_ = s.editor.LoadFile()
		s.fullPreview = s.previewScreen()
		s.modular = s.buildModular() // rebuilt so the bar names the way back to the editor
		_ = s.editorPanel.SetChild(s.fullPreview)
	}
	c := Of(sh)
	if c.lsp == nil {
		return s.modular.Init(sh)
	}
	if !c.lsp.Reconcile(c) {
		return s.modular.Init(sh)
	}
	s.lspWaiting = true
	return tea.Batch(s.modular.Init(sh), c.lsp.WaitCmd())
}

// notePane records the focused pane before each message, so esc in the editor can return
// the keys there (editorRelease). Before, because by the time focus reaches the editor the
// previous pane is already blurred; every route into the editor passes through here.
func (s *homeScreen) notePane() {
	if pane := s.focusedPane(); !s.isEditorPanel(pane) {
		s.lastPane = pane
	}
}

// paneFiltering reports whether the focused list has a typed or applied /-filter.
// ModularScreen.Filtering covers only typed ones, and esc must clear an applied filter
// first.
func (s *homeScreen) paneFiltering() bool {
	if l, ok := s.focusedPane().(interface{ List() *list.Model }); ok {
		return l.List().FilterState() != list.Unfiltered
	}
	return false
}

// chromeKey resolves the home screen's own layout and chrome keys. Each one closes the
// completion popup first; a key disabled by the launch mode is still consumed.
func (s *homeScreen) chromeKey(sh *core.Shared, k string) func() core.Action {
	none := func() core.Action { return core.Action{} }
	switch {
	case core.MatchKey(k, moveTabLeftKey):
		return func() core.Action { return s.moveTab(sh, -1) }
	case core.MatchKey(k, moveTabRightKey):
		return func() core.Action { return s.moveTab(sh, 1) }
	case core.MatchKey(k, findFilesKey):
		// Locked with the panels: a result forces the bottom panel open.
		if !s.panelToggles {
			return none
		}
		return func() core.Action { return core.Push(s.findFilesForm(sh)) }
	case core.MatchKey(k, newBufferKey):
		if s.minimal {
			return none
		}
		return func() core.Action { return s.finishHomeUpdate(sh, s.newUnsavedBuffer(sh)) }
	case core.MatchKey(k, sidebarKey):
		return func() core.Action { s.setSidebar(!s.sidebar); return core.Action{} }
	// ctrl+alt+a and alt+? bypass the capture gate: the editor pane always reports
	// Filtering, so bare "a" and "?" are text whenever it has the keys.
	case core.MatchKey(k, core.Keys.Actions) && (!s.modular.Filtering() || k == "ctrl+alt+a"):
		return func() core.Action { return core.Push(s.actionsMenu(sh)) }
	case core.MatchKey(k, helpKey) && (!s.modular.Filtering() || k == "alt+?"):
		return func() core.Action { return core.Push(s.helpScreen()) }
	case core.MatchKey(k, previewKey):
		return s.cyclePreview
	case core.MatchKey(k, fullPreviewKey):
		return s.toggleFullPreview
	// esc closes the reader ahead of the panes, whose Pop the router would clamp away at
	// the root. A list's /-filter keeps its own esc.
	case s.fullPreview != nil && core.MatchKey(k, core.Keys.Back) && !s.modular.Filtering():
		return s.closeFullPreview
	case core.MatchKey(k, wrapKey):
		return func() core.Action { s.editor.ToggleWrap(); return core.Action{} }
	case core.MatchKey(k, lineNumsKey):
		return func() core.Action { s.editor.ToggleLineNums(); return core.Action{} }
	}
	return nil
}

// Update handles the screen's own keys, then delegates to the inner ModularScreen. It
// always returns the same homeScreen.
func (s *homeScreen) Update(sh *core.Shared, msg tea.Msg) (next core.Screen, result core.Action) {
	defer func() { result.Cmd = tea.Batch(result.Cmd, s.syncDocsGit()) }()
	defer func() {
		if result.Msg != nil {
			s.closeGitDiff()
		}
		s.dismissInvalidGitDiff()
	}()
	if act, handled := s.gitDiffInput(sh, msg); handled {
		return s, act
	}
	s.notePane()
	if act, handled := s.statusBarInput(sh, msg); handled {
		return s, act
	}
	if act, handled := s.documentTabInput(sh, msg); handled {
		return s, s.finishHomeUpdate(sh, act)
	}
	if tick, ok := msg.(semanticTick); ok {
		s.handleSemanticTick(sh, tick)
		return s, core.Action{}
	}
	if tick, ok := msg.(outlineTick); ok {
		s.handleOutlineTick(sh, tick)
		return s, s.finishHomeUpdate(sh, core.Action{})
	}
	if tick, ok := msg.(completionTick); ok {
		act, _ := s.handleCompletionTick(sh, tick)
		return s, s.finishHomeUpdate(sh, act)
	}
	msg, wantsDefinition := s.retargetClick(sh, msg)
	// Ahead of the completion popup's own handling, not after it: the tooltip claims no
	// input, so whatever consumes this message must not also decide whether it survives.
	s.dismissHoverOn(msg)
	beforeCompletion := s.completionSnapshot()
	if s.completion.popup != nil {
		if act, handled := s.completion.popup.Update(sh, msg); handled {
			return s, s.finishHomeUpdate(sh, act)
		}
	}
	if km, ok := msg.(tea.KeyPressMsg); ok {
		k := km.String()
		if !s.minimal && s.sidebar && s.focusedPane() == s.docsPane() &&
			!s.modular.Filtering() && !s.modular.Resizing() && core.MatchKey(k, fileViewKey) {
			s.setFileView((s.fileView + 1) % fileViewCount)
			return s, core.Action{}
		}
		if s.sidebar && s.fileView == fileViewFolder && s.filePanel.Focused() &&
			!s.modular.Filtering() && !s.modular.Resizing() {
			switch {
			case core.MatchKey(k, descendKey):
				return s, s.descendFolder(sh)
			case core.MatchKey(k, hiddenKey):
				return s, s.toggleHidden()
			}
		}
		if core.MatchKey(k, bottomKey) {
			if !s.panelToggles {
				return s, core.Action{}
			}
			return s, s.toggleBottom(sh)
		}
		if s.bottomVisible && s.bottom.Focused() && !s.modular.Resizing() && core.MatchKey(k, core.Keys.Back) {
			return s, core.Async(s.modular.FocusSlot(s.editorSlot()))
		}
		if core.MatchKey(k, completionKey) {
			return s, s.finishHomeUpdate(sh, s.requestCompletion(sh, "", true))
		}
		if run := s.chromeKey(sh, k); run != nil {
			s.closeCompletion()
			return s, run()
		}
		// The return leg of editorRelease: esc (not Keys.Back — backspace and c belong to
		// the panes) hands the keys back to the editor, unless a list has a live filter.
		if k == "esc" && !s.editorPanel.Focused() && !s.modular.Filtering() &&
			!s.modular.Resizing() && !s.paneFiltering() {
			return s, core.Async(s.modular.FocusSlot(s.editorSlot()))
		}
		if act, handled := s.languageServerKey(sh, k); handled {
			return s, s.finishHomeUpdate(sh, act)
		}
	}
	_, act := s.modular.Update(sh, msg)
	s.promoteEditedScratch(sh)
	if act.Msg != nil {
		s.closeCompletion()
	} else {
		act.Cmd = tea.Batch(act.Cmd,
			s.updateCompletionAfterParent(sh, msg, beforeCompletion),
			s.updateSignatureAfterParent(sh, msg, beforeCompletion))
	}
	if wantsDefinition && s.editorPanel.Focused() {
		// The click already moved the caret and focused its pane, so a focused editor means the
		// click hit text.
		act = core.Seq(act, s.requestAt(sh, lspReqDefinition))
	}
	return s, s.finishHomeUpdate(sh, act)
}

// languageServerKey handles the caret LSP chords, matched above the panes so they work
// while typing.
func (s *homeScreen) languageServerKey(sh *core.Shared, k string) (core.Action, bool) {
	switch {
	case core.MatchKey(k, definitionKey):
		s.closeCompletion()
		return s.requestAt(sh, lspReqDefinition), true
	case core.MatchKey(k, hoverKey):
		s.closeCompletion()
		return s.requestAt(sh, lspReqHover), true
	case core.MatchKey(k, symbolsKey):
		s.closeCompletion()
		if !s.panelToggles {
			// Consume the shortcut even when the launch mode locks the outline away.
			return core.Action{}, true
		}
		return s.toggleOutline(sh), true
	case core.MatchKey(k, referencesKey):
		s.closeCompletion()
		return s.requestAt(sh, lspReqReferences), true
	case core.MatchKey(k, formatKey):
		s.closeCompletion()
		return s.requestAt(sh, lspReqFormat), true
	case core.MatchKey(k, jumpBackKey):
		s.closeCompletion()
		return s.jumpBack(sh), true
	}
	return core.Action{}, false
}

// retargetClick applies the two modifier-click gestures by rewriting the message. The
// context gesture becomes a right press (for terminals that keep the right button). The
// definition gesture passes through as a caret click; the caller acts on the caret after.
func (s *homeScreen) retargetClick(sh *core.Shared, msg tea.Msg) (tea.Msg, bool) {
	click, ok := msg.(tea.MouseClickMsg)
	if !ok || click.Button != tea.MouseLeft || sh == nil {
		return msg, false
	}
	cfg := Of(sh).Config
	switch {
	case clickModifierMatches(cfg.ClickContext, click.Mod):
		return tea.MouseClickMsg{X: click.X, Y: click.Y, Button: tea.MouseRight}, false
	case clickModifierMatches(cfg.ClickDefinition, click.Mod):
		return msg, true
	}
	return msg, false
}

// clickModifierMatches reads a configured click modifier; empty or unknown values mean
// "none", so a typo disables rather than rebinds.
func clickModifierMatches(setting string, mod tea.KeyMod) bool {
	switch setting {
	case clickAlt:
		return mod.Contains(tea.ModAlt)
	case clickCtrl:
		return mod.Contains(tea.ModCtrl)
	case clickShift:
		return mod.Contains(tea.ModShift)
	}
	return false
}

func (s *homeScreen) finishHomeUpdate(sh *core.Shared, act core.Action) core.Action {
	if s.groupLayoutDirty {
		s.groupLayoutDirty = false
		act.Cmd = tea.Batch(act.Cmd, s.rebuildGroups(sh))
	}
	act.Cmd = tea.Batch(act.Cmd, s.refreshVisibleGroups(sh))
	defer s.refreshDiagnostics()
	s.applyPendingJump()
	// Alongside the jump and for the same reason: both are a caret aimed at a buffer
	// whose file may still be loading, and both get another try on the next message.
	s.applyRestore(Of(sh))
	// Judge the signature hint after any jump, on the one exit every caret-moving path shares.
	s.dismissSignatureIfLeft()
	s.refreshPreview()
	s.syncPreviewScroll()
	s.syncOutlineCaret()
	// Batch into the cmd lane: core.Seq would drop the panes' cmds.
	act.Cmd = tea.Batch(act.Cmd, s.refreshGutter())
	if c := Of(sh); c.lsp != nil {
		if c.lsp.Reconcile(c) && !s.lspWaiting {
			s.lspWaiting = true
			act.Cmd = tea.Batch(act.Cmd, c.lsp.WaitCmd())
		}
		act.Cmd = tea.Batch(act.Cmd, s.scheduleSemanticTokens())
		act.Cmd = tea.Batch(act.Cmd, s.scheduleOutline())
	}
	return act
}

// View and HelpView draw the status line themselves (status.go), since the router's is
// masked. HelpView is only the status row: the key hints live in the ? overlay. In
// minimal mode the body's last row takes the status.
func (s *homeScreen) View(sh *core.Shared) string {
	s.refreshOpenTabs(sh)
	body := s.joinDockRule(sh, s.joinHeaderRule(s.modular.View(sh)))
	if s.minimal {
		body = statusOver(sh, body, s.h)
	}
	body = s.viewCompletion(sh, body)
	body = s.viewSignature(sh, body)
	body = s.viewHover(sh, body)
	return s.viewGitDiff(sh, body)
}

func (s *homeScreen) HelpView(sh *core.Shared) string {
	return s.statusBar(sh)
}

func (s *homeScreen) SetSize(sh *core.Shared, width, bodyHeight int) {
	s.sh = sh
	resized := width != s.w || bodyHeight != s.h
	s.w, s.h = width, bodyHeight
	s.modular.SetSize(sh, width, bodyHeight)
	s.refreshDiagnostics()
	s.refreshPreview() // the pane's new width re-wraps the render
	if resized {
		s.closeGitDiff()
		// A height-only resize moves both viewports, so re-sync once, but only on a real resize:
		// the router re-lays out every message and would undo a hand-scrolled pane.
		s.previewAt = -1
		s.syncPreviewScroll()
	}
}

// Filtering proxies the modular screen's capture state: the router must leave global
// single-key shortcuts alone while the editor types or a list filters.
func (s *homeScreen) Filtering() bool { return s.modular.Filtering() }

// chromeMask is gote's chrome rule: minimal mode hides everything; otherwise only the
// help row stays. Status is masked in both, and drawn by the screen (see status.go), so a
// message never changes the body's height. The breadcrumb is the layout's header leaf
// (see header.go), so its rule can join the pane dividers.
func chromeMask(minimal bool) core.ChromeMask {
	if minimal {
		return core.FullscreenMask()
	}
	return core.ChromeMask{Status: true, Breadcrumb: true}
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

// Receive handles broadcasts: ReseedMsg reloads the lists, and a theme change restyles the
// live lists in place. It does not use core.OnThemeChange, which would rebuild the root
// and lose the editor state.
func (s *homeScreen) Receive(sh *core.Shared, payload any) (result core.Action) {
	defer func() { result.Cmd = tea.Batch(result.Cmd, s.syncDocsGit()) }()
	defer func() {
		if result.Msg != nil {
			s.closeGitDiff()
		}
		s.dismissInvalidGitDiff()
	}()
	if act, handled := s.receiveDocsGit(payload); handled {
		return act
	}
	// Focus and blur pace the sidebar's git poll but are not consumed: blur also resets
	// mouse gestures below. Regaining focus is when an external commit most likely landed.
	switch payload.(type) {
	case ReseedMsg, tea.FocusMsg:
		defer func() { result.Cmd = tea.Batch(result.Cmd, s.refreshDocsGit()) }()
	case tea.BlurMsg:
		s.closeGitDiff()
		s.idleDocsGit()
	}
	defer s.refreshDiagnostics()

	switch msg := payload.(type) {
	case editor.DiskStateChangedMsg:
		if msg.Reloaded {
			Of(sh).open.each(func(entry *openEntry) {
				if entry.editor == msg.Editor {
					forgetSniffedLanguage(entry.path)
				}
			})
			for _, g := range s.groups() {
				if g.editor == msg.Editor && g.fullPreview != nil {
					g.fullPreview.Refresh()
				}
			}
			if msg.Editor == s.editor {
				s.closeCompletion()
				s.closeSignature()
				s.closeGitDiff()
				if s.fullPreview != nil {
					s.fullPreview.Refresh()
				}
			}
		}
		return s.finishHomeUpdate(sh, core.Action{})
	case findFilesRequest:
		return s.beginFindFiles(sh, msg)
	case findFilesResult:
		s.finishFindFiles(msg)
		return core.Action{}
	case lspEvent:
		return s.applyLSPEvent(sh, msg)
	case ReseedMsg:
		return s.reseed(sh)
	case baselineMsg:
		for _, g := range s.groups() {
			s.withGroup(g, func() tea.Cmd { s.applyBaseline(msg); return nil })
		}
		return core.Action{}
	case gitDiffBaselineMsg:
		return s.applyGitDiffBaseline(sh, msg)
	case gutterRefreshMsg:
		for _, g := range s.groups() {
			s.withGroup(g, func() tea.Cmd { s.applyGutterRefresh(msg); return nil })
		}
		return core.Action{}
	case SwitchVaultMsg:
		return s.requestVaultSwitch(sh, msg.Name)
	case core.MsgThemeChanged:
		core.StyleList(s.docsPanel.List())
		core.StyleList(s.filePanel.List())
		core.StyleList(s.groupedPanel.List())
		core.StyleList(s.outlinePanel.List())
		for _, g := range s.groups() {
			s.withGroup(g, func() tea.Cmd { s.refreshDiagnosticSigns(); return nil })
		}
		s.diagnostics.paint()
		s.search.paint()
	}
	return s.modular.Receive(sh, payload)
}

func (s *homeScreen) applyLSPEvent(sh *core.Shared, event lspEvent) core.Action {
	if event.completion != nil {
		s.applyCompletionResult(event.completion)
	}
	act := core.Action{}
	if event.request != nil {
		act = s.applyRequestResult(sh, event.request)
	}
	if event.semantic != nil {
		s.applySemanticTokens(sh, event.semantic)
	}
	if event.outline != nil {
		s.applyOutlineResult(event.outline)
	}
	for _, g := range s.groups() {
		s.withGroup(g, func() tea.Cmd { s.refreshDiagnosticSigns(); return nil })
	}
	s.lspWaiting = true
	wait := core.Async(tea.Batch(Of(sh).lsp.WaitCmd(), s.retryOutlineAfterLSP(sh)))
	if event.status != "" {
		return core.Seq(act, core.SetStatusAndLog(event.status), wait)
	}
	return core.Seq(act, wait)
}

// reseed rescans the documents and rebuilds every list from them.
func (s *homeScreen) reseed(sh *core.Shared) core.Action {
	groupCmd := s.reconcileGroups(sh)
	c := Of(sh)
	c.Seed()
	s.docsPanel.SetItems(s.docRows(c))
	s.groupedPanel.SetNodes(s.groupedDocNodes(c))
	s.filePanel.Refresh()
	if c.lsp != nil {
		active := c.lsp.Reconcile(c)
		s.refreshDiagnosticSigns()
		if active && !s.lspWaiting {
			s.lspWaiting = true
			return core.Async(tea.Batch(groupCmd, c.lsp.WaitCmd()))
		}
	}
	return core.Async(groupCmd)
}
