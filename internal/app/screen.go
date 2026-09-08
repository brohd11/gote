package app

import (
	"os"
	"strings"
	"time"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/components/editor"
	"github.com/brohd11/bubblestack/core"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"go.lsp.dev/protocol"
)

// sidebarWidth is the fixed cell width of the docs/open column; the editor flexes.
const sidebarWidth = 30

// The home screen's own keys. The panel toggles carry a modifier, so they pass the
// router's capture gate and fire even while typing in the editor; "a" and "?" are
// intercepted only when nothing is capturing, so typed text and /-filters never
// lose the letter (alt+? is the modified alias that summons help from anywhere,
// the editor included).
//
// alt+\ and alt+|, not alt+b/ctrl+b: a terminal sends ESC-prefixed letters for the
// editor's word motions (alt+b IS alt+left, alt+f IS alt+right), so claiming either
// letter here breaks word nav in every buffer — which is exactly what alt+b did until
// this pair took over. The punctuation keys are on no readline motion, sit next to each
// other on one physical key, and read as the two panel edges they toggle. Freeing
// ctrl+b also returns character-backward to the line edits and form fields, which this
// screen used to steal from them through the modifier bypass.
var (
	bottomKey  = key.NewBinding(key.WithKeys("alt+\\"), key.WithHelp("alt+\\", "bottom panel"))
	sidebarKey = key.NewBinding(key.WithKeys("alt+|"), key.WithHelp("alt+|", "sidebar"))
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
	wrapKey       = key.NewBinding(key.WithKeys("alt+z"), key.WithHelp("alt+z", "wrap"))
	lineNumsKey   = key.NewBinding(key.WithKeys("ctrl+l"), key.WithHelp("ctrl+l", "line nums"))
	completionKey = key.NewBinding(key.WithKeys("ctrl+space"), key.WithHelp("ctrl+space", "completion"))
	newBufferKey  = key.NewBinding(key.WithKeys("ctrl+n"), key.WithHelp("ctrl+n", "new unsaved file"))
	helpKey       = key.NewBinding(key.WithKeys("?", "alt+?"), key.WithHelp("?", "more"))
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
	descendKey = key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "enter selected folder (folder view)"))
	upKey      = key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "up a folder"))
	// The language-server keys. All carry a modifier, so they pass the router's capture
	// gate and fire while the editor is typing — which is the only place they mean
	// anything. alt+g/h/o/n/m are the free alt letters left after the editor's word and
	// clipboard chords (alt+b/c/d/f/i/v/x), core's alt+wasd arrows and alt+u, and gote's
	// own alt+p/r/t/z/\/|. ctrl+o remains vim's jump-back; alt+o now toggles the persistent
	// outline panel rather than pushing a picker.
	definitionKey = key.NewBinding(key.WithKeys("alt+g"), key.WithHelp("alt+g", "go to definition"))
	jumpBackKey   = key.NewBinding(key.WithKeys("ctrl+o"), key.WithHelp("ctrl+o", "jump back"))
	hoverKey      = key.NewBinding(key.WithKeys("alt+h"), key.WithHelp("alt+h", "hover info"))
	symbolsKey    = key.NewBinding(key.WithKeys("alt+o"), key.WithHelp("alt+o", "toggle outline"))
	referencesKey = key.NewBinding(key.WithKeys("alt+n"), key.WithHelp("alt+n", "find references"))
	formatKey     = key.NewBinding(key.WithKeys("alt+m"), key.WithHelp("alt+m", "format document"))
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
	gitDocs              docsGit
	bottomVisible        bool
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
	filePanel            *components.FilePanel // the folder view alt+t swaps into the docs slot
	openPanel            *components.CompactListPanel
	outlinePanel         *components.TreePanel
	openTabs             *documentTabBar
	openDocsTabs         bool
	panelSlots           map[components.Panel]int
	editorPanel          *components.ScreenPanel
	previewPanel         *components.ScrollContainer // the live preview pane
	editor               *editor.Screen              // the editor pane's live buffer (ScreenPanel exposes none)
	fullPreview          *components.DocScreen       // alt+p: the reader IN the editor pane; nil = the editor is
	currentID            string                      // stable buffer identity; path for saved docs, opaque for unsaved
	currentPath          string                      // filesystem path; empty while the current buffer is unsaved
	currentName          string                      // visible filename or unsaved_N label
	sidebar              bool
	sidebarW             int                  // adjusted sidebar width; zero uses sidebarWidth
	sidebarSplits        map[string][]float64 // adjusted vertical shares, keyed by visible pane composition
	editorFlex           float64              // editor's share when the preview flex column is present; zero uses half
	flat                 bool                 // the docs slot shows the flat scan (true) or the folder explorer
	minimal              bool                 // ModeFile: chrome masked; outline may supply the only side column
	indentGuides         bool                 // config-selected leading-indent visualization for every buffer
	gitGutter            bool                 // draw change markers against HEAD (see gitgutter.go)
	diagnosticsGutter    bool                 // independently toggle the LSP marker column
	gutter               gutter               // the baseline and last-drawn markers behind them
	gutterDebounce       time.Duration        // internal test seam; production uses gitGutterDebounce
	launchPreview        bool                 // --preview: open the reader from Init, once
	preview              int                  // previewOff/previewPane
	previewPrior         int                  // the ctrl+p mode alt+p folded away, restored when the reader closes
	previewSrc           string               // the buffer text the pane was last rendered from
	previewW             int                  // the width it was last rendered at (a resize must re-wrap)
	previewMap           []int                // that render's source line → pane row map (RenderMarkdownMapped)
	previewAt            int                  // the editor scroll offset the pane was last synced to; -1 re-syncs
	lspWaiting           bool                 // one blocking manager subscription is already in Bubble Tea
	semanticPath         string               // the path the last semantic-token fetch was issued for
	semanticSeq          int                  // and the edit generation it named, so one edit asks once
	semanticGen          int                  // debounce generation: a later edit supersedes a pending tick
	completion           completionUI         // parent-owned, input-transparent LSP completion popup
	hover                hoverUI              // the passive alt+h / context-menu tooltip
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
	jumps                []jumpSite // ctrl+o's back-stack of caret locations (navigate.go)
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
		openDocsTabs:  c.Config.OpenDocsView == "tabs",
		sidebarSplits: make(map[string][]float64), outlineDataSeq: -1, outlineScheduledSeq: -1,
		indentGuides: c.Config.IndentGuides,
		gitGutter:    gutterDefault(c.Config, c.Mode), diagnosticsGutter: c.lsp != nil,
		gutterDebounce: gitGutterDebounce}
	// Border on both sidebar lists: with three panes on screen the focused one has
	// to be visible, and the editor pane is framed automatically (ScreenPanel borders
	// a core.Borderer child).
	// No Help: that field only feeds the bar, and rename is documented in the ? overlay
	// with the rest. The binding stays live — OnKey (docsKey) is what fires it.
	s.docsPanel = components.NewCompactListPanel(s.docRows(c), "Docs", components.ListPanelOpts{
		OnSelect: s.pickDoc,
		OnKey:    s.docsKey,
		Border:   true,
	})
	s.openPanel = components.NewCompactListPanel(nil, "Open", components.ListPanelOpts{
		OnSelect: s.pickDoc,
		Border:   true,
	})
	s.outlinePanel = s.newOutlinePanel()
	s.openTabs = &documentTabBar{TabBar: components.NewTabBar()}
	// Built alongside the flat list rather than on first use: both panels outlive the
	// ModularScreen that holds them, and a layout rebuild does not Init what it builds
	// (see rebuildModular), so a panel that deferred its first read would swap in empty.
	s.filePanel = components.NewFilePanel(s.filePanelOpts(c))
	// The minimal editor goes through the ctx like any picked doc, so the buffer is
	// registered under its path and a save-as rekeys it the same way. ScreenPanel.Init
	// forwards Init to its child, so the file read fires at startup unprompted.
	if minimal {
		s.currentID = c.FilePath
		s.currentPath = c.FilePath
		s.currentName = docName(c.FilePath)
		s.editor = c.OpenDoc(c.FilePath, s.editorOpts())
	} else {
		s.installScratch(c)
	}
	s.configureSignColumns()
	// --preview needs a document to read, and ModeFile is the only launch that opens one
	// here — so a vault or scan launch never sets this, which is how the flag comes to be
	// silently ignored for every target that is not a single markdown file.
	s.launchPreview = c.Preview && minimal && s.previewable()
	s.editorPanel = components.NewScreenPanel(s.editor)
	s.previewPanel = components.NewScrollContainer("preview")
	// A bare "preview" on the edge, matching the two bordered list panels beside it —
	// the pane's keys are in the help bar (PanelHelp) where the rest of the screen's are.
	s.previewPanel.SetKeyHints(false)
	// Wired once; the hooks are rebuilt per click because the directory a relative link
	// resolves against moves with the open document.
	s.previewPanel.OnLink = func(sh *core.Shared, l components.Link) core.Action {
		return s.previewLinks().Do(sh, l)
	}
	s.diagnostics = newDiagnosticsPanel(s.activateDiagnostic)
	s.search = newSearchPanel(s.activateSearchResult)
	s.bottom = newBottomDock(s.diagnostics, s.search)
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
func (s *homeScreen) Init(sh *core.Shared) (initCmd tea.Cmd) {
	defer func() { initCmd = tea.Batch(initCmd, s.syncDocsGit()) }()
	s.sh = sh
	if s.launchPreview {
		s.launchPreview = false
		s.editor.SetText(fileText(s.currentPath))
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
func (s *homeScreen) Update(sh *core.Shared, msg tea.Msg) (next core.Screen, result core.Action) {
	defer func() { result.Cmd = tea.Batch(result.Cmd, s.syncDocsGit()) }()
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
		if core.MatchKey(k, descendKey) && s.sidebar && !s.flat && s.filePanel.Focused() &&
			!s.modular.Filtering() && !s.modular.Resizing() {
			return s, s.descendFolder(sh)
		}
		if core.MatchKey(k, bottomKey) {
			return s, s.toggleBottom(sh)
		}
		if core.MatchKey(k, findFilesKey) {
			s.closeCompletion()
			return s, core.Push(s.findFilesForm(sh))
		}
		if s.bottomVisible && s.bottom.Focused() && !s.modular.Resizing() && core.MatchKey(k, core.Keys.Back) {
			return s, core.Async(s.modular.FocusSlot(s.editorSlot()))
		}
		if core.MatchKey(k, newBufferKey) {
			if s.minimal {
				return s, core.Action{}
			}
			s.closeCompletion()
			return s, s.finishHomeUpdate(sh, s.newUnsavedBuffer(sh))
		}
		if core.MatchKey(k, completionKey) {
			return s, s.finishHomeUpdate(sh, s.requestCompletion(sh, "", true))
		}
		if core.MatchKey(k, sidebarKey) {
			s.closeCompletion()
			s.setSidebar(!s.sidebar)
			return s, core.Action{}
		}
		if core.MatchKey(k, flatKey) {
			s.closeCompletion()
			s.setFlat(!s.flat)
			return s, core.Action{}
		}
		if core.MatchKey(k, actionsKey) && !s.modular.Filtering() {
			s.closeCompletion()
			return s, core.Push(s.actionsMenu(sh))
		}
		if core.MatchKey(k, helpKey) && (!s.modular.Filtering() || k == "alt+?") {
			s.closeCompletion()
			return s, core.Push(s.helpScreen())
		}
		if core.MatchKey(k, previewKey) {
			s.closeCompletion()
			return s, s.cyclePreview()
		}
		if core.MatchKey(k, fullPreviewKey) {
			s.closeCompletion()
			return s, s.toggleFullPreview()
		}
		// esc closes the reader, as it did back when the reader was a pushed screen and
		// esc popped it. Claimed HERE, ahead of the panes: the pane child would answer
		// esc with a Pop the router clamps away at the root, and a list's /-filter still
		// needs its own esc (Filtering).
		if s.fullPreview != nil && core.MatchKey(k, core.Keys.Back) && !s.modular.Filtering() {
			s.closeCompletion()
			return s, s.closeFullPreview()
		}
		if core.MatchKey(k, wrapKey) {
			s.closeCompletion()
			s.editor.ToggleWrap()
			return s, core.Action{}
		}
		if core.MatchKey(k, lineNumsKey) {
			s.closeCompletion()
			s.editor.ToggleLineNums()
			return s, core.Action{}
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
		// The click has already moved the caret through the editor's ordinary press
		// handling, and ModularScreen focuses whatever slot a press landed in — so a
		// focused editor pane IS the test that this click hit the text, without gote
		// needing slot rectangles or the editor's unexported cell-to-position math.
		act = core.Seq(act, s.requestAt(sh, lspReqDefinition))
	}
	return s, s.finishHomeUpdate(sh, act)
}

// languageServerKey handles gote's caret-driven LSP chords. They are matched here, above
// the panes, for the reason every other screen key is: the editor consumes what reaches
// it, and these have to work while it is being typed in.
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

// retargetClick applies gote's two modifier-click gestures before anything else sees the
// message. Both are pure message rewriting on the way down, which is what keeps them out
// of bubblestack entirely.
//
// The context gesture becomes a real right press, so the editor's own context menu opens
// exactly as it does for a physical right click — the point being terminals that keep
// the right button for their own menu and never hand it over.
//
// The definition gesture is left ALONE on the way down: the editor already treats a
// modified left press as an ordinary caret click (only shift means anything to it), so
// letting it through both moves the caret and focuses the pane. The caller acts on the
// caret afterwards.
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

// clickModifierMatches reads one of the configured click modifiers. An empty or
// unrecognized value is "none" — a typo in the config should disable a gesture, not
// bind it to something the user did not ask for.
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
	defer s.refreshDiagnostics()
	s.applyPendingJump()
	// After the jump, so a caret that has just landed somewhere else is judged on where it
	// landed. Here rather than in the typing hook because this is the exit every path that
	// can move the caret shares — including the two that return before the hook runs.
	s.dismissSignatureIfLeft()
	s.refreshPreview()
	s.syncPreviewScroll()
	s.syncOutlineCaret()
	// Batched into the cmd lane rather than folded in with core.Seq: Seq builds an
	// Action carrying only a control message, which would drop whatever cmd the panes
	// just returned (the editor's clipboard writes, a list's own async work).
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

// semanticDebounce matches the editor's own highlight debounce. The two are deliberately
// the same number: the fetch exists to feed the exact parse, so asking on a different
// cadence only widens the window where the overlay describes text that has moved.
const semanticDebounce = 250 * time.Millisecond

type semanticTick struct {
	target     *homeScreen
	generation int
}

// scheduleSemanticTokens debounces the fetch behind a generation counter, the shape
// scheduleCompletion established. Every edit supersedes the pending tick, so a burst of
// typing sends nothing at all and one fetch goes out once it settles — where the previous
// version asked on every update, and startSemantic force-flushes the document ahead of the
// RPC, so that was a full document push per keystroke.
func (s *homeScreen) scheduleSemanticTokens() tea.Cmd {
	if s.editor == nil || s.currentPath == "" {
		return nil
	}
	seq := s.editor.EditSeq()
	if s.semanticPath == s.currentPath && s.semanticSeq == seq {
		return nil // this generation has already been asked for
	}
	s.semanticGen++
	generation := s.semanticGen
	return tea.Tick(semanticDebounce, func(time.Time) tea.Msg {
		return semanticTick{target: s, generation: generation}
	})
}

// handleSemanticTick issues the fetch the tick was scheduled for, unless a later edit
// already superseded it.
//
// A refusal is not recorded against semanticSeq, so a document opened before its server
// finished starting is asked again after the next edit rather than never. Nor is it
// reported: nobody pressed anything, and a buffer with no server is meant to look exactly
// as it did before this feature existed.
func (s *homeScreen) handleSemanticTick(sh *core.Shared, tick semanticTick) {
	if tick.target != s || tick.generation != s.semanticGen {
		return
	}
	if s.editor == nil || s.currentPath == "" {
		return
	}
	c := Of(sh)
	if c == nil || c.lsp == nil {
		return
	}
	seq := s.editor.EditSeq()
	if c.lsp.RequestSemanticTokens(s.currentPath, seq) != 0 {
		s.semanticPath, s.semanticSeq = s.currentPath, seq
	}
}

// View and HelpView both route through the status helpers (status.go): the router's own
// status row is masked away, so this screen is the one that has to find the message a
// home. In minimal mode there is no help bar, so the body's last row takes it.
func (s *homeScreen) View(sh *core.Shared) string {
	s.refreshOpenTabs(sh)
	body := s.modular.View(sh)
	if s.minimal {
		body = statusOver(sh, body, s.h)
	}
	body = s.viewCompletion(sh, body)
	body = s.viewSignature(sh, body)
	return s.viewHover(sh, body)
}

func (s *homeScreen) HelpView(sh *core.Shared) string {
	return statusBar(sh, s.modular.HelpView(sh))
}

func (s *homeScreen) SetSize(sh *core.Shared, width, bodyHeight int) {
	s.sh = sh
	resized := width != s.w || bodyHeight != s.h
	s.w, s.h = width, bodyHeight
	s.modular.SetSize(sh, width, bodyHeight)
	s.refreshDiagnostics()
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
// modal (the save-as/rename line edit, the help overlay).
func (s *homeScreen) QuitGate(sh *core.Shared) (core.Action, bool) {
	s.closeCompletion()
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

// dirtyDocs names every retained buffer with unsaved changes: the live editor first,
// then the remaining Open rows in opening order. The final fallback covers a pathless
// editor dirtied outside the ordinary Update route before promotion can run.
func (s *homeScreen) dirtyDocs(sh *core.Shared) []string {
	var names []string
	if s.editor != nil && s.editor.Dirty() {
		names = append(names, s.previewName())
	}
	c := Of(sh)
	for _, doc := range c.OpenDocs() {
		ed, ok := c.buffer(doc.ID)
		if ok && ed != nil && ed != s.editor && ed.Dirty() {
			names = append(names, doc.Name)
		}
	}
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
func (s *homeScreen) Receive(sh *core.Shared, payload any) (result core.Action) {
	defer func() { result.Cmd = tea.Batch(result.Cmd, s.syncDocsGit()) }()
	if act, handled := s.receiveDocsGit(payload); handled {
		return act
	}
	if _, ok := payload.(ReseedMsg); ok {
		defer func() { result.Cmd = tea.Batch(result.Cmd, s.requestDocsGit()) }()
	}
	defer s.refreshDiagnostics()
	if request, ok := payload.(findFilesRequest); ok {
		return s.beginFindFiles(sh, request)
	}
	if search, ok := payload.(findFilesResult); ok {
		s.finishFindFiles(search)
		return core.Action{}
	}
	if event, ok := payload.(lspEvent); ok {
		if event.completion != nil {
			s.applyCompletionResult(event.completion)
		}
		act := core.Action{}
		if event.request != nil {
			act = s.applyRequestResult(sh, event.request)
		}
		if event.semantic != nil {
			s.applySemanticTokens(event.semantic)
		}
		if event.outline != nil {
			s.applyOutlineResult(event.outline)
		}
		s.refreshDiagnosticSigns()
		s.lspWaiting = true
		wait := core.Async(tea.Batch(Of(sh).lsp.WaitCmd(), s.retryOutlineAfterLSP(sh)))
		if event.status != "" {
			return core.Seq(act, core.SetStatusAndLog(event.status), wait)
		}
		return core.Seq(act, wait)
	}
	if _, ok := payload.(ReseedMsg); ok {
		c := Of(sh)
		c.Seed()
		s.docsPanel.SetItems(s.docRows(c))
		s.filePanel.Refresh()
		s.openPanel.SetItems(openDocItems(c, s.currentID))
		if c.lsp != nil {
			active := c.lsp.Reconcile(c)
			s.refreshDiagnosticSigns()
			if active && !s.lspWaiting {
				s.lspWaiting = true
				return core.Async(c.lsp.WaitCmd())
			}
		}
		return core.Action{}
	}
	if msg, ok := payload.(baselineMsg); ok {
		s.applyBaseline(msg)
		return core.Action{}
	}
	if msg, ok := payload.(gutterRefreshMsg); ok {
		s.applyGutterRefresh(msg)
		return core.Action{}
	}
	if msg, ok := payload.(SwitchVaultMsg); ok {
		return s.requestVaultSwitch(sh, msg.Name)
	}
	if _, ok := payload.(core.MsgThemeChanged); ok {
		core.StyleList(s.docsPanel.List())
		core.StyleList(s.filePanel.List())
		core.StyleList(s.openPanel.List())
		core.StyleList(s.outlinePanel.List())
		s.refreshDiagnosticSigns()
		s.diagnostics.paint()
		s.search.paint()
	}
	return s.modular.Receive(sh, payload)
}

// requestVaultSwitch validates the target before consulting dirty state. A broken
// saved path must never make the user discard work for a switch that cannot happen.
func (s *homeScreen) requestVaultSwitch(sh *core.Shared, name string) core.Action {
	s.closeCompletion()
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
	s.resetDocsGit()
	s.installScratch(c)
	s.fullPreview = nil // the vault's scratch buffer is the editor, not a reader over it
	cmd := s.editorPanel.SetChild(s.editor)
	s.docsPanel.SetItems(s.docRows(c))
	// Rebuilt, not re-pointed: the new vault brings a new root as well as a new directory,
	// and the explorer's floor is fixed at construction.
	s.filePanel = components.NewFilePanel(s.filePanelOpts(c))
	s.openPanel.SetItems(nil)
	if s.outlineVisible {
		s.prepareOutlineDocument()
	}
	s.preview, s.previewPrior = previewOff, previewOff
	s.resetPreviewCache()
	s.minimal = false
	s.sidebar = true
	// The launch mode this screen was built for is gone; a vault is the full editor, so
	// the auto default has to be asked again rather than carrying ModeFile's answer over.
	gutterCmd := s.setGitGutter(gutterDefault(c.Config, ModeVault))
	if c.lsp != nil {
		c.lsp.Reconcile(c)
	}
	focus := s.rebuildModular(sh, 0)
	return core.Seq(core.Async(tea.Batch(cmd, focus, gutterCmd)), core.ResetToRoot())
}

// editorLeft is the terminal column the editor pane starts at: the side column's adjusted
// width when it is up, zero otherwise (buildModular puts the side column first and
// everything after it flexes). Caret-anchored panels use it as their
// left bound — a tooltip is about the caret, so it belongs over the text rather than
// spilling across the file list.
func (s *homeScreen) editorLeft() int {
	if s.sideColumnVisible() {
		return s.sidebarPaneWidth()
	}
	return 0
}

func (s *homeScreen) sideColumnPanels() []components.Panel {
	if s.minimal {
		if s.outlineVisible {
			return []components.Panel{s.outlinePanel}
		}
		return nil
	}
	if !s.sidebar {
		return nil
	}
	panels := []components.Panel{s.docsPane()}
	if !s.tabsVisible() {
		panels = append(panels, s.openPanel)
	}
	if s.outlineVisible {
		panels = append(panels, s.outlinePanel)
	}
	return panels
}

func (s *homeScreen) sideColumnVisible() bool { return len(s.sideColumnPanels()) > 0 }

func (s *homeScreen) sidebarSplitKey() string {
	var names []string
	for _, panel := range s.sideColumnPanels() {
		switch panel {
		case s.docsPanel, s.filePanel:
			names = append(names, "docs")
		case s.openPanel:
			names = append(names, "open")
		case s.outlinePanel:
			names = append(names, "outline")
		}
	}
	return strings.Join(names, "/")
}

func (s *homeScreen) sidebarPaneWidth() int {
	if s.sidebarW > 0 {
		return s.sidebarW
	}
	return sidebarWidth
}

// editorSlot is the editor pane's flat slot index in the current layout.
func (s *homeScreen) editorSlot() int {
	return s.panelSlot(s.editorPanel)
}

// setSidebar rebuilds the internal modular screen with or without the sidebar
// column. The old screen's focused panel is blurred before it is discarded, and the
// new one (which auto-focuses its first focusable slot — the docs list, or the lone
// editor) gets the current size.
//
// Minimal mode returns here: this is the single door the sidebar can come back through
// (alt+|, and the unhide branches of editorExit/editorRelease), so refusing it here is
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
// the rename box anchors to.
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

// buildModular declares gote's pane tree. The framework knows only horizontal
// and vertical splits; tool visibility and the initial workspace split live here.
// Depth-first leaf order keeps the existing upper-pane indexes stable.
func (s *homeScreen) buildModular() *components.ModularScreen {
	s.editor.SetTitleVisible(!s.tabsVisible())
	opts := components.ModularOpts{
		// One entry, and it is the pointer at all the others: every app key gote has
		// is documented in the ? overlay (helpText), so the bar names the way in
		// rather than reprinting a handful of them beside the framework's own
		// pane/back/select hints. The keys themselves are untouched.
		Help:      []key.Binding{helpKey},
		HelpLimit: 4,
	}
	if s.fullPreview != nil {
		// The one exception, and it earns the cell: a reader sitting where the editor was
		// has to say how to get the editor back. A ScreenPanel contributes no PanelHelp of
		// its own, so the reader's own hints never reach this bar.
		opts.Help = append([]key.Binding{fullPreviewKey}, opts.Help...)
	}
	leaf := func(panel components.Panel) components.LayoutNode {
		s.panelSlots[panel] = len(s.panelSlots)
		return components.LayoutNode{Slot: &components.Slot{Panel: panel}}
	}
	s.panelSlots = make(map[components.Panel]int)
	main := components.LayoutNode{ID: "main", Axis: components.LayoutHorizontal}
	if panels := s.sideColumnPanels(); len(panels) > 0 {
		children := make([]components.LayoutNode, 0, len(panels))
		for _, panel := range panels {
			children = append(children, leaf(panel))
		}
		main.Children = append(main.Children, components.LayoutNode{
			ID: "sidebar", Axis: components.LayoutVertical, Size: s.sidebarPaneWidth(),
			Children: children,
		})
	}
	if s.tabsVisible() {
		bar := leaf(s.openTabs)
		bar.Size, bar.FixedSize = 1, true
		editors := components.LayoutNode{ID: "editors", Axis: components.LayoutHorizontal,
			Children: []components.LayoutNode{leaf(s.editorPanel)}}
		if panel := s.previewTarget(); panel != nil {
			editors.Children = append(editors.Children, leaf(panel))
		}
		main.Children = append(main.Children, components.LayoutNode{ID: "documents", Axis: components.LayoutVertical,
			Children: []components.LayoutNode{bar, editors}})
	} else {
		main.Children = append(main.Children, leaf(s.editorPanel))
		if panel := s.previewTarget(); panel != nil {
			main.Children = append(main.Children, leaf(panel))
		}
	}
	root := main
	if s.bottomVisible {
		main.Weight = 3
		root = components.LayoutNode{ID: "workspace", Axis: components.LayoutVertical,
			Children: []components.LayoutNode{main, {
				ID: "tools", Axis: components.LayoutHorizontal,
				Children: []components.LayoutNode{leaf(s.bottom)},
			}},
		}
	}
	opts.Resize = &components.ResizeOpts{State: s.resizeState(), OnChange: s.saveResize}
	return components.NewModularLayout(root, opts)
}

// resizeState maps the persistent gote pane identities onto ModularScreen's
// positional state. The sidebar and preview columns can disappear on a rebuild,
// while the editor always remains between them.
func (s *homeScreen) resizeState() components.ResizeState {
	// Gote maps pane identities to split children. Hidden panes keep their own
	// preferences, rather than saving a snapshot of a different child list over them.
	state := components.ResizeState{Splits: make(map[string]components.SplitState)}
	sizes, weights := []int{}, []float64{}
	if panels := s.sideColumnPanels(); len(panels) > 0 {
		sizes, weights = append(sizes, s.sidebarPaneWidth()), append(weights, 1)
		if saved := s.sidebarSplits[s.sidebarSplitKey()]; len(saved) == len(panels) {
			state.Splits["sidebar"] = components.SplitState{Sizes: make([]int, len(panels)), Weights: append([]float64(nil), saved...)}
		}
	}
	share := s.editorFlex
	if share <= 0 || share >= 1 {
		share = 0.5
	}
	if s.tabsVisible() {
		sizes, weights = append(sizes, 0), append(weights, 1)
		editors := components.SplitState{Sizes: []int{0}, Weights: []float64{share}}
		if s.previewTarget() != nil {
			editors.Sizes = append(editors.Sizes, 0)
			editors.Weights = append(editors.Weights, 1-share)
		}
		state.Splits["editors"] = editors
	} else {
		sizes, weights = append(sizes, 0), append(weights, share)
		if s.previewTarget() != nil {
			sizes, weights = append(sizes, 0), append(weights, 1-share)
		}
	}
	state.Splits["main"] = components.SplitState{Sizes: sizes, Weights: weights}
	if s.bottomVisible && s.bottomFraction > 0 && s.bottomFraction < 1 {
		state.Splits["workspace"] = components.SplitState{Sizes: []int{0, 0}, Weights: []float64{1 - s.bottomFraction, s.bottomFraction}}
	}
	return state
}

// saveResize retains gote's pane preferences independently of which panes exist
// in this layout. The framework knows only named splits and their child weights.
func (s *homeScreen) saveResize(state components.ResizeState) {
	if split, ok := state.Splits["workspace"]; s.bottomVisible && ok && len(split.Weights) == 2 {
		s.bottomFraction = split.Weights[1] / (split.Weights[0] + split.Weights[1])
	}
	editorCol := 0
	if s.sideColumnVisible() {
		if split, ok := state.Splits["main"]; ok && len(split.Sizes) > 0 && split.Sizes[0] > 0 {
			s.sidebarW = split.Sizes[0]
		}
		if split, ok := state.Splits["sidebar"]; ok && len(split.Weights) == len(s.sideColumnPanels()) {
			sum := 0.0
			for _, weight := range split.Weights {
				sum += weight
			}
			if sum > 0 {
				weights := make([]float64, len(split.Weights))
				for i, weight := range split.Weights {
					weights[i] = weight / sum
				}
				s.sidebarSplits[s.sidebarSplitKey()] = weights
			}
		}
		editorCol = 1
	}
	group := "main"
	if s.tabsVisible() {
		group, editorCol = "editors", 0
	}
	if split, ok := state.Splits[group]; ok && s.previewTarget() != nil && len(split.Weights) > editorCol+1 {
		s.editorFlex = split.Weights[editorCol] / (split.Weights[editorCol] + split.Weights[editorCol+1])
	}
}
