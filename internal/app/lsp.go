package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/brohd11/bubblestack/components/editor"
	"github.com/brohd11/bubblestack/core"

	tea "charm.land/bubbletea/v2"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

const (
	lspRetryDelay      = 15 * time.Second
	lspDialLimit       = 1500 * time.Millisecond
	lspInitLimit       = 5 * time.Second
	lspCompletionLimit = 2 * time.Second
	lspChangeDebounce  = 500 * time.Millisecond
)

// lspDocument is the manager's desired view of one live editor buffer. Version is an
// LSP version, independent of EditorScreen.EditSeq: it begins at one on didOpen and
// advances monotonically whenever the editor generation changes.
type lspDocument struct {
	path, language, server, root, text string
	editSeq                            int
	version                            int32
}

func (d lspDocument) key() string { return d.server + "\x00" + d.root }

// lspDiagnostic is the UI-sized projection of protocol.Diagnostic. Keeping protocol
// unions out of the renderer makes snapshots cheap and gives the rest of Gote stable
// strings regardless of whether a server used a string or markup message/code.
type lspDiagnostic struct {
	Line, Character       uint32
	EndLine, EndCharacter uint32
	Severity              protocol.DiagnosticSeverity
	Message, Source, Code string
}

type lspCompletionEdit struct {
	Range   protocol.Range
	NewText string
}

type lspCompletionItem struct {
	Label, Detail, FilterText, SortText, InsertText string
	Edit                                            *lspCompletionEdit
	Stops                                           []editor.CompletionStop
	Snippet                                         bool
	Preselect                                       bool
}

type lspCompletionRequest struct {
	id               uint64
	path             string
	editSeq          int
	position         protocol.Position
	triggerCharacter string
	manual           bool
}

type lspCompletionResult struct {
	id       uint64
	path     string
	editSeq  int
	position protocol.Position
	items    []lspCompletionItem
	// incomplete is the server's isIncomplete: "ask me again as the user types" rather
	// than "I truncated this". Nothing reads it, deliberately. gopls sets it on every
	// answer — a three-item member list as readily as a scope-wide one — and the popup
	// already schedules a fresh request on each identifier keystroke, so acting on the
	// flag would ask for exactly what the debounce asks for anyway.
	incomplete bool
	err        error
}

type lspEvent struct {
	status     string
	completion *lspCompletionResult
	request    *lspRequestResult
}

// lspManager is an actor around all server and document lifecycle work. The UI writes
// the latest desired snapshots under mu and nudges wake; the actor converges sessions
// to that state off Bubble Tea's update goroutine. Repeated edits are retained as one
// latest snapshot and sent only after the change debounce expires.
type lspManager struct {
	cfg            Config
	version        string
	changeDebounce time.Duration

	mu                 sync.Mutex
	desired            map[string]lspDocument
	saves              map[string]bool
	diagnostics        map[string][]lspDiagnostic
	completion         *lspCompletionRequest
	nextComplete       uint64
	completionTriggers map[string]map[string]bool
	// The on-demand lane (lsp_request.go): its own slot and counter, so a queued
	// hover never cancels a completion the same keystroke asked for.
	request           *lspRequest
	nextRequest       uint64
	signatureTriggers map[string]map[string]bool
	capabilities      map[string]*protocol.ServerCapabilities
	restart           bool
	closed            bool

	wake    chan struct{}
	stop    chan struct{}
	done    chan struct{}
	events  chan lspEvent
	dead    chan deadSession
	once    sync.Once
	workers sync.WaitGroup
}

type deadSession struct {
	key  string
	conn jsonrpc2.Conn
	err  error
}

type lspSession struct {
	key, serverID, root string
	server              protocol.Server
	conn                jsonrpc2.Conn
	sent                map[string]int32
	retryAt             time.Time
	failure             string
	// caps is the whole initialize answer, not just the completion options: every
	// on-demand request is gated on what this server said it can do.
	caps *protocol.ServerCapabilities
}

func newLSPManager(cfg Config, version string) *lspManager {
	return &lspManager{
		cfg: cfg, version: version, changeDebounce: lspChangeDebounce,
		desired: map[string]lspDocument{}, saves: map[string]bool{},
		diagnostics: map[string][]lspDiagnostic{}, completionTriggers: map[string]map[string]bool{},
		signatureTriggers: map[string]map[string]bool{},
		capabilities:      map[string]*protocol.ServerCapabilities{},
		wake:              make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{}),
		events: make(chan lspEvent, 64), dead: make(chan deadSession, 8),
	}
}

func (m *lspManager) start() { m.once.Do(func() { go m.loop() }) }

func (m *lspManager) signal() {
	m.start()
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// Reconcile captures all supported open buffers. It performs no IO and never waits for
// the actor: one map replacement and a coalescing wake are the whole UI-thread cost.
func (m *lspManager) Reconcile(c *Ctx) bool {
	if m == nil || c == nil {
		return false
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return false
	}
	next := make(map[string]lspDocument)
	hadDocuments := len(m.desired) > 0
	c.EachDoc(func(path string, ed *editor.Screen) {
		profile := languageForPath(path)
		if profile == nil || profile.lsp == nil || ed == nil {
			return
		}
		serverCfg, ok := m.cfg.LanguageServers[profile.lsp.server]
		if !ok || serverCfg.Disabled {
			return
		}
		root := lspRootForPath(path, profile)
		if root == "" {
			return
		}
		path = filepath.Clean(path)
		doc := lspDocument{
			path: path, language: profile.id, server: profile.lsp.server, root: root,
			text: ed.Text(), editSeq: ed.EditSeq(), version: 1,
		}
		if prior, ok := m.desired[path]; ok && prior.server == doc.server && prior.root == doc.root {
			doc.version = prior.version
			if prior.editSeq != doc.editSeq || prior.text != doc.text {
				doc.version++
			}
		}
		next[path] = doc
	})
	for path := range m.desired {
		if _, ok := next[path]; !ok {
			delete(m.diagnostics, path)
			if m.completion != nil && m.completion.path == path {
				m.completion = nil
			}
			if m.request != nil && m.request.path == path {
				m.request = nil
			}
		}
	}
	m.desired = next
	m.mu.Unlock()
	if len(next) > 0 || hadDocuments {
		m.signal()
	}
	return len(next) > 0
}

// CompletionTrigger reports whether the initialized server for path declared text as
// a completion trigger character. Before initialization there is no advertised set and
// false is returned; manual and identifier-driven requests remain available.
func (m *lspManager) CompletionTrigger(path, text string) bool {
	if m == nil || path == "" || text == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	doc, ok := m.desired[filepath.Clean(path)]
	if !ok {
		return false
	}
	return m.completionTriggers[doc.key()][text]
}

// RequestCompletion replaces any queued completion request with the newest editor
// snapshot. It performs no IO on the UI goroutine and returns the generation carried by
// the eventual result.
func (m *lspManager) RequestCompletion(path string, editSeq int, position protocol.Position,
	triggerCharacter string, manual bool) uint64 {
	if m == nil || path == "" {
		return 0
	}
	path = filepath.Clean(path)
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return 0
	}
	if _, ok := m.desired[path]; !ok {
		m.mu.Unlock()
		return 0
	}
	m.nextComplete++
	id := m.nextComplete
	m.completion = &lspCompletionRequest{
		id: id, path: path, editSeq: editSeq, position: position,
		triggerCharacter: triggerCharacter, manual: manual,
	}
	m.mu.Unlock()
	m.signal()
	return id
}

func (m *lspManager) takeCompletion() *lspCompletionRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	request := m.completion
	m.completion = nil
	return request
}

func (m *lspManager) DidSave(path string) {
	if m == nil || path == "" {
		return
	}
	m.mu.Lock()
	if !m.closed {
		m.saves[filepath.Clean(path)] = true
	}
	m.mu.Unlock()
	m.signal()
}

func (m *lspManager) Restart() {
	if m == nil {
		return
	}
	m.mu.Lock()
	if !m.closed {
		m.restart = true
		m.completion = nil
		m.nextComplete++
		m.request = nil
		m.nextRequest++
	}
	m.mu.Unlock()
	m.signal()
}

func (m *lspManager) Close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	m.mu.Unlock()
	m.start()
	close(m.stop)
	<-m.done
}

// WaitCmd is the single Bubble Tea subscription. Events are broadcasts because an LSP
// notification may arrive while a menu or diagnostics page is above the home screen.
func (m *lspManager) WaitCmd() tea.Cmd {
	if m == nil {
		return nil
	}
	m.start()
	return func() tea.Msg {
		event, ok := <-m.events
		if !ok {
			return nil
		}
		return core.PropagateAll(event)
	}
}

func (m *lspManager) loop() {
	defer close(m.done)
	sessions := map[string]*lspSession{}
	pending := map[string]lspDocument{}
	debounce := m.changeDebounce
	if debounce <= 0 {
		debounce = lspChangeDebounce
	}
	var changeTimer *time.Timer
	var changeTimerC <-chan time.Time
	var cancelCompletion context.CancelFunc
	var cancelRequest context.CancelFunc
	stopChangeTimer := func() {
		if changeTimer == nil {
			return
		}
		if !changeTimer.Stop() {
			select {
			case <-changeTimer.C:
			default:
			}
		}
		changeTimerC = nil
	}
	resetChangeTimer := func() {
		if changeTimer == nil {
			changeTimer = time.NewTimer(debounce)
		} else {
			stopChangeTimer()
			changeTimer.Reset(debounce)
		}
		changeTimerC = changeTimer.C
	}
	for {
		select {
		case <-m.wake:
			queued := m.converge(sessions, pending, false)
			if request := m.takeCompletion(); request != nil {
				if cancelCompletion != nil {
					cancelCompletion()
				}
				cancelCompletion = m.startCompletion(sessions, pending, *request)
			}
			if request := m.takeRequest(); request != nil {
				if cancelRequest != nil {
					cancelRequest()
				}
				cancelRequest = m.startRequest(sessions, pending, *request)
			}
			switch {
			case len(pending) == 0:
				stopChangeTimer()
			case queued || changeTimerC == nil:
				resetChangeTimer()
			}
		case <-changeTimerC:
			changeTimerC = nil
			m.converge(sessions, pending, true)
			if len(pending) > 0 {
				resetChangeTimer()
			}
		case dead := <-m.dead:
			if session := sessions[dead.key]; session != nil && session.conn == dead.conn {
				if dead.err == nil {
					dead.err = fmt.Errorf("connection closed")
				}
				m.failSession(session, dead.err)
				clearPendingSession(pending, session.key)
			}
		case <-m.stop:
			stopChangeTimer()
			if cancelCompletion != nil {
				cancelCompletion()
			}
			if cancelRequest != nil {
				cancelRequest()
			}
			for _, session := range sessions {
				m.closeSession(session)
			}
			m.workers.Wait()
			close(m.events)
			return
		}
	}
}

func (m *lspManager) desiredSnapshot() (map[string]lspDocument, map[string]bool, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	docs := make(map[string]lspDocument, len(m.desired))
	for path, doc := range m.desired {
		docs[path] = doc
	}
	saves := m.saves
	m.saves = map[string]bool{}
	restart := m.restart
	m.restart = false
	return docs, saves, restart
}

// converge applies document lifecycle work immediately. Ordinary content changes are
// retained in pending until a quiet period; saves and reconnects always use the latest
// snapshot without waiting. The return value reports whether a newer pending version
// was observed and therefore needs a fresh debounce window.
func (m *lspManager) converge(sessions map[string]*lspSession, pending map[string]lspDocument, flushChanges bool) bool {
	docs, saves, restart := m.desiredSnapshot()
	queuedChange := false
	for path, queued := range pending {
		doc, ok := docs[path]
		if !ok || queued.key() != doc.key() {
			delete(pending, path)
		}
	}
	if restart {
		for key, session := range sessions {
			m.closeSession(session)
			delete(sessions, key)
		}
		clear(pending)
	}

	// Close documents that disappeared or moved to another session before opening their
	// replacements. That ordering makes save-as one clean close/open transition.
	for _, session := range sessions {
		if session.server == nil {
			continue
		}
		for path := range session.sent {
			doc, ok := docs[path]
			if ok && doc.key() == session.key {
				continue
			}
			if err := session.server.DidClose(context.Background(), &protocol.DidCloseTextDocumentParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: uri.File(path)},
			}); err != nil {
				m.failSession(session, err)
				clearPendingSession(pending, session.key)
				break
			}
			delete(session.sent, path)
		}
	}

	grouped := make(map[string][]lspDocument)
	for _, doc := range docs {
		grouped[doc.key()] = append(grouped[doc.key()], doc)
	}
	keys := make([]string, 0, len(grouped))
	for key := range grouped {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		group := grouped[key]
		sort.Slice(group, func(i, j int) bool { return group[i].path < group[j].path })
		session := sessions[key]
		if session == nil {
			session = &lspSession{key: key, serverID: group[0].server, root: group[0].root, sent: map[string]int32{}}
			sessions[key] = session
		}
		if session.server == nil {
			if time.Now().Before(session.retryAt) {
				clearPendingSession(pending, session.key)
				continue
			}
			if err := m.startSession(session); err != nil {
				m.failSession(session, err)
				clearPendingSession(pending, session.key)
				continue
			}
		}
		for _, doc := range group {
			version, opened := session.sent[doc.path]
			var err error
			switch {
			case !opened:
				err = session.server.DidOpen(context.Background(), &protocol.DidOpenTextDocumentParams{
					TextDocument: protocol.TextDocumentItem{
						URI: uri.File(doc.path), LanguageID: protocol.LanguageKind(doc.language),
						Version: doc.version, Text: doc.text,
					},
				})
			case version != doc.version:
				queued, wasQueued := pending[doc.path]
				ready := saves[doc.path] || (flushChanges && wasQueued && queued.version == doc.version && queued.key() == doc.key())
				if !ready {
					if !wasQueued || queued.version != doc.version || queued.key() != doc.key() {
						pending[doc.path] = doc
						queuedChange = true
					}
					continue
				}
				err = session.server.DidChange(context.Background(), &protocol.DidChangeTextDocumentParams{
					TextDocument: protocol.VersionedTextDocumentIdentifier{
						TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: uri.File(doc.path)},
						Version:                doc.version,
					},
					ContentChanges: []protocol.TextDocumentContentChangeEvent{
						&protocol.TextDocumentContentChangeWholeDocument{Text: doc.text},
					},
				})
			}
			if err != nil {
				m.failSession(session, err)
				clearPendingSession(pending, session.key)
				break
			}
			session.sent[doc.path] = doc.version
			delete(pending, doc.path)
			if saves[doc.path] {
				text := doc.text
				if err := session.server.DidSave(context.Background(), &protocol.DidSaveTextDocumentParams{
					TextDocument: protocol.TextDocumentIdentifier{URI: uri.File(doc.path)}, Text: &text,
				}); err != nil {
					m.failSession(session, err)
					clearPendingSession(pending, session.key)
					break
				}
			}
		}
	}
	return queuedChange
}

func clearPendingSession(pending map[string]lspDocument, key string) {
	for path, doc := range pending {
		if doc.key() == key {
			delete(pending, path)
		}
	}
}

// startCompletion flushes the requested document to its session and starts the RPC.
// The notification write happens first on the actor goroutine, so the server observes
// the exact text whose cursor position the request names. Only the RPC wait is moved to
// a worker.
func (m *lspManager) startCompletion(sessions map[string]*lspSession,
	pending map[string]lspDocument, request lspCompletionRequest) context.CancelFunc {
	m.mu.Lock()
	doc, ok := m.desired[request.path]
	m.mu.Unlock()
	if !ok || doc.editSeq != request.editSeq {
		m.emitCompletion(request, nil, false, nil)
		return nil
	}
	session := sessions[doc.key()]
	if session == nil || session.server == nil || session.caps == nil || session.caps.CompletionProvider == nil {
		m.emitCompletion(request, nil, false, nil)
		return nil
	}
	if session.sent[doc.path] != doc.version {
		if err := session.server.DidChange(context.Background(), &protocol.DidChangeTextDocumentParams{
			TextDocument: protocol.VersionedTextDocumentIdentifier{
				TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: uri.File(doc.path)},
				Version:                doc.version,
			},
			ContentChanges: []protocol.TextDocumentContentChangeEvent{
				&protocol.TextDocumentContentChangeWholeDocument{Text: doc.text},
			},
		}); err != nil {
			m.failSession(session, err)
			clearPendingSession(pending, session.key)
			m.emitCompletion(request, nil, false, err)
			return nil
		}
		session.sent[doc.path] = doc.version
		delete(pending, doc.path)
	}

	kind := protocol.CompletionTriggerKindInvoked
	var trigger *string
	if !request.manual && request.triggerCharacter != "" {
		for _, candidate := range session.caps.CompletionProvider.TriggerCharacters {
			if candidate == request.triggerCharacter {
				kind = protocol.CompletionTriggerKindTriggerCharacter
				value := request.triggerCharacter
				trigger = &value
				break
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), lspCompletionLimit)
	server := session.server
	m.workers.Add(1)
	go func() {
		defer m.workers.Done()
		result, err := server.Completion(ctx, &protocol.CompletionParams{
			TextDocumentPositionParams: protocol.TextDocumentPositionParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: uri.File(doc.path)},
				Position:     request.position,
			},
			Context: protocol.CompletionContext{TriggerKind: kind, TriggerCharacter: trigger},
		})
		items, incomplete := projectCompletions(result)
		m.emitCompletion(request, items, incomplete, err)
	}()
	return cancel
}

func (m *lspManager) emitCompletion(request lspCompletionRequest, items []lspCompletionItem,
	incomplete bool, err error) {
	m.mu.Lock()
	current := !m.closed && request.id == m.nextComplete
	m.mu.Unlock()
	if !current {
		return
	}
	m.emit(lspEvent{completion: &lspCompletionResult{
		id: request.id, path: request.path, editSeq: request.editSeq, position: request.position,
		items: items, incomplete: incomplete, err: err,
	}})
}

func projectCompletions(result protocol.CompletionResult) ([]lspCompletionItem, bool) {
	var source []protocol.CompletionItem
	incomplete := false
	switch result := result.(type) {
	case protocol.CompletionItemSlice:
		source = []protocol.CompletionItem(result)
	case *protocol.CompletionList:
		if result != nil {
			source, incomplete = result.Items, result.IsIncomplete
		}
	}
	items := make([]lspCompletionItem, 0, len(source))
	for _, item := range source {
		projected := lspCompletionItem{Label: item.Label}
		if value, ok := item.Detail.Get(); ok {
			projected.Detail = value
		}
		if value, ok := item.FilterText.Get(); ok {
			projected.FilterText = value
		}
		if projected.FilterText == "" {
			projected.FilterText = item.Label
		}
		if value, ok := item.SortText.Get(); ok {
			projected.SortText = value
		}
		if value, ok := item.InsertText.Get(); ok {
			projected.InsertText = value
		}
		if projected.InsertText == "" {
			projected.InsertText = item.Label
		}
		if value, ok := item.Preselect.Get(); ok {
			projected.Preselect = value
		}
		switch edit := item.TextEdit.(type) {
		case *protocol.TextEdit:
			if edit != nil {
				projected.Edit = &lspCompletionEdit{Range: edit.Range, NewText: edit.NewText}
			}
		case *protocol.InsertReplaceEdit:
			if edit != nil {
				projected.Edit = &lspCompletionEdit{Range: edit.Replace, NewText: edit.NewText}
			}
		}
		if item.InsertTextFormat == protocol.InsertTextFormatSnippet {
			text := projected.InsertText
			if projected.Edit != nil {
				text = projected.Edit.NewText
			}
			expanded, stops, ok := parseLSPSnippet(text)
			if !ok {
				continue
			}
			projected.Snippet = true
			projected.Stops = stops
			if projected.Edit != nil {
				projected.Edit.NewText = expanded
			} else {
				projected.InsertText = expanded
			}
		}
		items = append(items, projected)
	}
	return items, incomplete
}

func (m *lspManager) startSession(session *lspSession) error {
	cfg, ok := m.cfg.LanguageServers[session.serverID]
	if !ok || cfg.Disabled {
		return fmt.Errorf("%s language server is disabled", session.serverID)
	}
	if cfg.InitializationOptions == nil {
		cfg.InitializationOptions = defaultLanguageServers()[session.serverID].InitializationOptions
	}
	hasAddress, hasCommand := strings.TrimSpace(cfg.Address) != "", len(cfg.Command) > 0 && strings.TrimSpace(cfg.Command[0]) != ""
	if hasAddress == hasCommand {
		return fmt.Errorf("%s language server needs exactly one of address or command", session.serverID)
	}

	var transport io.ReadWriteCloser
	if hasAddress {
		address := strings.TrimSpace(cfg.Address)
		conn, err := net.DialTimeout("tcp", address, lspDialLimit)
		if err != nil {
			return fmt.Errorf("connect %s at %s: %w", session.serverID, address, err)
		}
		transport = conn
	} else {
		cmd := exec.Command(cfg.Command[0], cfg.Command[1:]...)
		cmd.Dir = session.root
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return err
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			_ = stdin.Close()
			return err
		}
		cmd.Stderr = io.Discard
		if err := cmd.Start(); err != nil {
			_ = stdin.Close()
			_ = stdout.Close()
			return fmt.Errorf("start %s: %w", strings.Join(cfg.Command, " "), err)
		}
		transport = &stdioTransport{Reader: stdout, Writer: stdin, stdin: stdin, stdout: stdout, cmd: cmd}
	}

	client := &lspClient{manager: m, root: session.root}
	ctx, conn, server := protocol.NewClient(context.Background(), client, jsonrpc2.NewStream(transport))
	rootURI := uri.File(session.root)
	pid := int32(os.Getpid())
	version := protocol.NewOptional(m.version)
	yes := true
	var initializationOptions protocol.LSPAny
	if cfg.InitializationOptions != nil {
		encoded, marshalErr := json.Marshal(cfg.InitializationOptions)
		if marshalErr != nil {
			_ = conn.Close()
			return fmt.Errorf("initialize options for %s: %w", session.serverID, marshalErr)
		}
		initializationOptions = encoded
	}
	initCtx, cancel := context.WithTimeout(ctx, lspInitLimit)
	defer cancel()
	initialized, err := server.Initialize(initCtx, &protocol.InitializeParams{
		ProcessID:             &pid,
		ClientInfo:            protocol.ClientInfo{Name: "gote", Version: version},
		RootURI:               &rootURI,
		InitializationOptions: initializationOptions,
		WorkspaceFoldersInitializeParams: protocol.WorkspaceFoldersInitializeParams{
			WorkspaceFolders: protocol.NewNullable([]protocol.WorkspaceFolder{{
				URI: rootURI, Name: filepath.Base(session.root),
			}}),
		},
		Capabilities: protocol.ClientCapabilities{
			Workspace: &protocol.WorkspaceClientCapabilities{WorkspaceFolders: &yes},
			TextDocument: &protocol.TextDocumentClientCapabilities{
				PublishDiagnostics: &protocol.PublishDiagnosticsClientCapabilities{VersionSupport: &yes},
				Completion: &protocol.CompletionClientCapabilities{
					ContextSupport: &yes,
					CompletionItem: &protocol.ClientCompletionItemOptions{SnippetSupport: &yes},
				},
				// Nothing below is optional politeness: a server that is not told the
				// client supports a feature is entitled not to advertise it back, and
				// Supports() gates every on-demand request on that advertisement.
				Hover: &protocol.HoverClientCapabilities{
					ContentFormat: []protocol.MarkupKind{protocol.MarkupKindMarkdown, protocol.MarkupKindPlainText},
				},
				Definition: &protocol.DefinitionClientCapabilities{LinkSupport: &yes},
				References: &protocol.ReferenceClientCapabilities{},
				DocumentSymbol: &protocol.DocumentSymbolClientCapabilities{
					HierarchicalDocumentSymbolSupport: &yes,
				},
				Formatting: &protocol.DocumentFormattingClientCapabilities{},
				CodeAction: &protocol.CodeActionClientCapabilities{
					CodeActionLiteralSupport: protocol.ClientCodeActionLiteralOptions{
						CodeActionKind: protocol.ClientCodeActionKindOptions{
							ValueSet: []protocol.CodeActionKind{protocol.CodeActionKindSourceOrganizeImports},
						},
					},
				},
				SignatureHelp: &protocol.SignatureHelpClientCapabilities{
					SignatureInformation: &protocol.ClientSignatureInformationOptions{
						DocumentationFormat: []protocol.MarkupKind{protocol.MarkupKindMarkdown, protocol.MarkupKindPlainText},
						ParameterInformation: &protocol.ClientSignatureParameterInformationOptions{
							LabelOffsetSupport: &yes,
						},
					},
				},
			},
			General: &protocol.GeneralClientCapabilities{
				PositionEncodings: []protocol.PositionEncodingKind{protocol.PositionEncodingKindUTF16},
			},
		},
	})
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("initialize %s: %w", session.serverID, err)
	}
	if err := server.Initialized(ctx, &protocol.InitializedParams{}); err != nil {
		_ = conn.Close()
		return fmt.Errorf("initialize %s: %w", session.serverID, err)
	}
	session.server, session.conn = server, conn
	if initialized != nil {
		session.caps = &initialized.Capabilities
	}
	m.mu.Lock()
	m.forgetCapabilitiesLocked(session.key)
	if session.caps != nil {
		m.capabilities[session.key] = session.caps
		if completion := session.caps.CompletionProvider; completion != nil {
			m.completionTriggers[session.key] = triggerSet(completion.TriggerCharacters)
		}
		if signature := session.caps.SignatureHelpProvider; signature != nil {
			m.signatureTriggers[session.key] = triggerSet(signature.TriggerCharacters)
		}
	}
	m.mu.Unlock()
	session.failure, session.retryAt = "", time.Time{}
	session.sent = map[string]int32{}
	go func(key string, watch jsonrpc2.Conn) {
		<-watch.Done()
		select {
		case m.dead <- deadSession{key: key, conn: watch, err: watch.Err()}:
		case <-m.stop:
		}
	}(session.key, conn)
	return nil
}

// triggerSet indexes an advertised trigger-character list for lookup by typed text.
func triggerSet(characters []string) map[string]bool {
	triggers := make(map[string]bool, len(characters))
	for _, trigger := range characters {
		triggers[trigger] = true
	}
	return triggers
}

// forgetCapabilitiesLocked drops everything cached from one session's initialize answer.
// Held under mu by every caller; a session without capabilities answers "unsupported" to
// every on-demand request, which is the right state for one that is down.
func (m *lspManager) forgetCapabilitiesLocked(key string) {
	delete(m.completionTriggers, key)
	delete(m.signatureTriggers, key)
	delete(m.capabilities, key)
}

func (m *lspManager) failSession(session *lspSession, err error) {
	if session.conn != nil {
		_ = session.conn.Close()
	}
	session.server, session.conn, session.caps = nil, nil, nil
	m.mu.Lock()
	m.forgetCapabilitiesLocked(session.key)
	m.mu.Unlock()
	session.sent = map[string]int32{}
	session.retryAt = time.Now().Add(lspRetryDelay)
	message := err.Error()
	if message == session.failure {
		return
	}
	session.failure = message
	m.emit(lspEvent{status: "LSP: " + message})
}

func (m *lspManager) closeSession(session *lspSession) {
	if session == nil || session.conn == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	_ = session.server.Shutdown(ctx)
	_ = session.server.Exit(ctx)
	cancel()
	_ = session.conn.Close()
	session.server, session.conn, session.caps = nil, nil, nil
	m.mu.Lock()
	m.forgetCapabilitiesLocked(session.key)
	m.mu.Unlock()
}

func (m *lspManager) emit(event lspEvent) {
	select {
	case m.events <- event:
	default:
	}
}

// lspClient ignores every server-to-client feature except diagnostics. The embedded
// implementation still answers optional notifications safely and rejects unsupported
// requests with the protocol package's standard response.
type lspClient struct {
	protocol.UnimplementedClient
	manager *lspManager
	root    string
}

func (*lspClient) RegisterCapability(context.Context, *protocol.RegistrationParams) error {
	return nil
}

func (*lspClient) UnregisterCapability(context.Context, *protocol.UnregistrationParams) error {
	return nil
}

func (*lspClient) WorkDoneProgressCreate(context.Context, *protocol.WorkDoneProgressCreateParams) error {
	return nil
}

func (c *lspClient) Configuration(_ context.Context, params *protocol.ConfigurationParams) ([]protocol.LSPAny, error) {
	if params == nil {
		return nil, nil
	}
	return make([]protocol.LSPAny, len(params.Items)), nil
}

func (c *lspClient) WorkspaceFolders(context.Context) ([]protocol.WorkspaceFolder, error) {
	return []protocol.WorkspaceFolder{{URI: uri.File(c.root), Name: filepath.Base(c.root)}}, nil
}

func (c *lspClient) PublishDiagnostics(_ context.Context, params *protocol.PublishDiagnosticsParams) error {
	if params == nil || !params.URI.IsFile() {
		return nil
	}
	path := filepath.Clean(params.URI.FsPath())
	c.manager.mu.Lock()
	doc, open := c.manager.desired[path]
	if !open {
		c.manager.mu.Unlock()
		return nil
	}
	if version, ok := params.Version.Get(); ok && version < doc.version {
		c.manager.mu.Unlock()
		return nil
	}
	diagnostics := make([]lspDiagnostic, 0, len(params.Diagnostics))
	for _, item := range params.Diagnostics {
		diagnostics = append(diagnostics, projectDiagnostic(item))
	}
	c.manager.diagnostics[path] = diagnostics
	c.manager.mu.Unlock()
	c.manager.emit(lspEvent{})
	return nil
}

func projectDiagnostic(item protocol.Diagnostic) lspDiagnostic {
	diagnostic := lspDiagnostic{
		Line: item.Range.Start.Line, Character: item.Range.Start.Character,
		EndLine: item.Range.End.Line, EndCharacter: item.Range.End.Character,
		Severity: item.Severity,
	}
	switch message := item.Message.(type) {
	case protocol.String:
		diagnostic.Message = string(message)
	case *protocol.MarkupContent:
		diagnostic.Message = message.Value
	}
	if source, ok := item.Source.Get(); ok {
		diagnostic.Source = source
	}
	switch code := item.Code.(type) {
	case protocol.String:
		diagnostic.Code = string(code)
	case protocol.Integer:
		diagnostic.Code = fmt.Sprint(int32(code))
	}
	return diagnostic
}

func (m *lspManager) Diagnostics(path string) []lspDiagnostic {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]lspDiagnostic(nil), m.diagnostics[filepath.Clean(path)]...)
}

// stdioTransport joins a child's stdout and stdin into the bidirectional shape the LSP
// framer expects. Closing it tears down only the process Gote created.
type stdioTransport struct {
	io.Reader
	io.Writer
	stdin  io.WriteCloser
	stdout io.ReadCloser
	cmd    *exec.Cmd
	once   sync.Once
}

func (t *stdioTransport) Close() error {
	var first error
	t.once.Do(func() {
		if err := t.stdin.Close(); err != nil {
			first = err
		}
		if err := t.stdout.Close(); first == nil && err != nil {
			first = err
		}
		if t.cmd.Process != nil {
			_ = t.cmd.Process.Kill()
		}
		go func() { _ = t.cmd.Wait() }()
	})
	return first
}
