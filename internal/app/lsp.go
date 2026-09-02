package app

import (
	"context"
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

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"

	tea "charm.land/bubbletea/v2"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

const (
	lspRetryDelay     = 15 * time.Second
	lspDialLimit      = 1500 * time.Millisecond
	lspInitLimit      = 5 * time.Second
	lspChangeDebounce = 500 * time.Millisecond
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

type lspEvent struct{ status string }

// lspManager is an actor around all server and document lifecycle work. The UI writes
// the latest desired snapshots under mu and nudges wake; the actor converges sessions
// to that state off Bubble Tea's update goroutine. Repeated edits are retained as one
// latest snapshot and sent only after the change debounce expires.
type lspManager struct {
	cfg            Config
	version        string
	changeDebounce time.Duration

	mu          sync.Mutex
	desired     map[string]lspDocument
	saves       map[string]bool
	diagnostics map[string][]lspDiagnostic
	restart     bool
	closed      bool

	wake   chan struct{}
	stop   chan struct{}
	done   chan struct{}
	events chan lspEvent
	dead   chan deadSession
	once   sync.Once
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
}

func newLSPManager(cfg Config, version string) *lspManager {
	return &lspManager{
		cfg: cfg, version: version, changeDebounce: lspChangeDebounce,
		desired: map[string]lspDocument{}, saves: map[string]bool{},
		diagnostics: map[string][]lspDiagnostic{},
		wake:        make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{}),
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
	c.EachDoc(func(path string, ed *components.EditorScreen) {
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
		}
	}
	m.desired = next
	m.mu.Unlock()
	if len(next) > 0 || hadDocuments {
		m.signal()
	}
	return len(next) > 0
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
			for _, session := range sessions {
				m.closeSession(session)
			}
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

func (m *lspManager) startSession(session *lspSession) error {
	cfg, ok := m.cfg.LanguageServers[session.serverID]
	if !ok || cfg.Disabled {
		return fmt.Errorf("%s language server is disabled", session.serverID)
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
	initCtx, cancel := context.WithTimeout(ctx, lspInitLimit)
	defer cancel()
	_, err := server.Initialize(initCtx, &protocol.InitializeParams{
		ProcessID:  &pid,
		ClientInfo: protocol.ClientInfo{Name: "gote", Version: version},
		RootURI:    &rootURI,
		WorkspaceFoldersInitializeParams: protocol.WorkspaceFoldersInitializeParams{
			WorkspaceFolders: protocol.NewNullable([]protocol.WorkspaceFolder{{
				URI: rootURI, Name: filepath.Base(session.root),
			}}),
		},
		Capabilities: protocol.ClientCapabilities{
			Workspace: &protocol.WorkspaceClientCapabilities{WorkspaceFolders: &yes},
			TextDocument: &protocol.TextDocumentClientCapabilities{
				PublishDiagnostics: &protocol.PublishDiagnosticsClientCapabilities{VersionSupport: &yes},
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

func (m *lspManager) failSession(session *lspSession, err error) {
	if session.conn != nil {
		_ = session.conn.Close()
	}
	session.server, session.conn = nil, nil
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
	session.server, session.conn = nil, nil
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
